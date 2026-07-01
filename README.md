# EdgeGrid

**EdgeGrid** is a decentralized ML training network built on personal computers. Submit a training job via HTTP, and EdgeGrid finds a capable machine in the network, runs the job, streams logs back in real time, and stores the model checkpoint — with no manual setup on the worker machine beyond running the agent.

```
You                    Coordinator                  Worker (friend's PC)
 │                          │                              │
 │  POST /jobs              │                              │
 │  {script, requirements,  │                              │
 │   requires_gpu: true}    │                              │
 │─────────────────────────>│                              │
 │                          │  match GPU/RAM/disk reqs     │
 │                          │  assign worker               │
 │                          │─────────────────────────────>│
 │                          │                              │ pip install (cached)
 │                          │                              │ run train.py
 │  GET /jobs/{id}/logs     │                              │
 │─────────────────────────>│<── log lines via JetStream ──│
 │<── SSE stream ───────────│                              │
 │  Epoch 1/10 loss=0.84    │                              │
 │  Epoch 2/10 loss=0.71    │                              │
 │  ...                     │                              │
 │                          │<── checkpoint + result ──────│
 │  GET /jobs/{id}/artifact │                              │
 │─────────────────────────>│                              │
 │<── model.tar.gz ─────────│                              │
```

---

## How it works

EdgeGrid is fully event-driven. Workers have no inbound ports from the coordinator — they pull jobs from NATS JetStream. All state (job lifecycle, worker registry, checkpoints) lives in NATS, not in the coordinator process. The coordinator can crash and restart with zero data loss.

**Coordinator** — HTTP API for job submission and status. Matches job hardware requirements (GPU, RAM, VRAM, disk) to registered workers. Dispatches jobs directly to the matching worker's personal NATS subject. If no worker is free, the job stays QUEUED and is auto-dispatched when capacity appears.

**Worker** — Registers its hardware capabilities at startup. Listens for jobs addressed to it. Runs training inside an isolated directory with a cached Python venv. Streams stdout/stderr to NATS as log lines. Pushes `output/` as a checkpoint every 5 minutes during training and once on completion.

**NATS JetStream** — The single source of truth. Carries job messages, log lines, results, heartbeats, and cancel signals. Stores worker state and job state in KV buckets. Stores datasets and checkpoints in object store buckets. Replication is configurable for production clusters.

---

## Features

- **Intelligent routing** — jobs matched to workers by GPU, VRAM, RAM, and disk requirements
- **Job queuing** — no free worker? job waits and auto-dispatches when one becomes available
- **CAS-safe dispatch** — multiple coordinators can run simultaneously without double-assigning workers
- **Log streaming** — real-time stdout/stderr via SSE; late-connecting clients get full replay from the start
- **Job cancellation** — `DELETE /jobs/{id}` kills the running Python process on the worker
- **Mid-training checkpointing** — `output/` uploaded to object store every 5 minutes during training
- **Stale job recovery** — if a worker dies, the job is automatically requeued within ~90 seconds
- **Venv caching** — SHA256(requirements.txt) keyed venvs; repeated jobs with the same deps skip pip install
- **Single binary** — run as coordinator, worker, or both

---

## Getting Started

### Prerequisites

- Go `1.21+`
- NATS Server with JetStream enabled
- Python 3 (only for `training` executor — auto-detected on the worker machine)

### Build

```bash
git clone https://github.com/edgegrid/edgegrid.git
cd edgegrid
go build -o edgegrid ./cmd/edgegrid
```

### Run locally (single node, dev)

```bash
# Terminal 1 — NATS
nats-server -js

# Terminal 2 — coordinator + worker (default: both enabled, mock executor)
./edgegrid

# Terminal 3 — submit a training job
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "training_script": "import os\nprint(\"training...\")\nopen(os.environ[\"OUTPUT_DIR\"]+\"/model.pt\",\"w\").write(\"weights\")",
    "dataset_ref": "my-dataset",
    "requires_gpu": false
  }'
# → {"job_id":"a1b2c3d4","status":"queued"}

# Stream logs
curl -N http://localhost:8080/jobs/a1b2c3d4/logs

# Check status
curl http://localhost:8080/jobs/a1b2c3d4

# Cancel
curl -X DELETE http://localhost:8080/jobs/a1b2c3d4
```

### Run as separate coordinator and worker

```bash
# Coordinator only
./edgegrid -server -nats nats://localhost:4222 -port 8080

# Worker only (on another machine)
./edgegrid -client -nats nats://coordinator:4222 -executor training -worker-id worker-gpu-01
```

---

## Configuration

Flags take precedence over environment variables. If neither `-server` nor `-client` is passed, both are enabled.

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-server` | — | `true`* | Enable coordinator (HTTP API) |
| `-client` | — | `true`* | Enable worker |
| `-nats` | `NATS_URL` | `nats://localhost:4222` | NATS connection URL |
| `-port` | `PORT` | `8080` | Coordinator HTTP API port |
| `-worker-id` | `WORKER_ID` | auto-generated | Custom worker identifier |
| `-executor` | `EXECUTOR` | `mock` | Executor backend (`mock` or `training`) |
| `-replicas` | `NATS_REPLICAS` | `1` | NATS JetStream replication factor (1=dev, 3=prod) |

---

## HTTP API

### `POST /jobs` — Submit a training job

```json
{
  "training_script":      "print('hello')",
  "requirements":         "torch==2.0.0\nnumpy==1.24.0",
  "dataset_type":         "object_store",
  "dataset_ref":          "my-dataset-key",
  "base_model_type":      "hf",
  "base_model_ref":       "bert-base-uncased",
  "training_config_json": "{\"epochs\": 10, \"lr\": 0.001}",
  "requires_gpu":         true,
  "min_ram_gb":           16.0,
  "min_vram_gb":          8.0,
  "min_disk_gb":          20.0
}
```

Response `202 Accepted`:
```json
{"job_id": "a1b2c3d4", "status": "queued"}
```

Hardware requirement fields are all optional. Omit them and any free worker qualifies.

### `GET /jobs/{id}` — Job status

```json
{
  "job_id":         "a1b2c3d4",
  "state":          "COMPLETED",
  "worker_id":      "worker-gpu-01",
  "checkpoint_key": "a1b2c3d4",
  "updated_at":     "2026-07-01T12:34:56Z"
}
```

### `GET /jobs/{id}/logs` — Live log streaming (SSE)

```bash
curl -N http://localhost:8080/jobs/a1b2c3d4/logs
# data: Epoch 1/10 loss=0.842
# data: Epoch 2/10 loss=0.761
# ...
# event: done
# data: COMPLETED
```

Connects via Server-Sent Events. Late-connecting clients receive all prior log lines from the beginning (JetStream `DeliverAll`). Stream closes with `event: done` when the job reaches a terminal state.

### `DELETE /jobs/{id}` — Cancel a job

Cancels a `QUEUED` or `RUNNING` job. Returns `202 Accepted`. The job state becomes `CANCELLED`. If running, the training process on the worker is killed within seconds.

### `POST /jobs/{id}/upload` — Upload a dataset

Upload a dataset file for the job before or after submission (referenced by `dataset_ref` in the job request).

### `GET /jobs/{id}/artifact` — Download checkpoint

Downloads the latest model checkpoint as a `.tar.gz` archive. Available after training completes or during training (mid-training checkpoints are uploaded every 5 minutes).

### `GET /health`

Returns `200 ok`. Used by load balancers and Docker Compose health checks.

---

## Job Lifecycle

```
QUEUED ──► RUNNING ──► COMPLETED
   │                      
   │        └──────────► FAILED
   │
   └──────────────────► CANCELLED
```

- **QUEUED** — job created, waiting for a capable worker
- **RUNNING** — dispatched to a worker, training in progress
- **COMPLETED** — training finished, checkpoint available
- **FAILED** — training script exited with a non-zero status
- **CANCELLED** — cancelled via `DELETE /jobs/{id}`

If a worker dies mid-job, the stale job recovery process requeues it to `QUEUED` within ~90 seconds.

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                    NATS JetStream Cluster                 │
│                                                          │
│  JOBS stream        workers_state KV    jobs_state KV    │
│  jobs.train.*       (TTL: 1 min)        (TTL: 24h)       │
│  jobs.results       worker caps,        job state,        │
│  jobs.logs.*        free/busy state     RequestProto      │
│  jobs.cancel                                             │
│  workers.register   datasets store      checkpoints store │
│  workers.heartbeat  (TTL: 48h)          (TTL: 7 days)    │
└──────────────────────────────────────────────────────────┘
         ▲                                    ▲
         │                                    │
┌────────┴────────┐                  ┌────────┴────────┐
│   Coordinator   │                  │     Worker      │
│                 │                  │                 │
│  HTTP API       │                  │  RegisterWorker │
│  Job routing    │                  │  StartHeartbeat │
│  TryDispatch    │                  │  StartJobListen │
│  StaleRecovery  │                  │  StartCancel    │
│                 │                  │  TrainingExec   │
└─────────────────┘                  └─────────────────┘
```

### Packages

| Package | Responsibility |
|---|---|
| `cmd/edgegrid` | Binary entrypoint |
| `internal/agent` | Boots coordinator and/or worker from a shared NATS connection |
| `internal/coordinator` | HTTP API, job routing, dispatch, stale recovery |
| `internal/coordinator/workerman` | Worker KV registry, capability matching, CAS assignment |
| `internal/worker` | Job listener, heartbeat, registration, cancel listener |
| `internal/worker/executor` | Training executor (venv cache, script runner) and mock |
| `internal/broker` | NATS JetStream, KV, and Object Store helpers |
| `internal/jobstate` | Job state read/write helpers |
| `internal/proto/worker` | Protobuf schemas and generated Go code |

---

## Documentation

Detailed design docs are in [`docs/`](./docs/):

| Doc | What it covers |
|---|---|
| [`intelligent-routing.md`](./docs/intelligent-routing.md) | Hardware capability matching, why routing is coordinator-owned |
| [`training-executor.md`](./docs/training-executor.md) | Venv caching, script execution, environment injection |
| [`job-queuing.md`](./docs/job-queuing.md) | FIFO queue, RequestProto persistence, CAS dispatch |
| [`log-streaming.md`](./docs/log-streaming.md) | JetStream + SSE, DeliverAll for late clients |
| [`job-cancellation.md`](./docs/job-cancellation.md) | Per-job context, cancel signal broadcast, state protection |
| [`reliability.md`](./docs/reliability.md) | Stale job recovery, mid-training checkpointing |
| [`nats-raft-replicas.md`](./docs/nats-raft-replicas.md) | Raft consensus, replication, stateless coordinator design |
