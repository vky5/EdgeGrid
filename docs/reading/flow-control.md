# Flow control: why 4 TB never sits in memory

Code: `Send` and `Receive` in [`chunk.go`](../../internal/blob/chunk.go).
Previous: [framing](framing.md). Next: [the receive window](receive-window.md).
Index: [reading](README.md).

## The claim

A 4 TB file never sits anywhere in memory, and it never "sits in the TCP
stream" either. Peak live memory for a transfer is roughly one chunk buffer
on each end plus the sockets' own buffers — on the order of 10 MB — whether
the file is 40 MB or 4 TB.

Nothing in `blob` enforces that with a limit or a queue. It falls out of
three things working together.

## 1. The sender reads from disk one chunk at a time

`Send` reuses a single buffer:

```go
for i, c := range m.Chunks {
    io.ReadFull(f, buf[:c.Length])   // one chunk off disk
    WriteChunk(rw, c.Index, buf)     // one chunk onto the wire
}
```

The rest of the file stays on the sender's disk until its turn.

## 2. `Write` blocks when the receiver has no room

`WriteChunk` doesn't return until the bytes have been accepted into the
sender's send buffer. When the receiver isn't draining its end, that buffer
fills, and `Write` **blocks**. The loop above stops at that line, so the next
`ReadFull` never runs, so no more of the file is read. The sender is
throttled by a mechanism nobody wrote — the kernel (here, `tsnet`'s userspace
stack) refuses to accept bytes the other side has no room for.

This is **backpressure**, and it is TCP's *flow control*: the receiver
controlling how fast the sender may go.

## 3. The receiver only reads when it's ready

`Receive` does real work between reads — hash the chunk, compare it, `WriteAt`
to disk:

```go
idx, data, err := ReadChunk(rw)      // drain one chunk from the socket
sha256.Sum256(data)                  // verify
f.WriteAt(data, c.Offset)            // persist
// only now does the loop come back and drain the socket again
```

While it is busy, the socket's receive buffer isn't being emptied, so it fills,
so the window it advertises shrinks (see [the receive window](receive-window.md)),
so the sender's `Write` blocks. **A slow disk or a slow hash on the receiver
slows the sender automatically**, with no message saying so.

## The chain, end to end

```
receiver disk slow
   -> Receive's loop is late calling ReadChunk
   -> receive buffer fills
   -> advertised window shrinks toward zero
   -> sender's send buffer fills
   -> sender's rw.Write blocks
   -> sender's loop stops reading the source file
```

## Memory at any instant

```
sender:     one chunk buffer (4 MiB, reused)  + socket send buffer
in flight:  about one receive window
receiver:   one chunk buffer (4 MiB)          + socket recv buffer
```

## Worked example: a 10 GiB file at the 4 MiB default

`BuildManifest` reads the file once and produces 2,560 `ChunkInfo` entries —
about 150 bytes each in JSON, so a manifest near 380 KB. It keeps no file
data.

`Send` writes the manifest, waits for the verdict, reopens the file, then
loops 2,560 times: read 4 MiB into the reused buffer, write
`[index][length][4 MiB]`, blocking whenever the receiver is behind.

`Receive` reads the manifest, learns to expect exactly 2,560 frames, then
loops 2,560 times: `ReadChunk` fills a fresh 4 MiB buffer, hash it, compare
against `m.Chunks[i].SHA256`, `WriteAt(data, c.Offset)`. The offset is
absolute, so arrival order never matters.

Finally `verifyWhole` re-reads the assembled file. Per-chunk hashes prove each
piece arrived intact; only the whole-file hash catches a piece written to the
wrong place. That is a second full read of the file on the receiver — roughly
the cost of one more pass over the data.

## Flow control is not congestion control

Two different things share the "slow the sender down" effect:

- **Flow control** — the receiver protects *itself*: "I have this much room."
  This is what these pages are about.
- **Congestion control** — the sender protects *the network*: it grows and
  shrinks how much it has in flight based on loss and delay, independent of
  what the receiver said.

The sender is limited by the smaller of the two. The code sees only the
result — `Write` taking longer.

## How this breaks

- **No progress-based timeout.** A receiver stuck mid-transfer and a merely
  slow one look identical to the sender: `Write` blocked. Only the overall
  30-minute deadline ends it. A deadline that resets on each chunk written
  would tell them apart.
- **The receiver's per-chunk work is the throttle.** Hashing is cheap next to
  a network hop, but `WriteAt` to a slow or busy disk sets the ceiling for
  the whole transfer — and it is invisible from the sender's side.
- **The final `verifyWhole` pass is unthrottled by the network** and runs
  after the last chunk, so the sender sees the transfer as done before the
  receiver has confirmed the file. The receiver's log, not the sender's, is
  where a whole-blob mismatch shows.
