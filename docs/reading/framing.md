# Framing: finding the edges in a byte stream

Code: [`wire.go`](../../internal/blob/wire.go) (manifest),
[`chunk.go`](../../internal/blob/chunk.go) (`WriteChunk` / `ReadChunk`),
[`verdict.go`](../../internal/blob/verdict.go). Previous:
[two pipes](two-pipes.md). Next: [flow control](flow-control.md).
Index: [reading](README.md).

## The problem

TCP delivers an ordered stream of bytes and nothing else. It has no concept
of a message. Write 4 MiB in one call and the receiver may see it arrive as
900 reads of 4 KiB; write three small messages and they may arrive
coalesced into a single read. The OS is free to split and merge however it
likes, and what a `Read` returns tells you nothing about where the sender's
`Write` calls began or ended.

So the receiver can only find message boundaries if the messages carry them.
Every frame here does, in one of two shapes.

## The frames

```
manifest:  [4-byte length][JSON body]                 sender   -> receiver
verdict:   [1 byte status][2-byte length][reason]     receiver -> sender
chunk:     [4-byte index][4-byte length][raw bytes]   sender   -> receiver, repeated
```

Lengths are big-endian. That is spelled out rather than left to the platform
because both ends must agree, and a Windows machine and a Linux machine
would otherwise be relying on luck.

The order is fixed — manifest, then the verdict, then exactly `len(m.Chunks)`
chunk frames — so **no frame needs a type tag**. Position in the stream is
the type: the receiver already knows a manifest is coming because the hello
said `IntentBlob`, and it knows a verdict is next because a manifest just
finished. If messages could arrive in any order, you would need
`[type][length][body]` on every frame instead.

## `io.ReadFull`: exactly N bytes, never more

`io.Reader.Read` is allowed to return *fewer* bytes than you asked for — ask
for 4, get 2. `io.ReadFull` loops until the buffer is exactly full (or an
error), and critically **never reads past it**. That is what keeps the read
cursor aligned from one call to the next:

```
ReadManifest:  read 4 bytes  -> N        read N bytes  -> the JSON
               (cursor now sits exactly on the verdict / first frame)

ReadChunk:     read 8 bytes  -> idx,len  read len bytes -> the data
               (cursor now sits exactly on the next chunk header)
```

Each call consumes its own frame and stops. No delimiters, no lookahead, no
scanning for markers. The same reason `ReadHello` works.

`ReadFull` also returns `io.ErrUnexpectedEOF` if the stream ends part-way
through a buffer, which is how a connection dying mid-chunk becomes an error
instead of a silently short chunk.

### The `bufio` trap

If the connection is ever wrapped in a `bufio.Reader`, that **same** reader
must be threaded through every call. `bufio` reads ahead into its own buffer,
so reading the manifest through a wrapper and then chunks from the raw
connection loses whatever was sitting in the wrapper's buffer, and the
stream desyncs. Either use the raw connection throughout, or wrap once and
pass the wrapper everywhere.

## Allocation is bounded by a check *before* the read

Every length prefix is peer-supplied, so every one is compared against a cap
before `make([]byte, n)`:

| Frame | Cap |
|---|---|
| manifest | `maxManifestSize` — 64 MiB |
| chunk | `maxChunkSize` — 64 MiB |
| verdict reason | `maxReasonLen` — 1 KiB |
| hello | `maxHelloSize` — 64 KiB |

Without the check, a garbled or hostile prefix of `0xFFFFFFFF` makes the
receiver try to allocate 4 GiB before reading a single byte of body.

## A chunk's length is redundant, on purpose

The receiver already knows every chunk's length from the manifest. It is sent
again because it costs 4 bytes against a multi-MiB payload, it lets the
stream be parsed without consulting the manifest, and a disagreement between
the two is itself a signal something is wrong — `Receive` checks
`len(data) != c.Length` before trusting anything.

## Chunks are not packets

TCP already segments the stream into roughly 1,400-byte packets to fit the
path, and re-segments as it likes. Chunking at 4 MiB buys **nothing** at the
network layer. It buys:

- **Verification granularity** — which 4 MiB is bad, not merely "something,
  somewhere, is".
- **Resume** — restart at chunk 87, not byte 0.
- **Cheap refetch** — re-request 4 MiB, not 10 GiB.
- **Later, out-of-order and multi-peer fetch** — indexed, independently
  verifiable units can arrive in any order, and `WriteAt` (an absolute
  offset) already supports that.

A bigger chunk costs more on a retransmit-after-corruption and more memory
per buffer; a smaller one costs a larger manifest and more per-chunk hashing
overhead. 4 MiB is in the range BitTorrent and OCI registries use, not a
derived number.
