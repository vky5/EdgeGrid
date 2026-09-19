# Blob transfer

How a large payload moves from one node to another: chunked, hashed,
verified against a manifest agreed in advance, and — eventually — resumable
after an interruption. This is the subsystem `internal/blob` exists for.

Peer discovery ([`peer-discovery.md`](peer-discovery.md)) is a hard
dependency: it's what makes an authenticated connection to a named peer
possible at all. This doc assumes that layer works and builds on top of it.

## The question that started this

`internal/executor`'s `doc.go` has always declared the scope: "chunk a set
of files, hash each chunk, stream them to a peer over the tailnet, and
resume from the last verified chunk after an interruption." That sentence
hides two separate jobs — *knowing about files on disk* and *knowing how to
move bytes reliably* — and only the second one is reusable.

So the real question wasn't "how do we send a file," it was: what's the
smallest thing that moves bytes correctly between two nodes, and where's
the line between it and everything that will eventually want to use it?

## Decisions

**A separate `internal/blob` package, not code inside `executor`.**
`executor` is artifact-specific — it knows about files on disk and turns
them into blobs. `blob` only knows how to move a blob once one exists, and
has no opinion about what the bytes mean. Anything that later needs to move
a large payload (task output, a dataset, a bundle) uses `blob` directly
rather than pretending to be an artifact to get through `executor`'s door.

**A manifest, agreed before any byte moves — not hashes riding alongside
the data.** TCP and WireGuard already guarantee a chunk arrives exactly as
it was sent, so a hash bundled with that chunk, in the same stream, from
the same sender, proves nothing that wasn't already true. What no transport
can tell you is whether what was sent is *correct* — the sender's own disk
could have handed it bad bytes. A manifest built once from the source and
shared up front makes "does this match what was promised" an independent
question instead of the sender grading its own homework. It's also what
makes verifying already-on-disk chunks possible after a crash, which is the
whole basis for resume. Borrowed from OCI's image-manifest/blob-digest
split.

**The manifest carries metadata only, never bytes.** Per chunk: `Index`,
`Offset`, `Length`, `SHA256`. Plus the whole blob's size and hash. The
whole-blob hash is not redundant with the per-chunk hashes — it's what
catches a bug in *reassembly* (wrong order, a skipped chunk), which no
individual chunk hash can see, since each one only proves its own bytes are
right in isolation.

**Manifest goes on the wire as length-prefixed JSON; chunks do not.** The
manifest is structured data and small enough that JSON costs nothing. A
chunk is raw bytes, and base64-ing 4 MiB of them to fit inside JSON would
inflate it by roughly a third to buy nothing at all.

**Every chunk is verified against the manifest on arrival.** This is the
entire point of having a manifest. A length check only proves the right
*number* of bytes arrived — a single flipped bit produces a chunk that is
exactly the right length and still wrong. Skipping the hash check makes the
manifest decorative.

**Transfer rides the existing discovery pipeline.** Every connection is
identity-gated the same way discovery already does it: `WhoIs` resolves who
is on the other end from the tailnet's control plane (not from anything the
stream claims), then a hello exchange runs, and only then can a manifest
follow. No parallel identity mechanism.

**One fresh connection per transfer, opened only when there's something to
send.** Concurrency then comes free from TCP: a connection is identified by
its 4-tuple (source IP, source port, destination IP, destination port), so
two transfers to the same peer on the same port are already independent —
the OS assigns a different ephemeral source port to each. Sending three
blobs to one peer at once is just three dials. Nothing needs stream IDs,
interleaving, or head-of-line-blocking handling; cramming multiple transfers
down one socket is how you reinvent HTTP/2 by accident. A connection's
lifetime also matches its transfer's, so a failed transfer cleans up by
closing rather than leaving a shared socket in an unclear state.

**`Hello` carries an explicit `Intent` naming what follows on this
connection.** Plain discovery says so and nothing more follows; a transfer
says so and a manifest comes next.

The alternative was inferring it — a connection that keeps talking past the
hello *is* a transfer, since presence-checking dials close immediately.
That works, but it forces the receiver into a bounded-timeout read that
fundamentally cannot tell "nothing more is coming" apart from "the sender is
slow": every discovery ping pays the timeout to learn nothing, and a briefly
stalled sender gets misread as a plain ping. An explicit field makes this a
parse instead of a race.

Three properties this has to keep:

- **Unknown or empty intent means hello exchange only, nothing further.**
  This is also what makes the change backward compatible for free: an older
  node's hello unmarshals with an empty intent and lands on exactly that
  path, and `encoding/json` ignores unknown fields, so a newer node's hello
  doesn't break an older one either.
- **Intent routes; it does not authorize.** `Hello` is the peer's own
  self-report — unlike `WhoIs`, which is the tailnet's control plane and
  unspoofable. Any tagged node can claim any intent. Deciding whether a peer
  is *allowed* to send is a separate thing that doesn't exist yet.
- **It names the protocol that follows, not the payload's meaning.** What
  the receiver has to dispatch on is "a manifest comes next," not "this is
  an artifact" — `blob` is content-agnostic by design, so an intent named
  after artifacts would be a lie the moment task output rides the same path.

**The node layer sets the intent and dispatches on it.** `discovery` must
not import `blob` to route a transfer: that would make the lower, generic
layer depend on the higher, specific one, and every future intent would add
another import to `discovery`. The dispatcher instead lives where both are
already in scope — `node`, which already constructs the discovery `Server`
and sets its `OnPeer`. `OnPeer` becomes a switch on intent that hands the
connection to whichever subsystem owns it.

On the receiving side this falls out of machinery that already exists:
`discovery.Server.Serve` spawns `go s.handle(ctx, conn)` per accepted
connection, so `OnPeer` is one handler invoked concurrently — once per
connection, each with its own `conn`. Peer identity arrives as an argument,
not as configuration on the callback, so two transfers from the same machine
are simply two calls with the same `who` and different `conn`.

**Transfers are user-triggered, not automatic.** A user picks a file and
sends it. Nothing initiates a transfer on its own. Automatic, declarative
placement ("I want artifact X on node B") is task dispatch, which is
deferred and needs its own proposal — see peer-discovery.md.

## Wire format

Everything below rides one connection, in this order, after the hello
exchange has completed on it.

```
manifest:  [4-byte big-endian length][JSON body]

chunk:     [index][length][raw bytes]   — repeated, once per chunk
```

The manifest's framing is deliberately identical to `discovery.Hello`'s: a
length prefix exists because TCP is a byte stream with no message
boundaries, so the receiver has no other way to know where one logical unit
ends and the next begins.

A chunk's length is technically redundant — the receiver already knows every
chunk's length from the manifest it just read. It's kept anyway because it's
4 bytes against a multi-MiB payload, it lets the stream be parsed without
consulting the manifest, and a disagreement between the two is itself a
signal something is wrong before any bytes get trusted.

The index is what makes anything other than "send everything, in order"
possible. Position in the stream would be enough for a full sequential send,
but it stops being enough the moment only chunks 4, 7 and 12 are being sent
— which is exactly what resume and selective re-fetch look like.

## The shape of it

```
Node A (dialer)                        Node B (listener)
  │                                      │
  │ Start()                              │ Start()
  │ Snapshot() — who's online            │ server.Serve() — accepting
  │                                      │
  │ dialAndGreet() / SendBlob()          │
  │── Dial() ───────────────────────────>│ handle()
  │                                      │ IdentifyPeer() — WhoIs
  │                                      │
  │── hello {intent} ───────────────────>│ ExchangeAsListener()
  │<──────────────────── hello {reply} ──│
  │                                      │
  │                                      │ OnPeer → handlePeer()
  │                                      │ switch hello.Intent
  │                                      │
  │                       intent=hello:  │   close
  │                       intent=blob:   │   keep reading ↓
  │                                      │
  │── manifest ─────────────────────────>│
  │   (file size, chunk hashes)          │
  │                                      │
  │── chunk 0 ──────────────────────────>│
  │── chunk 1 ──────────────────────────>│
  │── chunk 2 ──────────────────────────>│
  │           ...                        │
```

The dialer's side is one function start to finish (`SendBlob`). The
listener's side is split across `discovery` — `Serve` accepts, `handle`
identifies and greets — before anything reaches `node`'s `handlePeer`,
which is the first place the intent is looked at.

## Sequence (Mermaid)

```mermaid
sequenceDiagram
    participant U as User
    participant A as Node A (sender)
    participant B as Node B (receiver)

    U->>A: picks a file to send
    A->>A: BuildManifest — chunk, hash each, hash the whole

    A->>B: tsnet.Dial (fresh connection, opened for this transfer)
    B->>B: WhoIs — identity from the tailnet, not from the stream
    A->>B: Hello{intent: blob transfer}
    B->>A: Hello
    Note over A,B: identity gate passed; intent says a manifest follows,<br/>so B keeps the connection open instead of closing it

    A->>B: manifest — [length][JSON]
    B->>B: now knows every chunk's offset, length and expected hash

    loop for each chunk
        A->>B: [index][length][raw bytes]
        B->>B: sha256(bytes) == manifest.Chunks[i].SHA256 ?
        B->>B: write at Offset
    end

    B->>B: sha256(whole file) == manifest.SHA256 ?
```

## Still open (decide before building past step 4)

- **Port.** Reuse discovery's listener on `:9797` and its `OnPeer` handoff,
  or stand up a second listener on its own port. Now a code-reuse/isolation
  question rather than a correctness one — the on-demand-connection decision
  above resolves the ambiguity either way.
- **Whether to extract the shared framing primitive, and where it lives.**
  The "write a 4-byte length then that many bytes" mechanic is now
  duplicated between `discovery.WriteHello` and `blob.WriteManifest`, and a
  third copy is coming. Only the length-prefix mechanics are shared, not the
  JSON step — chunks aren't JSON. The catch: `blob` currently imports nothing
  from `discovery`, deliberately, so that it stays usable for payloads that
  have nothing to do with peer identity. Extracting into `discovery` would
  reverse that. A third, smaller package both import is the obvious shape,
  but that's a package-boundary call that hasn't been made.
- **Push vs. pull.** Does the sender stream everything, or does the receiver
  ask for specific chunk indices? Resume and selective re-fetch need the
  receiver to be able to say "just these," one way or another.
- **Resume mechanics.** Where partial-transfer state lives, and how a
  receiver works out what it's still missing after a restart.
- **Failure policy.** A chunk fails its hash check, or the connection drops
  mid-transfer: retry that one chunk, or abort the whole transfer?

## How it breaks

Known gaps, so they're not discovered the hard way:

- **Nothing bounds concurrent transfers.** 500 simultaneous inbound
  transfers means 500 goroutines each holding a chunk buffer — at the 4 MiB
  default, roughly 2 GB, from a single over-eager or misbehaving peer. This
  eventually needs a semaphore capping concurrent transfers.
- **`OnPeer` must be safe to call concurrently.** It already is today, but
  the moment it touches shared state — a transfer registry, a progress map,
  a peer table — that's a live data race unless it's behind a mutex or
  funneled through a channel.
- **Identity is gated, authorization is not.** Any tagged tailnet node that
  can reach the port can start a transfer. There is no notion of "may this
  peer send me this."

## Task breakdown

Ordered so each step is independently testable. Steps 2, 4, 5 and 6 need no
network at all — sender and receiver can be proven against `net.Pipe()`
in-process before either open transport question is settled.

Step 3 (the TUI) was built out of order on purpose: it makes manifest
generation something you can watch happen on a real file, which is worth
more during design than keeping the sequence tidy.

### 1. Manifest generation — done
Read a file once, split it at a fixed chunk size, hash each chunk, and
accumulate a hash of the whole blob in the same pass.
**Done when:** a file that divides evenly, one that doesn't, and an empty
one all produce correct chunk counts, offsets, lengths and hashes.
Landed as `BuildManifest` with tests covering all three plus the
default-chunk-size and missing-file paths.

### 2. Manifest wire encoding — done
`WriteManifest` / `ReadManifest`, length-prefixed JSON, with an upper bound
on the claimed length so a garbled or hostile prefix can't drive a huge
allocation before anything is verified.
**Done when:** a manifest survives a round trip through an `io.Pipe`
unchanged, and an oversized length prefix is rejected rather than allocated.

### 3. Pick a peer and a file in the TUI — done
The Peers tab gets a cursor, and `s` on an online peer starts a send flow:
type a path, the file is hashed off the UI goroutine, and the resulting
manifest is shown — size, chunk count, whole-blob hash, and the first few
chunk hashes with their offsets.
**Done when:** a real file picked in the dashboard displays a correct
manifest, and confirming says plainly that nothing sends it yet.

Two details the "done when" doesn't cover, both caused by the 5s membership
refresh running underneath the flow: the send target is captured **by
value** rather than as an index (a reorder mid-flow would otherwise
retarget the send at a different machine), and hashing runs as a `tea.Cmd`
(inline, a multi-gigabyte file would freeze the TUI for minutes).

File selection is a typed path, not a browser — the smallest thing that
makes the flow usable. A browser is a later upgrade, not a blocker.

### 4. Intent on `Hello`, and the dispatcher
Add the field, have `node` set it when dialing and switch on it in
`OnPeer`. Unknown and empty both mean "hello only, nothing further".
**Done when:** a node dialing with a transfer intent reaches a different
handler than a plain discovery dial, and a node that has never heard of an
intent value closes cleanly after the exchange instead of erroring.

This touches `discovery`, which is already merged — worth checking
`hello_test.go` still pins the behaviour it means to.

### 5. Chunk framing
`[index][length][raw bytes]` — the reader and writer for one chunk frame.
**Done when:** a chunk round-trips through a pipe and a frame whose length
disagrees with the manifest is rejected.

### 6. Sender — stream chunks in order
Walk a manifest, read each chunk from the source, write its frame. Happy
path only, no resume.
**Done when:** every chunk of a multi-chunk file is written in order.

### 7. Receiver — verify on arrival, write at offset
Read frames, hash each against the manifest **before** trusting it, write
at `Offset`, then verify the whole-blob hash once everything has landed.
**Done when:** a file round-trips byte-identical through a pipe, and a
deliberately corrupted chunk is caught rather than written.

### 8. Request protocol
How a receiver says what it wants: everything, or specific indices. Blocked
on the push/pull decision above.

### 9. Resume
Given chunks already verified on disk, work out what's missing and fetch
only that. Depends on 7 and 8.

### 10. Failure handling
Bad chunk, dropped connection, failed disk write. Blocked on the failure
policy decision above.

### 11. Wire it end to end
Dial a real peer over the tailnet and run a transfer from the TUI's send
button. Blocked on the port decision above.
