# Bug: Non-Embedding Coordinator Silently No-Ops on Join Approval

**Status:** Partially fixed. Registration and empty-address gaps are closed; the returned address is still wrong for real multi-host deployments (see "What's Still Open").

**Subsystem:** [node-auth-propagation.md](../node-auth-propagation.md)

---

## Summary

A coordinator started with an explicit `--nats-url` (external NATS, no embedded server) could still serve `POST /admin/join/{nodeID}/approve` and return `200 OK` — but the approval was hollow. The node's NATS credential was never registered anywhere, and the response handed the joining node an empty `coordURL`/`clusterRoutes`, giving it nothing to connect to.

## Where It Lived

`internal/coordinator/joinapi/joinapi.go`, inside `Approve`, pre-fix:

```go
var clusterSecret, coordURL string
var clusterRoutes []string
if ns != nil {
    if addErr := ns.AddUser(natsserver.NodeCred{Username: nodeID, Password: token}); addErr != nil {
        log.Printf("warning: NATS reload failed for node %s: %v", nodeID, addErr)
    }
    coordURL = fmt.Sprintf("nats://localhost:%d", 4222)
    if req.Role == joinmgr.RoleServer {
        clusterSecret = nodeident.LoadToken(dataDir, "cluster.secret")
        clusterRoutes = []string{fmt.Sprintf("nats://localhost:%d", 6222)}
    }
}
```

`ns` is the coordinator's embedded NATS server handle — `nil` whenever `--nats-url` was passed explicitly (see `internal/config/config.go`: setting an explicit URL disables `EmbedNATS` entirely, not just the address). When `ns == nil`, this whole block — credential registration *and* the address fields the joining node needs — was skipped. Yet execution fell through to `jm.Approve(...)` and `w.WriteHeader(http.StatusOK)` regardless, so the HTTP layer reported success unconditionally.

## Why It Was Introduced

Reasoned inference, not confirmed against original intent — no commit history or comment explains this directly. Every coordinator in the system's original shape embedded its own NATS server; `--nats-url` (connect to something external instead of embedding) reads like a later escape hatch. The `if ns != nil` guard was almost certainly added just to stop `ns.AddUser(...)` from a nil-pointer panic — a legitimate, narrow concern. But `coordURL`/`clusterRoutes` computation lived in the same lexical block by proximity, not by deliberate design, and nobody split "guard the one call that can panic" from "these values are needed regardless of embedding." With no one running a non-embedding coordinator in practice yet, the gap had no chance to surface.

## Bug Class

Incorrect guard scope — a nil-check written to protect one specific operation ended up gating unrelated logic that should have run unconditionally. Closest general pattern: **CWE-390, Detection of Error Condition Without Action** — the "I can't do the NATS part" condition was detected (implicitly, via `ns == nil`) but produced no error response; the caller was told the operation succeeded.

## Severity

**Medium — correctness/availability, not confidentiality/integrity.** A legitimate node fails to join when its assigned coordinator runs in external-NATS mode; the failure mode is invisible until the node's connection attempts start failing, with no error message pointing back to why. No unauthorized access is granted at any point — the defect only prevents a *valid* operation, it never permits an invalid one.

## Security Assessment

**Not a vulnerability.** This fails closed: a node that should have been able to join simply couldn't, which is the safe direction to fail in. The one adjacent point worth naming: a token was still generated and persisted to `node_auth` even though (pre-fix) it was never wired into any NATS auth config — a latent, unused secret sitting in storage. That's not a new exposure class; it's already covered by the existing secret-handling posture in `security/token-inventory.md`.

## The Fix

- `kv.Put` was already unconditional (outside the `ns != nil` block) — no change needed there.
- The direct `ns.AddUser(...)` call was removed from `Approve` entirely, in favor of every embedding coordinator's own `node_auth` watcher applying it locally (see [node-auth-propagation.md](../node-auth-propagation.md) and [the sibling stale-credentials bug](node-auth-stale-until-restart.md)) — this means registration now happens on whichever embedding coordinator(s) actually exist in the cluster, not gated on the *approving* coordinator specifically.
- The `if ns != nil` guard around `coordURL`/`clusterRoutes` was deleted; those values are now always computed, regardless of whether the approving coordinator embeds NATS.
- The now-dead `ns *natsserver.EmbeddedServer` parameter was removed from `Approve`, and the pass-through cascaded out of `router.StartHTTPServer` and its call site in `coordinator.go`.

Files touched: `internal/coordinator/joinapi/joinapi.go`, `internal/coordinator/router.go`, `internal/coordinator/coordinator.go`.

## What's Still Open

The address returned is still the literal `fmt.Sprintf("nats://localhost:%d", 4222)` / `6222` — no longer empty, but still wrong for any real multi-host deployment, and still meaningless on a coordinator that has no NATS running on this host at all. This needs a real advertised-address concept (what address can other machines actually reach this coordinator's NATS at) rather than a hardcoded loopback placeholder. Tracked as a separate, larger fix, not addressed here.
