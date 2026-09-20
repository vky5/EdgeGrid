# Reading: how the bytes actually move

Background for [`internal/blob`](../../internal/blob/chunk.go). The design
decisions — why a manifest, why an ACL, what the wire format is — are in
[`blob-transfer.md`](../blob-transfer.md). This section is the layer under
that: what TCP is actually doing when `Send` writes and `Receive` reads, and
why a 4 TB file doesn't need 4 TB of anything.

Read in this order. Each one leans on the one before.

| # | Read | The question it answers |
|---|---|---|
| 1 | [`two-pipes.md`](two-pipes.md) | What is a connection, what is `io.ReadWriter`, and where does the sender *wait*? |
| 2 | [`framing.md`](framing.md) | TCP has no message boundaries — so how does the receiver know where the manifest ends and a chunk starts? |
| 3 | [`flow-control.md`](flow-control.md) | Why does a slow receiver slow the sender down, and why is memory bounded? |
| 4 | [`receive-window.md`](receive-window.md) | The one number that makes #3 work: who owns it, what it does at zero, and what it costs on a slow path. |

## Where each idea lives in the code

| Idea | Code |
|---|---|
| One handle onto two pipes | `Send` / `Receive` take an `io.ReadWriter` — [`chunk.go`](../../internal/blob/chunk.go) |
| The sender waiting | `ReadVerdict` right after `WriteManifest` in `Send` — [`chunk.go`](../../internal/blob/chunk.go), [`verdict.go`](../../internal/blob/verdict.go) |
| Length-prefixed frames | `WriteManifest` / `ReadManifest` — [`wire.go`](../../internal/blob/wire.go); `WriteChunk` / `ReadChunk` — [`chunk.go`](../../internal/blob/chunk.go) |
| Exactly-N-bytes reads | `io.ReadFull` in every `Read*` function above |
| Backpressure | nothing — it isn't in the code, which is the point of #3 |
| The deadlines that bound a wait | `blobSendTimeout` — [`blob_send.go`](../../internal/node/blob_send.go); `blobReceiveTimeout` — [`blob_receive.go`](../../internal/node/blob_receive.go) |

## A note on confidence

These pages describe the general TCP model — RFC 793 and RFC 7323 — as it
applies to this code. Two things they do **not** claim: exact buffer sizes
(those vary by OS and by stack, and this repo's inbound and outbound
connections both run on `tsnet`'s userspace stack rather than the kernel's),
and anything about gVisor's internals, which weren't read for this. Where a
number appears, it is illustrative arithmetic, not a measurement.
