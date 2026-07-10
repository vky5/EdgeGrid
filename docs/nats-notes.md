# NATS and JetStream Notes

This note explains the NATS pieces used by EdgeGrid: connections, subjects,
publishing, streams, consumers, ACK/NAK behavior, timeouts, KV buckets, object
stores, and replicas.

## One Sentence Model

NATS Core is fast live messaging. JetStream is the persistent layer on top of
NATS that adds event logs, durable consumers, ACK/NAK, replay, key-value buckets,
object storage, and replication.

```text
NATS server/cluster
  |
  |-- NATS Core
  |     live publish/subscribe
  |     messages are ephemeral
  |
  |-- JetStream
        persistent streams
        durable consumers
        ACK/NAK/redelivery
        key-value buckets
        object stores
        replication
```

## Connection vs JetStream Context

In the Go client, `*nats.Conn` is the live connection to a NATS server:

```go
nc *nats.Conn
```

It can publish and subscribe using NATS Core:

```go
nc.Publish(subject, data)
nc.Subscribe(subject, handler)
```

`nc.JetStream()` creates a JetStream API handle using the same connection:

```go
js, err := nc.JetStream()
```

That `js` value is not a second network connection. It is a context/helper for
using persistent JetStream features through the existing NATS connection.

EdgeGrid wraps both in `Broker`:

```go
type Broker struct {
    Conn     *nats.Conn
    JS       nats.JetStreamContext
    Replicas int
}
```

Use `Conn` when the message is a short-lived signal. Use `JS` when the message
or state should survive late subscribers, reconnects, worker crashes, or
coordinator restarts.

## Subject

A subject is a routing address. It is similar to a topic name.

Examples in EdgeGrid:

```text
jobs.train.worker-a
jobs.results
jobs.logs.job-123
workers.register
workers.heartbeat
workers.reject
workers.stats.worker-a
```

NATS subjects are dot-separated tokens:

```text
jobs.train.worker-a
```

has three tokens:

```text
jobs | train | worker-a
```

### Exact Subject vs Wildcard

`jobs.train` means exactly this two-token subject:

```text
jobs.train
```

It does not match:

```text
jobs.train.worker-a
```

`jobs.train.*` means:

```text
jobs.train.<one-token>
```

It matches:

```text
jobs.train.worker-a
jobs.train.worker-b
jobs.train.gpu-node-7
```

It does not match:

```text
jobs.train.us.worker-a
```

because `*` matches exactly one token.

## Publish and Subscribe

Publishing sends data to a subject:

```go
nc.Publish("workers.reject", data)
js.Publish("jobs.train.worker-a", data)
```

Subscribing receives messages from a subject:

```go
nc.Subscribe("workers.stats.*", handler)
js.QueueSubscribe("jobs.results", "coord-results", handler)
```

The subject decides where the message is addressed. Whether the message is
ephemeral or persisted depends on whether it is sent through NATS Core only, or
through JetStream and captured by a stream.

## Stream

A JetStream stream is best visualized as an append-only event log with subject
filters.

It is not the same thing as a queue.

```text
Stream: JOBS

sequence  subject                 payload
1         workers.register         worker-a info
2         jobs.train.worker-a      train job 1
3         jobs.logs.job-1          "downloading model..."
4         workers.heartbeat        worker-a alive
5         jobs.results             job 1 completed
6         jobs.train.worker-b      train job 2
```

The stream stores messages. Consumers read from it.

```text
Queue mental model:
  take item -> item disappears

JetStream mental model:
  append message -> stream stores it -> consumers read it -> ACK updates
  consumer state
```

The queue-like part in JetStream is usually the consumer, not the stream.

## EdgeGrid's JOBS Stream

EdgeGrid creates one main application message stream:

```go
const StreamName = "JOBS"
```

The stream captures several subjects:

```go
subjects := []string{
    SubjectTrainWildcard,   // jobs.train.*
    SubjectResults,         // jobs.results
    SubjectCancel,          // jobs.cancel
    SubjectLogsWildcard,    // jobs.logs.*
    SubjectRegister,        // workers.register
    SubjectHeartbeat,       // workers.heartbeat
}
```

So the shape is:

```text
JetStream
  Stream: JOBS
    captures:
      jobs.train.*
      jobs.results
      jobs.cancel
      jobs.logs.*
      workers.register
      workers.heartbeat
```

There is not one stream per subject here. There is one stream named `JOBS` that
stores messages for all configured matching subjects.

Small caveat: JetStream KV buckets and object stores are also backed by
JetStream internally, but the main app event stream is `JOBS`.

## EnsureStream

`EnsureStream` makes startup repeatable. It creates or updates the `JOBS` stream
so the app can rely on it existing.

```go
_, err := b.JS.StreamInfo(StreamName)
if err != nil {
    _, err = b.JS.AddStream(&nats.StreamConfig{
        Name:     StreamName,
        Subjects: subjects,
        Replicas: b.Replicas,
    })
} else {
    _, err = b.JS.UpdateStream(&nats.StreamConfig{
        Name:     StreamName,
        Subjects: subjects,
        Replicas: b.Replicas,
    })
}
```

### AddStream vs UpdateStream

`AddStream` creates a brand-new stream.

```text
JOBS does not exist -> AddStream
```

`UpdateStream` changes/verifies the configuration of an existing stream.

```text
JOBS already exists -> UpdateStream
```

This matters because the app may restart many times. The first run creates the
stream. Later runs verify or update it.

Example: if the code later adds a subject like `jobs.metrics.*`, `UpdateStream`
can add that subject to the existing stream config.

Not every JetStream stream property can always be changed freely after creation,
but updating subject coverage is a normal reason to call `UpdateStream`.

## Consumer

A consumer is a reader over a stream. It has its own delivery state.

The stream is the stored log. The consumer is the cursor/bookmark that says:

```text
which messages have been delivered
which messages have been ACKed
which messages should be redelivered
where this reader should resume after reconnect
```

Diagram:

```text
Stream: JOBS

1 workers.register
2 jobs.train.worker-a       <-- worker-a consumer reads this
3 jobs.logs.job-1           <-- logs consumer reads this
4 jobs.results              <-- coordinator results consumer reads this
5 jobs.train.worker-b       <-- worker-b consumer reads this

Consumers:
  training-consumer-worker-a -> subject filter jobs.train.worker-a
  training-consumer-worker-b -> subject filter jobs.train.worker-b
  coord-results              -> subject filter jobs.results
  log subscriber             -> subject filter jobs.logs.<jobID>
```

In EdgeGrid, a worker listens only to its own training subject:

```go
subject := broker.SubjectTrainPrefix + a.id
durableConsumer := "training-consumer-" + a.id

sub, err := a.broker.JS.PullSubscribe(
    subject,
    durableConsumer,
    nats.ManualAck(),
)
```

If the worker ID is `worker-a`, the subject is:

```text
jobs.train.worker-a
```

That means worker A is not competing for every job in `jobs.train.*`. The
coordinator has already selected worker A and published the job to worker A's
specific subject.

## Subscribe vs PullSubscribe

JetStream supports both push-style and pull-style delivery.

`Subscribe`
: Push style. NATS/JetStream pushes messages into your callback when they
arrive.

```go
sub, err := a.broker.JS.Subscribe(
    broker.SubjectCancel,
    func(msg *nats.Msg) {
        // handle message immediately
        msg.Ack()
    },
    nats.DeliverNew(),
    nats.ManualAck(),
)
```

In this style, application code does not call `Fetch`. The server delivers
messages to the callback.

`PullSubscribe`
: Pull style. Code creates or opens a consumer, then explicitly fetches messages
when it is ready.

```go
sub, err := a.broker.JS.PullSubscribe(
    "jobs.train.worker-a",
    "training-consumer-worker-a",
    nats.ManualAck(),
)

msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
```

In this style, the worker controls demand:

```text
Fetch(1) = I am ready for at most one message now
```

That is why EdgeGrid uses `PullSubscribe` for training jobs. Jobs are expensive,
so the worker should pull work only when it is ready to run it.

EdgeGrid uses push-style `Subscribe` for lightweight signals like cancellation.
Cancel messages should be delivered to the callback immediately, and handling
them is cheap.

### Where Is the Consumer in Subscribe?

`Subscribe` still uses a JetStream consumer. It is just less visible in the API.

```text
Subscribe(...)
  -> creates/uses a push consumer
  -> server pushes messages to callback

PullSubscribe(...)
  -> creates/uses a pull consumer
  -> client calls Fetch(...)
```

If `Subscribe` is used without a durable name, JetStream can create an
ephemeral push consumer behind the scenes. That consumer still tracks delivery
and ACK state, but the code did not give it an explicit durable name.

With `PullSubscribe`, the durable name is explicit in EdgeGrid:

```go
durableConsumer := "training-consumer-" + a.id
```

So the worker job consumer survives reconnects/restarts under the same durable
identity.

## Push, Pull, and the Worker Listener

EdgeGrid uses a hybrid shape:

```text
Coordinator decides the worker:
  publish job to jobs.train.worker-a

Worker pulls from its own inbox:
  PullSubscribe("jobs.train.worker-a", "training-consumer-worker-a")
```

So assignment is push-style:

```text
coordinator -> jobs.train.<workerID>
```

Delivery is pull-style:

```text
worker fetches from its JetStream consumer
```

This is useful because:

```text
the coordinator controls placement
the worker controls when it fetches
JetStream tracks ACK/NAK/redelivery
```

## ACK, NAK, NAK With Delay, and Term

With manual ACK enabled, the worker must explicitly tell JetStream what happened
to a delivered message.

`Ack`
: The message was processed successfully by this consumer.

`Nak`
: The message was not processed. JetStream may redeliver it.

`NakWithDelay`
: The message was not processed. Redeliver it later, after a delay.

`Term`
: Stop redelivering this message to this consumer. Used when the message itself
is bad and retrying would not help.

EdgeGrid examples:

```go
msg.Ack()
msg.Nak()
msg.NakWithDelay(10 * time.Second)
msg.Term()
```

Worker busy case:

```go
if !a.busy.CompareAndSwap(false, true) {
    msg.NakWithDelay(10 * time.Second)
    return
}
```

That means:

```text
worker is already busy
tell JetStream "not now"
retry delivery later
```

Bad payload case:

```go
if err := proto.Unmarshal(msg.Data, &req); err != nil {
    msg.Term()
    return
}
```

That means:

```text
this message cannot be decoded
do not redeliver forever
```

## Timeout / Ack Wait

If JetStream delivers a message and the consumer does not ACK it before the ack
wait window expires, JetStream treats it like the worker failed to finish.

```text
message delivered
  |
  |-- ACK before timeout -> done for this consumer
  |
  |-- NAK -> redeliver according to policy
  |
  |-- no ACK before timeout -> redeliver according to policy
```

This is different from stream retention.

```text
ACK/NAK/timeout = consumer delivery state
retention/limits = whether the message remains stored in the stream
```

ACK does not necessarily mean "delete this message from the event log right
now." It means "this consumer handled it." Whether the stream keeps or removes
the message depends on the stream's retention policy and limits.

## Rejection vs NAK in EdgeGrid

There are two related but separate mechanisms:

1. JetStream-level NAK
2. App-level worker rejection

JetStream NAK is about delivery of a message to a consumer:

```text
I could not process this delivery; JetStream should redeliver it.
```

App-level rejection is an EdgeGrid protocol message:

```text
worker publishes workers.reject with job_id and worker_id
coordinator requeues the job
coordinator records this worker in RejectedBy
coordinator tries another free worker
```

That matters for the issue discussed earlier: without memory of who rejected a
job, a system can loop and re-offer the same task to the same worker. EdgeGrid
stores `RejectedBy` in job state so `TryDispatchQueued` skips workers that
already rejected that job.

## NATS Core vs JetStream in EdgeGrid

EdgeGrid intentionally uses both.

NATS Core via `Conn`:

```text
workers.reject
workers.decision.<workerID>.<jobID>
workers.stats.*
```

These are short-lived signals. Persistence has little value because stale
versions can be harmful or noisy.

JetStream via `JS`:

```text
jobs.train.*
jobs.results
jobs.cancel
jobs.logs.*
workers.register
workers.heartbeat
jobs_state KV
workers KV
datasets object store
checkpoints object store
```

These need durability, replay, or shared state.

## KV Buckets

JetStream KV is a key-value store backed by JetStream.

EdgeGrid helper:

```go
kv, err := b.JS.KeyValue(bucket)
if err != nil {
    kv, err = b.JS.CreateKeyValue(&nats.KeyValueConfig{
        Bucket:   bucket,
        TTL:      ttl,
        Replicas: b.Replicas,
    })
}
```

Examples:

```text
workers
  worker ID -> worker info, state, stats, current job
  TTL: 1 minute

jobs_state
  job ID -> QUEUED/RUNNING/COMPLETED/etc.
  TTL: 24 hours

node_auth
  node ID -> approved credentials
```

KV TTL means individual keys expire after the configured duration. For example,
if a worker stops refreshing its presence, the `workers` key can expire and the
coordinator can treat that worker as gone.

## Object Store

JetStream Object Store stores larger blobs.

EdgeGrid uses it for:

```text
datasets
checkpoints
```

The helper creates or retrieves a bucket:

```go
obs, err := b.JS.ObjectStore(bucket)
if err != nil {
    obs, err = b.JS.CreateObjectStore(&nats.ObjectStoreConfig{
        Bucket:   bucket,
        TTL:      ttl,
        Storage:  nats.FileStorage,
        Replicas: b.Replicas,
    })
}
```

This keeps large file-like data out of normal job messages.

## Clustering: How Coordinators Find Each Other

A NATS cluster is defined by three things matching across servers, plus a live
route connection:

```text
Cluster.Name       must match (e.g. "edgegrid")
Cluster.Username/Password   shared route auth (cluster.secret)
route connection   at least one, to get discovered
```

Clustering is full mesh, not hub-and-spoke. A joining server only needs to
dial one existing member (the "seed"); after that, membership is gossiped:

```text
Boot:
  Primary A (no routes configured, first up)

Secondary B joins via --join, receives clusterRoutes=[A]:
  B dials A:6222
  A's INFO tells B about everyone A already knows (nobody yet)
  A <---route---> B

Secondary C joins via --join (approved by A OR B), receives clusterRoutes=[whoever approved it]:
  C dials that node, say B, at 6222
  B's INFO tells C about A too
  C automatically opens a direct route to A as well

Result: full mesh, not routed through a hub
  A <---route---> B
  A <---route---> C
  B <---route---> C
```

The "seed" only matters for first contact. Nobody is a permanent hub — every
coordinator ends up with a direct route to every other coordinator.

JetStream replication (Raft/"NRG") does not use a separate connection. It runs
as system-level messages over these same route connections.

## Client Port vs Route Port

```text
4222  client protocol (CONNECT / PUB / SUB / MSG)
      workers, dashboards, any nats.Connect(...) caller
      authenticated via Users list (per-node username/password)

6222  server-to-server route protocol
      coordinators only, gossip + JetStream/Raft replication
      authenticated via Cluster.Username/Password (shared "cluster" secret)
```

A worker only ever speaks the 4222 protocol. It never opens or knows about
6222 connections — that's coordinator-to-coordinator only.

## How a Worker Connects (and What Happens After)

```go
nc, err := nats.Connect(cfg.NatsURL, connectOpts...)
```

`cfg.NatsURL` is one specific address — from `--nats`/`NATS_URL`, or from
`joinResult.CoordURL` if the worker joined via `--join`. The initial dial is
deterministic: exactly the coordinator named there, never random.

```text
Worker                          Coordinator (whichever cfg.NatsURL points to)
  |-- TCP connect :4222 ------------>|
  |-- CONNECT {user,pass} ---------->|
  |<----------------- INFO ----------|
  |   (includes connect_urls: other known cluster peers, if any)
  |
  |-- PUB / SUB ------- normal traffic ------->|
```

`INFO`'s `connect_urls` field (real NATS protocol, re-sent whenever cluster
topology changes) is how the client *library* learns about other cluster
members. `nats.go` adds them to an internal reconnect pool. Since this repo
never sets `NoRandomize`, a dropped connection can reconnect to a *different*
coordinator than the one first dialed — but the first connection is always
the configured one.

## Publish Routing Across the Cluster

The worker's publish never leaves its one live connection. Fan-out across the
cluster, if needed, is the *server's* job, not the client's:

```text
worker --PUB jobs.results--> Coordinator A (worker's only connection)
                                  |
                                  | Coordinator A has no local subscriber,
                                  | but Coordinator B does (subject-interest
                                  | propagated via route gossip)
                                  v
                             Coordinator B ---> delivers to its subscriber
```

The worker never knows this forwarding happened. It only ever "pubs to my one
connection" — subject-based routing across 6222 does the rest.

## Queue Groups vs Broadcast Subscribe

Two different subscription models, chosen per subscription, not cluster-wide:

```text
Subscribe (no group)            -- broadcast --
  subject: workers.stats.*
  every subscriber gets every message

QueueSubscribe(subject, "coord-results")   -- load-balanced --
  subject: jobs.results
  only ONE member of group "coord-results" gets each message
  (prevents two coordinators both marking the same job completed)
```

## Pub/Sub Is Not Request/Reply

Publishing is fire-and-forget — no implicit reply, no round trip. (NATS does
offer a real request/reply pattern, `nc.Request(...)`, built on pub/sub with
an auto-generated inbox subject — this codebase does not use it anywhere.)

```text
Coordinator publishes job:
  jobs.train.worker-a  { job_id: "42", ... }
  -- done, no reply expected --

... time passes, unrelated to the above ...

Worker independently publishes result:
  jobs.results  { job_id: "42", status: "completed" }
```

These are two separate messages on two separate subjects. Nothing at the
protocol level links them — the app correlates them itself, by `job_id`
inside the payload.

## Replicas

`Replicas` controls how many NATS server nodes store a copy of a JetStream
asset.

For the `JOBS` stream:

```go
Replicas: b.Replicas
```

If `b.Replicas == 1`:

```text
JOBS stream
  one copy on one NATS server
```

Good for local development.

If `b.Replicas == 3`:

```text
JOBS stream
  replica on NATS server A
  replica on NATS server B
  replica on NATS server C
```

JetStream uses Raft internally for replicated assets. With three replicas, the
system can usually tolerate one replica node failure while keeping the data
available.

Replicas apply to more than streams. EdgeGrid also passes `b.Replicas` when
creating:

```text
KV buckets
Object Store buckets
```

Subject coverage decides what messages go into a stream. Replicas decide how
many NATS nodes store copies of that persisted data.

```text
Subjects:
  what gets stored

Replicas:
  how many NATS nodes store it
```

Important caveat: `Replicas: 3` requires enough JetStream-capable NATS servers.
A single-node dev server should use `Replicas: 1`.

## End-to-End Job Message Flow

```text
HTTP submit
  |
  v
Coordinator writes jobs_state[jobID] = QUEUED
  |
  v
Coordinator picks worker-a
  |
  v
JetStream publish:
  subject = jobs.train.worker-a
  stream  = JOBS captures it via jobs.train.*
  |
  v
Worker-a consumer:
  PullSubscribe("jobs.train.worker-a", "training-consumer-worker-a")
  |
  v
Worker fetches message
  |
  |-- busy -> NakWithDelay
  |-- bad payload -> Term
  |-- runs job -> publishes logs/results
  |
  v
Worker Ack after successful handling
```

## Log Replay Flow

```text
Training executor prints line
  |
  v
Worker publishes to JetStream:
  jobs.logs.<jobID>
  |
  v
JOBS stream stores it via jobs.logs.*
  |
  v
Client opens /jobs/{id}/logs later
  |
  v
Coordinator subscribes with DeliverAll
  |
  v
JetStream replays old log lines, then streams new ones
```

This is why logs use JetStream instead of plain NATS Core. Plain NATS Core would
lose messages if nobody was subscribed at the exact time they were published.

## Quick Glossary

`Conn`
: The live client connection to NATS.

`JetStreamContext`
: API handle for persistent JetStream features through a NATS connection.

`Subject`
: Routing address, such as `jobs.train.worker-a`.

`Publish`
: Send a message to a subject.

`Subscribe`
: Receive messages from a subject.

`Stream`
: Persistent event log that stores messages matching configured subjects.

`Consumer`
: Reader/cursor over a stream. Tracks delivery, ACKs, redelivery, and resume
position.

`Durable consumer`
: A named consumer whose state survives reconnects/restarts.

`PullSubscribe`
: Consumer mode where the client explicitly fetches messages.

`QueueSubscribe`
: Subscription mode where multiple subscribers in a group share work.

`ACK`
: Message handled successfully by this consumer.

`NAK`
: Message was not handled; redeliver according to policy.

`NAK with delay`
: Redeliver later after a specific delay.

`Term`
: Stop redelivering this message to this consumer.

`AckWait`
: Time JetStream waits for ACK before considering the delivery failed.

`Retention`
: Stream policy that decides how long messages remain stored.

`KV bucket`
: JetStream-backed key-value store.

`Object Store`
: JetStream-backed blob/file store.

`Replicas`
: Number of NATS server nodes that store copies of a JetStream asset.

## Best Mental Picture

```text
NATS cluster
  |
  |-- connection: nc
  |
  |-- Core subjects
  |     workers.reject
  |     workers.stats.*
  |     workers.decision.<workerID>.<jobID>
  |
  |-- JetStream context: js
        |
        |-- stream: JOBS
        |     stored event log
        |     captures jobs.train.*, jobs.logs.*, jobs.results, ...
        |
        |-- consumers
        |     training-consumer-worker-a reads jobs.train.worker-a
        |     coord-results reads jobs.results
        |     log subscriber reads jobs.logs.<jobID>
        |
        |-- KV buckets
        |     workers
        |     jobs_state
        |     node_auth
        |
        |-- object stores
              datasets
              checkpoints
```

The sharpest version:

```text
Stream stores history.
Subject labels/routes messages.
Consumer creates queue-like behavior.
ACK/NAK controls delivery state.
Retention controls stored-message lifetime.
Replicas control how many NATS nodes persist the data.
```
