# KV Revisions and CAS

This note explains the revision-based update pattern used in
`internal/coordinator/workerman/schedule.go`.

## The Problem

Multiple coordinators can run at the same time. That means two coordinators can
look at the same worker and both think it is free.

```text
Coordinator A reads worker-B: state=free
Coordinator C reads worker-B: state=free
```

If both coordinators then assign different jobs to `worker-B`, the system has a
double-assignment bug.

## KV Revisions

Every NATS JetStream KV entry has a revision number. When code reads a key, it
gets both the value and the current revision:

```go
entry, err := wm.kv.Get(workerID)
revision := entry.Revision()
```

The revision changes every time the key is updated.

## CAS

CAS means compare-and-swap:

```text
write this new value only if the key is still at the revision I read
```

In this codebase, CAS is done with:

```go
wm.kv.Update(workerID, data, entry.Revision())
```

If another coordinator updated the same key after we read it, the revision no
longer matches and `Update` fails.

## Race Example

```text
Coordinator A reads worker-B: revision=5, state=free
Coordinator C reads worker-B: revision=5, state=free

Coordinator A writes worker-B busy with revision=5
  -> success, worker-B becomes revision=6

Coordinator C writes worker-B busy with revision=5
  -> fail, because current revision is now 6
```

So only one coordinator wins the worker assignment.

## Reference: FindAndAssignWorker

In `internal/coordinator/workerman/schedule.go`, `FindAndAssignWorker` scans
workers and uses the entry revision when marking a worker busy:

```go
if _, err := wm.kv.Update(key, data, entry.Revision()); err != nil {
    continue
}
```

If the update fails, this coordinator assumes another coordinator got there
first and continues scanning.

## Reference: TryAssignWorker

`TryAssignWorker` does the same thing for one specific worker:

```go
if _, err := wm.kv.Update(workerID, data, entry.Revision()); err != nil {
    return fmt.Errorf("CAS conflict: worker grabbed by another coordinator: %w", err)
}
```

This protects queued-job dispatch too, because `TryDispatchQueued` calls
`TryAssignWorker` before publishing the job to `jobs.train.<workerID>`.

## Why This Matters

The coordinator does not rely on local memory or locks. The source of truth is
the NATS JetStream KV bucket, and the revision number acts like the distributed
lock-free guard.

```text
local mutex:
  protects one process only

KV revision CAS:
  protects all coordinators sharing the same JetStream KV bucket
```
