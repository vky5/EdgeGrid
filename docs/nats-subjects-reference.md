# NATS Subjects Reference — Who Publishes, Who Listens

A quick-lookup table for every subject constant in `internal/broker/broker.go`. For *why* JetStream/streams/consumers work the way they do, see [nats-notes.md](./nats-notes.md) — this doc is just "which side does what, on which subject."

## Table

| Subject (constant) | Pattern | Transport | Published by | Subscribed by | Purpose |
|---|---|---|---|---|---|
| `SubjectTrainPrefix` | `jobs.train.<workerID>` | JetStream | **Coordinator** — `jobsapi.Submit` (new job) and `dispatch.go:TryDispatchQueued` (requeued job) | **Worker** — `jobs.go:StartJobListener`, `PullSubscribe` on its own personal subject only | Dispatch one specific training job to one specific worker. Each worker only ever pulls its own subject — it never sees jobs meant for anyone else. |
| `SubjectResults` | `jobs.results` | JetStream, queue group `coord-results` | **Worker** — `jobs.go:handleJob`, after the training pipeline finishes (success or failure) | **Coordinator** — `subscriptions.go:SubscribeToResults` | Worker reports job outcome (success/failure, checkpoint key) back to the coordinator. Queue group means if you ever run multiple coordinator replicas, only one processes each result — not all of them. |
| `SubjectCancel` | `jobs.cancel` | JetStream | **Coordinator** — `jobsapi.Cancel`, when a user cancels a `RUNNING` job | **Every worker** — `jobs.go:StartCancelListener` (broadcast, `nats.DeliverNew()`) | Broadcast a cancellation to *all* workers at once, since the coordinator doesn't route this to a specific one by subject. Every worker checks its own local `cancels` map and only the one actually running that job acts on it; everyone else just acks and moves on. |
| `SubjectLogsPrefix` | `jobs.logs.<jobID>` | JetStream | **Worker** — via a callback wired in `internal/agent/agent.go`, invoked from the training executor's stdout/stderr capture during script execution | **Coordinator** — `jobsapi.Logs` (the `GET /jobs/{id}/logs` SSE handler), `ChanSubscribe` with `DeliverAll()` | Stream one job's training-script output from the worker running it, live, to whoever's watching the dashboard. `DeliverAll()` means a client connecting mid-job still gets every line from the start. |
| `SubjectRegister` | `workers.register` | JetStream, queue group `coord-register` | **Worker** — `heartbeat.go:RegisterWorker`, once at startup | **Coordinator** — `subscriptions.go:SubscribeToWorkerEvents` | Worker announces itself and its hardware (GPU/RAM/disk) so the coordinator can add it to the `workers` KV. |
| `SubjectHeartbeat` | `workers.heartbeat` | JetStream, queue group `coord-heartbeat` | **Worker** — `heartbeat.go:StartHeartbeat`, every 10s | **Coordinator** — same handler as registration | Worker self-reports free/busy. The coordinator **unconditionally overwrites** the worker's KV `State` with whatever it says — no cross-check against what the coordinator thinks that worker is doing (see the duplicate-execution caveat in [reliability.md](./reliability.md)). |
| `SubjectWorkerReject` | `workers.reject` | **NATS Core** (not JetStream — no replay, no ack, fire-and-forget) | **Worker** — `approval.go:sendRejection`, when it declines a `PENDING_REVIEW` job or times out | **Coordinator** — `rejections.go:SubscribeToRejections` | Worker says "not running this one." Coordinator requeues the job, records the rejecting worker so it isn't offered the same job again, and tries every other currently-free worker. |
| `SubjectWorkerDecisionFmt` | `workers.decision.<workerID>.<jobID>` | NATS Core | **Coordinator** — `jobsapi.Decision` (approve/reject) and `jobsapi.Cancel` (publishes `"cancel"` as the decision when cancelling a `PENDING_REVIEW` job) | **Worker** — `approval.go:awaitApproval`, one ephemeral subscription per in-flight approval wait | Relay a human's approve/reject/cancel decision to the one worker that's blocked waiting on it for that one job. |
| `SubjectWorkerStatsFmt` / `SubjectWorkerStatsWildcard` | `workers.stats.<workerID>` / `workers.stats.*` | NATS Core | **Worker** — `heartbeat.go`, every 10s alongside the heartbeat | **Coordinator** — `subscriptions.go:SubscribeToWorkerStats`, wildcard subscribe, extracts `workerID` from the subject itself | Live RAM/disk usage for the dashboard. Kept on a separate NATS-Core subject (not folded into the heartbeat proto) so new stats fields don't require a protobuf schema change. |

## Quick filter: who listens to what

**Worker subscribes to:** `jobs.train.<its own ID>` (pull), `jobs.cancel` (broadcast, push), `workers.decision.<its own ID>.<jobID>` (ephemeral, per in-flight approval).

**Coordinator subscribes to:** `jobs.results`, `workers.register`, `workers.heartbeat`, `workers.reject`, `workers.stats.*`, plus `jobs.logs.<jobID>` (only while an SSE client is actively watching that job's logs).

**Nobody else subscribes to anything** — there's no third listener role. Multi-coordinator setups would all independently subscribe to the same coordinator-side subjects (the queue groups on `jobs.results`/`workers.register`/`workers.heartbeat` are specifically there so only one coordinator replica processes each message, not all of them).

## One thing worth knowing while reading the code

**JetStream vs NATS Core isn't arbitrary here — it tracks whether replay/durability matters.** The job-lifecycle subjects (train, results, logs, cancel, register, heartbeat) are JetStream because losing one of these messages would actually break something (a job never gets dispatched, a result never lands, log history disappears for a reconnecting client). The approval/rejection/stats subjects are plain NATS Core because they're either short-lived signals with a narrow time window where being missed doesn't matter much (a decision that arrives after the worker's timeout is simply too late either way) or continuously-repeated live data where the next update supersedes the last one anyway (stats).

## Fixed since this doc was first written

- `jobs.progress` (`SubjectProgress`) was dead code — declared and registered in the stream but never published or subscribed anywhere. Removed from `broker.go` entirely. (It reappears as a *planned* subject in `training_extension.md`'s forward-looking design for live epoch/loss progress — that's a future feature, not a resurrection of this dead constant.)
- `jobs.logs.<jobID>` used to be published via a hardcoded string literal in `internal/agent/agent.go` instead of the `SubjectLogsPrefix` constant. Now uses `broker.SubjectLogsPrefix+jobID` like every other call site.
