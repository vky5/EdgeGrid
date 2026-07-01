export type WorkerState = 'healthy' | 'busy' | 'dead'
export type JobState = 'RUNNING' | 'QUEUED' | 'PENDING_REVIEW' | 'COMPLETED' | 'FAILED' | 'CANCELLED'

export interface Worker {
  id: string
  state: WorkerState
  gpu: string | null
  gpuTotal: number
  gpuUsed: number
  ram: number
  ramUsed: number
  disk: number
  diskFree: number
  currentJob: string | null
  lastSeen: string
  registeredAt: string
  jobsCompleted: number
}

export interface Job {
  id: string
  state: JobState
  worker: string | null
  submittedAt: string
  startedAt: string | null
  finishedAt: string | null
  duration: string
  requiresGpu: boolean
  minRam: number
  minVram: number
  checkpointKey: string | null
  logs: string
}

export const WORKERS: Worker[] = [
  {
    id: 'worker-gpu-01',
    state: 'busy',
    gpu: 'RTX 3080',
    gpuTotal: 10,
    gpuUsed: 8.5,
    ram: 32,
    ramUsed: 20.8,
    disk: 120,
    diskFree: 90,
    currentJob: 'job-a1b2c3d4',
    lastSeen: '2s ago',
    registeredAt: '2026-07-01T08:00:00Z',
    jobsCompleted: 24,
  },
  {
    id: 'worker-gpu-02',
    state: 'healthy',
    gpu: 'RTX 4090',
    gpuTotal: 24,
    gpuUsed: 0,
    ram: 64,
    ramUsed: 7.7,
    disk: 500,
    diskFree: 460,
    currentJob: null,
    lastSeen: '1s ago',
    registeredAt: '2026-07-01T07:45:00Z',
    jobsCompleted: 31,
  },
  {
    id: 'worker-gpu-03',
    state: 'dead',
    gpu: 'RTX 2080 Ti',
    gpuTotal: 11,
    gpuUsed: 0,
    ram: 32,
    ramUsed: 0,
    disk: 256,
    diskFree: 256,
    currentJob: null,
    lastSeen: '45m ago',
    registeredAt: '2026-06-30T14:00:00Z',
    jobsCompleted: 8,
  },
  {
    id: 'worker-cpu-01',
    state: 'healthy',
    gpu: null,
    gpuTotal: 0,
    gpuUsed: 0,
    ram: 128,
    ramUsed: 43.5,
    disk: 1000,
    diskFree: 580,
    currentJob: null,
    lastSeen: '3s ago',
    registeredAt: '2026-07-01T06:30:00Z',
    jobsCompleted: 12,
  },
]

export const JOBS: Job[] = [
  {
    id: 'job-a1b2c3d4',
    state: 'RUNNING',
    worker: 'worker-gpu-01',
    submittedAt: '2 min ago',
    startedAt: '1m 30s ago',
    finishedAt: null,
    duration: '1m 23s',
    requiresGpu: true,
    minRam: 16,
    minVram: 8,
    checkpointKey: 'checkpoints/job-a1b2c3d4/epoch-3.pt',
    logs: `[14:32:15] Initializing distributed training session
[14:32:16] Connecting to parameter server...
[14:32:17] Connected. Sync point 1/10.
[14:32:18] Model loaded: 2.4GB
[14:32:19] Gradient accumulation steps: 4
[14:32:20] Starting epoch 1/20...
[14:32:45] Epoch 1: loss = 2.341 | val_accuracy = 0.764
[14:33:10] Epoch 2: loss = 1.892 | val_accuracy = 0.821
[14:33:35] Epoch 3: loss = 1.564 | val_accuracy = 0.856
[14:34:00] Computing batch norm statistics...
[14:34:05] Batch norm complete. Ready for next epoch.`,
  },
  {
    id: 'job-x7y8z9w0',
    state: 'COMPLETED',
    worker: 'worker-gpu-02',
    submittedAt: '8 min ago',
    startedAt: '7m 40s ago',
    finishedAt: '2m ago',
    duration: '5m 42s',
    requiresGpu: true,
    minRam: 8,
    minVram: 4,
    checkpointKey: 'checkpoints/job-x7y8z9w0/final.pt',
    logs: `[14:26:00] Starting training pipeline
[14:26:01] Venv cache hit: /tmp/edgegrid-venvs/a3f9c2...
[14:26:02] Launching training script
[14:26:03] Epoch 1: loss = 1.823 | val_accuracy = 0.802
[14:27:10] Epoch 2: loss = 1.201 | val_accuracy = 0.874
[14:28:20] Epoch 3: loss = 0.923 | val_accuracy = 0.901
[14:29:31] Epoch 4: loss = 0.788 | val_accuracy = 0.923
[14:30:42] Epoch 5: loss = 0.654 | val_accuracy = 0.941
[14:31:43] Training complete. Pushing checkpoint...
[14:31:44] Checkpoint saved to object store.`,
  },
  {
    id: 'job-m1n2o3p4',
    state: 'FAILED',
    worker: 'worker-gpu-03',
    submittedAt: '15 min ago',
    startedAt: '14m 50s ago',
    finishedAt: '11m ago',
    duration: '3m 21s',
    requiresGpu: true,
    minRam: 32,
    minVram: 10,
    checkpointKey: null,
    logs: `[14:18:00] Starting training pipeline
[14:18:02] Venv cache miss. Building venv...
[14:19:30] Venv ready.
[14:19:31] Launching training script
[14:19:32] Epoch 1: loss = 3.421
[14:21:00] Error: CUDA out of memory. Tried to allocate 2.5 GiB
[14:21:01] Attempting recovery...
[14:21:02] Recovery failed. CUDA device unavailable.
[14:21:03] Job terminated with error.`,
  },
  {
    id: 'job-q5r6s7t8',
    state: 'QUEUED',
    worker: null,
    submittedAt: '25 min ago',
    startedAt: null,
    finishedAt: null,
    duration: '—',
    requiresGpu: false,
    minRam: 4,
    minVram: 0,
    checkpointKey: null,
    logs: 'Waiting for worker availability.',
  },
  {
    id: 'job-p9q0r1s2',
    state: 'CANCELLED',
    worker: 'worker-gpu-02',
    submittedAt: '32 min ago',
    startedAt: '31m ago',
    finishedAt: '29m ago',
    duration: '2m 10s',
    requiresGpu: true,
    minRam: 16,
    minVram: 8,
    checkpointKey: null,
    logs: `[14:02:00] Starting training pipeline
[14:02:01] Epoch 1: loss = 2.901
[14:04:10] Job cancelled by user request.`,
  },
  {
    id: 'job-r7s8t9u0',
    state: 'PENDING_REVIEW',
    worker: 'worker-gpu-02',
    submittedAt: '3 min ago',
    startedAt: null,
    finishedAt: null,
    duration: '—',
    requiresGpu: true,
    minRam: 32,
    minVram: 16,
    checkpointKey: null,
    logs: 'Awaiting worker approval before execution.',
  },
  {
    id: 'job-t3u4v5w6',
    state: 'COMPLETED',
    worker: 'worker-cpu-01',
    submittedAt: '1h ago',
    startedAt: '59m ago',
    finishedAt: '44m ago',
    duration: '15m 02s',
    requiresGpu: false,
    minRam: 8,
    minVram: 0,
    checkpointKey: 'checkpoints/job-t3u4v5w6/final.pt',
    logs: `[13:05:00] Starting CPU training pipeline
[13:05:02] Model: tiny-bert (14M params)
[13:10:00] Epoch 5/10: loss = 0.443
[13:15:00] Epoch 10/10: loss = 0.221
[13:20:02] Training complete.`,
  },
]

export const JOB_HISTORY_BY_WORKER: Record<string, Job[]> = {
  'worker-gpu-01': [JOBS[0], JOBS[5]],
  'worker-gpu-02': [JOBS[1], JOBS[4]],
  'worker-gpu-03': [JOBS[2]],
  'worker-cpu-01': [JOBS[5]],
}

export function getWorker(id: string): Worker | undefined {
  return WORKERS.find((w) => w.id === id)
}

export function getJob(id: string): Job | undefined {
  return JOBS.find((j) => j.id === id)
}

export function stateColor(state: JobState | WorkerState): string {
  switch (state) {
    case 'RUNNING':
    case 'busy':
      return '#f59e0b'
    case 'COMPLETED':
    case 'healthy':
      return '#22c55e'
    case 'FAILED':
    case 'CANCELLED':
    case 'dead':
      return '#ef4444'
    case 'PENDING_REVIEW':
      return '#8b5cf6'
    case 'QUEUED':
      return '#6b7280'
    default:
      return '#6b7280'
  }
}
