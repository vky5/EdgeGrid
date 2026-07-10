# Log Streaming — `GET /jobs/{id}/logs`

## What it does

When you submit a training job, the Python script runs on a remote worker machine. Without log streaming, that run is a black box — you submit, you wait, you get a result. Log streaming lets you `curl` a single endpoint and watch stdout/stderr from the training script in real time, line by line, as if it were running locally.

It also handles late connections: if you connect 30 seconds into a job that started 2 minutes ago, you get **all prior output from the beginning**, not just future lines.

---

## Why NATS JetStream + SSE

Two choices had to be made: how to move logs from worker to coordinator, and how to push them from coordinator to the HTTP client.

**Worker → Coordinator: JetStream, not plain NATS Core**

Plain NATS Core is fire-and-forget pub/sub. If the coordinator isn't subscribed at the exact moment the worker publishes a log line, that line is gone. JetStream persists every message in a stream with an ordered sequence number. A subscriber can say "give me all messages from sequence 0" and get everything ever published to that subject — even messages from before the subscription was created.

This is what makes late connections work. The client connects mid-job, the coordinator subscribes with `DeliverAll`, and JetStream replays every line from the beginning before catching up to live.

**Coordinator → HTTP client: SSE, not WebSockets**

SSE (Server-Sent Events) is one-directional: server pushes, client reads. That is exactly the shape of this problem. WebSockets are bidirectional and require an upgrade handshake and a more complex protocol. SSE works over plain HTTP, `curl -N` understands it natively, and every browser supports it with `EventSource`. No library needed on the client side.

---

## Step-by-step flow

### Step 1 — Training script writes output

The Python training script runs via `exec.CommandContext`. Its stdout and stderr are both wired to a `logWriter`:

```go
// internal/worker/executor/training.go

type logWriter struct {
    prefix  string
    publish func([]byte) // optional NATS publish func
}

func (lw *logWriter) Write(p []byte) (int, error) {
    log.Printf("%s%s", lw.prefix, p)  // always print locally
    if lw.publish != nil {
        lw.publish(p)                  // also publish to NATS if wired up
    }
    return len(p), nil
}
```

`logWriter` implements `io.Writer`. Every chunk of bytes the Python process writes to stdout or stderr passes through `Write`. The `prefix` field tags lines in the worker's local terminal so you can tell which job they belong to — `[job abc123] Epoch 1/10 loss=0.42`.

### Step 2 — logWriter publishes to NATS JetStream

When the worker agent starts with `--executor training`, the agent creates a JetStream context and wires up a publish function:

```go
// internal/agent/agent.go

js, _ := nc.JetStream()
execInstance = executor.NewTrainingExecutor(func(jobID, line string) {
    js.Publish(broker.SubjectLogsPrefix+jobID, []byte(line))
})
```

This func is stored on the `TrainingExecutor` struct:

```go
// internal/worker/executor/training.go

type TrainingExecutor struct {
    logPublish func(jobID, line string)
}
```

Inside `runScript`, a bound version is passed into `logWriter`:

```go
var pub func([]byte)
if e.logPublish != nil {
    pub = func(p []byte) { e.logPublish(jobID, string(p)) }
}
lw := &logWriter{prefix: fmt.Sprintf("[job %s] ", jobID), publish: pub}
cmd.Stdout = lw
cmd.Stderr = lw
```

So every byte the Python script prints becomes a NATS JetStream message on subject `jobs.logs.<jobID>`.

Note: the venv setup logs (pip install, etc.) use a separate `logWriter` with no `publish` func — they stay local to the worker terminal only. Only the actual training script output is published.

### Step 3 — NATS JetStream stores the messages

`jobs.logs.*` is registered in the JOBS stream:

```go
// internal/broker/broker.go

subjects := []string{
    SubjectTrainWildcard,   // jobs.train.*
    SubjectResults,         // jobs.results
    SubjectLogsWildcard,    // jobs.logs.*  ← added for log streaming
    ...
}
```

JetStream stores every message with a sequence number, ordered per-subject. Messages are retained for 24 hours (the stream TTL). A subscriber that joins late can request delivery from sequence 1 and receive all prior messages in order.

### Step 4 — Coordinator SSE handler subscribes

When `GET /jobs/{id}/logs` is called:

```go
// internal/coordinator/jobsapi/jobsapi.go — Logs

msgCh := make(chan *nats.Msg, 64)
sub, err := jsBroker.JS.ChanSubscribe(
    broker.SubjectLogsPrefix+jobID,  // "jobs.logs.<jobID>"
    msgCh,
    nats.DeliverAll(),   // replay from the very first message
    nats.AckNone(),      // no acks needed — logs are read-only
)
```

`ChanSubscribe` delivers messages to a Go channel instead of a callback. This lets us `select` on it alongside other signals (client disconnect, job completion).

### Step 5 — SSE stream to HTTP client

```go
w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")
w.Header().Set("Connection", "keep-alive")

for {
    select {
    case <-r.Context().Done():
        return  // client disconnected

    case msg := <-msgCh:
        fmt.Fprintf(w, "data: %s\n\n", msg.Data)
        flusher.Flush()  // push immediately, don't buffer

    case <-ticker.C:
        // poll job state every 2s
        status, _ := jobstate.GetJobStatus(kv, jobID)
        if status.State == jobstate.StateCompleted ||
           status.State == jobstate.StateFailed ||
           status.State == jobstate.StateCancelled {
            // drain any remaining messages
            for {
                select {
                case msg := <-msgCh:
                    fmt.Fprintf(w, "data: %s\n\n", msg.Data)
                    flusher.Flush()
                default:
                    fmt.Fprintf(w, "event: done\ndata: %s\n\n", status.State)
                    flusher.Flush()
                    return
                }
            }
        }
    }
}
```

Each SSE message has the format `data: <line>\n\n`. The double newline is the SSE spec — it signals end of event. `flusher.Flush()` forces Go's HTTP server to send the bytes immediately rather than buffering.

The 2-second ticker polls job state. When the job hits a terminal state (COMPLETED, FAILED, CANCELLED), the handler drains any remaining queued messages, sends a final `event: done` event, and closes the stream.

---

## End-to-end example

```bash
# Submit a job
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{"training_script": "...", "dataset_ref": "my-dataset"}'
# → {"job_id":"abc123","status":"queued"}

# Stream logs — works even if job already started
curl -N http://localhost:8080/jobs/abc123/logs
# data: Epoch 1/10 loss=0.842
# data: Epoch 2/10 loss=0.761
# data: Epoch 3/10 loss=0.693
# ...
# event: done
# data: COMPLETED
```

The `-N` flag disables curl's output buffering so you see lines as they arrive.

---

## Edge cases

**Client connects after job finishes** — JetStream still has all the messages. The coordinator subscribes with `DeliverAll`, drains all stored messages, then immediately sees the terminal job state and closes with `event: done`.

**Mock executor** — `NewMockExecutor()` is created without a `logPublish` func, so `logWriter.publish` is nil. No NATS messages are published. The SSE stream returns no data events, only the final `event: done` when the mock completes.

**Worker crashes mid-job** — log messages already published to JetStream are safe. A late-connecting client will see all output up to the crash point.
