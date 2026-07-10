# Python Execution Lifecycle

How a training job actually runs, from coordinator dispatch to Python process
exit. There is no sidecar process, no local IPC, and no persistent model
server — each job is a fresh `os/exec` invocation of the user's own script.

```text
┌─────────────┐  jobs.train.<workerID>   ┌─────────────┐   os/exec   ┌───────────────┐
│ Coordinator │ ───────────────────────> │  Go Worker  │ ──────────> │ python train.py │
│ (dispatch)  │                          │ (Worker)    │             │  (user script) │
└─────────────┘                          └─────────────┘             └───────────────┘
```

---

## 1. Dispatch — coordinator picks a specific worker

Unlike a fan-out queue, the coordinator addresses a job to one worker
directly. `TryDispatchQueued` (`internal/coordinator/dispatch.go:19`) runs
whenever a worker becomes free (new registration or job completion):

1. Scans the `jobs_state` KV for every job still in `QUEUED`.
2. Skips any job this worker already rejected (`RejectedBy`, set when a human
   declines the approval prompt — see §3).
3. Filters by hardware requirements (`workerman.MeetsRequirements`: GPU,
   VRAM, RAM, disk).
4. Picks the oldest matching job, assigns the worker
   (`manager.TryAssignWorker`), and publishes the `TrainingJobRequest`
   protobuf directly to that worker's private subject:
   `jobs.train.<workerID>` (`broker.SubjectTrainPrefix + workerID`).

If publishing fails, the worker is put back to `WorkerFree` so the next
dispatch attempt can retry.

## 2. Ingestion — worker pulls its own jobs

`StartJobListener` (`internal/worker/jobs.go:40`) opens a durable pull
subscription scoped to this worker alone:

- Subject: `jobs.train.<workerID>`
- Durable consumer name: `training-consumer-<workerID>`
- `sub.Fetch(1, nats.MaxWait(5*time.Second))` in a loop — no message ever
  goes to a worker it wasn't addressed to.

`handleJob` (`internal/worker/jobs.go:77`) then:
1. CAS's `a.busy` — a worker only ever runs one job at a time; if already
   busy, `msg.NakWithDelay(10s)` so JetStream retries later instead of
   dropping the job.
2. Unmarshals the NATS payload into a `workerpb.TrainingJobRequest`
   (`internal/proto/worker/worker.proto:25`) — job ID, raw training script
   bytes, inlined `requirements.txt`, dataset/base-model refs, a JSON
   hyperparameter blob, and hardware minimums.

## 3. Optional human approval gate

If the worker was started with `--require-approval`, `handleJob` ACKs the
message immediately (taking ownership so it won't be redelivered elsewhere),
sets job state to `PENDING_REVIEW`, and calls `awaitApproval`
(`internal/worker/approval.go:26`):

- Subscribes on `workers.decision.<workerID>.<jobID>` (plain NATS core, not
  JetStream — this is a short-lived signal, not durable state).
- Waits up to 60s for a human decision relayed by the coordinator.
- `"approve"` → proceeds to §4. Anything else (reject, timeout, cancelled
  context) → publishes to `workers.reject` and returns; the coordinator
  requeues the job (marking this worker in `RejectedBy`) and
  `TryDispatchQueued` tries the next candidate.

## 4. Training pipeline (`runTrainingPipeline`, `internal/worker/pipeline.go:20`)

1. **Disk pre-check** — if `min_disk_gb` is set, verifies free space via
   `hardware.DiskFreeGB()` before doing any work.
2. **Isolated job directory**: `$TMPDIR/edgegrid-jobs/<jobID>/{input,output}`,
   `defer os.RemoveAll(jobDir)` so it's always cleaned up on return.
3. **Checkpoint resume**: `pullCheckpoint` fetches a prior checkpoint tarball
   from the NATS Object Store (keyed by job ID) and extracts it into
   `output/` — this is what lets a job continue after a crash-requeue rather
   than restart from scratch. `nats.ErrObjectNotFound` is the expected,
   non-error case for a job's first attempt.
4. **Dataset pull** — only for `dataset_type == "object_store"`; HF-hosted
   datasets are downloaded by the training script itself, not by the worker.
5. **Background checkpoint snapshots**: a goroutine ticks every 5 minutes
   and tars `output/` to the Object Store while training runs, so a crash
   mid-job loses at most 5 minutes of progress, not the whole run. Stopped
   via a `checkpointStop` channel once training finishes.
6. **Execute** — hands off to the `Executor` interface (§5).
7. **Final checkpoint push** — one more snapshot after the script exits
   successfully, so the last few minutes of work aren't left stranded in the
   5-minute gap.

## 5. Script execution (`TrainingExecutor.Execute`, `internal/worker/executor/training.go:30`)

1. Writes `req.TrainingScript` to `input/train.py`.
2. **Venv resolution** (`resolveVenv`): if `requirements` is non-empty, keys
   a venv by `SHA256(requirements.txt)` under `/tmp/edgegrid-venvs/<hash>/`.
   A `.ready` sentinel file marks a completed install, so two jobs with
   identical dependencies skip `pip install` entirely on the second run. If
   `requirements` is empty, falls back to the system `python3`/`python`.
3. **Runs the script** directly on the worker host — `exec.CommandContext(ctx,
   python, "-u", scriptPath)`, no container or restricted user (`-u` disables
   Python's stdout buffering so log lines stream immediately instead of
   batching). Env is `os.Environ()` plus `OUTPUT_DIR`, `JOB_ID`,
   `TRAINING_CONFIG` — the script reads hyperparameters from
   `TRAINING_CONFIG` and is expected to write its output to `$OUTPUT_DIR`.
4. `cmd.Stdout`/`cmd.Stderr` are wired to a `logWriter` that splits on
   newlines, logs locally, and (if a `logPublish` func was supplied) forwards
   each line to NATS JetStream for the SSE log stream — see
   [log-streaming.md](log-streaming.md).
5. Blocks on `cmd.Run()` until the script exits. A non-zero exit becomes a Go
   error, which propagates back up as job `FAILED`.

There is no sandboxing here and the subprocess inherits the worker's full
environment — see `docs/security/known-gaps.md` #3/#4 for the implications.

## 6. Result & acknowledgment (back in `handleJob`)

- Success: `JobResponse{Success: true, CheckpointKey: jobID}` published to
  `jobs.results`; job state set to `COMPLETED`.
- Failure: `JobResponse{Success: false, Error: err.Error()}`; job state set
  to `FAILED`.
- The original NATS message is `Ack()`'d only after the result is
  successfully published. If the worker crashes anywhere in §4/§5, the
  message is never acked — JetStream redelivers it to another worker, which
  resumes from whatever checkpoint made it to the Object Store (§4 step 3).

## 7. Cancellation

`StartCancelListener` (`internal/worker/jobs.go:19`) subscribes to the
broadcast subject `jobs.cancel`; every worker gets every cancel message, but
only the one holding that job ID in its local `cancels` map (registered for
the duration of `runTrainingPipeline`) actually calls the per-job
`context.CancelFunc`, which propagates through `exec.CommandContext` to kill
the running Python process.
