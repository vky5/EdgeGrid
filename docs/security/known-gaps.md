# Known gaps

Things flagged in the original security pass that are still open. Ordered
roughly by how much I'd worry about each. None of these are fixed yet — this
is a punch list, not a changelog.

## 1. `data/` secrets aren't gitignored

Confirmed live right now: `git status --porcelain data/` shows `?? data/` —
untracked, which means a stray `git add -A` or `git add .` would stage
`data/admin.token`, `data/coord.secret`, `data/cluster.secret`,
`data/node.token`, and `data/node.id` straight into the repo. `.gitignore`
currently only excludes `data/nats/` (the JetStream storage directory), not
the token files sitting next to it.

**Fix:** add to `.gitignore`:
```
data/*.token
data/*.secret
data/node.id
```
(or just ignore `data/` wholesale and rely on `nodeident.SaveToken`'s
`MkdirAll` to recreate it — nothing in `data/` needs to be committed.)

## 2. `GET /join/{nodeID}` leaks node credentials to anyone who knows the node ID

This one's structural, not a simple oversight. `isOpenPath()`
(`internal/coordinator/api.go:98`) deliberately leaves `GET /join/{nodeID}`
unauthenticated — it has to be, since a newly-joining node has no credential
yet and needs to poll its own approval status.

The problem: once a node is **approved**, that same open endpoint returns
its `token` and (for server nodes) `cluster_secret` and `cluster_routes` in
plaintext (`handleJoinStatus`, `api.go:641` — only strips secrets when
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

## 4. The training subprocess inherits the full worker process environment

`cmd.Env = append(os.Environ(), "OUTPUT_DIR=...", "JOB_ID=...",
"TRAINING_CONFIG=...")` (`training.go:109`) — note the `os.Environ()` base.
Whatever's in the environment of whoever started the worker process (cloud
credentials, other apps' API keys, SSH agent socket, shell profile exports)
is visible to arbitrary user-submitted training code. Combined with #3,
this means: submit a job whose "training script" is actually
`import os; print(os.environ)`, and you've exfiltrated the operator's shell
environment.

**Fix:** build an explicit allowlist env for the subprocess instead of
inheriting the parent's — this was already discussed as tied to the broader
sandboxing decision (rootless Podman was the leaning candidate: GPU
passthrough works via `nvidia-container-toolkit`, gVisor's GPU support is
immature, Firecracker/microVMs don't do GPU passthrough at all, WASM has no
CUDA path). Whatever sandboxing approach lands, "don't pass `os.Environ()`
through" is a five-minute fix that doesn't need to wait for it.

## 5. No request size limit on job submission

`handleSubmitJob` (`api.go:278`) decodes the request body with
`json.NewDecoder(r.Body).Decode(&body)` directly — no
`http.MaxBytesReader` wrapping the body. `training_script` and
`requirements` are arbitrary-length strings. A large enough payload (or many
concurrent ones) can pressure coordinator memory since the whole body gets
buffered and then re-marshaled into a protobuf and written to JetStream.

**Fix:** wrap `r.Body` in `http.MaxBytesReader(w, r.Body, someLimit)` before
decoding.

## 6. SSE log stream doesn't escape training script output

`handleJobLogs` (`api.go:404-419`) writes `msg.Data` — raw stdout/stderr
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

---

None of these are fixed. This file exists so the punch list doesn't live
only in a chat transcript.
