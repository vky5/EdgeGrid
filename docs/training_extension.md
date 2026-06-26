# EdgeGrid Training Extension: Design Guide

This document captures all architectural decisions for extending EdgeGrid from embedding inference into a distributed model training network. It is the authoritative design reference before implementation begins.

---

## Vision

A user has a dataset and a training script on their machine. They want to train a model on a friend's laptop without manually copying files, setting up environments, or configuring anything on the remote machine. The friend runs a single binary. The system handles the rest.

Same pull-based architecture. Same single binary. New executor type.

---

## What Changes vs. the Embedding System

| Concern | Embedding | Training |
| :--- | :--- | :--- |
| Job payload | `{model_name, input_text}` | `{script, requirements, dataset, base_model, config}` |
| Execution time | Milliseconds | Minutes to hours |
| Worker concurrency | Many jobs at once | One training job at a time |
| Result | Float32 embedding vector | Trained model checkpoint files |
| Artifact transport | None needed | Dataset in, checkpoint out |
| Worker capability | Model name support | GPU, VRAM, RAM, disk |
| Failure recovery | NATS redelivers immediately | Resume from last checkpoint |

---

## 1. Job Schema

Training jobs are submitted via `POST /jobs` with a richer payload:

```json
{
  "job_type": "train",
  "training_script": "<base64-encoded .py file>",
  "requirements": "torch\ntransformers\ndatasets\n",
  "base_model": {
    "type": "hf",
    "ref": "meta-llama/Llama-3.2-1B"
  },
  "dataset": {
    "type": "hf",
    "ref": "roneneldan/TinyStories"
  },
  "training_config": {
    "epochs": 3,
    "lr": 2e-4,
    "batch_size": 16
  },
  "requires_gpu": false,
  "min_ram_gb": 8.0,
  "min_vram_gb": 0.0,
  "min_disk_gb": 20.0
}
```

For a private dataset already uploaded to the coordinator:

```json
"dataset": {
  "type": "object_store",
  "key": "datasets/job-abc123"
}
```

### Notebook Users

Accept `.py` scripts only. Users export their notebook via `File → Download → .py`. Adding Jupyter as a worker dependency to support `.ipynb` execution is not worth it.

---

## 2. Proto Changes

### Extend `WorkerInfo`

```proto
message WorkerInfo {
  string id = 1;
  repeated string supported_model = 2;
  bool has_gpu = 3;
  float gpu_vram_gb = 4;
  float ram_gb = 5;
  float disk_free_gb = 6;
  string gpu_name = 7;
  string sandbox = 8; // "none" or "docker"
}
```

### New `TrainingJobRequest`

```proto
message TrainingJobRequest {
  string job_id = 1;
  bytes training_script = 2;       // base64-decoded .py file bytes
  string requirements = 3;         // raw requirements.txt content
  string dataset_type = 4;         // "hf" or "object_store"
  string dataset_ref = 5;          // HF dataset ID or Object Store key
  string base_model_type = 6;      // "hf" or "object_store"
  string base_model_ref = 7;       // HF model ID or Object Store key
  string training_config_json = 8; // serialized training hyperparameters
  bool requires_gpu = 9;
  float min_ram_gb = 10;
  float min_vram_gb = 11;
  float min_disk_gb = 12;
}
```

### Extend `JobResponse`

```proto
message JobResponse {
  string job_id = 1;
  bool success = 2;
  repeated float embedding = 3;   // kept for embedding jobs
  string error = 4;
  string worker_id = 5;
  string checkpoint_key = 6;      // Object Store key for training jobs
  string job_type = 7;            // "embed" or "train"
}
```

---

## 3. Artifact Transport Layer

A new internal package handles all file movement between coordinator and workers.

**Package:** `internal/artifacts/store.go`

```go
type ArtifactStore struct {
    obs nats.ObjectStore
}

func NewArtifactStore(js nats.JetStreamContext) (*ArtifactStore, error)
func (s *ArtifactStore) Push(key string, r io.Reader) error
func (s *ArtifactStore) Pull(key string, w io.Writer) error
func (s *ArtifactStore) Size(key string) (uint64, error)
func (s *ArtifactStore) Delete(key string) error
```

NATS Object Store handles chunking automatically. No file ever lands on the coordinator's own disk — the coordinator pipes directly between the HTTP connection and the Object Store.

### Object Store Key Convention

```
datasets/{job_id}        ← training data (TTL: 48h)
checkpoints/{job_id}     ← trained model output (TTL: 7 days)
```

### Tiered Dataset Strategy

```
Dataset source in job?
  ├── type: "hf"           → worker calls datasets.load_dataset(ref) directly
  │                          no coordinator involvement, no Object Store
  └── type: "object_store" → worker calls ArtifactStore.Pull(key, localFile)
```

Public HuggingFace datasets (the majority of use cases) bypass the artifact store entirely. The Object Store path is only for private datasets the user uploads themselves.

### New HTTP Endpoints on Coordinator

| Method | Path | Purpose |
| :--- | :--- | :--- |
| `POST` | `/jobs/{id}/upload` | Stream private dataset → NATS Object Store |
| `GET` | `/jobs/{id}/artifact` | Stream trained checkpoint ← NATS Object Store |

---

## 4. Worker Directory Isolation

Every training job gets a clean, isolated working directory:

```
/tmp/edgegrid-jobs/{job_id}/
    input/              ← dataset downloaded here (never sent back)
    script.py           ← training script written here (never sent back)
    requirements.txt    ← written from inline job field (never sent back)
    output/             ← ONLY this directory is pushed to Object Store
```

The worker passes paths to the training script via environment variables:

```go
cmd.Env = append(os.Environ(),
    "DATASET_PATH=/tmp/edgegrid-jobs/"+jobID+"/input",
    "OUTPUT_DIR=/tmp/edgegrid-jobs/"+jobID+"/output",
    "DEVICE=cuda",       // or "cpu"
    "EPOCHS=3",
    "LR=0.0002",
    "BATCH_SIZE=16",
)
```

Training script reads them:

```python
import os
device      = os.environ["DEVICE"]
dataset_dir = os.environ["DATASET_PATH"]
output_dir  = os.environ["OUTPUT_DIR"]

model.save_pretrained(output_dir)
tokenizer.save_pretrained(output_dir)
```

After the job completes (or fails), the entire `/tmp/edgegrid-jobs/{job_id}/` directory is deleted. The worker cleans up after itself.

### What Goes Back to the Sender

Only the contents of `output/` — typically:
```
config.json
model.safetensors
tokenizer.json
tokenizer_config.json
special_tokens_map.json
```

No dataset, no source script, no venv, no cached model weights.

---

## 5. GPU Detection

GPU capability is detected once at worker startup using `nvidia-smi`. No Python required.

```go
// internal/worker/hardware.go

type HardwareSpec struct {
    HasGPU     bool
    GPUName    string
    VRAMgb     float32
    RAMgb      float32
    DiskFreeGB float32
}

func DetectHardware() HardwareSpec {
    spec := HardwareSpec{
        RAMgb:      detectRAMgb(),
        DiskFreeGB: detectDiskFreeGB(),
    }

    out, err := exec.Command(
        "nvidia-smi",
        "--query-gpu=name,memory.total",
        "--format=csv,noheader",
    ).Output()
    if err == nil {
        spec.HasGPU = true
        spec.GPUName, spec.VRAMgb = parseNvidiaSmi(out)
    }
    return spec
}
```

This is included in `WorkerInfo` at registration time. Coordinator stores it in the `workers` KV bucket.

### Job Routing by Capability

Before publishing a `TrainingJobRequest` to NATS, the coordinator scans the `workers` KV bucket and verifies at least one registered worker satisfies the job's requirements:

```
requires_gpu: true  → at least one worker has has_gpu = true
min_vram_gb: 8.0   → at least one worker has gpu_vram_gb >= 8.0
min_ram_gb: 16.0   → at least one worker has ram_gb >= 16.0
min_disk_gb: 20.0  → at least one worker has disk_free_gb >= 20.0
```

If no worker qualifies, the job is rejected immediately with a `400` response and a clear reason:

```json
{"error": "no workers available matching: GPU with 8GB VRAM, 20GB free disk"}
```

---

## 6. Venv Caching

Per-job `pip install` with no caching downloads PyTorch on every run (2-3GB). Instead, venvs are cached keyed by the SHA256 hash of the `requirements` string.

```
~/.edgegrid/venvs/
    a3f9c2d8.../       ← SHA256("torch\ntransformers\n")
        bin/python
        lib/...
        .ready          ← sentinel file written AFTER successful pip install
    8b12de44.../       ← different requirements hash
        ...
```

**Lookup logic:**

```
hash = SHA256(requirements string)
venvPath = ~/.edgegrid/venvs/{hash}/

if venvPath exists AND venvPath/.ready exists:
    reuse venv, skip pip install entirely

if venvPath exists AND .ready missing:
    delete venvPath (corrupted/interrupted install)
    create fresh venv → pip install → write .ready

if venvPath missing:
    create fresh venv → pip install → write .ready
```

The `.ready` sentinel prevents a half-completed pip install (worker crashed mid-install) from being mistaken for a valid venv.

---

## 7. Disk Pre-Check

Worker re-checks available disk immediately before pulling the dataset, regardless of what was advertised at registration time.

```go
func checkDiskSpace(path string, neededBytes uint64) error {
    var stat syscall.Statfs_t
    if err := syscall.Statfs(path, &stat); err != nil {
        return err
    }
    freeBytes := stat.Bavail * uint64(stat.Bsize)
    // Keep a 2GB buffer for the venv and output artifacts
    if freeBytes < neededBytes + 2*1024*1024*1024 {
        return fmt.Errorf("insufficient disk: need %dGB, have %dGB free",
            neededBytes/1e9, freeBytes/1e9)
    }
    return nil
}
```

- **NATS Object Store datasets**: call `ArtifactStore.Size(key)` before pulling to get exact byte count.
- **HuggingFace datasets**: size is unknown upfront. Check that at least 10GB is free before starting. Monitor during download and NAK the job if space runs out.

If the check fails, the worker NAKs the job and NATS redelivers it to another worker.

---

## 8. Mid-Training Resume

If a worker crashes mid-training, NATS redelivers the job. Without resume support, the new worker starts from epoch 0.

### How It Works

**During training:** A background goroutine on the worker pushes `OUTPUT_DIR` to the Object Store every 10 minutes under the key `checkpoints/{job_id}/latest`. HuggingFace `Trainer` saves intermediate checkpoints to `OUTPUT_DIR` automatically.

**On job pickup:** Before starting training, the worker checks the Object Store for `checkpoints/{job_id}/latest`. If found, it downloads it to `OUTPUT_DIR` and sets `RESUME_FROM` env var.

```go
// Before starting training subprocess
checkpointKey := "checkpoints/" + jobID + "/latest"
if info, err := artifactStore.obs.GetInfo(checkpointKey); err == nil && info != nil {
    artifactStore.Pull(checkpointKey, outputDir)
    env = append(env, "RESUME_FROM="+outputDir)
}
```

Training script handles the rest natively:

```python
resume_from = os.environ.get("RESUME_FROM")
trainer.train(resume_from_checkpoint=resume_from)
```

HuggingFace `Trainer.train(resume_from_checkpoint=...)` is the standard API for this — no custom logic needed in the script.

---

## 9. Worker Busy Lock

A worker must not accept more than one training job simultaneously. Unlike embedding inference, training saturates CPU/RAM/GPU and cannot be safely parallelized on a single machine.

The worker maintains a local atomic flag:

```go
type TrainingExecutor struct {
    busy atomic.Bool
    // ...
}

func (e *TrainingExecutor) Execute(...) ([]float32, error) {
    if !e.busy.CompareAndSwap(false, true) {
        return nil, fmt.Errorf("worker busy: training job already running")
    }
    defer e.busy.Store(false)
    // ... run training
}
```

On a busy response, the worker NAKs the job with a delay so NATS redelivers it to a free worker.

---

## 10. Security Model

Workers execute arbitrary Python code submitted by job senders. This is a deliberate design choice for the "friends" trust model, but it must be explicit.

### v1: Explicit Consent + Resource Limits

Workers must be started with `--allow-arbitrary-code` to accept training jobs. Without this flag, all `TrainingJobRequest` messages are NAK'd immediately.

```bash
./edgegrid -client -executor training --allow-arbitrary-code
```

The subprocess is launched with OS-level resource limits to prevent runaway jobs:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{
    Setpgid: true, // ensures kill() hits the full subprocess tree
}
// Set via syscall.Setrlimit before exec:
// RLIMIT_AS  → cap virtual memory
// RLIMIT_CPU → cap CPU time (wall-clock is handled by job timeout)
```

### v2: Docker Sandbox (when available)

If Docker is detected at worker startup, training jobs run inside a container with strict isolation:

```go
args := []string{"run", "--rm",
    "--network", "none",
    "--memory", ramLimit,
    "-v", datasetPath + ":/data:ro",   // dataset read-only
    "-v", outputDir + ":/output",      // only output writable
    "-e", "DEVICE=" + device,
    "python:3.11", "python", "/tmp/script.py",
}
```

Worker advertises `sandbox: "docker"` or `sandbox: "none"` in `WorkerInfo`. Submitters can filter to sandboxed workers only if needed.

WasmEdge sandboxing (from `futures.md`) remains the long-term target for cross-platform isolation without Docker.

---

## 11. Training Job Lifecycle

```
QUEUED
  └── Coordinator publishes TrainingJobRequest to NATS
        └── Worker picks up job
              ├── Disk pre-check → NAK if insufficient space
              ├── Capability check (GPU, RAM) → NAK if not qualified
              └── RUNNING
                    ├── Pull dataset (HF download or Object Store)
                    ├── Resolve venv (cache hit or fresh install)
                    ├── Check for existing checkpoint (resume path)
                    ├── Launch training subprocess
                    │     └── Background goroutine: push checkpoint every 10min
                    │
                    ├── COMPLETED
                    │     ├── Push OUTPUT_DIR → Object Store ("checkpoints/{job_id}")
                    │     ├── Publish JobResponse (checkpoint_key set)
                    │     ├── Coordinator updates KV: state=COMPLETED, checkpoint_key
                    │     └── Cleanup /tmp/edgegrid-jobs/{job_id}/
                    │
                    └── FAILED
                          ├── Capture last 50 lines of stderr → store in job status
                          ├── Publish JobResponse (success=false, error=stderr tail)
                          ├── Coordinator updates KV: state=FAILED, error
                          └── Cleanup /tmp/edgegrid-jobs/{job_id}/
```

---

## 12. Receiving the Trained Model

Once `GET /jobs/{id}` returns `"state": "COMPLETED"`:

```bash
# Download the checkpoint
curl http://localhost:8080/jobs/abc123/artifact -o checkpoint.tar

# Extract
tar -xf checkpoint.tar -C ./my_model/
```

The coordinator streams directly from NATS Object Store to the HTTP response body. It does not buffer to disk.

The Object Store entry TTL is 7 days. After that it is purged automatically. Senders should download their checkpoint promptly.

---

## 13. New NATS Subjects

Training adds the following to the existing `JOBS` stream:

| Subject | Direction | Purpose |
| :--- | :--- | :--- |
| `jobs.train.<model>` | Coordinator → Worker | Training job distribution |
| `jobs.progress` | Worker → Coordinator | Live epoch/loss updates |
| `jobs.cancel` | Coordinator → Worker | Cancel signal for running job |

`jobs.progress` payload:

```json
{"job_id": "abc123", "epoch": 2, "step": 840, "loss": 0.84, "worker_id": "w-1"}
```

Exposed via `GET /jobs/{id}/progress` as a Server-Sent Events stream.

---

## 14. Out of Scope for v1

These are explicitly deferred:

- **Multi-GPU / distributed training** (PyTorch DDP across workers) — requires workers to communicate with each other, entirely separate system
- **NATS Object Store datasets > 50GB** — practical ceiling based on coordinator disk; use presigned cloud storage URL for very large datasets
- **Job priority queue** — FIFO only via NATS JetStream
- **Persistent job history** — job state TTL is 24h; no database
- **Coordinator high availability** — single coordinator; Raft clustering is in `p2p_agent_implementation_plan.md`
- **Tauri desktop app** — build after the CLI workflow is solid
- **Federated / split training** — single worker per job only
