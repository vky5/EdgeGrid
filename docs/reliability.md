# Reliability — Stale Job Recovery & Mid-Training Checkpointing

## The problem

A worker runs a training job for 40 minutes. At minute 25, the machine loses power, the process crashes, or the network drops permanently. From the system's perspective:

- The worker's entry in `workers_state` KV expires after 1 minute (TTL-based auto-reap — no heartbeat = dead)
- The job's entry in `jobs_state` KV still says `RUNNING` with `worker_id = "worker-xyz"`
- That job stays `RUNNING` forever unless something fixes it
- 25 minutes of training output in `output/` on the dead machine is gone

Two features work together to handle this: **stale job recovery** requeues the job so it can run again, and **mid-training checkpointing** ensures progress is not lost when it does.

---

# Part 1 — Stale Job Recovery

## What it does

Every 30 seconds, the coordinator scans all jobs in `RUNNING` or `PENDING_REVIEW` state and checks whether the worker assigned to each job still exists in the workers KV. If the worker entry is gone (TTL expired), the job is reset to `QUEUED` and dispatched to the next available worker. `PENDING_REVIEW` is included because a job can be sitting there waiting on the worker-approval gate ([worker-approval.md](./worker-approval.md)) when its worker dies — without this, an approval-gated job whose worker crashed before deciding would be stuck forever, since nothing else would ever move it out of `PENDING_REVIEW`.

## Why 30 seconds

Workers send heartbeats every 10 seconds. The workers KV TTL is 1 minute. A worker is considered dead when it misses ~6 consecutive heartbeats and its KV entry expires. The recovery scan at 30s catches the job within one scan cycle after the TTL fires — so total detection time is at most 1 min (TTL) + 30s (scan interval) = ~90 seconds from crash to requeue.

## Why not use a NATS watch / push notification

The NATS KV watcher can notify when a key expires. This would give instant detection. However, a key expiry event tells you the worker is gone — it does not tell you which job that worker was running. You'd still need to scan all jobs and cross-reference. The polling approach does both in one pass and has no edge cases around missed events.

---

## Step-by-step flow

### Step 1 — Coordinator starts the recovery goroutine

```go
// internal/coordinator/coordinator.go — Start()

go c.StartStaleJobRecovery(ctx)
```

```go
// internal/coordinator/recovery.go

func (c *Coordinator) StartStaleJobRecovery(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            c.recoverStaleJobs(ctx)
        }
    }
}
```

### Step 2 — Scan all RUNNING jobs

```go
func (c *Coordinator) recoverStaleJobs(ctx context.Context) {
    jobsKV, _ := c.jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
    keys, _ := jobsKV.Keys()

    var requeued int
    for _, key := range keys {
        entry, _ := jobsKV.Get(key)
        var status jobstate.JobStatus
        json.Unmarshal(entry.Value(), &status)

        if (status.State != jobstate.StateRunning && status.State != jobstate.StatePendingReview) || status.WorkerID == "" {
            continue  // only care about RUNNING/PENDING_REVIEW jobs with a known worker
        }
```

### Step 3 — Check if the worker still exists

```go
        if _, err := c.manager.GetWorker(status.WorkerID); err == nil {
            continue  // worker is alive, skip
        }

        // Worker entry is gone from KV — it died
        log.Printf("stale job recovery: worker %s gone, requeueing job %s",
            status.WorkerID, status.JobID)
```

`GetWorker` does a `kv.Get(workerID)`. If the entry expired, NATS returns `nats.ErrKeyNotFound`. Any error here is treated as "worker gone" — the safe default.

### Step 4 — Requeue the job, preserving RequestProto

This is the critical step. The coordinator needs to dispatch the job again. `TryDispatchQueued` relies on `RequestProto` — the serialized `TrainingJobRequest` bytes stored in the job's KV entry when the job was first created. Without these bytes, the coordinator cannot reconstruct the original job request.

`RequeueJob` reads the current KV entry, resets the state fields, and writes it back — keeping `RequestProto` intact:

```go
// internal/jobstate/state.go

func RequeueJob(kv nats.KeyValue, jobID string) error {
    entry, _ := kv.Get(jobID)
    var status JobStatus
    json.Unmarshal(entry.Value(), &status)

    status.State = StateQueued
    status.WorkerID = ""
    status.Error = ""
    status.CheckpointKey = ""
    status.UpdatedAt = time.Now()
    // RequestProto is preserved — TryDispatchQueued needs it to re-dispatch

    bytes, _ := json.Marshal(status)
    _, err = kv.Put(jobID, bytes)
    return err
}
```

If `UpdateJobStatus` were used here instead, it would overwrite the entire struct and lose `RequestProto`. The job would be stuck — state is `QUEUED` but there are no bytes to dispatch from.

### Step 5 — Dispatch to available workers

```go
    requeued++
}

if requeued == 0 {
    return  // nothing to dispatch
}

freeIDs, _ := c.manager.FreeWorkerIDs()
for _, workerID := range freeIDs {
    go c.TryDispatchQueued(ctx, workerID)
}
```

`FreeWorkerIDs` scans the workers KV and returns IDs of workers in `free` state:

```go
// internal/coordinator/workerman/manager.go

func (wm *WorkerManager) FreeWorkerIDs() ([]string, error) {
    keys, _ := wm.kv.Keys()
    var free []string
    for _, key := range keys {
        entry, _ := wm.kv.Get(key)
        var w Worker
        json.Unmarshal(entry.Value(), &w)
        if w.State == WorkerFree {
            free = append(free, key)
        }
    }
    return free, nil
}
```

`TryDispatchQueued` then finds the oldest QUEUED job that the worker can handle (matching GPU/RAM/disk requirements) and dispatches it via NATS with a CAS-safe worker assignment. It already existed for the job-queuing feature — recovery reuses it directly.

## Same scan, second job — failing abandoned dataset uploads

`recoverStaleJobs` also does one unrelated bit of housekeeping in the same pass: object_store jobs left waiting on `POST /jobs/{id}/upload` (see [job-queuing.md](./job-queuing.md), "The dataset upload race") that never got it. `Submit` marks these `QUEUED` with `AwaitingDataset: true` and deliberately never dispatches them — nothing else in the system clears that flag except a successful `Upload` call. A client that submits and then walks away (crashes, forgets, never calls `Upload`) would otherwise leave that entry parked in `jobs_state` indefinitely.

```go
// internal/coordinator/recovery.go

const datasetUploadTimeout = 10 * time.Minute

if status.State == jobstate.StateQueued && status.AwaitingDataset {
    if time.Since(status.UpdatedAt) > datasetUploadTimeout {
        log.Printf("stale job recovery: job %s never received its dataset upload, failing", status.JobID)
        jobstate.UpdateJobStatus(jobsKV, status.JobID, jobstate.StateFailed, "", "dataset upload timed out", "")
    }
    continue
}
```

This branch runs before the dead-worker check below it, since these jobs have no `WorkerID` yet — there's nothing to check them against. Past 10 minutes with no upload, the job is moved straight to `FAILED` rather than staying `QUEUED` forever with no path to ever leaving that state.

---

## Known limitation — a false "worker is dead" can cause duplicate execution

Staleness here is inferred entirely from KV TTL expiry — the worker's entry disappearing because it stopped heartbeating for ~60s. That's not the same thing as "the worker actually crashed." A worker that's just slow (CPU pegged by the training job itself, a temporary network partition, a GC pause) can miss enough heartbeats to get reaped from the `workers` KV while it is still very much alive and still running the job.

When that happens, recovery requeues the job and dispatches it to a *different* worker. The original worker doesn't know any of this happened — it keeps training, and when it finishes, it publishes its own `JobResponse` on `workers.results` exactly as if nothing were wrong. Now two workers may genuinely both complete the same job.

`SubscribeToResults` (`internal/coordinator/subscriptions.go`) only guards against one case — a result arriving for a job the coordinator already marked `CANCELLED`. It has no concept of "is this result coming from the worker I currently have assigned to this job," so whichever `JobResponse` arrives, first or last, simply overwrites `jobs_state` via `UpdateJobStatus`. If the original (falsely-declared-dead) worker's result lands after the replacement worker's, its checkpoint silently wins — with no signal to anyone that two runs happened, or which one is reflected in the final state.

This hasn't caused visible problems at the scale this system runs at today (heartbeat misses this severe are rare outside of genuine crashes), but it's a real gap, not a hypothetical: fixing it would mean either fencing (the coordinator hands out a generation/epoch number with each dispatch, and `SubscribeToResults` rejects results from a stale generation) or having workers check "am I still the assigned worker for this job" against `jobs_state` before publishing a result.

---

# Part 2 — Mid-Training Checkpointing

## What it does

While training runs, a background goroutine tars the job's `output/` directory every 5 minutes and uploads it to the NATS Object Store under the job's ID. If the worker dies at minute 25 of a 40-minute job, the object store has a checkpoint from minute 20 or 25. When the job is requeued, the new worker's `runTrainingPipeline` pulls that checkpoint down and extracts it into `output/` *before* the training script ever starts (`pullCheckpoint`, `internal/worker/pipeline.go`) — so the script sees prior output already sitting there, the same as if it had never crashed.

## Why overwrite, not versioned checkpoints

Each periodic upload overwrites the same object store key (`jobID`). There is no checkpoint history — only the latest snapshot. Keeping every checkpoint would accumulate gigabytes of data per job. The goal is crash recovery, not time travel. The final checkpoint (written after training completes successfully) is the authoritative one and also overwrites the same key.

## Why 5 minutes

Short enough to limit re-work after a crash (at most 5 minutes of training lost), long enough that the I/O overhead of taring and uploading `output/` does not meaningfully slow training. This is a hardcoded constant for now — it could be made configurable via the job request if needed.

---

## Step-by-step flow

### Step 1 — Resume: pull any prior checkpoint before training starts

```go
// internal/worker/pipeline.go — pullCheckpoint, called from runTrainingPipeline
// right after outputDir is created, before the goroutine or Execute below.
```

Extracts the checkpoint tar into `outputDir` if one exists for this job ID; returns nil (not an error) if there isn't one yet, which is the normal case for a job's first attempt.

### Step 2 — Goroutine starts alongside training

The goroutine is launched inside `runTrainingPipeline`, right before the blocking `executor.Execute` call:

```go
// internal/worker/pipeline.go — runTrainingPipeline

checkpointStop := make(chan struct{})
go func() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return  // job cancelled or worker shutting down
        case <-checkpointStop:
            return  // training finished normally
        case <-ticker.C:
            entries, _ := os.ReadDir(outputDir)
            if len(entries) == 0 {
                continue  // nothing written yet, skip
            }
            if err := a.pushCheckpoint(req.JobId, outputDir); err != nil {
                log.Printf("mid-training checkpoint failed for job %s: %v", req.JobId, err)
            } else {
                log.Printf("mid-training checkpoint saved for job %s", req.JobId)
            }
        }
    }
}()
```

The empty-directory check prevents uploading an empty tar during early training before the script has written anything to `output/`.

### Step 3 — Training runs, goroutine snapshots periodically

```
Timeline:
  t=0:00  pullCheckpoint finds nothing (first attempt), executor.Execute starts, goroutine starts
  t=0:00  Python script begins training
  t=5:00  goroutine tick → output/ has files → tar + upload to object store
  t=10:00 goroutine tick → tar + upload (overwrites previous)
  t=15:00 goroutine tick → tar + upload
  t=18:00 WORKER CRASHES
  ...
  [stale job recovery kicks in at ~t=19:30]
  [new worker picks up the job]
  [pullCheckpoint extracts the t=15:00 snapshot into outputDir before training starts]
```

### Step 4 — Training finishes, goroutine stops

When `executor.Execute` returns (success or error), `checkpointStop` is closed:

```go
// 5. Run training
if err := a.executor.Execute(ctx, req, jobDir); err != nil {
    close(checkpointStop)
    return "", err
}
close(checkpointStop)

// 6. Push final checkpoint to Object Store
if err := a.pushCheckpoint(req.JobId, outputDir); err != nil {
    return "", fmt.Errorf("checkpoint push failed: %w", err)
}
```

The goroutine exits via `<-checkpointStop`. The final `pushCheckpoint` call then uploads the complete, consistent output — the authoritative result for the job.

### Step 5 — pushCheckpoint tars and uploads

```go
// internal/worker/pipeline.go — pushCheckpoint

func (a *Worker) pushCheckpoint(jobID, outputDir string) error {
    pr, pw := io.Pipe()

    go func() {
        gw := gzip.NewWriter(pw)
        tw := tar.NewWriter(gw)

        filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
            // write each file into the tar
        })

        tw.Close()
        gw.Close()
        pw.CloseWithError(err)
    }()

    return a.broker.PushCheckpoint(jobID, pr)  // streams directly to object store
}
```

The tar is written to a pipe and streamed directly to the NATS Object Store — no intermediate file on disk. The object store key is `jobID`, so each call overwrites the previous.

---

## How the two features work together

Stale job recovery and mid-training checkpointing are independent features but they compose naturally:

1. Worker dies at minute 25
2. KV TTL fires at minute 26 — worker entry gone
3. Recovery scan at minute 26:30 — detects RUNNING job with dead worker
4. RequeueJob — job back to QUEUED, RequestProto preserved
5. TryDispatchQueued — new worker assigned, job dispatched
6. New worker's pipeline runs `pullCheckpoint` before training starts — extracts the minute-20 snapshot into `outputDir`
7. Training script sees that prior output already in place and resumes from minute 20, not from scratch

Without mid-training checkpointing, step 6 starts from scratch. Without stale job recovery, the job never gets to step 4 and stays RUNNING forever.

---

## Example

```bash
# Submit a long job
curl -X POST http://localhost:8080/jobs \
  -d '{"training_script": "...", "dataset_ref": "my-dataset"}'
# → {"job_id":"abc123","status":"queued"}

# ... 15 minutes later, worker machine loses power ...

# ~90 seconds after crash, job is automatically requeued
curl http://localhost:8080/jobs/abc123
# {"job_id":"abc123","state":"QUEUED",...}

# New worker picks it up
curl http://localhost:8080/jobs/abc123
# {"job_id":"abc123","state":"RUNNING","worker_id":"worker-new-456",...}

# Download the latest checkpoint at any time
curl http://localhost:8080/jobs/abc123/artifact -o checkpoint.tar.gz
```
