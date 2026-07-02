# Intelligent Routing — Hardware-Based Job Dispatch

## What it does

When a training job is submitted, the coordinator does not blindly pick any free worker. It matches the job's hardware requirements against each worker's actual capabilities — GPU presence, VRAM, RAM, and free disk — and only assigns the job to a worker that can satisfy every requirement. A GPU training job never lands on a CPU-only machine. A job needing 32GB RAM never lands on a machine with 8GB.

---

## Why routing lives in the coordinator, not the worker

An alternative design: publish the job to a shared subject, let workers pull it, check their own capabilities, and Nak (reject) if they can't handle it. This is how some queue systems work.

- The problem: with many workers and many requirements mismatches, you get a storm of Naks and re-deliveries before the right worker gets the job. Every worker reads the job message before deciding it can't run it. That wastes resources and adds latency.

- another problem: Imagine that a worker NACked the job because it failed resource constraints, the NACked job (since there is a common jobs subject) will be redelieved to the common subject and all workers will see it again, even the ones that already rejected it. This can lead to a lot of unnecessary message churn.

EdgeGrid flips it: the coordinator knows every worker's capabilities (they're stored in KV at registration time) and knows the job's requirements at submission time. It does the match centrally and publishes the job **directly to the worker's personal subject** (`jobs.train.<workerID>`). No Nak churn. The right worker gets the message on the first publish, every other worker never sees it.

---

## Step 1 — Worker advertises capabilities at registration

When a worker starts, it runs hardware detection and publishes a `WorkerInfo` proto to `workers.register`:

```go
// internal/worker/heartbeat.go — RegisterWorker

info := &workerpb.WorkerInfo{
    Id:         a.id,
    HasGpu:     a.hw.HasGPU,
    GpuName:    a.hw.GPUName,
    GpuVramGb:  a.hw.GPUVramGB,
    RamGb:      a.hw.RAMGB,
    DiskFreeGb: a.hw.DiskFreeGB,
    Sandbox:    "none",
}
a.broker.PublishProto(broker.SubjectRegister, info)
```

Hardware is detected once at `worker.Start()` and stored on the Worker struct:

```go
// internal/worker/worker.go

func (w *Worker) Start(ctx context.Context) error {
    w.hw = detectHardware()  // called once, stored — not re-detected per job
    ...
}
```

`detectHardware` calls platform-specific functions:

- **Linux**: `syscall.Sysinfo` for RAM, `syscall.Statfs` for disk
- **macOS / Windows**: equivalent platform syscalls in separate build-tag files
- **GPU**: `nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits` — same command on all platforms

```go
// internal/worker/hardware.go

out, err := exec.Command(
    "nvidia-smi",
    "--query-gpu=name,memory.total",
    "--format=csv,noheader,nounits",
).Output()
if err == nil {
    // parse "NVIDIA GeForce RTX 3080, 10240" → HasGPU=true, GPUVramGB=10.0
}
```

If `nvidia-smi` is not found or returns an error, `HasGPU` stays false. The worker still registers — it just gets no GPU jobs.

---

## Step 2 — Coordinator stores worker capabilities in KV

The coordinator's `RegisterWorker` handler stores the `WorkerInfo` proto alongside the worker's state in `workers_state` KV:

```go
// internal/coordinator/workerman/registerWorker.go

type Worker struct {
    Info     *workerpb.WorkerInfo `json:"info"`
    LastSeen time.Time            `json:"last_seen"`
    State    string               `json:"state"`
    Job      *Job                 `json:"job"`
}
```

The `Info` field preserves the original `WorkerInfo` proto. Every capability check later reads directly from this stored struct — no need to ask the worker again.

---

## Step 3 — Job submission declares requirements

The HTTP client specifies what the job needs:

```json
POST /jobs
{
  "training_script": "...",
  "dataset_ref": "my-dataset",
  "requires_gpu": true,
  "min_ram_gb": 16.0,
  "min_vram_gb": 8.0,
  "min_disk_gb": 20.0
}
```

These fields map directly to the `TrainingJobRequest` proto:

```go
// internal/coordinator/jobsapi/jobsapi.go — Submit

req := &workerpb.TrainingJobRequest{
    JobId:       jobID,
    RequiresGpu: body.RequiresGPU,
    MinRamGb:    body.MinRAMGB,
    MinVramGb:   body.MinVRAMGB,
    MinDiskGb:   body.MinDiskGB,
    ...
}
```

All four requirement fields are optional. A job with all zeroes and `requires_gpu: false` runs on any worker.

---

## Step 4 — Coordinator matches requirements to workers

`FindAndAssignWorker` scans all workers in KV and calls `MeetsRequirements` on each:

```go
// internal/coordinator/workerman/schedule.go

func MeetsRequirements(info *workerpb.WorkerInfo, req *workerpb.TrainingJobRequest) bool {
    if req.RequiresGpu && !info.HasGpu {
        return false
    }
    if req.MinRamGb > 0 && info.RamGb < req.MinRamGb {
        return false
    }
    if req.MinVramGb > 0 && info.GpuVramGb < req.MinVramGb {
        return false
    }
    if req.MinDiskGb > 0 && info.DiskFreeGb < req.MinDiskGb {
        return false
    }
    return true
}
```

Each check is a simple comparison. The function returns `false` on the first failing requirement — no point checking further. If all requirements are met (or not specified), it returns `true`.

`FindAndAssignWorker` picks the first free worker that passes:

```go
func (wm *WorkerManager) FindAndAssignWorker(jobID string, req *workerpb.TrainingJobRequest) (string, error) {
    keys, _ := wm.kv.Keys()
    for _, key := range keys {
        entry, _ := wm.kv.Get(key)
        var worker Worker
        json.Unmarshal(entry.Value(), &worker)

        if worker.State != WorkerFree {
            continue
        }
        if !MeetsRequirements(worker.Info, req) {
            continue
        }

        // CAS-assign this worker (see job-queuing.md for detail on CAS)
        if err := wm.TryAssignWorker(key, jobID); err != nil {
            continue  // another coordinator grabbed it, try next
        }
        return key, nil
    }
    return "", fmt.Errorf("no free worker meets requirements")
}
```

---

## Step 5 — Job published to worker's personal subject

Once a matching worker is found and assigned:

```go
// internal/coordinator/jobsapi/jobsapi.go — Submit

subject := broker.SubjectTrainPrefix + workerID  // "jobs.train.worker-xyz"
jsBroker.PublishProto(subject, req)
```

Only that specific worker subscribes to `jobs.train.<its-own-id>`. No other worker receives the message. The job arrives exactly where it should, without any broadcast or filtering on the worker side.

---

## End-to-end example

```
Workers registered:
  worker-A: HasGPU=false, RAM=8GB,  Disk=50GB
  worker-B: HasGPU=true,  RAM=32GB, VRAM=10GB, Disk=100GB
  worker-C: HasGPU=true,  RAM=16GB, VRAM=6GB,  Disk=80GB

Job submitted:
  requires_gpu=true, min_ram_gb=16, min_vram_gb=8, min_disk_gb=20

Routing:
  worker-A → MeetsRequirements? NO  (requires_gpu=true, HasGPU=false)
  worker-B → MeetsRequirements? YES (GPU ✓, RAM 32≥16 ✓, VRAM 10≥8 ✓, Disk 100≥20 ✓)

→ job published to jobs.train.worker-B
```

---

## What happens when no worker qualifies

If `FindAndAssignWorker` finds no matching free worker, the job is not dropped. It stays `QUEUED` in KV with its full `RequestProto` preserved. When any worker later becomes free (new registration or job completion), `TryDispatchQueued` runs — it scans all QUEUED jobs and calls `MeetsRequirements` against the newly free worker, dispatching the oldest matching job. See `job-queuing.md` for the full detail on that flow.
