# The receive window: the number behind flow control

Code: none — the window lives in the TCP stack, not in `blob`. That is the
point. Previous: [flow control](flow-control.md). Index: [reading](README.md).

## What it is

Every TCP acknowledgement carries a **window**: *"I have this many more bytes
of room in my receive buffer."* The sender is not allowed to have more than
that many unacknowledged bytes in flight. As the receiving application drains
the buffer (`Read`), room opens up and the advertised window grows; as data
arrives faster than it is drained, the window shrinks.

```
receiver's receive buffer

  [ already read ][ buffered, unread bytes ][ free room      ]
                   <---- application ---->   <-- the window -->
                        hasn't called Read       advertised to the sender
```

## Who manages it

**The receiver's TCP stack, never the sender's.** The sender only obeys what
it is told. This is why nothing in `Send` decides how fast to go.

In this repo both ends of an EdgeGrid-to-EdgeGrid connection run on `tsnet`'s
userspace stack (gVisor's netstack), not the kernel's: the receiver
`Listen`s through `tsnet`, and since the dial fix the sender `Dial`s through
it too. So the window in question is managed by Go code inside your own
process. Before that fix the outbound side went through the kernel, which
is also why it couldn't reach tailnet addresses at all.

WireGuard, underneath, adds no window and no flow control of its own. It
encrypts and forwards datagrams; everything about pacing is TCP's, inside the
tunnel.

## What happens at zero

When the receiver's buffer is full it advertises a window of **0**. The sender
must stop sending new data. Because a zero-window advertisement is itself a
packet that could be lost, the sender doesn't wait forever for a reopening:
it sends small **window probes** on a timer until the receiver answers with a
non-zero window. (This is the "persist timer" in the TCP specification.)

From `Send`'s point of view none of that is visible. `Write` simply takes
longer, then blocks. See [flow control](flow-control.md) for the chain from a
slow receiver disk to a blocked `Write`.

## The window has to be big enough to fill the pipe

A window that is too small caps throughput no matter how fast the network is,
because the sender runs out of permitted bytes and sits idle waiting for
acknowledgements. The window needed to keep a path full is the
**bandwidth-delay product**: bandwidth × round-trip time.

```
100 Mbit/s path, 50 ms round trip:
    100,000,000 bits/s  ÷ 8  = 12,500,000 bytes/s
    12,500,000 bytes/s × 0.05 s = 625,000 bytes   (~610 KiB)
```

So on that path the receiver must be able to advertise about 610 KiB, or the
transfer runs slower than the link can carry. A higher round trip needs a
proportionally larger window — which is why a path that falls back to a
relay, with a longer RTT, needs more than a direct one.

The classic TCP window field is 16 bits, capping it at 64 KiB. **Window
scaling** (RFC 7323) is a negotiated multiplier that lifts that ceiling, and
is how windows of hundreds of KiB or several MiB are possible at all.

## What this means for chunk size

The window and the chunk are unrelated. A 4 MiB chunk is far larger than a
typical window, so one `WriteChunk` is delivered as many window-sized bursts,
with `Write` blocked in between. Chunk size is about verification and refetch
granularity ([framing](framing.md)), not about network behaviour.

## What isn't claimed here

- **No specific buffer or window sizes for this stack.** They depend on the
  OS and on `tsnet`/gVisor's configuration and any auto-tuning, and were not
  measured. The figures above are the general model with illustrative
  arithmetic, not observed values.
- **Nothing about gVisor's internals.** Whether it auto-tunes its receive
  buffer, and how, wasn't read for this. If a transfer is slower than the
  path allows, the receive window is one of the first things to check, and
  that would mean reading `tsnet`'s configuration, not `blob`.
