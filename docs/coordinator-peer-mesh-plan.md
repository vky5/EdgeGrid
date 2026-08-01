# Plan: Coordinator Peer Mesh

**Status:** Proposed. No code written.

**Fixes:** [bug/coordinators-never-cluster.md](bug/coordinators-never-cluster.md)

**Supersedes:** goal #4 of [p2p_agent_implementation_plan.md](p2p_agent_implementation_plan.md)
("Decentralized High Availability ... automatically cluster via NATS's built-in
Raft engine"). That goal is not achievable in the shape it assumes — see below.

---

## The decision

**Coordinators never cluster their NATS servers. Every coordinator stays a
standalone JetStream node forever.** Cross-coordinator visibility is built one
layer up: coordinators hold plain NATS client connections to each other and
answer each other's questions.

The gate at `internal/natsserver/embedded.go:218` (`if len(cluster.Routes) > 0`)
stays exactly as written. It is correct. Nothing about the embedded NATS server
config changes.

## Why not clustering — settled empirically

The bug doc left this open. It is now closed, against clustering.

JetStream KV is **CP**: its meta layer is a raft group, and raft's entire
purpose is refusing to serve unless a majority of members agree. What EdgeGrid
needs is **AP**: any coordinator remains useful alone, and no fixed number of
nodes has to be live at once. That is not a configuration difference. It is a
different consistency model, and no amount of route configuration bridges it.

Two experiments were run against real embedded servers (nats-server v2.10.22),
polling `JetStreamIsLeader()`/`JetStreamIsCurrent()` directly rather than
inferring liveness from the client API:

| Setup | Result |
|---|---|
| JetStream node, one **unreachable dummy** route | `clustered=true leader=false current=false` — never stabilized (25s) |
| JetStream node, one **live, JetStream-disabled** peer, route established | identical — never stabilized (25s) |

Both hang forever, and `raft.go:282-321` (`bootstrapRaftNode`) says why:

```go
expected := len(knownPeers)
if expected < 2 {
    expected = 2            // hard floor
}
nrs := len(opts.Routes)
if expected < nrs+ngwps {
    expected = nrs + ngwps  // grows with every configured route
}
```

The expected peer count floors at 2 and **grows with each configured route**,
while only real, live, JetStream-speaking servers can ever cast a vote. So:

- A dummy/fake route cannot help — it adds to `expected` and contributes no vote.
  Adding fake peers makes quorum strictly *harder*.
- A JetStream-disabled seed cannot help — it forms the route fine but never
  becomes a JetStream member, so it never votes.
- There is no partial state. The instant `Cluster.Port != 0`,
  `standAloneMode()` (`server.go:1406`) goes false and the meta layer commits to
  raft. You cannot get the networking without the consensus obligation.

The practical consequence is worse than "clustering doesn't work": a clustered
node with no quorum **cannot create a bucket in its own store**, because bucket
creation is an administrative decision that must pass through the meta layer —
even at `Replicas: 1`. Clustering therefore makes a lone coordinator *less*
functional than leaving it standalone.

## The model

Each coordinator is a shop with its own ledger. To get a total, you phone the
other shops and add up — you never write in someone else's ledger.

**The one rule everything follows from:**

> A record is only ever written by its owning coordinator. Peers ask; they never
> reach in.

This is what keeps the existing CAS logic in
`internal/coordinator/workerman/schedule.go` (`wm.kv.Update(key, data,
entry.Revision())`) valid and unchanged — every write still happens inside a
single KV store, so revision-based CAS still means what it always meant.

Conflict resolution is not needed, because **every key has exactly one writer by
construction**: a worker is attached to one coordinator, a job was accepted by
one, a token was minted by one. Cross-coordinator state is a disjoint union, not
a merge. No CRDTs, no vector clocks, no gossip database.

## Data classification

| Data | TTL | Class | Handling |
|---|---|---|---|
| `workers` | 1 min (`workerman/manager.go:52`) | Soft — rebuilt from heartbeats | Never replicated. Scatter-gather on read. |
| `jobs_state` | 24 hours (e.g. `jobsapi.go:136`) | Rolling window, not permanent | Owned by submitter. Scatter-gather for listing. |
| `node_auth` | **0 — permanent** (`coordinator.go:126`, explicit "no TTL") | Hard, append-mostly | Optionally async-replicated (Phase 8). |
| `minted_tokens` | **0 — permanent** (`tokenmgr.go:36`, "a mint record's history persists") | Hard, append-mostly | Optionally async-replicated (Phase 8). |
| `join_requests` | Local — handled where it arrives | — | Never shared. |

The `workers` bucket having a TTL is load-bearing: it is a liveness cache, not a
record of truth. Replicating it would be replicating a cache. It gets a live
query instead.

`jobs_state` is not an audit log — anything older than 24h is already gone by
design, mesh or no mesh. `node_auth`/`minted_tokens` are the opposite: no TTL at
all, by deliberate choice. This asymmetry matters for what "losing a
coordinator's disk" actually costs — see Replicas, below.

## Jobs: who owns what

A job submitted to B, run by a worker attached to A.

**Dispatch routing** — B tries its own workers first (unchanged, zero network).
Only if no local worker is eligible does B ask peers "can anyone take this?" A
answers yes only if A has an eligible worker of its own. So a coordinator only
ever receives a job it can actually run.

**Record ownership — recommended: the submitter owns the record permanently.**
B writes and keeps job X in B's `jobs_state` for its whole life. A runs it and
relays status transitions (QUEUED → RUNNING → DONE) back to B over the peer
connection. Logs are **not** relayed — they stay on A, and a dashboard viewing
from B proxies the stream from A on demand.

The rejected alternative was transferring ownership to A when it takes the job.
That avoids the status-relay channel, but moves a durable record between two KV
stores non-atomically — a classic way to duplicate or lose state on a crash
mid-transfer. Records never moving is worth one low-volume status channel.

**While unassigned, the submitter holds it queued** and retries. This is exactly
where the bug doc's outstanding `tryDispatch` complaint becomes mandatory rather
than cosmetic: B must now distinguish "every eligible worker is busy, retry" from
"no worker in the entire grid can ever satisfy this" — and a third case that did
not exist before, "peers unreachable, unknown."

## Replicas: stays 1, permanently

`Replicas` (`internal/config/config.go:193`) stays 1 forever. R=3 is not merely
unnecessary here — it is **unachievable**, because it requires three JetStream
nodes inside one cluster, and this design has no clusters at all.

`NATS_REPLICAS > 1` becomes a footgun: a standalone server cannot place a second
replica, so setting it would break bucket creation at startup. Phase 0 should
pin or reject it rather than leave it configurable.

**This means stored state has no redundancy.** What the mesh buys is
*availability* (B keeps working, and keeps being useful, when A is down), never
*durability*. Real durability remains a separate, larger project and is not in
scope here. What "A's disk dies" actually costs, precisely, splits in two:

- **`jobs_state` (24h TTL) — narrow exposure.** Anything older than a day is
  already gone by TTL regardless; disk death changes nothing there. What's
  actually lost is whatever was **QUEUED or RUNNING at the exact moment of the
  crash** — and unlike `workers`, nothing rebuilds this. A worker re-announces
  itself every heartbeat; a job has no equivalent signal telling a coordinator
  that lost its memory "I'm still running, I still exist." If it had produced a
  checkpoint, the pointer to it (`JobStatus.CheckpointKey`) is what's lost — the
  checkpoint blob itself lives in a separate object-store bucket and may survive,
  orphaned with nothing connecting it back to the job that made it.
- **`node_auth` / `minted_tokens` (no TTL) — full loss, not time-bounded.** A
  worker keeps its own token saved locally (`credentials.go` persists
  `node.token` worker-side) regardless of what happens to the coordinator. But
  the coordinator's own record that the token is valid is gone, so on reconnect
  it rejects a token it minted itself. Every node that coordinator ever approved
  needs re-approval by hand — this does not heal on its own the way `workers`
  does.

So "if all nodes die, everyone starts from their own KV store" is true for
`workers`, and only partly true for the rest: a coordinator that loses its disk
comes back with a blank slate on those two buckets, not a recovered history.
Whether that's worth building around depends on how often a coordinator is
expected to genuinely lose its disk, as opposed to just restart.

## Phases

Each phase is independently shippable.

**Phase 0 — Guard rails.** Pin `Replicas` to 1 and reject/warn on higher.
Document that clustering is permanently disabled. No behavior change.

**Phase 1 — Peer registry.** Nothing currently tracks "who are the other
coordinators" — `credentials.go:52` only ever persists a single seed route. Seed
the peer list from the join response, persist it, gossip additions over the mesh
once one peer is known.

**Phase 2 — Peer connections.** One `*nats.Conn` per peer, dialed through the
existing tsnet dialer (`tsnetDialer`, already used for every other connection).
Reconnect/backoff handling.

**Phase 3 — Worker visibility.** Responder on `coord.workers.list` returning the
local `AllWorkers()`; fan-out + merge + timeout in `workersapi.List`. **This
ships the originally reported symptom fix.**

**Phase 4 — Job visibility.** Same shape on `coord.jobs.list`.

**Phase 5 — Remote dispatch.** Two subjects, deliberately split:

- `coord.workers.query` — **parallel, read-only.** Carries the
  `TrainingJobRequest`; each peer answers from its own bucket reusing
  `MeetsRequirements` (`schedule.go:69`) unchanged. Changes no state, so it is
  free to fan out, time out, and retry. A peer that does not answer inside the
  budget counts as "no capacity."
- `coord.jobs.claim` — **sequential, exactly one in flight.** B picks one
  candidate and asks only that peer, which runs `TryAssignWorker` and then
  publishes `jobs.train.<workerID>` on its *own* NATS, where the worker is
  actually subscribed. On CAS failure B falls through to the next candidate.

The split is what prevents double-running a job: broadcasting a claimable offer
would let two peers each assign a worker and both believe they own it. Keeping
the only state-changing call singular makes CAS sufficient — no distributed
commit protocol.

A stale query answer is expected and harmless — the answer is advisory, the claim
is authoritative. `TryAssignWorker` re-reads at claim time and rejects with one
of three explicit errors (record gone / already busy / CAS conflict), any of which
means B may safely try its next candidate.

**Timeout is not one of them.** CAS protects against a race; it does nothing for
a lost reply. If A assigns the worker and publishes the job but its reply is lost,
B moving on to the next candidate runs the job twice — on two GPUs, with two sets
of checkpoints, and no CAS anywhere catches it because nothing actually raced.
Training jobs are not idempotent, so this costs real money. The rule:

> Explicit error → safe to try the next candidate.
> Timeout → **unknown**; re-ask the same peer "did you take job X?" and only fall
> through once it answers no.

This requires the claim handler to be **idempotent on `jobID`**: a repeat claim
for a job that peer already assigned returns ok with the same worker rather than
consuming a second one.

Plus the status relay described above. The owning coordinator serves the claim
with its existing
`TryAssignWorker` (`workerman/schedule.go:106`) — already written for "assign
this specific worker," CAS included, and unchanged. Note this is the point at
which the CAS in `schedule.go:55`/`:130` stops being a no-op: today no second
coordinator can see B's bucket to race on it, but once peers can request a
specific worker, two of them genuinely can ask for the same one at once.

"Try local first" needs **no code at all**. `FindAndAssignWorker` already scans
the whole local bucket, and that bucket already contains only this coordinator's
own workers — registration writes land wherever the worker's single NATS
connection terminates (`worker/heartbeat.go:33` → `subscriptions.go:30`). The
current disjointness that makes the bug is exactly the ownership partition this
design needs.

**Phase 6 — Honest dispatch failures.** Fix `tryDispatch`'s silent `nil` to
distinguish busy / impossible / peers-unreachable.

**Phase 7 — Log proxying.** Stream a peer's `jobs.logs.*` on demand.

**Phase 8 — Auth replication (optional).** Only needed if a worker should be able
to connect to a coordinator other than the one that approved it. Skip if workers
stay pinned to their approving coordinator.

**Phase 9 — Cleanup.** Delete the cluster port, `cluster.secret` minting,
`--routes`/`--cluster-port` flags, and the tsnet cluster-port bridging in
`agent.go:187-191`. Only after the above is proven.

## Failure modes

- **Fan-out latency on every read.** Needs a hard timeout (~250ms), partial
  results, and an explicit "N of M peers reachable" indicator in the UI. If this
  ever blocks the dashboard, the cure is worse than the disease.
- **A peer being down hides its workers.** This is *correct* — those workers
  cannot run jobs while their coordinator is down, so listing them as available
  would be a lie. But the UI must say "partial view", not silently show a smaller
  grid.
- **A coordinator dying strands its in-flight jobs.** They resume when it
  returns; nothing else can adopt them.
- **Exactly-once dies under partition.** Two partitioned coordinators can both
  believe they own something. Ownership routing prevents this in the normal case,
  never during a genuine split. This is the price of AP and it is not fixable
  within this design — only bounded by keeping ownership explicit.
- **A dead worker inside its TTL window still reads `Free`.** The `workers`
  bucket TTL is 1 minute (`workerman/manager.go:52`), so a worker that dies can be
  claimed successfully, have the job published to `jobs.train.<id>`, and leave it
  `RUNNING` forever with nobody subscribed. **Pre-existing** — local dispatch has
  the identical hole today — but the mesh does not fix it and should not be read
  as fixing it.
- **Duplicate worker IDs across coordinators** are ambiguous in the merged view.
  Ownership itself needs no stored field — a coordinator learns which peer owns a
  worker from *which connection answered* — so this is a display/collision
  concern only, not a KV schema change.

## Open questions

- Peer list membership/removal: how does a coordinator learn a peer is gone for
  good, versus temporarily down? Currently unanswered.
- **Retry trigger for remotely-queued jobs.** `TryDispatchQueued` fires only on
  local worker registration (`subscriptions.go:40`), so it will never fire when a
  *peer's* worker frees up. Either a retry timer on the submitting coordinator
  (simple, laggy) or a "capacity freed" announcement from peers (responsive, more
  moving parts). Start with the timer.
- Does the status-relay channel need durability (JetStream) or is core NATS
  request/reply with retry sufficient?
- known-gaps.md #7 (external-NATS coordinator addressing) — still relevant for
  how peers advertise their reachable NATS URL to each other.

## Docs to update once this lands

- `bug/coordinators-never-cluster.md` — Options A and B are both superseded; the
  blocking unknown (standalone → clustered data survival) becomes moot, since no
  such transition ever happens.
- `nats-raft-replicas.md` — its framing ("if a NATS node crashes, Raft elects a
  new leader and the cluster keeps running") describes a topology EdgeGrid will
  not have.
- `p2p_agent_implementation_plan.md` — goal #4 as written is unachievable.
