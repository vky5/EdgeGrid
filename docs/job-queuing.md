# Job Queuing — Dispatch When No Worker Is Free

## What it does

When a training job is submitted and no worker is currently free (or no free worker meets the job's hardware requirements), the job is not rejected. It stays in `QUEUED` state in the KV store. The moment any worker becomes available — either a new worker registers or an existing worker finishes a job — the coordinator scans the queue and dispatches the oldest matching job to that worker automatically.

---

## Why not reject with 503

The simplest implementation: if no worker is free, return HTTP 503 and tell the client to retry. The problem is that the client now has to implement retry logic, poll for capacity, and handle partial submissions. In a distributed training network where workers come and go, there will frequently be bursts of jobs with no immediately free worker. Queuing makes the system usable; rejection makes it fragile.

---

## Why store RequestProto in the job's KV entry

To re-dispatch a QUEUED job when a worker becomes free, the coordinator needs the original `TrainingJobRequest` — specifically all the hardware requirements and the training script bytes. This information is only available at submission time (it comes in via HTTP). If it is not persisted, by the time a worker becomes available, the data is gone.

The `JobStatus` struct in KV has a `RequestProto []byte` field:

```go
// internal/jobstate/state.go

type JobStatus struct {
    JobID           string    `json:"job_id"`
    State           State     `json:"state"`
    WorkerID        string    `json:"worker_id,omitempty"`
    Error           string    `json:"error,omitempty"`
    CheckpointKey   string    `json:"checkpoint_key,omitempty"`
    UpdatedAt       time.Time `json:"updated_at"`
    RequestProto    []byte    `json:"request_proto,omitempty"`  // serialized TrainingJobRequest
    AwaitingDataset bool      `json:"awaiting_dataset,omitempty"` // see "The dataset upload race" below
}
```

At submission time, the proto is marshaled and stored once via `InitJobState`:

```go
// internal/coordinator/jobsapi/jobsapi.go — Submit

req := &workerpb.TrainingJobRequest{ ... }
reqBytes, _ := proto.Marshal(req)

awaitingDataset := body.DatasetType == "object_store"
jobstate.InitJobState(kv, jobID, reqBytes, submittedBy, awaitingDataset)  // writes QUEUED + proto bytes
```

`InitJobState` is separate from `UpdateJobStatus` — subsequent state updates (`RUNNING`, `COMPLETED`, `FAILED`) use `UpdateJobStatus` which does not touch `RequestProto`. The bytes are written once and preserved for the lifetime of the job.

---

## Step 1 — Submission: fast path vs queue path

```go
// internal/coordinator/jobsapi/jobsapi.go — Submit

// object_store jobs need a follow-up POST /jobs/{id}/upload before there's
// any data to train on — dispatch has to wait for that, not for a free worker.
awaitingDataset := body.DatasetType == "object_store"
jobstate.InitJobState(kv, jobID, reqBytes, submittedBy, awaitingDataset)

if awaitingDataset {
    // Leave it QUEUED — Upload() is the one that dispatches this job.
    log.Printf("job %s awaiting dataset upload before dispatch", jobID)
} else if err := tryDispatch(jsBroker, manager, jobID, req); err != nil {
    http.Error(w, "failed to dispatch job", http.StatusInternalServerError)
    return
}

// Always return 202 — client never sees a difference
w.WriteHeader(http.StatusAccepted)
json.NewEncoder(w).Encode(SubmitJobResponse{JobID: jobID, Status: "queued"})
```

`tryDispatch` is a small shared helper (also used by `Upload`, see below) wrapping `FindAndAssignWorker` + `PublishProto`:

```go
// internal/coordinator/jobsapi/jobsapi.go

func tryDispatch(jsBroker *broker.Broker, manager *workerman.WorkerManager, jobID string, req *workerpb.TrainingJobRequest) error {
    workerID, err := manager.FindAndAssignWorker(jobID, req)
    if err != nil {
        log.Printf("no free worker for job %s, leaving queued: %v", jobID, err)
        return nil  // no free worker — job stays QUEUED, dispatched later
    }
    subject := broker.SubjectTrainPrefix + workerID
    if pubErr := jsBroker.PublishProto(subject, req); pubErr != nil {
        manager.SetWorkerState(workerID, workerman.WorkerFree)
        return pubErr
    }
    log.Printf("job %s dispatched to worker %s", jobID, workerID)
    return nil
}
```

The HTTP response is always `202 Accepted` with `status: queued`. The client does not need to know whether the job was dispatched immediately, is waiting for capacity, or is waiting on its own dataset upload. It polls `GET /jobs/{id}` to watch state transitions.

---

## Step 1b — The dataset upload race, and why object_store jobs get a third path

`dataset_ref`/`dataset_type: "object_store"` jobs don't carry their data in the submit request — the client uploads it separately, afterward, via `POST /jobs/{id}/upload`, because the object store key is the job ID and the job ID doesn't exist until `Submit` generates it.

That gap between "job created" and "dataset uploaded" collides with the fast path above: if a worker happens to be free at submit time, `tryDispatch` would fire immediately and the worker could call `pullDataset` (`internal/worker/pipeline.go`) before the upload request even arrives. Unlike `pullCheckpoint`, `pullDataset` treats "not found" as a hard failure, not "first attempt, nothing there yet" — so the job would fail with `dataset pull failed: nats: object not found`, deterministically, any time a worker is sitting idle when the job is submitted.

`AwaitingDataset` closes this gap by making dispatch wait on the right event instead of racing it:

- `Submit` sets `AwaitingDataset = true` for `object_store` jobs and skips `tryDispatch` entirely — no worker is ever assigned, nothing is ever published to `jobs.train.*` for this job yet.
- `TryDispatchQueued`'s scan (`internal/coordinator/dispatch.go`) also skips any `AwaitingDataset` job, so no *other* trigger (a different worker registering, another job finishing) can dispatch it early either.
- `Upload` (`internal/coordinator/jobsapi/jobsapi.go`), once `PushDataset` succeeds, reads the job's `RequestProto` back out of `jobs_state`, clears the flag via `jobstate.MarkDatasetReady`, and calls the same `tryDispatch` helper `Submit` uses. This is the first and only point at which the job can be dispatched.

The result is an invariant on the worker side: if a worker ever receives a `TrainingJobRequest` with `dataset_type == "object_store"`, the dataset is guaranteed to already be sitting in the Object Store. `pullDataset` needed no changes — it stays a hard error, no polling or retry logic added to the worker at all.

**Failure mode this doesn't fix**: a client that calls `Submit` and never calls `Upload`. That job would sit `AwaitingDataset` in the KV forever. See [reliability.md](./reliability.md) for the TTL sweep that fails it after 10 minutes.

---

## Step 2 — Two triggers for TryDispatchQueued

`TryDispatchQueued` is called in two places:

**On worker registration:**
```go
// internal/coordinator/subscriptions.go — SubscribeToWorkerEvents

if err := c.manager.RegisterWorker(ctx, &info); err != nil { ... }
go c.TryDispatchQueued(ctx, info.Id)  // new worker just joined — check queue
```

**On job completion:**
```go
// internal/coordinator/subscriptions.go — SubscribeToResults

c.manager.SetWorkerState(resp.WorkerId, workerman.WorkerFree)
go c.TryDispatchQueued(ctx, resp.WorkerId)  // worker just freed — check queue
```

Both run in a goroutine so they don't block the NATS message handler. The pattern is: whenever a worker transitions to free, immediately check if something is waiting for it.

---

## Step 3 — TryDispatchQueued scans for the best match

```go
// internal/coordinator/dispatch.go

func (c *Coordinator) TryDispatchQueued(ctx context.Context, workerID string) {
    // 1. Verify the worker is actually free right now
    worker, err := c.manager.GetWorker(workerID)
    if err != nil || worker.State != workerman.WorkerFree {
        return
    }

    kv, _ := c.jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
    keys, _ := kv.Keys()

    var bestStatus *jobstate.JobStatus
    var bestReq    *workerpb.TrainingJobRequest

    // 2. Scan all jobs for the oldest QUEUED job this worker can handle
    for _, key := range keys {
        entry, _ := kv.Get(key)
        var status jobstate.JobStatus
        json.Unmarshal(entry.Value(), &status)

        if status.State != jobstate.StateQueued || len(status.RequestProto) == 0 {
            continue
        }

        var req workerpb.TrainingJobRequest
        proto.Unmarshal(status.RequestProto, &req)

        if !workerman.MeetsRequirements(worker.Info, &req) {
            continue  // this worker can't run this job
        }

        // FIFO: pick the job with the earliest UpdatedAt timestamp
        if bestStatus == nil || status.UpdatedAt.Before(bestStatus.UpdatedAt) {
            s := status
            bestStatus = &s
            bestReq = &req
        }
    }

    if bestStatus == nil {
        return  // nothing in queue for this worker
    }
    ...
}
```

**FIFO selection**: jobs are compared by `UpdatedAt` (set at `InitJobState` time). The oldest job wins. This prevents starvation — a job that has been waiting longest gets dispatched first. If two jobs have identical timestamps, the iteration order of KV keys determines the winner (acceptable for a v1).

**Why reconstruct the proto from bytes**: The `RequestProto` bytes are the original serialized `TrainingJobRequest`. Calling `proto.Unmarshal` turns them back into a struct. This is the same struct originally sent to the worker — same job ID, same script, same requirements. Re-dispatching is identical to initial dispatch.

---

## Step 4 — CAS-safe worker assignment

Between the moment `TryDispatchQueued` checks the worker is free and the moment it tries to assign it, another coordinator instance (in a multi-coordinator setup) might do the same thing for the same worker. Without coordination, two coordinators could both assign different jobs to the same worker.

`TryAssignWorker` uses NATS KV's Compare-And-Swap (CAS) to prevent this:

```go
// internal/coordinator/workerman/schedule.go

func (wm *WorkerManager) TryAssignWorker(workerID, jobID string) error {
    entry, _ := wm.kv.Get(workerID)  // read current state + revision number
    var worker Worker
    json.Unmarshal(entry.Value(), &worker)

    if worker.State != WorkerFree {
        return fmt.Errorf("worker %s is %s", workerID, worker.State)
    }

    worker.State = WorkerBusy
    worker.Job   = &Job{ID: jobID, Status: "running", StartedAt: time.Now()}
    data, _ := json.Marshal(worker)

    // Update only if the KV entry hasn't changed since we read it
    if _, err := wm.kv.Update(workerID, data, entry.Revision()); err != nil {
        return fmt.Errorf("CAS conflict — worker grabbed by another coordinator: %w", err)
    }
    return nil
}
```

`kv.Update(key, data, revision)` tells NATS: "write this value, but only if the current revision matches what I read." If another coordinator wrote to the same key between our read and our write, NATS rejects the update with an error. The losing coordinator backs off and tries the next worker.

**Concrete example:**
```
Coordinator A reads worker-B: revision=5, state=free
Coordinator C reads worker-B: revision=5, state=free

Coordinator A calls kv.Update(worker-B, busy, revision=5) → SUCCESS, revision becomes 6
Coordinator C calls kv.Update(worker-B, busy, revision=5) → FAIL (revision is now 6, not 5)

Coordinator C moves on to try the next free worker.
```

This guarantees exactly-once assignment per worker per job, across any number of coordinators.

---

## Step 5 — Job dispatched directly to worker

```go
// internal/coordinator/dispatch.go

if err := c.manager.TryAssignWorker(workerID, bestStatus.JobID); err != nil {
    return  // CAS lost, another coordinator got there first
}

subject := broker.SubjectTrainPrefix + workerID  // "jobs.train.worker-xyz"
if err := c.jsBroker.PublishProto(subject, bestReq); err != nil {
    // Publish failed — roll back worker assignment so it stays available
    c.manager.SetWorkerState(workerID, workerman.WorkerFree)
    return
}

log.Printf("dispatched queued job %s to newly available worker %s",
    bestStatus.JobID, workerID)
```

If the NATS publish fails after the CAS succeeds, the worker is rolled back to `free`. Without this rollback, the worker would be stuck in `busy` state with no job message — it would never process anything and never send a result.

---

## Full timeline example

```
t=0:00  POST /jobs (job-1, requires GPU)
        → FindAndAssignWorker: no free GPU worker
        → InitJobState: job-1 = QUEUED + RequestProto stored
        → 202 returned

t=0:00  POST /jobs (job-2, requires GPU)
        → same, job-2 = QUEUED

t=0:30  worker-A registers (HasGPU=true, RAM=32GB)
        → RegisterWorker stores worker-A in KV
        → go TryDispatchQueued(ctx, "worker-A")
              scan jobs_state:
                job-1 QUEUED, proto ok, MeetsRequirements? YES, UpdatedAt=0:00
                job-2 QUEUED, proto ok, MeetsRequirements? YES, UpdatedAt=0:00
              bestStatus = job-1 (same timestamp, first in iteration)
              TryAssignWorker(worker-A, job-1) → CAS succeeds
              PublishProto("jobs.train.worker-A", req)

t=0:30  worker-A receives job-1, starts training
        job-1 state → RUNNING

t=5:00  worker-A finishes job-1, sends JobResponse
        → SetWorkerState(worker-A, free)
        → go TryDispatchQueued(ctx, "worker-A")
              scan: job-2 QUEUED, MeetsRequirements? YES
              TryAssignWorker(worker-A, job-2) → CAS succeeds
              PublishProto("jobs.train.worker-A", req)

t=5:00  worker-A receives job-2, starts training
```

Two jobs queued at submission time, both dispatched automatically as soon as capacity exists, in submission order.
