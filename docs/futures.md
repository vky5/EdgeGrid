# Future Engineering & Roadmap

This document captures architectural design challenges, trade-offs, and plans for future improvements to the EdgeGrid codebase.

> **Training Extension**: All decisions around distributed model training (artifact transport, GPU detection, venv caching, security model, mid-training resume, directory isolation) are documented separately in [`training_extension.md`](./training_extension.md).

## 1. Subprocess Exit & Crash Detection (Executor Sidecars)

### Current Behavior

- The `HuggingFaceExecutor` spawns background Python `runner.py` subprocesses using `context.Background()`.
- Go tracks the assigned TCP port for each model and sends HTTP queries to them.
- If a Python subprocess crashes (e.g. Out of Memory, Python runtime exception, or manual termination by OS):
  - The Go worker will receive `connection refused` errors on subsequent HTTP query executions.
  - The NATS JetStream job message will be negative-acknowledged (`msg.Nak()`) and redelivered to other workers.
  - The port mapping remains in memory, and the Go code does not attempt to restart the process.

### Future Work / Proposed Solution

To keep the initial codebase simple, we avoided complex process-supervisor loops. In the future, we should implement a process watchdog/supervisor inside the `HuggingFaceExecutor`:

1. **Background Wait Loop**: Run a goroutine calling `cmd.Wait()` on each spawned process.
2. **State Cleanup**: On unexpected exits (i.e. when not during a shutdown sequence), remove the model socket mapping so future runs fail fast or fall back immediately.
3. **Auto-Respawn with Backoff**: Re-run the startup sequence (generate socket path ➔ launch process ➔ poll for UDS readiness) up to 3 consecutive retries with an exponential backoff.
4. **Deregistration**: If the process cannot be restarted, update the worker info and notify the coordinator to remove the capability.

## 2. Active IPC Architecture: Unix Domain Sockets (UDS) + Protobuf
EdgeGrid uses **Unix Domain Sockets (UDS) + Protobuf** for local inter-process communication (IPC) between the Go worker and the Python embedding runner:
* **Zero Network Stack Overhead**: IPC travels through local socket files, avoiding TCP localhost overhead.
* **Direct Binary Serialization**: Requests and responses are serialized directly into binary protobuf payloads (`JobRequest` and `JobResponse`), bypassing text JSON parsing.

### Future Work
* **Request Batching**: Collect incoming single embedding jobs and pass them as a batch (e.g. up to 16/32 texts) to the inference engine to speed up neural network vectorization.
* **GPU/CUDA Acceleration (Large Model Path)**:
  * **CUDA Auto-Detection**: *(Design decided)* Detect GPU at worker startup via `nvidia-smi --query-gpu=name,memory.total --format=csv,noheader`. Result is stored in the extended `WorkerInfo` proto fields (`has_gpu`, `gpu_vram_gb`, `gpu_name`). Device is passed to the Python subprocess via `DEVICE=cuda` env var. See [`training_extension.md § GPU Detection`](./training_extension.md).
  * **Precision Optimizations**: Enable half-precision (FP16 or BF16) to reduce VRAM memory footprint and increase inference throughput.
  * **Hardware Metadata Registration**: *(Design decided)* `WorkerInfo` proto extended with `has_gpu`, `gpu_vram_gb`, `ram_gb`, `disk_free_gb`, `gpu_name`, `sandbox`. Coordinator uses these fields to route training jobs only to qualifying workers. See [`training_extension.md § Proto Changes`](./training_extension.md).
* **WasmEdge Sandboxing (Lightweight Edge Path)**:
  * **Eliminate Python Sidecar**: Compile a lightweight WebAssembly module (`.wasm` runner) written in Rust/C++ that utilizes the **WASI-NN API** for executing model graphs.
  * **Model Format Transition**: Export PyTorch model weights to ONNX format (or GGUF for llama.cpp/ggml backends) and load them dynamically into WasmEdge.
  * **Sandbox Security & Portability**: *(v1 approach decided)* v1 uses `--allow-arbitrary-code` flag + OS resource limits (`RLIMIT_AS`, `RLIMIT_CPU`) + Docker isolation when available. WasmEdge remains the long-term target. See [`training_extension.md § Security Model`](./training_extension.md).
  * **Dynamic Resource & Model Management**: Allow the Go agent to dynamically download, cache, and delete model files from the filesystem while passing paths to the Wasm module at runtime.

## 3. Performance Optimization Opportunities

To prepare EdgeGrid for high-throughput production workloads, the following low-hanging performance optimizations should be implemented:

### 1. IPC Connection Reuse (Connection Pooling)
* **Problem**: Currently, Go dials the UDS socket and closes the connection (`defer conn.Close()`) for every single execution request.
* **Solution**: Keep a persistent connection open between the Go worker and the Python sidecar. This avoids OS-level system call overhead (`socket`, `connect`, `accept`) for every inference job.

### 2. Buffered I/O on Sockets
* **Problem**: Direct unbuffered socket reads and writes trigger immediate, small context-switches and system calls (`sys_read`, `sys_write`) for length headers.
* **Solution**: Wrap socket descriptors with buffering interfaces:
  * In Go: `bufio.NewReader(conn)` / `bufio.NewWriter(conn)`
  * In Python: `conn.makefile('rwb')`

### 3. PyTorch Model Compilation & Runtime Tweaks
* **Problem**: Default PyTorch settings use full precision and scale CPU threads globally, causing thread thrashing inside containerized environments.
* **Solution**:
  * Disable gradient tracking globally: `torch.set_grad_enabled(False)`.
  * Limit thread count to match CPU core allocations: `torch.set_num_threads(N)`.
  * Enable half-precision (FP16/BF16) or run `torch.compile(model)` (PyTorch 2.x+) for faster kernel graph execution.

### 4. Memory Allocations (Buffer Pooling)
* **Problem**: Allocating a fresh byte slice for every response (`make([]byte, respLength)`) triggers high garbage collection overhead in Go.
* **Solution**: Implement a `sync.Pool` to reuse byte slices for reading socket payloads.

---

## 4. Training Extension

All training-specific future work is tracked in [`training_extension.md`](./training_extension.md). Items still open after the initial design:

* **Multi-GPU / Distributed Training**: PyTorch DDP across multiple workers requires workers to communicate directly with each other — a separate coordination layer on top of the current architecture. Deferred post-v1.
* **Large Dataset Support (> 50GB)**: NATS Object Store is bounded by coordinator disk. For very large datasets, the job spec should accept a presigned S3/GCS/R2 URL as a third `dataset.type` option (`"url"`), with the worker downloading directly. No coordinator involvement.
* **Persistent Job History**: Current job state TTL is 24h in NATS KV. Long-term, a lightweight SQLite or embedded key-value store on the coordinator would allow querying historical jobs and checkpoint keys beyond the TTL window.
* **Job Priority Queue**: NATS JetStream delivers FIFO. A priority mechanism would require a separate subject per priority tier (e.g. `jobs.train.high.<model>`, `jobs.train.low.<model>`) with workers subscribing to higher-priority subjects first.
* **Federated / Split Training**: A single worker handles one job entirely. Split training (sharding a model across multiple workers) requires a new coordination protocol between workers and is out of scope for the current architecture.

