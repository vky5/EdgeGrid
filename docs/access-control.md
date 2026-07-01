# Access Control & Multi-Machine Setup

## The Problem

By default, any node that knows the NATS server URL can connect and register
as a worker. The coordinator will happily dispatch jobs to it. There is no
identity verification, no allowlist, and no way to kick a bad actor without
rotating a shared credential that affects every other node.

This document describes the two-layer gate model and how to wire up machines
across a network.

---

## How Machines Connect Right Now

All nodes — coordinator and workers — connect to the **same NATS server**.
That server is the only hub. Workers do not talk to each other directly.

```
                        NATS server
                       (hub / broker)
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
   coordinator         worker-gpu-01     worker-gpu-02
   (PC-A :8080)        (PC-B)            (PC-C)
```

To add a new machine:

```bash
edgegrid -client -nats=nats://<NATS-HOST>:4222
```

The coordinator and NATS can run on the same machine:

```bash
# PC-A: run both
nats-server -p 4222
edgegrid -server -nats=nats://localhost:4222

# PC-B, PC-C, etc.
edgegrid -client -nats=nats://PC-A-LAN-IP:4222
```

### Same LAN

Works immediately. Use the machine's local IP (`192.168.x.x`).

### Different Networks (Internet)

The NATS port must be reachable. Three practical options:

| Option | Effort | Cost |
|--------|--------|------|
| **Tailscale** | Lowest — one command per machine | Free |
| **VPS running NATS** | Medium — any $5/mo server | ~$5/mo |
| **Router port-forward** | Medium — varies by router | Free, unreliable |

**Tailscale** is the recommended path for early testing. Each machine installs
the client, joins your tailnet, and gets a stable private IP. No port
forwarding, no firewall rules:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up
# NATS now reachable at the Tailscale IP shown by `tailscale ip`
```

---

## Layer 1 — NATS Token Auth (Connection Gate)

This is the hard outer wall. Without a valid token, a client cannot connect to
NATS at all — it never sees any subjects or messages.

### Configure the NATS server

```
# nats-server.conf
port: 4222

authorization {
  token: "your-long-random-secret-here"
}
```

```bash
nats-server -c nats-server.conf
```

### Workers connect with the token

```bash
edgegrid -client -nats=nats://your-long-random-secret-here@host:4222
```

Or via env var:

```bash
NATS_URL=nats://your-token@host:4222 edgegrid -client
```

Anyone without the token gets a `nats: Authorization Violation` at the
TCP handshake. They cannot read or publish to any subject.

### Why a shared token is enough for early testing

A single token shared with approved participants is operationally simple.
When someone needs to be removed, rotate the token and redistribute.
For larger deployments, see NKeys below.

---

## Layer 2 — Coordinator Allowlist (Application Gate)

Even after passing the NATS token check, a worker must be in the coordinator's
allowlist to receive jobs. The coordinator maintains a `worker_allowlist`
KV bucket. On `workers.register`, if the worker's ID is not in the allowlist,
the coordinator drops the message — the worker is connected but invisible.

```
worker registers → coordinator checks allowlist
                          │
              ┌───────────┴───────────┐
           approved               not approved
              │                       │
    proceeds normally         ignored — no jobs
                                dispatched ever
```

This gives you per-worker control without rotating the global token. You can:
- Block a specific worker without affecting others
- See which workers have been approved (allowlist KV is inspectable)
- Add/remove workers dynamically without restarting anything

> **Status**: the allowlist gate is designed but not yet implemented.
> See [futures.md](futures.md) for the implementation plan.

---

## Invite-Only Testing Model

The model being considered:

1. Applicant submits a form with GitHub handle + a short message
2. Existing approved members vote via the dashboard
3. When ≥ N votes are cast (fixed threshold, e.g. 5), membership is granted
4. Coordinator adds the worker ID to the allowlist KV
5. The new member receives a NATS credential out-of-band (email, DM)

The fixed-threshold approach (N votes regardless of total membership) scales
better than a percentage: at 100 members, a 50% vote requirement means 50
approvals per new person, which kills momentum.

---

## Per-User Identity with NATS NKeys

For production, each approved user gets their own cryptographic keypair (NKey)
instead of sharing one token. NKeys are ed25519 key pairs. The public key goes
into the NATS server config; the user keeps their private key.

```
# nats-server.conf (NKey-based)
authorization {
  users = [
    { nkey: "UABC123..." }   # user-A public NKey
    { nkey: "UDEF456..." }   # user-B public NKey
  ]
}
```

Benefits over a shared token:
- Revoking one user does not affect others — remove their public key from config
- Each connection is cryptographically identified, no passwords in URLs
- Key rotation per-user without any coordination

Workers connect:
```bash
edgegrid -client -nats=nats://host:4222 -nkey=/path/to/user.nk
```

This is the target architecture for the public testing phase.

---

## Summary

| Layer | What it gates | When to use |
|-------|--------------|-------------|
| NATS token auth | Network connection | Always — set this up first |
| Coordinator allowlist | Job dispatch | Invite-only or multi-tenant |
| NATS NKeys | Per-user identity | Production / revocable access |

For a first deployment: configure a NATS token, use Tailscale for cross-network
connectivity, and implement the allowlist when you start onboarding people you
don't personally know.
