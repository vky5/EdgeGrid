# NATS JetStream, Raft Consensus, and Replicas

## The core idea

Every piece of EdgeGrid state — which workers exist, what jobs are running, what their logs say, where the checkpoints are stored — lives inside NATS JetStream, not inside the coordinator process. The coordinator has no in-memory state of its own. It is purely a message router that reads from and writes to NATS.

This is not just a design choice — it is what makes the whole system resilient. If the coordinator crashes, you restart it and it picks up exactly where it left off. If a NATS node crashes, the Raft protocol elects a new leader and the cluster keeps running. No data is lost. No manual recovery. No state to reconstruct.

Understanding why requires understanding how JetStream uses Raft.

---

## What Raft is

Raft is a distributed consensus algorithm. Its job: given a cluster of N nodes, ensure they all agree on the same sequence of writes, even when some nodes crash or become unreachable.

The key properties:

**Leader election** — at any moment, exactly one node is the leader. All writes go through the leader. If the leader dies, the remaining nodes elect a new one automatically. This takes milliseconds to seconds.

**Write quorum** — a write is only committed when a majority of nodes acknowledge it. For a 3-node cluster: 2 out of 3 must confirm. For a 5-node cluster: 3 out of 5. This is called the quorum.

```
3-node cluster:  quorum = 2  → tolerates 1 node failure
5-node cluster:  quorum = 3  → tolerates 2 node failures
1-node cluster:  quorum = 1  → tolerates 0 failures (dev mode)
```

**Why majority and not all nodes** — if you required all nodes to confirm, one slow or crashed node would block every write forever. Majority means you can lose up to `floor(N/2)` nodes and still make progress.

**Split brain prevention** — if a network partition splits a 3-node cluster into a group of 2 and a group of 1, only the group of 2 can reach quorum and continue accepting writes. The group of 1 refuses writes — it knows it cannot guarantee consistency without hearing from a majority.

---

## How JetStream uses Raft

JetStream is NATS's persistence layer. Under the hood, each JetStream asset — a stream, a KV bucket, an object store — runs its own Raft group among the nodes that store replicas of it.

When you write a message to a stream with `Replicas: 3`:
1. Your client sends the message to any NATS node
2. That node forwards it to the Raft leader for this stream's group
3. The leader appends it to its log and sends it to the other 2 replicas
4. Once 2 out of 3 nodes confirm the write, the leader acknowledges success to your client
5. The message is now durably stored — it survives any single node failure

When you read from that stream on another node, the read is served from that node's local replica. Reads are fast and do not go through the leader.

This is transparent to the application. The NATS Go client just calls `js.Publish()`. Whether there is 1 replica or 5, the API is identical.

---

## How replicas flow through EdgeGrid

A single integer, `Replicas`, is set at startup and flows into every JetStream asset EdgeGrid creates.

### Config — where it comes from

```go
// internal/config/config.go

replicas := flag.Int("replicas", 0, "NATS JetStream replication factor")
flag.Parse()

finalReplicas := *replicas
if finalReplicas == 0 {
    finalReplicas = envInt("NATS_REPLICAS", 1)  // env var fallback, default 1
}
if finalReplicas < 1 {
    finalReplicas = 1  // floor at 1, never 0
}
```

Two ways to set it:
- `--replicas 3` flag
- `NATS_REPLICAS=3` environment variable

Default is 1 (single-node dev mode). In production with a 3-node NATS cluster, set it to 3.

### Broker — stored once, used everywhere

```go
// internal/broker/broker.go

type Broker struct {
    Conn     *nats.Conn
    JS       nats.JetStreamContext
    Replicas int   // set once at construction, used for every asset
}
```

`Replicas` is set when the broker is created in `agent.go` via `broker.NewBroker(nc, cfg.Replicas)`. After that, every JetStream asset creation reads from this single field.

### Stream — covers all pub/sub subjects

```go
// internal/broker/broker.go — EnsureStream

_, err = b.JS.AddStream(&nats.StreamConfig{
    Name:     StreamName,   // "JOBS"
    Subjects: subjects,     // jobs.train.*, jobs.results, jobs.logs.*, workers.register, ...
    Replicas: b.Replicas,   // ← replicated across this many NATS nodes
})
```

The JOBS stream covers every subject EdgeGrid uses for messaging — job dispatch, results, log lines, worker registration, heartbeats, cancels. With `Replicas: 3`, every message published to any of these subjects is stored on 3 nodes. A training job dispatch message survives 1 NATS node failure without loss.

### KV stores — workers and jobs

```go
// internal/broker/kv.go — GetOrCreateKV

kv, err = b.JS.CreateKeyValue(&nats.KeyValueConfig{
    Bucket:   bucket,
    TTL:      ttl,
    Replicas: b.Replicas,  // ← same replica count
})
```

This is called in two places:

**`workers_state` KV** (TTL: 1 minute) — created by `NewWorkerManager`:
```go
kv, err := jsBroker.GetOrCreateKV("workers", 1*time.Minute)
```
Stores every registered worker's hardware info, current state (free/busy), and active job. With `Replicas: 3`, this KV survives a NATS node failure. Workers keep heartbeating. The TTL still fires correctly on remaining nodes.

**`jobs_state` KV** (TTL: 24 hours) — created on demand in API handlers:
```go
kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
```
Stores every job's state, workerID, error, checkpoint key, and `RequestProto` bytes. This is the most critical state in the system. Losing it means losing all knowledge of which jobs exist. With `Replicas: 3`, it survives a NATS node failure.

### Object stores — datasets and checkpoints

```go
// internal/broker/objects.go — GetOrCreateObjectStore

obs, err = b.JS.CreateObjectStore(&nats.ObjectStoreConfig{
    Bucket:   bucket,
    TTL:      ttl,
    Storage:  nats.FileStorage,
    Replicas: b.Replicas,  // ← same replica count
})
```

Two buckets:

| Bucket | TTL | Contents |
|---|---|---|
| `datasets` | 48 hours | Training data uploaded before job starts |
| `checkpoints` | 7 days | Model output tarballs, overwritten each checkpoint |

With `Replicas: 3`, a 10GB dataset file is chunked and stored across 3 NATS nodes. A node failure during training does not interrupt the worker's access to the dataset.

---

## Dev vs prod configuration

### Dev: `--replicas 1` (default)

```
Single NATS node
  ┌─────────────────┐
  │   nats-server   │
  │  JOBS stream    │
  │  workers KV     │
  │  jobs_state KV  │
  │  datasets store │
  │  checkpoints    │
  └─────────────────┘
```

Quorum = 1. Every write is immediately confirmed. No network round-trips for replication. Fast for development. Zero tolerance for failure — if this node dies, the cluster is down.

### Prod: `--replicas 3`

```
3-node NATS cluster (Raft)

  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
  │   nats-node-1   │◄──►│   nats-node-2   │◄──►│   nats-node-3   │
  │  (Raft leader)  │    │   (follower)    │    │   (follower)    │
  │  JOBS stream ✓  │    │  JOBS stream ✓  │    │  JOBS stream ✓  │
  │  workers KV  ✓  │    │  workers KV  ✓  │    │  workers KV  ✓  │
  │  jobs_state  ✓  │    │  jobs_state  ✓  │    │  jobs_state  ✓  │
  └─────────────────┘    └─────────────────┘    └─────────────────┘
         ▲
         │ write confirmed when 2/3 nodes ack
```

Quorum = 2. Tolerates 1 node failure. EdgeGrid keeps running if `nats-node-2` or `nats-node-3` dies. If `nats-node-1` (the leader) dies, Raft elects a new leader from the remaining two in under 1 second. In-flight writes may fail and be retried, but no committed data is lost.

---

## Why coordinators being stateless is the beautiful part

Most distributed systems struggle with coordinator state. If the coordinator holds job state in memory and crashes, you lose everything. Common solutions: write-ahead logs, sticky sessions, leader election just for the coordinator.

EdgeGrid sidesteps all of this. The coordinator holds exactly zero state in memory. Look at the struct:

```go
// internal/coordinator/coordinator.go

type Coordinator struct {
    jsBroker *broker.Broker   // a NATS connection + JetStream context
    manager  *workerman.WorkerManager  // a wrapper around a KV handle
}
```

`jsBroker` is a connection to NATS. `manager` is a handle to the `workers_state` KV bucket. Neither holds any state that isn't already in NATS. If the coordinator process crashes:

1. NATS is unaffected — it's a separate process, possibly on a separate machine
2. All workers keep heartbeating — their KV entries stay alive
3. All jobs stay in their current state — `jobs_state` KV is unchanged
4. Any in-flight NATS messages that weren't acked get redelivered when the coordinator restarts
5. The coordinator restarts, reconnects to NATS, re-subscribes — done

**You can run multiple coordinators simultaneously.** They all connect to the same NATS cluster, read from the same KV stores, and compete to handle the same events. NATS queue groups (`QueueSubscribe` with group name `"coordinators"`) ensure each message is delivered to exactly one coordinator instance:

```go
// internal/coordinator/subscriptions.go

const coordinatorGroup = "coordinators"

c.jsBroker.JS.QueueSubscribe(broker.SubjectRegister, coordinatorGroup, func(msg *nats.Msg) {
    // only one coordinator in the group receives this
})
```

CAS on the workers KV (`TryAssignWorker`) prevents two coordinators from double-assigning the same worker. The combination of queue groups + CAS gives you exactly-once semantics across any number of coordinator instances — no central coordinator election needed.

---

## What gets replicated — summary

```
NATS Cluster (3 nodes, Raft)
│
├── JOBS stream (Replicas: 3)
│     subjects: jobs.train.*, jobs.results, jobs.logs.*,
│               workers.register, workers.heartbeat, jobs.cancel
│     → training job messages, log lines, results, worker events
│
├── workers_state KV (Replicas: 3, TTL: 1 min per key)
│     → worker hardware specs, free/busy state, active job
│
├── jobs_state KV (Replicas: 3, TTL: 24h per key)
│     → job state machine, RequestProto bytes, checkpoint key
│
├── datasets Object Store (Replicas: 3, TTL: 48h)
│     → training data uploaded by job submitter
│
└── checkpoints Object Store (Replicas: 3, TTL: 7 days)
      → model output tarballs (overwritten each checkpoint push)
```

Every write to any of these is replicated before being acknowledged. The EdgeGrid coordinator and workers write once, trust NATS to handle durability.

---

## What a NATS node failure looks like in practice

```
t=0:00  3-node cluster running, job-1 RUNNING on worker-A
         job-1 logs publishing to JOBS stream → acked by 2/3 nodes ✓

t=0:30  nats-node-2 loses power

t=0:30  Raft detects missing heartbeat from nats-node-2
t=0:31  nats-node-1 and nats-node-3 elect new leader (quorum = 2, they have 2)

t=0:31  cluster continues with nats-node-1 as leader, nats-node-3 as follower
         writes still reach quorum (2 out of remaining 2)

t=0:35  worker-A publishes mid-training checkpoint → stored on node-1 and node-3 ✓
t=1:00  job-1 completes, result published → coordinator updates jobs_state KV ✓

         nothing was lost
         no intervention required
         nats-node-2 can rejoin later and sync from the leader
```

From EdgeGrid's perspective, nothing happened. The NATS Go client automatically reconnects and retries if its connection to a node is lost. The JetStream publish call returns only after quorum is reached — the application never sees partial writes.
