# EdgeGrid: Decentralized Embedding Inference Network

**EdgeGrid** is a decentralized, pull-based AI embedding inference network. It coordinates heterogeneous worker nodes (laptops, PCs, VMs) to generate text embeddings asynchronously via **NATS JetStream**, routed by model compatibility.

---

## Architecture

EdgeGrid is fully event-driven. Workers have no inbound ports — they pull work from NATS.

* **Coordinator (Control Plane)**:
  * Exposes a REST API (`POST /jobs`, `GET /jobs/<id>`) to submit and track embedding jobs.
  * Publishes jobs to model-specific NATS subjects (e.g. `jobs.build.all-minilm`).
  * Subscribes to worker registration, heartbeats, and result events.
  * Stores worker registry and job lifecycle state in **NATS KV** buckets (`workers`, `jobs_state`).
* **Worker (Client Node)**:
  * Announces supported models (e.g. `all-minilm`) to the registry.
  * Sends periodic heartbeats.
  * Pulls pending jobs via NATS JetStream durable pull consumers.
  * Runs local inference through a pluggable **executor** and publishes results to `jobs.results`.
* **NATS JetStream (Message Broker)**:
  * Durable job queue and event bus. Jobs are buffered and distributed to matching workers.

### Unified Agent Binary

A single `edgegrid` binary can run as a coordinator, a worker, or both (default for local development):

```bash
# Coordinator + worker on one node (dev mode)
./edgegrid

# Coordinator only
./edgegrid -server

# Worker only
./edgegrid -client -models all-minilm -executor mock
```

---

## Core Components

| Component | Responsibility |
| :--- | :--- |
| **`cmd/edgegrid`** | Main entrypoint; boots the unified P2P agent |
| **`internal/agent`** | Orchestrates coordinator and/or worker from a shared NATS connection |
| **`internal/coordinator`** | HTTP API, worker registry, job state, result accumulation |
| **`internal/worker`** | Job pull listeners, heartbeats, registration |
| **`internal/worker/executor`** | Pluggable inference backends (`mock`, `huggingface`) |
| **`internal/broker`** | Shared NATS JetStream and KV helpers |
| **`internal/proto/worker`** | Protobuf schemas (`worker.proto`) and generated Go models |

### Executors

| Executor | Description |
| :--- | :--- |
| **`mock`** | Deterministic normalized vectors for testing and Docker Compose (no Python required) |
| **`huggingface`** | Real inference via a Python sidecar (`runner.py`) over Unix domain sockets + Protobuf |

Set via flag or environment variable:

```bash
EXECUTOR=mock    # fast, no dependencies
EXECUTOR=huggingface  # requires Python 3; venv is auto-provisioned on first run
```

---

## Getting Started

### Prerequisites

* **Go**: `1.24+`
* **NATS Server**: Running with JetStream enabled (`nats-server -js`)
* **Python 3** (only for `huggingface` executor): auto-venv at `internal/worker/executor/.venv`

### Setup & Build

```bash
git clone https://github.com/edgegrid/edgegrid.git
cd edgegrid

make proto   # generate protobuf Go code
make build   # produces ./edgegrid binary
make test    # run all tests
```

---

## Local End-to-End Test

### Option A: Single binary (quickest)

```bash
# Terminal 1 — NATS
nats-server -js

# Terminal 2 — agent (coordinator + worker, mock executor)
NATS_URL=nats://localhost:4222 EXECUTOR=mock go run ./cmd/edgegrid

# Terminal 3 — submit a job
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{"model_name": "all-minilm", "input_text": "EdgeGrid decentralized embedding inference"}'

# Check job status (replace <job_id> with the id from the response)
curl http://localhost:8080/jobs/<job_id>
```

### Option B: Docker Compose (multi-worker)

Runs NATS, one coordinator, and five mock workers:

```bash
make compose-up
```

Submit a job:

```bash
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{"model_name": "all-minilm", "input_text": "hello from docker compose"}'
```

Tear down:

```bash
make compose-down
```

> **Note:** The Docker image is Go-only. Use `EXECUTOR=mock` in containers. For real HuggingFace inference, run the worker on bare metal with `EXECUTOR=huggingface`.

---

## Configuration

Flags take precedence over environment variables.

| Flag | Env var | Default | Description |
| :--- | :--- | :--- | :--- |
| `-server` | — | `true`* | Enable coordinator (HTTP API) |
| `-client` | — | `true`* | Enable worker |
| `-nats` | `NATS_URL` | `nats://localhost:4222` | NATS connection URL |
| `-port` | `PORT` | `8080` | Coordinator HTTP port |
| `-models` | `SUPPORTED_MODELS` | `all-minilm` | Comma-separated model list |
| `-worker-id` | `WORKER_ID` | auto-generated | Custom worker identifier |
| `-executor` | `EXECUTOR` | `huggingface` | Executor backend (`mock` or `huggingface`) |

\*If neither `-server` nor `-client` is passed, both are enabled.

### Job Lifecycle States

Jobs progress through NATS KV (`jobs_state` bucket):

`QUEUED` → `RUNNING` → `COMPLETED` | `FAILED`

Poll status via `GET /jobs/<job_id>`.

---

## HTTP API

### `POST /jobs`

Submit an embedding job.

```json
{"model_name": "all-minilm", "input_text": "your text here"}
```

Response (`202 Accepted`):

```json
{"job_id": "a1b2c3d4", "status": "queued"}
```

### `GET /jobs/<job_id>`

Retrieve full job state including embedding vector on completion.

### `GET /health`

Health check endpoint used by Docker Compose.