# Bug: Coordinators Never Actually Cluster — Every Coordinator Runs Its Own Private Grid

**Status:** Open. Root cause identified, no fix written. Blocked on a design
decision and one unverified assumption (see below).

**Subsystem:** embedded NATS / cluster formation
(`internal/natsserver`, `internal/agent`)

**Related:** [known-gaps.md #8](../security/known-gaps.md) (single seed route),
[known-gaps.md #7](../security/known-gaps.md) (external-NATS coordinator addressing)

---

## Summary

A primary coordinator never opens its NATS cluster listener, so no secondary
coordinator can ever route to it. Both sides come up as fully standalone,
single-node JetStream servers that independently create their own private
copies of every KV bucket — `workers`, `jobs_state`, `join_requests`,
`node_auth`, `minted_tokens` — with identical names and completely disjoint
contents.

The result is not "the Workers tab looks empty on the second coordinator."
It is that **each coordinator runs a separate, independent grid that happens
to share a cluster name.** A job submitted to one can only ever run on
workers attached to that same one.

## Where It Lives

`internal/natsserver/embedded.go:218`, in `buildOpts` — the entire `Cluster`
options block is gated on there already being routes to dial:

```go
if len(cluster.Routes) > 0 {
    ...
    opts.Cluster = server.ClusterOpts{...}
    opts.Routes = routes
}
```

A bootstrap primary starts with zero routes — there is nothing to route *to*
yet, since no secondary has joined. So `opts.Cluster` is never populated and
the cluster port (default 6222) is never bound.

When a secondary later joins, `joinapi.Approve`
(`internal/coordinator/joinapi/joinapi.go:139-141`) correctly hands it
`nats://<primary>:6222` as a seed route. The secondary's own embedded server
*does* configure clustering (its route list is non-empty at its own `Start()`)
and dials out — at a port nothing is listening on.

Nothing ever promotes the primary afterward. `AddUser`/`SetUsers`
(`embedded.go:114,139`) are the only post-start reconfiguration paths, and both
only replace `.Users` in a `ReloadOptions` call; `baseOpts.Cluster` is carried
forward unchanged from a `Start()` that never set it. Confirmed by grep:
`buildOpts` is the only place in the codebase that ever assigns `opts.Cluster`.

## Symptoms

**1. Disjoint worker registries.** `workerman.NewWorkerManager`
(`internal/coordinator/workerman/manager.go:52`) calls
`GetOrCreateKV("workers", ...)` on whichever NATS the coordinator is connected
to. Two unrouted servers means two buckets named `workers` holding different
data. `workersapi.List` (`workersapi.go:13`) reads `AllWorkers()` from the
local one, so each coordinator only ever reports its own directly-attached
workers. This is the originally reported symptom.

**2. Cross-coordinator job dispatch fails silently, reported as success.**
`jobsapi.tryDispatch` (`internal/coordinator/jobsapi/jobsapi.go:74-79`):

```go
workerID, err := manager.FindAndAssignWorker(jobID, req)
if err != nil {
    log.Printf("no free worker for job %s, leaving queued: %v", jobID, err)
    return nil          // not an error
}
```

Submit a job to the primary when the only eligible worker is attached to the
secondary and it fails at two independent layers: the primary can't see that
worker as a candidate at all (symptom 1), and even if it could, dispatch
publishes to `jobs.train.<workerID>` on the primary's JetStream while the
worker is subscribed on the secondary's. Because "no eligible worker" returns
`nil`, the HTTP submit succeeds, the job is stored `QUEUED`, and it waits
forever for a dispatch trigger that can never match.

This is the most dangerous symptom: the failure is indistinguishable from the
legitimate, correct behavior of "every worker is currently busy." Nothing
surfaces to whoever submitted the job.

**3. The secondary dials a dead port indefinitely.** Its outbound route
connection can never establish. *Unverified:* how loudly this is logged, and
whether it surfaces anywhere in the TUI. Worth checking — a visible signal here
would have caught the root cause far earlier than the worker-visibility symptom
did.

**4. `EmbeddedServer.ClusterPort()` returns 0 on the primary.** A direct
consequence of the root cause (no `Cluster` opts means the zero value), not an
independent defect. Currently masked by the default-port fallback in
`joinapi.Approve`. Resolves when the root cause does.

## Why It Was Introduced

Not an oversight — a known, deliberately deferred gap. `internal/agent/agent.go:173-179`
states it outright:

> A lone bootstrap coordinator (no --routes, nothing joined yet) stays
> standalone — natsserver.buildOpts only enables clustering when clusterRoutes
> is non-empty [...] Cluster-mode wiring for a node that later gets joined is a
> separate follow-up, not handled yet.

The standalone gate itself is correct and necessary (see the next section). What
was never built is the follow-up: the transition from standalone to clustered
once a peer actually appears.

## Why the Obvious Fix Doesn't Work

"Just always set `opts.Cluster`" is not viable. Verified against
nats-server v2.10.22 source:

1. `server.go:1406` — `standAloneMode()` returns
   `opts.Cluster.Port == 0 && opts.Gateway.Port == 0`. Merely binding the
   cluster port flips this to `false`.
2. `jetstream.go:461` — when not standalone, JetStream init unconditionally
   calls `enableJetStreamClustering()`, and **propagates its error**. JetStream
   does not fall back to single-node mode; it fails to come up.
3. `jetstream_cluster.go:755` — that function returns an error when
   `configuredRoutes() == 0`, where `configuredRoutes()` is `len(opts.Routes)`,
   the static seed list — not the live connection count.

So a primary with a cluster port but no routes doesn't idle harmlessly waiting
for peers: **JetStream refuses to initialize at all.** That would break every
single-coordinator deployment, which is the overwhelmingly common case.

Reload can't do it either. `reload.go:2401` (`validateClusterOpts`) hard-rejects
any change to `Cluster.Port`:

```go
if old.Port != new.Port {
    return fmt.Errorf("config reload not supported for cluster port: old=%d, new=%d", ...)
}
```

Reload *does* support adding and removing routes (`diffRoutes`) — but only for a
server that is already clustered. The initial standalone → clustered transition
is not a reloadable change. It requires stopping and recreating the
`*server.Server` with new options.

Note this is a **one-time** cost, not per-join: once the primary is genuinely
clustered, every subsequent peer is just a route addition via reload.

## Bug Class

Deferred-transition gap — a correct steady-state design for two states
(standalone, clustered) with no implementation of the edge between them. The
system is correct at both endpoints and silently wrong in the only path that
actually reaches the second one.

Compounded by a **silent-failure amplifier**: the dispatch path's "no eligible
worker" case is legitimately non-exceptional, so the cluster-partition symptom
inherits its non-exceptional treatment and never surfaces as an error.

## Severity

**High for any multi-coordinator deployment; zero for single-coordinator.**

Single-coordinator setups never exercise this — one grid, one NATS, everything
works. The moment a second coordinator is added the deployment silently splits
in two, and the most visible consequence (jobs queueing forever) looks like
normal capacity pressure. Operators can lose substantial time before suspecting
a partition, because every individual component reports itself healthy.

## Security Assessment

**Not a vulnerability.** Fails closed in every direction: jobs don't run rather
than running somewhere unintended, and workers don't gain visibility or access
they shouldn't have. The cluster secret is minted and distributed but never
exercised, since no route is ever established. The cost is availability and
correctness, never unauthorized access.

## Blocking Unknown

**Does existing JetStream data survive a standalone → clustered restart?**

In standalone mode, streams are plain filestore with no raft. In clustered mode
they live in raft groups under a meta layer. On restart-as-clustered, the server
must re-adopt its on-disk streams into that meta layer.

This is unverified. A search of nats-server's own test suite found no
standalone → cluster migration test. If the answer is no, then any fix built
around promoting the primary in place **wipes its entire state the first time a
secondary joins** — every worker record, job, join request, and minted token.

Best guess, flagged as a guess: it survives, since filestore is keyed by
account + stream name and the sole meta peer should re-register on startup. This
must be settled empirically — two real embedded servers, data written before
promotion, verified present after — before any fix depending on it is written.

## Candidate Fixes

**Option A — promote in place.** Primary boots standalone as today. On first
peer approval, `EmbeddedServer` shuts down its `*server.Server` and recreates it
with `Cluster` populated and `Routes` pointing at the new peer (roughly a
`PromoteToCluster(ClusterConfig) error` method). Subsequent peers use the
existing reload path. Requires resolving the blocking unknown above, plus
handling the coordinator's own NATS connection surviving the bounce, and the
in-flight `joinapi.Approve` request that triggered it.

**Option B — secondaries don't embed NATS at all.** A secondary coordinator
connects as a client to the primary's NATS, exactly as a worker does, but with
`Server.Enabled = true` so it still serves the HTTP API, approves joins, and
dispatches. One NATS server, one `workers` bucket, one `jobs.train.*` subject
space — cross-visibility becomes structural rather than a feature that can
regress. Deletes the cluster port, cluster secret, tsnet route bridging, and this
entire bug class. `buildCoordinator` (`internal/agent/build.go:19`) already
accepts and merely passes through `embeddedNATS`, so a nil handle is an already
contemplated shape. Cost: the primary's NATS becomes a single point of failure —
though see below, it effectively already is. Requires closing
[known-gaps.md #7](../security/known-gaps.md), which this shape makes
load-bearing rather than an escape hatch; the secondary can hand out the same
`coordURL` it received in its own join response and saved as `nats.url`.

**Neither option delivers HA.** `Replicas` defaults to 1
(`internal/config/config.go:193`) and cannot be raised with two coordinators —
R=3 needs three nodes for quorum, R=2 has no tiebreaker. At R=1 every stream
lives on exactly one node, so even perfectly working clustering yields a shared
*view* with zero redundancy; killing either node takes out whichever arbitrary
subset of streams it happened to host. Real fault tolerance is a separate,
larger project requiring 3+ coordinators, R=3, and a migration path for
already-created streams.

The choice between A and B is therefore a product question, not a technical one:
**is a secondary coordinator meant to be a hot standby that survives the primary
dying, or just a second control-plane endpoint?** B is substantially simpler and
correct by construction; A is a prerequisite for eventual real HA.

## What's Still Open

- Everything. No fix is written.
- The design decision above is unmade.
- The blocking unknown is unverified.
- Symptom 3's logging behavior is unconfirmed.
- Independent of which option is chosen: `tryDispatch` returning `nil` for "no
  eligible worker" means a partitioned grid is indistinguishable from a busy
  one. Worth surfacing distinctly — a job queued with *zero* known workers
  capable of ever running it is a different condition from one waiting on a busy
  worker, and only the latter is normal.


## Solutions by VKY
- Remove the jetstream support from primary coordinator, then we can init a port. The secondary coordinator will have the port to connect to and form a cluster. (since the replication works on when the number of replica is 3+ then it wont be an issue). We can also start the secondary coordinator with primary coordinator

- use a hosted seed nats without jetstream to init the cluster

- give it fake IPs to satisfy the constraint to start in clusterd mode
