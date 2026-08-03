# Auth architecture

How identity and trust actually flow through EdgeGrid, end to end. There are
three separate trust systems that only touch at specific handoff points, and
it's easy to conflate them, so this doc keeps them apart.

**System 1 — human identity (GitHub OAuth, via Next.js/NextAuth)**
Used for: who's allowed into the dashboard, who owns which job, who can
approve/reject nodes, and (via the grid-access allowlist, §4) who's allowed
to submit jobs at all.

**System 2 — machine identity (NATS username/password)**
Used for: which processes (coordinator, workers, server peers) are allowed to
publish/subscribe on the message bus.

**System 3 — transport membership (Tailscale/tsnet)**
Used for: which devices can reach the private coordinator/NATS addresses at
all. This is network reachability, not EdgeGrid authorization.

The coordinator's HTTP API sits between them, gated by a third thing (the
gateway token) that isn't identity at all — it's a single shared secret that
just proves "this call came from the trusted Next.js backend."

---

## 1. Human identity: browser → Next.js

- `web/lib/auth.ts` configures NextAuth with the GitHub provider. Session
  strategy is JWT (default), signed with `NEXTAUTH_SECRET`.
- On login, the `jwt` callback stashes the GitHub `login` (username) into the
  token; the `session` callback copies it onto `session.user.login`. That's
  the only piece of GitHub data that survives into the session.
- `isAdmin(login)` (`web/lib/auth.ts:35`) compares against
  `ADMIN_GITHUB_USERNAME` (server-only env var). There's also a
  `NEXT_PUBLIC_ADMIN_GITHUB_USERNAME` fallback for client components that just
  need to show/hide UI — but the actual authorization decisions all happen in
  server-only code (`web/lib/coordinator.ts:currentUser()`), which prefers the
  non-public var. So a user can't spoof admin by messing with client-visible
  env vars; they'd need to control the server's environment.
- `proxy.ts` (NextAuth middleware) protects `/jobs/:path*`, `/workers/:path*`,
  `/nodes/:path*` — redirects to `/login` if there's no session. `/` is
  intentionally excluded: `app/page.tsx` is a server component that checks
  the session itself and renders the dashboard or the public landing page.

The browser never sees anything coordinator-related. It only ever holds a
NextAuth session cookie and talks to same-origin `/api/*` routes.

## 2. Next.js → coordinator: the gateway token

- `web/lib/coordinator.ts` is a server-only module (never imported by a
  client component). `coordFetch()` attaches
  `Authorization: Bearer ${COORDINATOR_ADMIN_TOKEN}` to every request to the
  coordinator.
- On the coordinator side, `requireGateway()` (`internal/coordinator/router.go:47`)
  wraps the entire mux and does a constant-time comparison
  (`crypto/subtle.ConstantTimeCompare`) of that bearer token against the
  coordinator's own copy, loaded from `data/admin.token`
  (`internal/agent/agent.go:155`). Any request without a valid token gets
  401, except a short allowlist in `isOpenPath()` (`internal/coordinator/router.go:69`) — see below.
- This token is **not tied to any GitHub user**. It's a single shared secret
  between "the Next.js backend" and "the coordinator." Anyone who has it can
  do anything the coordinator's API allows — submit jobs as anyone, read any
  job, approve/reject node joins, download any checkpoint.
- **Per-user authorization is enforced entirely in Next.js**, not the
  coordinator. `authorizeJob()` (`coordinator.ts:40`) fetches the job, reads
  `submitted_by`, and checks it against the session user (admins bypass this).
  The coordinator itself has no idea who "the user" is — it just trusts
  whatever the gateway-token holder tells it (e.g. `X-Submitted-By` header on
  job submission, `internal/coordinator/jobsapi/jobsapi.go:108`).

This is a deliberate BFF (backend-for-frontend) pattern: the coordinator's
attack surface is reduced to "does this caller have the one shared secret,"
and all the nuanced human-identity logic lives in the Next.js layer where the
GitHub session actually is.

### Open paths (no gateway token required)

From `isOpenPath()`:
- `GET /health`
- `POST /join` — a node submitting its first-ever join request has no
  credential yet, so this has to be open.
- `GET /join/{nodeID}` — a pending/approved node polling its own status.
  This one deserves scrutiny — see `known-gaps.md`.

`POST /join/claim/{nodeID}` is **not** open — it requires the gateway token,
meaning only the trusted Next.js backend can call it (see node-claim flow
below).

## 3. Node join & claim flow (where the two identity systems meet)

This is the one place GitHub identity and NATS identity touch.

1. A new node (worker or server) starts `tsnet`. If the operator supplied
   `--ts-authkey` / `TS_AUTHKEY`, tsnet consumes that Tailscale auth key and
   joins silently; otherwise it emits an interactive Tailscale login URL. The
   Tailscale step only gets the device onto the private network. It does not
   approve the EdgeGrid node or grant NATS credentials.
2. The node generates a random 128-bit `node.id`
   (`internal/nodeident/ident.go:22`) on first boot and persists it locally.
   It also generates a local `node.nonce`; later polling must present this as
   `X-Node-Nonce` so knowing a node ID is not enough to fetch its
   approval result.
3. It `POST /join`s with `{node_id, role, hostname, nonce, auth_key_hash, ip}`
   — open endpoint, stored in the `join_requests` JetStream KV bucket
   (`internal/joinmgr/joinmgr.go`) with status `pending`. `auth_key_hash` is
   only present when the node used a minted Tailscale auth key; it lets the
   Tokens tab correlate "this key was activated by this node" without storing
   the raw key.
4. The node operator opens `/claim/{nodeID}` in a browser
   (`web/app/claim/[nodeID]/page.tsx`), signs in with GitHub if not already,
   and the page fires `POST /api/claim/{nodeID}`
   (`web/app/api/claim/[nodeID]/route.ts`) — this Next.js route requires a
   session (`currentUser()`), then calls the coordinator's
   `POST /join/claim/{nodeID}` with `{github_username}` attached server-side.
   This is the only step where a GitHub identity gets recorded against a
   node — `joinmgr.Claim()` just stores the username as metadata on the join
   request. It's bookkeeping ("this human says this node is theirs"), not a
   credential.
5. An admin (checked via `isAdmin` in the Next.js route,
   `web/app/api/admin/join/[nodeID]/[action]/route.ts`) approves the request.
   This calls the coordinator's `POST /admin/join/{nodeID}/approve`
   (`internal/coordinator/joinapi/joinapi.go:70`, `joinapi.Approve`), which:
   - generates a random 32-byte hex token (`nodeident.RandomToken(32)`) —
     this becomes the node's NATS password,
   - persists it in the `node_auth` KV bucket (survives coordinator
     restarts),
   - hot-reloads the embedded NATS server to add a user
     `{Username: nodeID, Password: token}` (`natsserver.AddUser`),
   - if the node is joining as a `server` (cluster peer, not just a worker),
     also attaches `cluster.secret` and route URLs.
6. The node, which has been polling `GET /join/{nodeID}` with `X-Node-Nonce`
   all along, sees `status: approved` and receives its token/cluster secret in
   that same response. It saves them locally (`data/node.token`,
   `data/cluster.secret`) and reconnects to NATS using them.

After this point, the node authenticates to NATS purely by
username(`nodeID`)/password(`token`) — GitHub identity plays no further role.
There's no cryptographic binding between "GitHub user X claimed this node"
and "NATS connection with this token" beyond the coordinator's own
bookkeeping in `join_requests`/`node_auth`. If you ever need to prove
"this specific GitHub user controls this specific worker" for something
security-critical, that link is administrative record, not a verifiable
credential.

## 4. Grid access: a third, separate gate on top of both systems

Everything above answers "who is this" (GitHub OAuth) and "is this machine trusted" (NATS credentials). Neither answers "is this GitHub user allowed to submit jobs" — that used to just be "yes, if they're signed in," which meant any GitHub account could dispatch work to the grid without ever contributing a worker.

`internal/usermgr` adds an `approved_users` KV bucket, checked by `POST /api/jobs` (`isApprovedUser()`, `web/lib/coordinator.ts`) before a submission is forwarded to the coordinator. Admins always pass; everyone else needs a grant — either automatic (approving a claimed node also grants its owner access) or direct (an admin grants a username, no node required). Full flow in [grid-access.md](../grid-access.md).

This is deliberately a separate KV from `join_requests`/`node_auth`, not a derived check against node status — losing or reformatting a node doesn't silently revoke the owner's submission rights, because that was never how they were granted in the first place.

## 5. Machine identity: NATS username/password

- The coordinator's embedded NATS server (`internal/natsserver/embedded.go`)
  is configured with a static user list at boot (`buildOpts`,
  `credsToUsers`) plus hot-reloaded additions via `AddUser`/`SetUsers`.
- Every connecting process — the coordinator's own client connection
  (`coord.secret`), each worker, each server peer — authenticates with a
  username/password pair. There's no TLS client-cert layer; it's plain SASL
  password auth on top of whatever transport (plain TCP today, `wss://` if
  it ever moves behind a tunnel).
- Cluster route connections (coordinator-to-coordinator, for Raft) use a
  separate shared credential: username `cluster`, password `cluster.secret`
  (`embedded.go:156-157`). This is a single shared secret for *all* cluster
  peers — anyone with it can join the cluster as a full route peer, which is
  a much bigger trust grant than a worker's client credential (see
  `token-inventory.md`).

## 6. Transport membership: Tailscale/tsnet

Every `Agent` brings up a userspace Tailscale node with `tsnet.Server`
(`internal/agent/agent.go`). Its state lives under `data/tsnet/`, and the
node's Tailscale IPv4 is saved as `data/tailscale.ip` for later dashboard
startup and operator display.

There are two ways for a device to join the tailnet:

- **Interactive login** — no auth key is supplied, so tsnet prints a
  Tailscale login URL. The onboarding TUI surfaces that URL.
- **Minted auth key** — a primary coordinator with Tailscale OAuth credentials
  configured can call `POST /admin/tokens/mint`. EdgeGrid asks the Tailscale
  API for a single-use, pre-authorized, tagged auth key, shows the raw key
  exactly once, and stores only a hash/metadata record in `minted_tokens`.

The Tokens tab is therefore about network admission, not EdgeGrid permission.
Revoking an unused minted key stops another device from consuming it, but a
device that already joined still needs to be removed through Tailscale itself.
Separately, the EdgeGrid admin must approve the node join before it receives a
NATS `node.token` or server `cluster.secret`.

## Summary picture

```
Browser (GitHub session cookie only)
   │  same-origin, cookie auth
   ▼
Next.js API routes (web/app/api/**)
   │  - checks NextAuth session
   │  - enforces per-user / admin authorization
   │  - job submission also checks grid access (usersapi) — admin or granted, or 403
   │  - attaches GitHub login to outgoing requests where relevant
   ▼  Authorization: Bearer COORDINATOR_ADMIN_TOKEN
Coordinator HTTP API (internal/coordinator/router.go + jobsapi/joinapi/usersapi/workersapi)
   │  - trusts the gateway token blindly, no concept of GitHub identity
   │  - open paths: /health, POST /join, GET /join/{nodeID}
   ▼
Coordinator internals (joinmgr, node_auth KV, embedded NATS)
   │  - issues per-node NATS username/password on approval
   ▼
NATS (workers, server peers) — plain username/password auth, no GitHub identity at all

Tailscale/tsnet sits beside this path as the private network layer:
joining devices first need tailnet reachability, then EdgeGrid join approval,
then NATS credentials.
```
