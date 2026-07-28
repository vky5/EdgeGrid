# TUI context (handoff)

Written for an agent picking this up cold. Covers why the TUI exists, what's
actually built vs. stubbed, and — importantly — a NATS clustering bug saga
from this session that's easy to re-break if you touch `internal/natsserver`
or `internal/agent` without knowing the history.

## Why a TUI at all

The Next.js web dashboard (`web/`) was deleted entirely — full removal, not
a redesign in progress. Reasoning: the operator wants a single Go binary
that a non-technical friend can run, and a browser+backend+GitHub-login
stack was overkill for a ~5-person friend-group deployment. TUI (Charm's
bubbletea/bubbles/lipgloss) was chosen over a local web page or a Rust/Tauri
desktop app because it needs zero new toolchain, zero code-signing cost,
and reuses the existing single-binary distribution model. A Tauri wrapper
around this same Go binary (as a sidecar) was noted as worth revisiting
**after** the core distributed-training feature exists and "everyone needs
to use it" becomes a real, validated problem — not before.

**Consequence not yet resolved:** deleting `web/` orphaned real coordinator
backend code — `usersapi`, `joinapi.Claim`, and the whole
"admin-ness enforced by GitHub session in the Next.js backend" authorization
model. Nothing currently checks "is this caller actually an admin" for the
TUI's Admin tab (approve/reject join requests) — it's gated by nothing
today since the dashboard stub doesn't wire real auth yet. This is a live
gap, not a design decision — flag it before building the Admin tab for
real.

## Architecture

```
internal/tui/
  app/         App — the ONE root bubbletea Model. Owns the "/" command
               bar, autocomplete, the logs overlay, and the global quit
               key. Switches between dashboard and onboarding content.
               This is the only thing cmd/edgegrid ever hands to
               tea.NewProgram.
  onboarding/  Wizard — content only (role → coordinator addr → join
               status → starting → done). No chrome of its own.
  dashboard/   Dashboard — content only (Jobs/Workers/Admin tabs). No
               chrome of its own.
  client/      Client interface + Stub impl (canned data). Real transport
               (HTTP via backendMux vs NATS request-reply) is UNDECIDED.
  cmdbar/      Reusable "/"-triggered input bar with autocomplete
               (textinput.ShowSuggestions). Command list lives in
               app.go's `commands` var — currently ["onboard", "logs"].
  logsview/    Reusable log-viewer overlay, reads via internal/nodelog.
  style/       Shared lipgloss styles + Screen() full-frame layout helper.

internal/nodelog/   Single log-file abstraction: Setup() redirects Go's
                    log package AND nats-server's own internal logger
                    (separate systems!) to the same file. Path()/Tail()
                    used by both `edgegrid logs` and the TUI's /logs.

cmd/edgegrid/main.go   Subcommand dispatch:
  (no args)   → headless node, unchanged original behavior
  dashboard   → app.New(...), starts on dashboard screen
  onboard     → app.New(...).StartInOnboarding(), same program
  logs        → plain CLI, reads nodelog.Tail directly
```

`dashboard` and `onboard` are **the same program** — just a different
starting screen. From either, `/onboard` switches into the wizard live, no
new process. This mattered: an earlier version of this work built two
separate `tea.NewProgram` instances with duplicated command-bar/logs-overlay
logic — that was wrong and got consolidated into `app`.

Only the **primary-coordinator** path in the wizard hands off to a real
running node (`onboarding.startPrimaryCoordinator` → shared
`agent.NewAgentWithLogging`, same function the headless path uses).
Secondary-coordinator/worker roles reach a join-status screen that never
actually completes — `joinApprovedMsg` has nothing driving it yet.

## The NATS clustering bug saga (read before touching natsserver/agent)

Getting the primary-coordinator path working surfaced three real,
independent bugs, in order:

1. **`ServerName` unset.** JetStream requires a unique server name once
   clustering is configured at all. Fixed: `natsserver.Start` takes
   `serverName`, wired from `cfg.TailscaleHostname`.

2. **`EmbeddedServer.ClientURL()` returned `nats://0.0.0.0:4222`.** That's a
   bind wildcard, never a valid connect target — especially fragile since
   every dial in this codebase routes through tsnet's userspace network
   stack. Fixed: `ClientURL()` now substitutes `127.0.0.1` for the host
   when it's `0.0.0.0`/empty, since this URL is only ever used for a
   same-process self-connection.

3. **The real one — JetStream permanently stuck in clustered mode.** An
   earlier commit (before this TUI work) made the coordinator always bind
   the NATS cluster listener/port, even with zero peers, so a later
   coordinator could dial in. Side effect nobody caught at the time:
   nats-server's `standAloneMode()` is literally `opts.Cluster.Port == 0` —
   with the port always set, JetStream is *permanently* forced into
   clustered mode, even for a lone bootstrap node. Clustered JetStream
   requires `configuredRoutes() > 0` to even start (`enableJetStreamClustering`
   in nats-server's `jetstream_cluster.go`). A **self-route** (pointing the
   node at itself) satisfies that check statically — but nats-server always
   detects and closes a route to itself as `"Duplicate Route"` at runtime,
   correctly. Net effect: routing state never stabilizes
   (`"Waiting for routing to be established..."` forever), and every
   JetStream operation (stream/KV creation) times out with
   `context deadline exceeded`, no matter the replica count (confirmed
   `replicas=1`, ruled that out explicitly) and regardless of whether
   `data/nats/jetstream` is stale or completely fresh (tested both).

   **Fix applied:** reverted to conditional clustering —
   `natsserver.buildOpts` only sets `opts.Cluster` when
   `len(cluster.Routes) > 0`. A lone coordinator now runs genuinely
   standalone JetStream (fast, no hang). `bridgeInboundCluster` is also
   now conditional on having routes, matching.

   **Explicitly NOT done yet:** when a second coordinator actually gets
   approved to join a currently-standalone primary, something needs to
   flip that primary from standalone → clustered at that point (a live
   NATS config reload, most likely, following the existing `AddUser`
   reload pattern in `natsserver/embedded.go`). **Unverified**: whether
   nats-server's `Reload()` actually supports enabling JetStream clustering
   on an already-running standalone instance, or whether that fundamentally
   requires a process restart — this needs research before implementing.
   This is the single biggest concrete gap left from this session.

4. **Bonus, unrelated to clustering:** nats-server has its own internal
   logging system, entirely separate from Go's `log` package. Without
   `opts.LogFile` set, it wrote straight to stdout — invisible to
   `nodelog`'s file capture, and it corrupted bubbletea's alt-screen
   rendering (two systems fighting for the terminal). Fixed by pointing
   `opts.LogFile` at the same path `nodelog.Setup` uses.

## What's real vs. stubbed right now

**Real / working:**
- tsnet up, tailnet login (URL surfaces inside the TUI via `tsnet.Server.UserLogf`
  → `agent.NewAgent`'s `onProgress` callback → wizard's `agentEventMsg` channel).
- Primary-coordinator startup end-to-end: tsnet → standalone embedded NATS →
  JetStream streams/KV → coordinator/worker manager init.
- `/logs` and `edgegrid logs` — same `nodelog.Tail` underneath.
- Command bar autocomplete (tab-completes against `app.commands`).

**Stubbed / not real:**
- `client.Stub` — all of the dashboard's Jobs/Workers/Admin data. No HTTP
  or NATS transport wired in yet; that decision is still open.
- Secondary-coordinator and worker join flow through the wizard.
- Any authorization on the Admin tab's approve/reject actions.
- Live-enabling clustering when a second coordinator joins (see saga #3).

## Verification state as of last check

`go build ./...` and `go vet ./...` clean except one pre-existing,
unrelated finding in `internal/broker/broker_test.go:106` (a lock-copy vet
warning, not touched this session, deliberately left for later). `go mod
tidy` clean, no dependency drift beyond the charm libraries added for the
TUI (`bubbletea`, `bubbles`, `lipgloss`).

## Quick reference: running it

```
go run ./cmd/edgegrid                          # headless node, unchanged
go run ./cmd/edgegrid onboard                  # unified TUI, starts on onboarding
go run ./cmd/edgegrid dashboard --coord ADDR   # unified TUI, starts on dashboard
go run ./cmd/edgegrid logs -n 200              # plain CLI log tail
```
Inside either TUI screen: `/` opens the command bar, type `onboard` or
`logs`, tab to autocomplete, enter to run.
