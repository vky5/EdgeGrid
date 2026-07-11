# Bug: Approved Credentials Never Reached Sibling Coordinators Until Restart

**Status:** Fixed.

**Subsystem:** [node-auth-propagation.md](../node-auth-propagation.md)

---

## Summary

In a multi-coordinator cluster, a node approved via coordinator A's HTTP API only ever got its credential applied to A's own embedded NATS auth config. Coordinator B, C, etc. — even though the approval was durably persisted and already replicated to their local JetStream storage — had no live mechanism to notice the new entry and apply it to their own NATS server. They'd only pick it up on their *own* next restart.

## Where It Lived

`internal/coordinator/coordinator.go`, `restoreApprovedNodes` (removed by the fix) — called exactly once, from `Coordinator.Start`, before the subscribe/serve loop:

```go
if c.natsServer != nil {
    if err := c.restoreApprovedNodes(); err != nil {
        log.Printf("warning: could not restore approved nodes into NATS: %v", err)
    }
}
```

`restoreApprovedNodes` did a one-shot `kv.Keys()` + `kv.Get()` bulk read of `node_auth`, then `ns.SetUsers(...)` once. Confirmed by grep across the codebase: `AddUser`/`SetUsers` had exactly two call sites total — this one (boot-only) and `joinapi.Approve`'s direct call (which only ever touched the *approving* coordinator's own `ns`, see the [sibling bug](external-nats-coordinator-approval-noop.md)). No `kv.Watch`/`WatchAll`, ticker, or event handler existed anywhere to react to a `node_auth` change after boot.

## Why It Was Introduced

Reasoned inference. `restoreApprovedNodes` reads as durability logic for a *single* coordinator process — "if I crash and restart, don't make every previously-approved node re-request access." That's a sound, narrow feature on its own. Clustering (multiple coordinators, each embedding NATS, routed together) appears to have been layered on afterward without this function being revisited — the mental model of "restore what I knew before I died" was never updated to "also stay in sync with what my siblings learn while I'm alive."

## Bug Class

Distributed state synchronization gap — specifically, a **one-shot sync mistaken for a sufficient sync**. The underlying data (`node_auth` KV) was correctly and automatically replicated by JetStream/Raft to every peer's local storage; the bug was entirely on the *consumption* side — nothing translated "new replicated data is sitting here" into "apply it to this process's live auth config" except at the one moment each process happened to boot.

## Severity

**Medium-to-High for active multi-coordinator clusters, zero for single-coordinator deployments.** In a cluster, a node approved through A would get inconsistent, confusing connection behavior depending on which coordinator's NATS it happened to reach (working against A, silently rejected by B) — hard to diagnose because nothing logs "I don't recognize this user" as distinct from any other auth failure. Single-coordinator setups never exercised this path at all, since there was only ever one `ns` to keep in sync with itself.

## Security Assessment

**Not a vulnerability.** Same fail-closed direction as the sibling bug: coordinators that hadn't yet learned about a credential rejected it, they didn't accept anything they shouldn't have. The cost was availability/consistency (a valid node sometimes couldn't connect), never unauthorized access.

## The Fix

Replaced the one-shot `restoreApprovedNodes` with a continuous watch loop, `watchApprovedNodes`, started the same place in `Coordinator.Start` (still gated on `c.natsServer != nil`):

- `kv.WatchAll()` on `node_auth` — nats.go replays the bucket's full current contents when the watcher starts (covering exactly what the old boot-time restore did), then keeps the channel open for the rest of the process's life.
- Each incoming `KeyValueEntry` (skipping the nil sentinel that marks end-of-replay, and skipping delete/purge operations — no revoke support yet) is applied via `c.natsServer.AddUser(cred)`.
- Runs identically on every embedding coordinator in the cluster — there's no special case for "the coordinator that happened to approve this," which also let the redundant synchronous `AddUser` call in `joinapi.Approve` be deleted (see the sibling bug doc).

Files touched: `internal/coordinator/coordinator.go` (function replaced, `nodeident` import dropped as it became unused), `internal/coordinator/joinapi/joinapi.go` (redundant `AddUser` call removed).

## What's Still Open

- No retry if `AddUser` fails for one specific entry mid-stream — that node stays unapplied on that one coordinator until its own restart.
- No retry if `kv.WatchAll()` itself fails to establish at `Start` — logged as a warning, never retried.
- Revocation (reacting to `KeyValueDelete`/`KeyValuePurge`) is explicitly not handled — deferred until node removal itself is designed.
