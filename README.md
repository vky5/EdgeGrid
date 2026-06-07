# EdgeGrid: Decentralized Embedding Inference Network

**EdgeGrid** is a decentralized, pull-based AI embedding inference network. It coordinates heterogeneous worker nodes (such as client laptops, PCs, or VMs) to generate text embeddings asynchronously via **NATS JetStream** based on model compatibility.

---

## 💡 Architecture

EdgeGrid is fully event-driven, operating without public ports or inbound listeners on workers. 

* **Orchestrator (Control Plane)**:
  * Exposes a REST API (`POST /jobs`) to receive embedding jobs.
  * Publishes jobs to model-specific NATS subjects (e.g. `jobs.build.all-minilm`).
  * Subscribes to worker registration, heartbeats, and result events to maintain state and collect output vectors.
* **Worker (Client Node)**:
  * Announces its model capability (e.g. `all-minilm`, `llama3`) to the registry.
  * Heartbeats status periodically.
  * Pulls pending jobs via NATS JetStream pull consumers matching its supported models.
  * Calculates embedding vectors locally and publishes responses to `jobs.results`.
* **NATS JetStream (Message Broker)**:
  * Acts as the scheduler and buffer. Jobs are stored durably and dynamically distributed to matching workers.

---

## 🛠️ Core Components

| Component | Responsibility |
| :--- | :--- |
| **`apps/build-orchestrator`** | Control plane, HTTP API, registration monitor, and results accumulator |
| **`apps/build-worker`** | Agent client that pulls compatible jobs and runs local embedding inference |
| **`apps/shared`** | Unified protobuf schemas (`worker.proto`) and generated Go models |

---

## 🚀 Getting Started

### Prerequisites

* **Go**: Version `1.24.6`
* **NATS Server**: Installed and running with JetStream enabled (`nats-server -js`)

### Setup & Build

1. Clone the repository:
   ```bash
   git clone https://github.com/edgegrid/edgegrid.git
   cd edgegrid
   ```

2. Compile protobuf files:
   ```bash
   make proto
   ```

3. Build the applications:
   ```bash
   # Build Orchestrator
   cd apps/build-orchestrator && GOTOOLCHAIN=local go build ./...
   
   # Build Worker
   cd ../build-worker && GOTOOLCHAIN=local go build ./...
   ```

---

## 🔄 End-to-End Workflow Test

To test the system locally:

1. **Start NATS Server** (with JetStream enabled):
   ```bash
   nats-server -js
   ```

2. **Run the Orchestrator**:
   ```bash
   cd apps/build-orchestrator
   NATS_URL=nats://localhost:4222 PORT=8080 go run ./cmd
   ```

3. **Run a Worker Node**:
   Open a new terminal and run:
   ```bash
   cd apps/build-worker
   NATS_URL=nats://localhost:4222 SUPPORTED_MODELS=all-minilm go run ./cmd
   ```

4. **Submit a Job**:
   Open a new terminal and trigger an embedding job via the Orchestrator's API:
   ```bash
   curl -X POST http://localhost:8080/jobs \
     -H "Content-Type: application/json" \
     -d '{"model_name": "all-minilm", "input_text": "EdgeGrid decentralized embedding inference"}'
   ```

*You will observe:*
* The Orchestrator logging the incoming job and queueing it to NATS.
* The Worker pulling the job, generating a stub embedding vector, and publishing the response.
* The Orchestrator receiving and logging the completed embedding response from `jobs.results`.
