# Future Engineering & Roadmap

What's been built covers the full single-worker training pipeline end-to-end. The items below are the next natural layers — ordered roughly by impact.

---

## 1. Distributed GPU Training (Data Parallelism)

### The idea

Right now one job runs on one worker. Distributed training splits a single job across multiple workers — each trains on a different batch, gradients are averaged across all of them via AllReduce, and all model copies stay in sync. A 4-GPU job trains roughly 4× faster than a single-GPU job.

### How it would fit into EdgeGrid

NATS handles orchestration. NCCL (NVIDIA's collective communications library) handles the actual gradient sync — directly machine-to-machine, bypassing NATS entirely. NATS would only be used to tell workers "you're part of job X, here's the master IP, here's your rank."

```
coordinator allocates N workers for job X:
  worker-A → MASTER_ADDR=<A's IP>, RANK=0, WORLD_SIZE=3
  worker-B → MASTER_ADDR=<A's IP>, RANK=1, WORLD_SIZE=3
  worker-C → MASTER_ADDR=<A's IP>, RANK=2, WORLD_SIZE=3

each worker launches:
  torchrun --nproc_per_node=1 train.py

PyTorch DDP + NCCL handle all gradient synchronization
directly between workers over TCP — NATS never sees it
```

The training script only needs:
```python
torch.distributed.init_process_group("nccl")
model = torch.nn.parallel.DistributedDataParallel(model)
```

### What needs to change in EdgeGrid

- **Worker registration** must include the worker's reachable IP address (not just NATS identity)
- **Job submission** needs a `world_size` field — how many workers to allocate
- **Coordinator** must allocate N workers atomically, assign ranks, and inject `MASTER_ADDR`/`MASTER_PORT`/`WORLD_SIZE`/`RANK` into each job
- **Partial failure** — if one worker dies, the whole job stalls. `torch.distributed.elastic` (TorchElastic) handles worker failures but adds complexity

### The hard problem — NAT traversal

Personal machines sit behind home routers. PyTorch DDP requires direct TCP connections between workers. The coordinator can reach workers via NATS (pull-based, no inbound ports needed) but workers cannot reach each other without either:

- **Tailscale / WireGuard overlay** — require workers to join a VPN mesh. Simplest operational model.
- **STUN/TURN** — NAT hole-punching. Works for many NAT types but not all (symmetric NAT).
- **Worker-opened ports** — require workers to configure port forwarding on their router. Unreliable for consumer hardware.

Tailscale is the most pragmatic path. Workers run `tailscale up`, get a stable private IP, and DDP connections work transparently.

### What to read

- [PyTorch DDP Tutorial](https://pytorch.org/tutorials/intermediate/ddp_tutorial.html)
- [PyTorch Distributed Overview](https://pytorch.org/docs/stable/distributed.html)
- [NCCL Documentation](https://docs.nvidia.com/deeplearning/nccl/user-guide/docs/index.html)
- [Horovod](https://horovod.ai/) — more flexible than DDP for heterogeneous setups
- [ZeRO paper](https://arxiv.org/abs/1910.02054) — optimizer state sharding for large models
- [Tailscale blog on NAT traversal](https://tailscale.com/blog/how-nat-traversal-works)

---

## 2. Authentication

Nothing is authenticated today. Any client with the coordinator URL can submit jobs, cancel them, read logs, and download checkpoints. Any binary claiming to be a worker can register as one.

### What's needed

**API keys for job submitters** — HTTP bearer token checked in the coordinator. Simple middleware wrapping every handler. Keys stored in a NATS KV bucket or a config file.

**Worker trust** — more complex. A rogue binary could register as a worker, receive a job, and exfiltrate the training script and dataset. Options:
- **Shared secret** — workers include a pre-shared key in their registration proto. Coordinator rejects unknown keys.
- **mTLS** — workers and coordinator mutually authenticate via certificates. NATS natively supports this.
- **Token-based** — coordinator issues single-use registration tokens out-of-band; workers include the token at registration.

For a personal network (you trust the machines), a shared secret is sufficient. For a public network, mTLS is the right answer — NATS handles it at the connection level with no application code changes needed.

---

## 3. `GET /jobs` — Job List Endpoint

Currently you can only query a specific job by ID. A list endpoint is trivial — scan all keys in `jobs_state` KV, return them — but it's missing.

Useful additions: filter by state (`?state=RUNNING`), pagination, and sorting by submission time. All achievable with a single KV scan since the job count is small.

---

## 4. Persistent Job History

Job state TTL is 24 hours in NATS KV. After that, the job entry is gone — no way to look up what ran yesterday or retrieve an old checkpoint key.

Options:
- **SQLite on the coordinator** — lightweight, no extra infra. On every job state change, write to SQLite. On restart, coordinator reads from SQLite first, then NATS KV.
- **Extended NATS KV TTL** — just set a longer TTL (7 days, 30 days). Simple but wastes NATS storage for large RequestProto bytes.
- **Separate archive KV** — on job completion/failure, copy the final state to a long-TTL archive bucket. Main `jobs_state` stays short-lived for operational use.

SQLite is the cleanest for a proper query interface (filter by date, by worker, by state) without extra infrastructure.

---

## 5. Job Priority

NATS JetStream delivers FIFO. `TryDispatchQueued` picks the oldest QUEUED job. There is no way today to say "this job is urgent."

Two approaches:
- **Priority subjects** — `jobs.train.high.*` and `jobs.train.low.*` as separate streams. Workers and `TryDispatchQueued` check high-priority subjects first.
- **Priority field in JobStatus** — `TryDispatchQueued` scans all QUEUED jobs and picks by `(priority DESC, updatedAt ASC)` instead of pure FIFO.

The priority-field approach requires no stream changes and is a small modification to `TryDispatchQueued`. The priority-subjects approach is cleaner but requires workers to understand subject hierarchy.

---

## 6. Large Dataset Support (> 50GB)

NATS Object Store is bounded by the coordinator's disk. A 100GB ImageNet upload would saturate it immediately.

For large datasets, the job request should accept a presigned URL as a `dataset_type`:

```json
{
  "dataset_type": "url",
  "dataset_ref":  "https://storage.googleapis.com/bucket/dataset.tar.gz?X-Goog-Signature=..."
}
```

The worker downloads directly from the URL — no coordinator involvement, no NATS object store overhead. Coordinator just passes the URL through in the job request. Works with any storage provider (S3, GCS, R2, HuggingFace Datasets).

---

## 7. Resource Limits and Sandboxing

Today a training script can do anything — write to arbitrary paths, make network calls, read environment variables, consume all CPU. There is no isolation beyond the process boundary.

For a network where you're running other people's code on your machine, this matters.

**Short term** — OS resource limits:
- `RLIMIT_AS` — cap total virtual memory
- `RLIMIT_CPU` — cap CPU seconds before SIGKILL
- `RLIMIT_FSIZE` — cap file size writes
- Applied via Go's `syscall.Setrlimit` before exec

**Medium term** — Docker isolation:
- Run training script inside a container with `--network none`, `--memory`, `--cpus`, and a read-only rootfs except for the job directory
- Worker detects Docker availability at startup, uses it when available
- Coordinator can see `sandbox: "docker"` in WorkerInfo and prefer sandboxed workers for untrusted jobs

**Long term** — WebAssembly:
- Compile training scripts to WASM with WASI-NN for model inference
- True capability-based sandboxing — no filesystem or network access without explicit grants
- Only practical once WASM ML tooling matures (ONNX + WASI-NN is still early)

---

## 8. Multi-GPU Per Worker

Currently the worker uses one GPU even if the machine has 4. PyTorch supports multiple GPUs on a single machine via `torch.nn.DataParallel` (simple, but not optimal) or `torchrun --nproc_per_node=4` (one process per GPU, full DDP).

For single-machine multi-GPU:
- Worker reports `gpu_count` in addition to `gpu_vram_gb`
- Job requests `min_gpu_count: 2`
- Executor launches `torchrun --nproc_per_node=<gpu_count>` instead of plain `python train.py`
- No NAT traversal needed — all GPUs on the same machine communicate via NVLink or PCIe

This is simpler than cross-machine distributed training and would unlock significant speedups on machines with multiple GPUs.

---

## 9. Web UI

The `web/` directory contains a Next.js 16 dashboard with 5 pages:
- `/` — ops command center (workers, jobs, dispatch)
- `/jobs` — filterable job history
- `/jobs/[id]` — full log terminal, approve/reject/cancel buttons
- `/workers` — hardware grid
- `/workers/[id]` — per-worker detail and job history

Dispatch and approve/reject call the real coordinator API. Worker stats poll
`GET /workers` every 5 seconds. Log streaming via SSE (`GET /jobs/{id}/logs`)
is not yet wired in the dashboard.

---

## 10. Privacy and Data Confidentiality

**Current state: no privacy.** Every node that participates in a job sees:

| What | Who sees it |
|------|-------------|
| Training script (full Python source) | Coordinator (stored in KV as RequestProto) + worker (executes it) |
| Dataset ref / model ref | Same |
| Training config JSON | Same |
| Checkpoint output | Coordinator (object store) + any caller of `GET /jobs/{id}/artifact` |
| Log lines | Coordinator (JetStream) + any caller of `GET /jobs/{id}/logs` |

This is a **trusted-network model** — all participants are in a known circle.
Suitable for invite-only testing where you know every operator. Not suitable
for running untrusted code from strangers, or for confidential models.

### What's possible without redesign

**Checkpoint access control** — the artifact endpoint has no auth today.
Adding a bearer token check (job submitter gets a signed token at submission
time) would ensure only the submitter can download their own checkpoint.

**Script/dataset privacy from workers** — not possible without client-side
encryption. The worker must decrypt and execute the script, so it must have
the key. If the key travels through the coordinator, the coordinator also has
access. True script privacy requires a Trusted Execution Environment (Intel
TDX / AMD SEV) where even the host OS cannot inspect what runs inside.

**Privacy from the coordinator** — not possible in the current architecture.
The coordinator is a trusted intermediary that stores the full RequestProto
(including script content) in NATS KV. Making the coordinator a blind relay
would require end-to-end encryption where the coordinator sees only ciphertext —
a substantial redesign.

### Practical mitigations for now

1. **NATS token auth** — stops unknown nodes from joining the network at all
   (see [access-control.md](access-control.md))
2. **Coordinator allowlist** — limits which workers can receive jobs
3. **Worker opt-in approval** — worker operator sees the script before it runs;
   can reject suspicious jobs (see [worker-approval.md](worker-approval.md))
4. **Sandboxed execution** — Docker/cgroups limits blast radius if a script
   tries to exfiltrate data (see item 7 above)

The combination of these four makes the network safe enough for a small,
vetted community. Full cryptographic privacy is a long-term goal.
