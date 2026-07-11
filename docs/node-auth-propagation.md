# Node Credential Propagation (node_auth)

## Why This Exists

Every worker or secondary coordinator that joins the grid gets its own NATS username/password, minted once by whichever coordinator approves it (`joinapi.Approve`). That credential has to end up two places: durable storage (so it survives restarts) and every embedding coordinator's live NATS auth config (so the node can actually connect, no matter which coordinator's NATS it lands on). This doc covers how that second part works — the part that isn't obvious just from reading `Approve` in isolation.

See [`security/auth-architecture.md`](security/auth-architecture.md) for the end-to-end human-identity-to-NATS-credential flow this sits inside, and [`security/token-inventory.md`](security/token-inventory.md) for what the token itself protects.

---

## The Credential Lifecycle

1. **Mint.** `joinapi.Approve` generates a random 32-byte hex token (`nodeident.RandomToken(32)`) — this becomes the node's NATS password, username is the node's own ID.
2. **Persist.** The token is written into a JetStream KV bucket, `node_auth` (`kv.Put(nodeID, token)`), unconditionally — this happens regardless of whether the approving coordinator itself embeds NATS. `node_auth` has no TTL; entries are permanent until explicitly removed (removal isn't implemented yet).
3. **Apply.** Every coordinator that embeds its own NATS server (`c.natsServer != nil`) runs a background watch loop (`Coordinator.watchApprovedNodes`, `internal/coordinator/coordinator.go`) subscribed to `node_auth`. Whenever a `Put` lands — this one or any other coordinator's — the watcher calls `c.natsServer.AddUser(cred)`, which hot-reloads that coordinator's own embedded NATS server (`ns.ReloadOptions`, `internal/natsserver/embedded.go`) to recognize the new user, without dropping existing connections.

```
joinapi.Approve (any coordinator)
        │
        ▼
   node_auth KV  ──── JetStream replication (Raft) ────►  every JetStream-capable node's local copy
        │                                                          │
        │ (this coordinator's own watcher, if it embeds NATS)      │ (every other embedding coordinator's own watcher)
        ▼                                                          ▼
  ns.AddUser(cred)                                          ns.AddUser(cred)
  (local, in-process reload)                                (local, in-process reload)
```

---

## Two Layers That Look Similar But Aren't

This subsystem only makes sense once you separate two things that are easy to conflate (see [bug: stale credentials until restart](bug/node-auth-stale-until-restart.md) for what happens when they get conflated):

- **JetStream KV replication** (`node_auth`'s `replicas` setting) is a storage-layer guarantee. It copies the bucket's raw data to every JetStream-capable peer via Raft. This happens automatically, with no application code involved, the moment `kv.Put` is called anywhere.
- **NATS core auth config** (`server.Options.Users`) is a separate, non-JetStream, in-memory list on each embedded `*server.Server`. JetStream replication has zero awareness of it. Nothing in the NATS server itself bridges "a key changed in a KV bucket" to "recognize this as a valid login" — that bridge only exists because `watchApprovedNodes` explicitly builds it.

Replication makes sure the *data* is everywhere. The watcher makes sure the *auth config* is everywhere. Before this subsystem existed in its current form, only the first was true.

---

## Why a Live Watch, Not a Boot-Time Restore

The watcher replaces an older function, `restoreApprovedNodes`, which did the "apply KV contents to local auth config" step exactly once, at coordinator startup. That was sufficient for a single coordinator recovering its own memory after a restart, but not for a cluster of several coordinators running simultaneously — a credential approved by coordinator A never reached coordinator B's auth config until B itself restarted. See [bug: stale credentials until restart](bug/node-auth-stale-until-restart.md) for the full writeup.

`nats.go`'s `KeyValue.WatchAll()` conveniently does both jobs with one mechanism: it replays the bucket's entire current contents when the watcher first starts (covering what the old boot-time restore did), then stays open and delivers every subsequent change for the rest of the process's life. One code path, no separate "restore" step needed.

---

## What's Deliberately Not Handled Yet

- **Revocation.** The watcher only reacts to `KeyValuePut`; `KeyValueDelete`/`KeyValuePurge` entries are explicitly ignored (see the comment in `watchApprovedNodes`). Removing a node's access — planned, not built — will need its own handling here (likely a NATS `RemoveUser` counterpart to `AddUser`).
- **Retry on apply failure.** If `ns.AddUser` fails for one specific entry (e.g. a transient `ReloadOptions` error), it's logged and the loop moves on — that node stays un-added on that one coordinator until the process restarts and replays the bucket again. No per-entry retry exists.
- **Watch-establishment failure.** If `kv.WatchAll()` itself fails at `Start` (e.g. JetStream not ready yet), it's logged as a warning and never retried — this coordinator simply never learns about any node_auth changes, silently, for its whole lifetime.
