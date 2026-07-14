# Known gaps

Things flagged in the original security pass. Ordered roughly by how much I'd
worry about each. This is a punch list, not a changelog — items get marked
FIXED in place rather than removed, since other docs cross-reference them by
number.

## 1. `data/` secrets aren't gitignored — FIXED

Was live: `data/admin.token`, `data/coord.secret`, `data/cluster.secret`,
`data/node.token`, and `data/node.id` were untracked and would've been staged
by a stray `git add -A`. `.gitignore` now has `data/*.token`, `data/*.secret`,
and `data/node.id` alongside the existing `data/nats/` entry — `git status
--porcelain data/` comes back empty. Left this entry in place (rather than
deleting it) since `token-inventory.md` cross-references it by number.

## 2. `GET /join/{nodeID}` leaks node credentials to anyone who knows the node ID

This one's structural, not a simple oversight. `isOpenPath()`
(`internal/coordinator/router.go:69`) deliberately leaves `GET /join/{nodeID}`
unauthenticated — it has to be, since a newly-joining node has no credential
yet and needs to poll its own approval status.

The problem: once a node is **approved**, that same open endpoint returns
its `token` and (for server nodes) `cluster_secret` and `cluster_routes` in
plaintext (`joinapi.Status`, `internal/coordinator/joinapi/joinapi.go:51` — only strips secrets when
`Status != StatusApproved`). There's no expiry on this, no one-time-use
enforcement, and no proof-of-possession check. Anyone who calls
`GET /join/{nodeID}` for an approved node gets that node's live NATS
credentials, indefinitely, as many times as they want.

The mitigating factor: `node_id` is a random 128-bit value
(`nodeident.LoadOrCreate`, `ident.go:32`), so it's not guessable by brute
force. But it's not treated as secret anywhere else in the system — it
appears in the `/claim/{nodeID}` URL a node operator visits in their browser
(shareable, logged in browser history, visible in referrer headers), in
coordinator stdout logs (`"approved join request: node=%s..."`), and
presumably in whatever join-request listing UI exists for admins. Any of
those are plausible leak vectors for something that then grants standing
access to a node's NATS credential.

This also matters more once the coordinator's HTTP port is reachable
directly from the internet (as opposed to only through the Next.js
gateway-token proxy) — which is exactly the direction the hosting decision
is headed (raw TCP exposure for NATS implies the HTTP port is likely
reachable too, depending on how it's fronted).

**Fix ideas (not yet decided on one):**
- Require the node to present a client-generated nonce (chosen at `POST
  /join` time) back on the polling `GET`, and only serve credentials to
  whoever has that nonce — turns "knows the node ID" into "possesses the
  nonce," which never gets displayed in a browser URL.
- Or: serve the credential exactly once (first successful poll after
  approval), then require the gateway token for any subsequent read —
  closes the door after the node has had its one chance to fetch it.

## 3. Training script execution has no sandboxing

`internal/worker/executor/training.go` runs `pip install -r requirements.txt`
and then the user-submitted Python script directly via `os/exec` on the
worker host — no container, no VM, no restricted user. This is arbitrary
code execution by design (that's the product), which makes the blast radius
of the next point worse than it'd otherwise be.

## 4. The training subprocess inherits the full worker process environment — FIXED

Was: `cmd.Env = append(os.Environ(), "OUTPUT_DIR=...", "JOB_ID=...",
"TRAINING_CONFIG=...")` (`training.go:109`) — note the `os.Environ()` base.
Whatever's in the environment of whoever started the worker process (cloud
credentials, other apps' API keys, SSH agent socket, shell profile exports)
was visible to arbitrary user-submitted training code. Combined with #3,
this meant: submit a job whose "training script" is actually
`import os; print(os.environ)`, and you've exfiltrated the operator's shell
environment.

Fixed by replacing `os.Environ()` with `allowlistedEnv()`
(`internal/worker/executor/training.go`) — only `PATH` and `HOME` are passed
through, with `OUTPUT_DIR`/`JOB_ID`/`TRAINING_CONFIG` appended on top. This
was already discussed as tied to the broader sandboxing decision (rootless
Podman was the leaning candidate: GPU passthrough works via
`nvidia-container-toolkit`, gVisor's GPU support is immature,
Firecracker/microVMs don't do GPU passthrough at all, WASM has no CUDA
path) — sandboxing itself (#3) is still open, this closes only the
environment-leak half.

## 5. No request size limit on job submission

`jobsapi.Submit` (`internal/coordinator/jobsapi/jobsapi.go:64`) decodes the request body with
`json.NewDecoder(r.Body).Decode(&body)` directly — no
`http.MaxBytesReader` wrapping the body. `training_script` and
`requirements` are arbitrary-length strings. A large enough payload (or many
concurrent ones) can pressure coordinator memory since the whole body gets
buffered and then re-marshaled into a protobuf and written to JetStream.

**Fix:** wrap `r.Body` in `http.MaxBytesReader(w, r.Body, someLimit)` before
decoding.

## 6. SSE log stream doesn't escape training script output

`jobsapi.Logs` (`internal/coordinator/jobsapi/jobsapi.go:192-208`) writes `msg.Data` — raw stdout/stderr
from the user's training script — directly into an SSE frame:
`fmt.Fprintf(w, "data: %s\n\n", msg.Data)`. If a script prints a crafted
sequence containing `\n\n` followed by `event: done\ndata: ...`, it can
inject a fake SSE event into the stream (e.g. spoof a premature "done" event
to the frontend, or otherwise mess with client-side event parsing).

Low severity today since only the job's owner or an admin can view the
stream (gated by `authorizeJob` in the Next.js route), so this is
self-inflicted at worst right now. Worth a quick fix (escape/prefix each
line so embedded `\n\n` can't create new SSE frames) if this stream is ever
exposed more broadly — e.g. shared logs, public job pages, or multi-tenant
viewing.

## 7. `coordURL`/`clusterRoutes` are wrong for a coordinator with no embedded NATS

Fixed for the common case: an embedding coordinator's join response now
builds `coordURL`/`clusterRoutes` from `--advertise-host`
(`joinapi.Approve` → `EmbeddedServer.AdvertiseHost()`) instead of a
hardcoded `"localhost"` — see [node-auth-propagation.md](../node-auth-propagation.md)
and [bug/external-nats-coordinator-approval-noop.md](../bug/external-nats-coordinator-approval-noop.md).

**Deliberately left open:** a coordinator started with `--nats-url`
pointing at an external, non-embedded NATS server (`ns == nil`) still gets
the hardcoded `"localhost"` fallback — there's no `EmbeddedServer` for it to
query, and no equivalent `--advertise-host`-style answer exists for "what's
the reachable address of a NATS server this coordinator doesn't itself
run." Fixing this properly would need the operator to separately state the
external NATS's own reachable address (distinct from `cfg.NatsURL`, which is
just how *this* coordinator reaches it, and isn't guaranteed to be reachable
from anywhere else — see the Docker Compose walkthrough this gap came from).

Not fixed because the external-NATS-coordinator mode itself is a rarely-used
escape hatch for this project's actual deployment shape (every coordinator
normally embeds its own NATS) — not worth the design investment unless
there's a concrete deployment that needs it.

## 8. A joining node only ever gets one seed route, sourced from whichever coordinator approved it

`joinapi.Approve` builds `clusterRoutes` as a single-element list — the
approving coordinator's own address. There's no mechanism for it to also
include a couple of *other* already-known coordinators as backup seeds,
because nothing exposes the embedded NATS server's live, gossip-discovered
peer list (`s.routes` internally) back out through `EmbeddedServer` for
`joinapi` to read. So while `node_auth` data itself is fully distributed
(replicated to every embedding coordinator, applied live via the
`watchApprovedNodes` watcher), the *discovery path for a brand-new node*
is not — it depends entirely on the one coordinator that happened to
answer this specific approval.

**Why this is narrower than it first sounds:** a joining node must
successfully reach that same coordinator's HTTP join API *before* it ever
gets a seed address at all, and the HTTP API and the embedded NATS server
live in the same process. So if the HTTP request succeeded, the NATS
server it's about to hand out as a seed is, in all but a narrow partial-failure
window, also up. The real exposure is that one specific coordinator process
dying in the gap between "approval sent" and "new node's connection
attempt" (or shortly after, before the new node's own gossip has picked up
other peers) — not a general "is the seed reliable" problem.

**Not fixed:** acceptable for now — closing it properly means exposing
live peer info out of the embedded NATS server and threading multiple
seeds through the join response, real plumbing for a narrow timing-window
risk. Revisit if this stops being an early-stage project.

---

Items 2 through 8 are still open (7 partially — see above). This file
exists so the punch list doesn't live only in a chat transcript.
