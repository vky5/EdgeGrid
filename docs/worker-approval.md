# Worker Opt-In Approval Gate

## Why This Exists

EdgeGrid's original model was fully automatic: the coordinator dispatched a job
to a worker and the worker ran it immediately, no questions asked. This is fine
when you trust every job submitter. It breaks down when workers are contributed
by real people who don't know who else is using the network.

With `--require-approval`, the worker operator sees the incoming job before it
executes anything on their machine. They approve or reject it. If they reject
(or don't respond in time), the job is automatically routed to the next
available worker. The submitter never waits forever — they just end up on a
different machine.

---

## Job Lifecycle With Approval

Without approval (default):

```
QUEUED → RUNNING → COMPLETED / FAILED
```

With `--require-approval` on the assigned worker:

```
QUEUED → PENDING_REVIEW → RUNNING → COMPLETED / FAILED
                ↓
           (reject / timeout / cancel)
                ↓
           QUEUED again (RejectedBy += this worker)
                ↓
           dispatched to next eligible worker
```

The job never gets stuck. If every available worker rejects it, it stays
`QUEUED` and will be tried again when new workers join.

---

## The ACK Problem

NATS JetStream delivers messages to a durable consumer with an AckWait timer
(default: 30 seconds). If the consumer doesn't acknowledge within that window,
JetStream re-delivers the message — to the **same consumer**. NAK also
re-delivers to the same consumer after a configurable delay.

This creates a problem for the approval flow: we need up to 60 seconds for a
human to decide, and we don't want the message to time out and get re-delivered
mid-decision. More importantly, we never want a rejection to NAK the message,
because NAK keeps the job pinned to the rejecting worker's queue.

**The solution: ACK immediately, handle ownership at the application layer.**

```
Worker pulls message from JetStream
        │
        ▼
Unmarshal TrainingJobRequest
        │
        ▼ (only if --require-approval)
msg.Ack()   ← removes from JetStream permanently
        │
        ▼
Set job → PENDING_REVIEW in KV
        │
        ▼
Subscribe to workers.decision.<workerID>.<jobID>  (NATS Core, ephemeral)
        │
   wait up to 60s
        │
   ┌────┴────┐
approve    reject / timeout / ctx cancel
   │              │
proceed      sendRejection()  →  workers.reject  (NATS Core)
             coordinator requeues with RejectedBy
```

Once `msg.Ack()` is called, the worker fully owns the job. There is no
JetStream fallback — if the worker crashes here, the stale job recovery
(30-second scan) picks it up and requeues it. See
[reliability.md](reliability.md) for that flow.

---

## Decision Signaling (NATS Core, Not JetStream)

The approval decision is sent from the coordinator HTTP API to the worker via
a plain NATS Core subject:

```
workers.decision.<workerID>.<jobID>
```

**Why NATS Core and not JetStream here?**

The decision is a one-shot ephemeral signal. It is only meaningful during the
60-second window when the worker is subscribed and waiting. Persisting it in
JetStream adds overhead with zero benefit — if the worker is gone when the
decision arrives, there is nobody to receive it.

NATS Core publish-subscribe delivers the message to the currently subscribed
worker immediately. The worker subscribes before setting `PENDING_REVIEW` in
KV, so there is no race between the subscription and any incoming signal.

Payload is a plain string: `"approve"`, `"reject"`, or `"cancel"`.
Anything other than `"approve"` is treated as rejection.

---

## Rejection Flow (Coordinator Side)

When a worker rejects or times out, it publishes to `workers.reject` (NATS
Core) with a JSON body:

```json
{ "job_id": "abc123", "worker_id": "worker-gpu-01" }
```

The coordinator's `SubscribeToRejections` handler:

1. Checks the job is not already in a terminal state (`CANCELLED`, `COMPLETED`,
   `FAILED`). If it is, the rejection is ignored — the user cancelled the job
   while the worker was deciding.
2. Calls `RequeueJobAfterRejection(kv, jobID, workerID)` which:
   - Reads the current `JobStatus`
   - Appends `workerID` to `status.RejectedBy`
   - Resets state to `QUEUED`, clears `WorkerID`
   - Preserves `RequestProto` so the job can be re-dispatched
3. Calls `TryDispatchQueued` for every currently free worker.

`TryDispatchQueued` skips any job where the candidate worker's ID appears in
`RejectedBy`. This prevents the job from bouncing back to a worker that already
said no.

```
workers.reject published
        │
coordinator reads job state
        │
    terminal? → ignore
        │
    no → RequeueJobAfterRejection
              RejectedBy += worker-gpu-01
              state → QUEUED
        │
    TryDispatchQueued(worker-gpu-02)
              RejectedBy check: worker-gpu-02 not in list → dispatch
```

---

## Cancel During PENDING_REVIEW

If a user cancels a job while it is in `PENDING_REVIEW`, the coordinator:

1. Writes `CANCELLED` to KV (before sending any signal — prevents the approval
   race: even if the worker approves in the next millisecond, the result handler
   sees `CANCELLED` and ignores the result).
2. Publishes `"cancel"` to `workers.decision.<workerID>.<jobID>` via NATS Core.

The worker's `awaitApproval` select receives `"cancel"`, returns `false`, and
calls `sendRejection`. The coordinator's rejection handler checks the job state,
finds it `CANCELLED`, and stops — no requeue happens.

```
user cancels
     │
coordinator: KV → CANCELLED
             publish "cancel" to workers.decision.*
                    │
             worker: receives "cancel" → approved=false
                    │
             sendRejection() → workers.reject
                    │
             coordinator: job is CANCELLED → skip requeue
```

---

## Stale Recovery for PENDING_REVIEW

If the worker dies while waiting for approval (power cut, crash), its KV
entry expires after the 1-minute TTL (workers KV is configured with that TTL).
The 30-second stale job scanner sees a job in `PENDING_REVIEW` with a
`WorkerID` that no longer exists in the workers KV, and calls `RequeueJob` on
it — resetting it to `QUEUED` without adding to `RejectedBy` (the worker didn't
reject, it just died).

---

## Enabling Approval on a Worker

```bash
edgegrid -client -require-approval
```

Or via environment variable if you prefer config files:

```bash
# not yet wired — use the flag for now
```

Workers without the flag behave exactly as before: they pull a job, run it
immediately, no `PENDING_REVIEW` state is ever set.

---

## API Endpoints

```
POST /jobs/{id}/approve
POST /jobs/{id}/reject
```

Both require the job to be in `PENDING_REVIEW` state. They publish to the
worker's decision subject and return `202 Accepted`.

The `worker` query parameter is **not required** — the coordinator reads
`status.WorkerID` from KV to know which decision subject to publish on.

```bash
curl -X POST http://coordinator:8080/jobs/abc123/approve
curl -X POST http://coordinator:8080/jobs/abc123/reject
```

These are what the dashboard's APPROVE / REJECT buttons will eventually call.
Right now they are wired to the coordinator but the dashboard buttons are
static (no fetch call yet).

---

## Sequence Diagram

```
submitter       coordinator        NATS           worker
    │                │               │               │
    │ POST /jobs     │               │               │
    │───────────────>│               │               │
    │                │ KV: QUEUED    │               │
    │                │ Publish train │               │
    │                │──────────────>│               │
    │                │               │ Fetch(1)      │
    │                │               │──────────────>│
    │                │               │   msg         │
    │                │               │<──────────────│
    │                │               │               │ msg.Ack()
    │                │               │               │ KV: PENDING_REVIEW
    │                │               │               │ Subscribe decision.*
    │                │               │               │
    │ POST /approve  │               │               │
    │───────────────>│               │               │
    │                │ Publish       │               │
    │                │ "approve"─────────────────────>│
    │                │               │               │ decision="approve"
    │                │               │               │ KV: RUNNING
    │                │               │               │ ... training ...
    │                │               │               │
    │                │               │ Publish result│
    │                │               │<──────────────│
    │                │ KV: COMPLETED │               │
    │                │<──────────────│               │
```
