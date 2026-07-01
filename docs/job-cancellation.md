# Job Cancellation — `DELETE /jobs/{id}`

## What it does

Sends a signal to stop a running training job. The job's Python process is killed on the worker machine, the job state is set to `CANCELLED`, and any client streaming logs via SSE receives a final `event: done` and closes. Works for both QUEUED jobs (not yet started) and RUNNING jobs (actively training).

---

## Why this design

The core challenge is that the coordinator and workers are separate processes on separate machines. The coordinator receives the `DELETE` request but it is the worker that holds the running Python process. There is no direct channel from the coordinator to a specific worker's process — everything goes through NATS.

**Option considered: route cancel to the specific worker**
Publish the cancel signal to `jobs.cancel.<workerID>` so only the right worker receives it. This requires the coordinator to know which worker is running the job at publish time — which it does (it's in `JobStatus.WorkerID`). But it adds complexity: the coordinator must look up the worker ID before publishing, and if the worker re-registers with a new ID, the subject is wrong.

**Option chosen: broadcast cancel to all workers, each checks its own map**
Publish jobID as plain bytes to `jobs.cancel`. Every worker's `StartCancelListener` receives it and checks its own `cancels` map. Only the worker actually running that job has an entry in its map — all others ignore it silently. Simple, no routing, no stale subject problem.

---

## Step-by-step flow

### Step 1 — Coordinator marks job CANCELLED

```go
// internal/coordinator/api.go — handleCancelJob

status, _ := jobstate.GetJobStatus(kv, jobID)

// Reject if job is already in a terminal state
if status.State != jobstate.StateQueued && status.State != jobstate.StateRunning {
    http.Error(w, fmt.Sprintf("job is %s, cannot cancel", status.State), http.StatusConflict)
    return
}

// Write CANCELLED to KV immediately
jobstate.UpdateJobStatus(kv, jobID, jobstate.StateCancelled, status.WorkerID, "cancelled by user", "")
```

The KV write happens **before** publishing the cancel signal. This ordering matters: if the worker finishes the job in the milliseconds between the KV write and the publish, the result handler will see `StateCancelled` and skip overwriting it. If the order were reversed, there would be a window where the result handler runs, sees `StateRunning`, overwrites to `StateCompleted`, and then the cancel signal arrives too late.

### Step 2 — Coordinator publishes cancel signal (RUNNING jobs only)

```go
// If the job is running, signal the worker to stop it.
if status.State == jobstate.StateRunning {
    jsBroker.JS.Publish(broker.SubjectCancel, []byte(jobID))
}
```

For QUEUED jobs, no publish is needed. No worker has the job yet — the KV state update is the entire cancellation. `TryDispatchQueued` checks the job state before dispatching, so the CANCELLED job will simply be skipped if it comes up in a scan.

### Step 3 — Worker cancel listener receives the signal

Every worker runs `StartCancelListener` as a goroutine at startup:

```go
// internal/worker/listener.go

func (a *Worker) StartCancelListener(ctx context.Context) {
    sub, _ := a.broker.JS.Subscribe(broker.SubjectCancel, func(msg *nats.Msg) {
        jobID := string(msg.Data)
        a.mu.Lock()
        if cancel, ok := a.cancels[jobID]; ok {
            cancel()
            log.Printf("cancelling job %s on coordinator request", jobID)
        }
        a.mu.Unlock()
        msg.Ack()
    }, nats.DeliverNew(), nats.ManualAck())

    defer sub.Unsubscribe()
    <-ctx.Done()
}
```

`nats.DeliverNew()` means the worker only receives cancel messages published after it subscribed — old cancels for jobs from before the worker started are irrelevant. Every worker that is alive receives the message, checks the map, and only the worker holding that jobID acts on it. All others ack and move on.

### Step 4 — Per-job context is cancelled

The `cancels` map holds a `context.CancelFunc` for each job currently running on this worker. The map is populated in `handleJob`:

```go
// internal/worker/listener.go — handleJob

jobCtx, cancel := context.WithCancel(ctx)
a.mu.Lock()
a.cancels[req.JobId] = cancel
a.mu.Unlock()
defer func() {
    cancel()
    a.mu.Lock()
    delete(a.cancels, req.JobId)
    a.mu.Unlock()
}()

checkpointKey, err := a.runTrainingPipeline(jobCtx, &req)
```

`jobCtx` is a child of the worker's root context. Cancelling it does not stop other jobs or the worker itself — only this specific job's context tree is cancelled.

The reason for `sync.Mutex` here: the cancel listener goroutine and the job handler goroutine both access the `cancels` map concurrently. The mutex prevents a data race.

### Step 5 — Python process is killed

`runTrainingPipeline` passes `jobCtx` all the way down to the executor:

```go
// executor.Execute receives jobCtx
// → runScript receives jobCtx
// → exec.CommandContext(ctx, python, scriptPath)
```

`exec.CommandContext` attaches the context to the OS process. When `jobCtx` is cancelled, Go sends `SIGKILL` to the Python process. `cmd.Run()` returns an error wrapping `context.Canceled`.

### Step 6 — Worker sends result, coordinator ignores it

After the process is killed, `runTrainingPipeline` returns an error. The worker sends a `JobResponse` with `Success: false`:

```go
// internal/worker/listener.go — handleJob
resp.Success = false
resp.Error = err.Error()  // "training script exited with error: signal: killed"
```

The coordinator receives this in `SubscribeToResults` and checks the current state before updating:

```go
// internal/coordinator/subscriptions.go

current, _ := jobstate.GetJobStatus(kv, resp.JobId)
if current != nil && current.State == jobstate.StateCancelled {
    log.Printf("job %s result ignored (already cancelled)", resp.JobId)
} else if resp.Success {
    ...StateCompleted...
} else {
    ...StateFailed...
}
```

Because the coordinator wrote `StateCancelled` in Step 1 before the worker even received the cancel signal, by the time the result arrives the KV state is already `CANCELLED`. The coordinator skips the update, preserving the correct state.

### Step 7 — SSE log stream closes

The `GET /jobs/{id}/logs` handler polls job state every 2 seconds. When it sees `StateCancelled`:

```go
if status.State == jobstate.StateCompleted ||
   status.State == jobstate.StateFailed ||
   status.State == jobstate.StateCancelled {
    // drain remaining messages, send done event, close
    fmt.Fprintf(w, "event: done\ndata: %s\n\n", status.State)
    flusher.Flush()
    return
}
```

The client receives `event: done\ndata: CANCELLED\n\n` and the stream closes.

---

## End-to-end example

```bash
# Submit a job
curl -X POST http://localhost:8080/jobs \
  -d '{"training_script":"...", "dataset_ref":"..."}'
# → {"job_id":"abc123","status":"queued"}

# In another terminal, stream logs
curl -N http://localhost:8080/jobs/abc123/logs
# data: Epoch 1/10 loss=0.842
# data: Epoch 2/10 loss=0.761

# Cancel the job
curl -X DELETE http://localhost:8080/jobs/abc123
# HTTP 202

# Log stream closes:
# event: done
# data: CANCELLED

# Check state
curl http://localhost:8080/jobs/abc123
# {"job_id":"abc123","state":"CANCELLED","error":"cancelled by user",...}
```

---

## Edge cases

**Cancelling a QUEUED job** — no worker has the job. The coordinator writes `CANCELLED` to KV and returns 202. No NATS publish happens. `TryDispatchQueued` skips jobs that are not `QUEUED` state.

**Race between cancel and completion** — the job finishes in the same millisecond as the cancel. Two outcomes are possible:
- Cancel KV write wins → result handler sees `CANCELLED` → ignores → job stays `CANCELLED`
- Result handler wins → writes `COMPLETED` → cancel KV write overwrites to `CANCELLED`

Neither outcome is perfect but both are safe. In practice the cancel-before-result ordering described in Step 1 makes the first outcome far more likely.

**Worker crashes before receiving cancel** — the cancel signal is a JetStream message. If the worker is dead, it never receives it. The job is already `CANCELLED` in KV. When stale job recovery runs (every 30s), it finds the job `CANCELLED` (not `RUNNING`) and skips it correctly.
