# Plan: Coordinator Peer Mesh

**Status:** v1 in progress. The `peermgr` roster/credential split, `/peer/*`
routing, and the announce handler have landed; the pull loop has not.

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

JetStream KV is **CP**: its meta layer is a raft group, and raft's entire
purpose is refusing to serve unless a majority of members agree. EdgeGrid needs
**AP**: any coordinator remains useful alone, with no fixed number of nodes
live at once. That is a different consistency model, not a configuration
difference.

Two experiments against real embedded servers (nats-server v2.10.22), polling
`JetStreamIsLeader()`/`JetStreamIsCurrent()` directly:

| Setup | Result |
|---|---|
| JetStream node, one **unreachable dummy** route | `clustered=true leader=false current=false` — never stabilized (25s) |
| JetStream node, one **live, JetStream-disabled** peer, route established | identical — never stabilized (25s) |

`bootstrapRaftNode` (`raft.go:282-321`) explains it: the expected peer count
floors at 2 and **grows with every configured route**, while only live,
JetStream-speaking servers can vote. So a dummy route makes quorum strictly
*harder*, and a JetStream-disabled seed never votes. There is no partial state
— the instant `Cluster.Port != 0`, `standAloneMode()` goes false and the meta
layer commits to raft.

The consequence is worse than "clustering doesn't work": a clustered node with
no quorum **cannot create a bucket in its own store**, even at `Replicas: 1`.
Clustering makes a lone coordinator *less* functional than standalone.

## The model

Each coordinator is a shop with its own ledger. To get a total, you phone the
other shops and add up — you never write in someone else's ledger.

**The one rule everything follows from:**

> A record is only ever written by its owning coordinator. Peers ask; they never
> reach in.

This keeps the existing CAS logic in
`internal/coordinator/workerman/schedule.go` (`wm.kv.Update(key, data,
entry.Revision())`) valid and unchanged — every write still happens inside a
single KV store, so revision-based CAS still means what it always meant.

Conflict resolution is not needed, because **every key has exactly one writer by
construction**: a worker is attached to one coordinator, a job was accepted by
one, a token was minted by one. Cross-coordinator state is a disjoint union, not
a merge. No CRDTs, no conflict detection.

## Data classification

| Data | TTL | Class | Handling |
|---|---|---|---|
| `workers` | 1 min (`workerman/manager.go:52`) | Soft — rebuilt from heartbeats | Never replicated. Scatter-gather on read. |
| `jobs_state` | 24 hours (e.g. `jobsapi.go:136`) | Rolling window, not permanent | Owned by submitter. Scatter-gather for listing. |
| `node_auth` | **0 — permanent** (`coordinator.go:126`) | Hard, append-mostly | Optionally async-replicated (late phase). |
| `minted_tokens` | **0 — permanent** (`tokenmgr.go:36`) | Hard, append-mostly | Optionally async-replicated (late phase). |
| `roster` | **0 — permanent** | Membership, single-writer-per-key | Replicated by the membership layer below. |
| `peer_creds` | **0 — permanent** | Secret, pairwise | **Never leaves the box.** |
| `join_requests` | Local — handled where it arrives | — | Never shared. |

The `workers` TTL is load-bearing: it is a liveness cache, not a record of
truth. Replicating it would be replicating a cache. It gets a live query
instead.

---

# The membership layer

## Constraint

A learning about C through B must not make A↔C depend on B staying alive
afterward. This does **not** rule out gossip — it rules out **relay**. B may
tell A that C exists; B may never sit between A and C's traffic.

> **Discovery is transitive; connections are not.**

This also rules out a shared pub/sub topic ("B publishes, others subscribe"):
a topic lives on *somebody's* NATS server, so subscribing makes every
subscriber depend on that server. The fix is inversion — every coordinator
holds its own view and reconciles pairwise. N independent channels, not one
shared one.

## Data — implemented

```
roster      KV: NodeID → RosterEntry{NodeID, NatsURL, Incarnation, State, Cert}
peer_creds  KV: NodeID → EdgeCred{TokenPresent, TokenAccept}
```

Same `NodeID` key in both buckets links them. The split is structural, not
conventional: `RosterEntry` is what gets served over `GET /peer/roster`, so a
credential must not be a field on it. Redaction-at-the-call-site is the pattern
that eventually leaks; a type that never held the secret cannot.

Credentials are unshareable in principle anyway — only C's own `node_auth`/NATS
`Users` decide what is valid for C, so a credential B hands out for dialing C is
meaningless. **This is why discovery alone never establishes an edge.** Every
pair still needs its own handshake.

## Merge rule

One writer per key: the entry for C is only ever written by C. Merge is
**highest `Incarnation` wins**, per key. Same invariant as "The model" above,
applied to membership.

`Incarnation` is a **counter, not a timestamp** — laptop clocks skew. Two traps:

- Disk loss resets it to 0, so that node's updates look stale forever. Fix on
  rejoin: adopt `max(incarnation peers report about me) + 1`. *Deferred past
  v1 — see accepted gaps.*
- A removed node that was merely asleep can wake, bump its incarnation, and
  resurrect itself. Fix: `State=removed` is a tombstone that **wins regardless
  of incarnation**. A node may update its own URL; it may never undo its own
  removal.

The incarnation is what makes the whole design safe: the merge is idempotent
and order-independent, so **reconciliation frequency is a latency knob, never a
correctness one.** That is why the repair interval can be turned down freely.

## Propagation

Three mechanisms, each doing what it is actually good at.

**Push hint — fast path (v2).** On local roster change, the origin sends
`{node_id, incarnation}` — a hint, not the data — to every peer it already
holds a NATS connection to. Two rules:

- **Only the origin ever sends a hint about itself. Receivers never forward
  it.** Forwarding recreates exactly the B-relays-for-A dependency this section
  opened by rejecting.
- **The hint need not be reliable.** A receiver acts on it by pulling the
  authoritative entry, so a lost hint costs latency, not correctness.

**Pull on reconnect — event-triggered (v2).** When a peer connection
re-establishes (the real "laptop woke up" case), pull that peer once.

A sleeping node emits no signal that it is stale, so only an event on waking can
trigger catch-up. This is the asymmetry that decides push-vs-pull: a pull
failure is **self-evident to the requester** — its own request errors — while a
missed push is **invisible to the receiver**, which cannot distinguish "no news"
from "news I lost". Either side can die in either direction; only one of the two
leaves someone wrong without knowing it. Hence push needs a pull-shaped recovery
path, and pull never needs the reverse.

**Repair pull — anti-entropy backstop (v1).** Periodically reconcile with `k=3`
peers. This is the mechanism that actually provides the convergence guarantee;
everything else is latency optimization.

Exchange is digest-driven:

```
A → B:  digest {nodeID: incarnation, ...}      (~1KB at N=50)
B → A:  only the entries A is behind on
```

The digest is a **per-origin version map**. No `SHA256(roster)` short-circuit —
that saves ~1KB per request and matters at N in the thousands, not here. Skip it
until N justifies it.

Do not model the digest as a scalar "roster version". The roster as a whole has
no single writer, so B's version 17 and A's version 17 describe different sets
and `?since=17` is ill-defined. Only each *key* has a single writer, so the
version must be per-origin.

**Interval depends on whether push exists:**

| | Interval | Rationale |
|---|---|---|
| v1 (no push) | **60s** | Repair *is* propagation. ~4-5 rounds to converge ⇒ ~5 min worst case. Costs 2.5 req/s across a 50-node grid. |
| v2+ (push landed) | **15-30 min** | Rare-path safety net only. Longer intervals are justified by laptop battery/deep-sleep, not by traffic — digests already made rounds nearly free. |

## Peer selection for repair

```
candidates = peers with a live connection
pick       = 2 least-recently-synced from candidates + 1 uniform random
```

**Filter by liveness before ordering by staleness.** `last_sync` only advances
on success, so an offline peer is permanently least-recently-synced and would
pin itself to the top of the queue forever — every round burning all `k` slots
retrying corpses, exactly when the mesh is most degraded. Excluding dead peers
by the liveness filter (not by the ordering) avoids this; reconnect-triggered
pull already covers them when they return.

**Keep one uniform-random pick.** Fully deterministic selection loses the
independence the epidemic convergence bound rests on, and can settle into
correlated lockstep pairings. One random draw per round preserves a classical
floor for free.

`last_sync` advances on any successful digest exchange, including a no-delta
one — otherwise two converged peers each keep looking stale to the other.
Persisting it is optional: if lost on restart, everything looks maximally stale,
which produces "sync with everyone soon" — safe by accident.

Precedent worth reading when building this: Cassandra's gossiper is the same
shape — endpoint state map with generation/version, digest exchange, and a
deliberate split between picking a live node and (probabilistically) picking an
unreachable one.

## Trust

**v1: the tailnet is the boundary.** Treat tailnet membership plus presence in
the roster as sufficient to establish a peer edge. No keypairs, no signing, no
chain verification, no revocation.

This is not a new hole: the tailnet already gates every peer interaction in the
system, and admin-minted auth keys (`tokenapi`) already control who gets on it.
Cost, stated plainly: **any node on the tailnet can establish a peer edge and
obtain a NATS credential.** Coarse, admin-gated, reversible.

**v3: membership certs.** Each coordinator holds a keypair; a voucher issues a
signed cert (`{grid, node_id, pubkey, issued_at}`) that a stranger verifies
**offline**. Offline verification is forced by the opening constraint: B may be
dead when A first meets C, so "A asks B whether C is legitimate" reintroduces
the exact dependency being rejected. A signature A checks by itself does not.

The `Cert []byte` field already exists in `RosterEntry`, so v3 fills it in with
no schema change. The policy question — who may sign — is deferred with it:

| | Any active member vouches | Founder-signed only |
|---|---|---|
| New member joins via | any coordinator | must reach the founder |
| Founder offline | joins still work | no new joins (existing mesh fine) |
| One member compromised | attacker can inject nodes grid-wide | blast radius contained |

## Failure detection

Liveness is **each coordinator's own observed connection state**, used locally,
**never gossiped as fact**. No SWIM-style indirect probing, no gossiped
suspicion. Surfaced in the UI as "N of M peers reachable".

**Never auto-remove.** On a laptop grid, closing a lid must not delete a member.
Removal is an explicit admin action producing a `State=removed` tombstone,
retained indefinitely — dropping tombstones early lets a sleeping node wake and
resurrect a member you removed.

## What this is, precisely

**A single-writer membership map, reconciled by digest-driven delta exchange,
propagated by push hints, repaired by epidemic anti-entropy.**

Both classical epidemic mechanisms (Demers et al., 1987) are present: the push
hint is change notification (rumor-mongering's *trigger* without its *relay*),
and the repair pull is **anti-entropy** proper. Pull-based anti-entropy is
itself an epidemic process — the ignorant fraction squares each round
(`p → p²`), which is *faster* than push (`p → p/e`) once fewer than ~37% are
behind, and converges in O(log N) rounds. That is why sampling `k=3` peers
rather than all of them is sound: entries reach A by any path through the merge
rule.

`{nodeID: incarnation}` is structurally a version vector but is **not doing
version-vector work** — single-writer-per-key makes concurrent writes impossible
by construction, so there is no conflict detection, no siblings, no resolution
policy to implement. Calling it a version vector invites someone to build
conflict resolution this design provably never needs.

---

# Roadmap

## v1 — Membership over HTTP

No NATS peer connections. No certs. Pure HTTP over tsnet, which already works.

| Item | Where |
|---|---|
| Pin `Replicas` to 1; reject/warn on higher | `config.go:193` |
| Fix "skip if any peer exists" → per-peer check | `peers.go:28` |
| Announce reply carries full roster | `peers.go:90`, `peerapi.go:65` |
| `/peer/roster` on `tailnetMux` + `isOpenPath` | `router.go:224`, `router.go:90` |
| `GetRoster` takes `*http.Request`, accepts digest, returns delta | `peerapi.go:78` |
| Merge function + incarnation bump on self-change | `peermgr.go` |
| Pull-all on startup | `peers.go` |
| Repair loop, 60s, selection rules above | `peers.go` |

`peers.go:28` currently skips pairing once *any* peer exists, which blocks mesh
formation outright — it must become "do I have an edge with *this* peer?".

The announce-reply-carries-roster item matters more than it looks: a joining
node leaves its single handshake already knowing everyone, so it is not orphaned
if its voucher dies immediately afterward.

**Ships:** working mesh discovery, correct, ~5 min convergence.

## v2 — Peer connections + push hints

One `*nats.Conn` per peer via the existing `tsnetDialer`, with reconnect/backoff.
Adds the push-hint fast path and reconnect-triggered pull; relax the repair
interval to 15-30 min.

Safe to defer because v1's repair pull is already correct — push changes
latency, never outcome.

## v3 — Membership certs

Fill in `Cert`, generalize `peerapi.Announce` off the join nonce (`peerapi.go:41`
— that nonce only authenticates within a single join relationship, so it can
never authenticate A↔C, who never had one), decide the vouching policy.

## Accepted gaps in v1

- **Disk loss resets incarnation to 0** ⇒ that coordinator's updates are ignored
  as stale forever; it needs manual re-approval. Fix (`max(reported)+1`) deferred.
- **No removal endpoint.** Implement `State=removed` in the merge rule now (the
  field exists, it is cheap); defer the admin action that produces one.
- **Roster enumeration** by anyone on the tailnet. Accepted with the v1 trust
  decision.

---

# After the membership layer

These depend on v2's peer connections and are unchanged from the original plan.

**Worker visibility.** Responder on `coord.workers.list` returning local
`AllWorkers()`; fan-out + merge + timeout in `workersapi.List`. **This ships the
originally reported symptom fix.**

**Job visibility.** Same shape on `coord.jobs.list`.

**Remote dispatch.** Two subjects, deliberately split:

- `coord.workers.query` — **parallel, read-only.** Carries the
  `TrainingJobRequest`; each peer answers from its own bucket reusing
  `MeetsRequirements` (`schedule.go:69`) unchanged. Changes no state, so it is
  free to fan out, time out, and retry. A peer that does not answer inside the
  budget counts as "no capacity."
- `coord.jobs.claim` — **sequential, exactly one in flight.** B picks one
  candidate and asks only that peer, which runs `TryAssignWorker` and then
  publishes `jobs.train.<workerID>` on its *own* NATS, where the worker is
  actually subscribed. On CAS failure B falls through to the next candidate.

The split prevents double-running a job: broadcasting a claimable offer would let
two peers each assign a worker and both believe they own it. Keeping the only
state-changing call singular makes CAS sufficient — no distributed commit
protocol.

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

"Try local first" needs **no code at all**. `FindAndAssignWorker` already scans
the whole local bucket, and that bucket already contains only this coordinator's
own workers — registration writes land wherever the worker's single NATS
connection terminates (`worker/heartbeat.go:33` → `subscriptions.go:30`).

**Honest dispatch failures.** Fix `tryDispatch`'s silent `nil` to distinguish
busy / impossible / peers-unreachable.

**Log proxying.** Stream a peer's `jobs.logs.*` on demand.

**Auth replication (optional).** Only needed if a worker should connect to a
coordinator other than the one that approved it. Skip if workers stay pinned.

**Cleanup.** Delete the cluster port, `cluster.secret` minting,
`--routes`/`--cluster-port` flags, and the tsnet cluster-port bridging in
`agent.go:187-191`. Only after the above is proven.

## Jobs: who owns what

A job submitted to B, run by a worker attached to A.

**Dispatch routing** — B tries its own workers first (unchanged, zero network).
Only if no local worker is eligible does B ask peers. A answers yes only if it
has an eligible worker, so a coordinator only ever receives a job it can run.

**Record ownership — the submitter owns the record permanently.** B writes and
keeps job X in B's `jobs_state` for its whole life. A runs it and relays status
transitions (QUEUED → RUNNING → DONE) back to B. Logs are **not** relayed — they
stay on A and are proxied on demand.

The rejected alternative was transferring ownership to A when it takes the job.
That avoids the status-relay channel, but moves a durable record between two KV
stores non-atomically — a classic way to duplicate or lose state on a crash
mid-transfer. Records never moving is worth one low-volume status channel.

## Replicas: stays 1, permanently

`Replicas` (`internal/config/config.go:193`) stays 1 forever. R=3 is not merely
unnecessary — it is **unachievable**, requiring three JetStream nodes in one
cluster, and this design has no clusters. `NATS_REPLICAS > 1` is a footgun: a
standalone server cannot place a second replica, so setting it breaks bucket
creation at startup.

**Stored state therefore has no redundancy.** The mesh buys *availability*, never
*durability*. What "A's disk dies" costs:

- **`jobs_state` (24h TTL) — narrow.** Anything older than a day is gone by TTL
  regardless. What is lost is whatever was QUEUED or RUNNING at the moment of the
  crash, and unlike `workers`, nothing rebuilds it — a worker re-announces every
  heartbeat; a job has no equivalent signal. A checkpoint pointer
  (`JobStatus.CheckpointKey`) is lost while the blob survives orphaned.
- **`node_auth` / `minted_tokens` (no TTL) — full loss.** A worker keeps its token
  locally, but the coordinator's record that the token is valid is gone, so it
  rejects a token it minted itself. Every node it ever approved needs re-approval
  by hand.

## Failure modes

- **Fan-out latency on every read.** Needs a hard timeout (~250ms), partial
  results, and an explicit "N of M peers reachable" indicator. If this ever blocks
  the dashboard, the cure is worse than the disease.
- **A peer being down hides its workers.** *Correct* — those workers cannot run
  jobs while their coordinator is down. But the UI must say "partial view", not
  silently show a smaller grid.
- **A coordinator dying strands its in-flight jobs.** They resume when it returns;
  nothing else can adopt them.
- **Exactly-once dies under partition.** Two partitioned coordinators can both
  believe they own something. Ownership routing prevents this in the normal case,
  never during a genuine split. This is the price of AP.
- **A dead worker inside its TTL window still reads `Free`.** A worker that dies
  can be claimed, have the job published, and leave it `RUNNING` forever with
  nobody subscribed. **Pre-existing** — local dispatch has the identical hole — but
  the mesh does not fix it.
- **Duplicate worker IDs across coordinators** are ambiguous in the merged view.
  Ownership needs no stored field — a coordinator learns which peer owns a worker
  from *which connection answered* — so this is display-only, not a schema change.

## Open questions

- **Vouching policy — who may sign a membership cert?** Deferred to v3 with the
  certs themselves; v1's tailnet-boundary decision unblocks everything until then.
- **Retry trigger for remotely-queued jobs.** `TryDispatchQueued` fires only on
  local worker registration (`subscriptions.go:40`), so it never fires when a
  *peer's* worker frees up. Either a retry timer on the submitting coordinator
  (simple, laggy) or a "capacity freed" announcement (responsive, more moving
  parts). Start with the timer.
- Does the **status-relay** channel need durability (JetStream) or is core NATS
  request/reply with retry sufficient? The *roster* channel is settled (push hint
  + digest pull, no JetStream); this is a separate channel.
- known-gaps.md #7 (external-NATS coordinator addressing) — still relevant for how
  peers advertise their reachable NATS URL.

## Docs to update once this lands

- `bug/coordinators-never-cluster.md` — Options A and B are both superseded; the
  blocking unknown (standalone → clustered data survival) becomes moot.
- `nats-raft-replicas.md` — its framing ("Raft elects a new leader and the cluster
  keeps running") describes a topology EdgeGrid will not have.
- `p2p_agent_implementation_plan.md` — goal #4 as written is unachievable.
