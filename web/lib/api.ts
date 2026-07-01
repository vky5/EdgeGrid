const BASE = (process.env.NEXT_PUBLIC_COORDINATOR_URL ?? 'http://localhost:8080').replace(/\/$/, '')

export interface SubmitJobRequest {
  training_script: string
  requirements: string
  dataset_type: string
  dataset_ref: string
  base_model_type: string
  base_model_ref: string
  training_config_json: string
  requires_gpu: boolean
  min_ram_gb: number
  min_vram_gb: number
  min_disk_gb: number
}

export interface SubmitJobResponse {
  job_id: string
  status: string
}

export interface WorkerInfo {
  id: string
  has_gpu: boolean
  gpu_name: string
  gpu_vram_gb: number
  ram_gb: number
  disk_free_gb: number
}

export interface WorkerJob {
  id: string
  status: string
  started_at: string
  updated_at: string
}

export interface WorkerStats {
  ram_used_gb: number
  disk_used_gb: number
  disk_total_gb: number
}

export interface LiveWorker {
  info: WorkerInfo
  last_seen: string
  state: 'free' | 'busy' | 'dead'
  job: WorkerJob | null
  stats: WorkerStats
}

export async function submitJob(body: SubmitJobRequest): Promise<SubmitJobResponse> {
  const res = await fetch(`${BASE}/jobs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json()
}

export interface LiveJob {
  job_id: string
  state: string
  worker_id: string
  error: string
  checkpoint_key: string
  updated_at: string
}

export async function listJobs(): Promise<LiveJob[]> {
  const res = await fetch(`${BASE}/jobs`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const data = await res.json()
  return Array.isArray(data) ? data : []
}

export async function getJob(jobID: string): Promise<LiveJob | null> {
  const res = await fetch(`${BASE}/jobs/${jobID}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function listWorkers(): Promise<LiveWorker[]> {
  const res = await fetch(`${BASE}/workers`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const data = await res.json()
  return Array.isArray(data) ? data : []
}

export async function approveJob(jobID: string): Promise<void> {
  const res = await fetch(`${BASE}/jobs/${jobID}/approve`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
}

export async function rejectJob(jobID: string): Promise<void> {
  const res = await fetch(`${BASE}/jobs/${jobID}/reject`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
}

export async function cancelJob(jobID: string): Promise<void> {
  const res = await fetch(`${BASE}/jobs/${jobID}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
}
