# EdgeGrid Execution Lifecycle & IPC

This document describes the execution lifecycle of a job in EdgeGrid, tracing it from NATS JetStream ingestion to local Unix Domain Socket (UDS) Protobuf IPC execution inside the Python sidecar runner.

---

## Architecture Overview

EdgeGrid splits orchestration and execution into dedicated, isolated boundaries:

```text
┌─────────────────┐             ┌─────────────────┐             ┌───────────────────┐
│                 │             │                 │             │                   │
│   Coordinator   │ ── NATS ──> │    Go Worker    │ ── UDS ───> │   Python Runner   │
│   (Scheduler)   │             │   (Controller)  │  (Protobuf) │   (Model Server)  │
│                 │             │                 │             │                   │
└─────────────────┘             └─────────────────┘             └───────────────────┘
```

1. **Coordinator**: Publishes jobs to model-specific NATS JetStream subjects.
2. **Go Worker**: Orchestrates NATS pull subscriptions, tracks model runners, manages heartbeats, and dispatches tasks.
3. **Python Runner (Sidecar)**: Dedicated subprocess running local machine learning inference via Hugging Face/SentenceTransformers.

---

## 1. Bootstrapping & Handshake

When a Worker is started (`worker.Start`):

```text
Go Worker                                                Python Subprocess
    │                                                            │
    │── 1. Create socket file path ─────────────────────────────>│
    │── 2. exec.CommandContext("python3", runner.py, ...) ──────>│
    │                                                            │ (Initialize Model)
    │                                                            │ (Create socket & bind)
    │<── 3. Dial UDS socket periodically (Poll until ready) ─────│ (Start listening)
    │                                                            │
```

1. **Socket Allocation**: Go generates a unique local Unix domain socket path: `internal/worker/executor/runner-<model_name>.sock`. Any pre-existing stale socket file at this location is deleted to prevent binding conflicts.
2. **Environment Autoprovisioning**: The Go executor checks if a Python virtual environment exists at `internal/worker/executor/.venv`. If it does not exist, Go automatically:
   * Creates the virtual environment via `python3 -m venv .venv`.
   * Installs required dependencies via `<venv>/bin/pip install protobuf sentence-transformers`.
3. **Launch Subprocess**: Go spawns the Python sidecar process using the virtual environment's python interpreter:
   ```bash
   ./.venv/bin/python3 runner.py <model_name> <socket_path>
   ```
4. **Model Initialization**: The Python process starts inside the hermetic virtual environment. It loads the model weights from the local `hf_cache` (downloading them from Hugging Face Hub if missing), and binds to the UDS socket.
5. **UDS Dial Poll**: Go dials the UDS socket file every `500ms` for up to `3 minutes`. Once the Dial succeeds, the model is fully loaded in memory and ready for queries.
6. **NATS Capability Advertisement**: The Go worker registers its active models in the NATS Coordinator registry and starts its 10-second heartbeat loop.

---

## 2. Ingestion & Job Pulling

```text
NATS JetStream (JOBS Stream)
            │
            │  (Queue distribution across workers)
            ▼
    Go Worker Listener (sub.Fetch(1))
            │
            ▼
       handleJob()
```

1. **Durable Pull Consumers**: The worker listener opens a subscription on NATS JetStream under the subject `jobs.build.<model_name>` with a durable pull consumer named `consumer-<model_name>`.
2. **First-In-First-Out (FIFO) Delivery**: The worker calls `sub.Fetch(1)` to request exactly one job message. NATS distributes tasks one-by-one to whichever worker is free first.
3. **Receipt**: Once fetched, the message payload is unmarshaled into a Go `workerpb.JobRequest` protobuf structure.

---

## 3. Local IPC (Go Worker ➔ Python Runner)

Data is exchanged between the two processes over the Unix Domain Socket using **binary framing and Protobuf serialization**:

```text
Go Worker                                                Python Runner
    │                                                            │
    │── 1. Connects to socket path ─────────────────────────────>│
    │── 2. Writes: [4-byte length header] + [JobRequest bytes] ─>│
    │                                                            │ (Runs Model Inference)
    │<─ 3. Reads: [4-byte length header] + [JobResponse bytes] ──│
    │── 4. Closes socket connection ────────────────────────────>│
```

### The Message Frame Protocol
To read stream data reliably without partial-read issues or delimiters, messages use a length-prefix:
1. **Length Header**: A `4-byte` big-endian unsigned integer (uint32) indicating the length of the protobuf message payload.
2. **Payload**: The serialized raw bytes of the protobuf message.

### Execution Steps
1. **Serialize Request**: Go marshals the `JobRequest` protobuf (ID, model, input text) into binary bytes.
2. **Write**: Go sends the 4-byte length header followed by the payload bytes to the Unix Domain Socket.
3. **Inference**: Python reads the header, fetches the payload, deserializes it, feeds it to the loaded Hugging Face model, and formats the floating-point vector into a `JobResponse` protobuf.
4. **Serialize Response**: Python serializes `JobResponse` (ID, success flag, float32 embedding slice, error details) to binary bytes and writes it back to Go with a 4-byte length prefix.
5. **Release Socket**: Go reads the response, unmarshals the vector, and closes the connection.

---

## 4. Dispatch & Acknowledgment

Once execution finishes:

1. **Publish Results**: Go publishes the `JobResponse` payload back to the NATS subject `jobs.results`.
2. **Acknowledge Task**: Go calls `msg.Ack()` on the original NATS JetStream message. This tells JetStream the job is successfully processed and can be removed from the message buffer.
3. **Fault Tolerance**: If the worker crashed during execution, `msg.Ack()` is never called. JetStream will redeliver the job to another healthy worker.

---

## 5. Job State Lifecycle Tracking

EdgeGrid tracks the exact state of every submitted job in a distributed NATS KeyValue bucket named `jobs_state`. This allows clients to inspect where a job is at any moment.

### The Job States
* `QUEUED`: The job was accepted by the Coordinator's HTTP API and written to the JetStream message queue.
* `RUNNING`: A worker has pulled the job from JetStream and started execution on its local runner.
* `COMPLETED`: The job completed successfully, and the result was received.
* `FAILED`: The job execution failed (either due to a Python runner exception or a Go broker error).

### Observability API
Clients can track status using standard HTTP routes:
* **Submit Job**: `POST /jobs` (returns `job_id` and initial status `"queued"`).
* **Get Job Status**: `GET /jobs/<job_id>` (returns the full JSON state: status, model, execution worker, error messages, the final embedding vector if completed, and updated timestamp).

---

## 6. Teardown & Cleanup

Upon worker agent exit or cancellation:
1. **Process Termination**: The Go worker sends a `Kill()` signal to all active subprocesses.
2. **Filesystem Cleanup**: Go deletes the local `runner-<model_name>.sock` files from the filesystem.
