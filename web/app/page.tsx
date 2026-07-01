'use client'

import { useEffect, useState } from 'react'
import { WorkerNodes } from '@/components/worker-nodes'
import { JobsPanel } from '@/components/jobs-panel'
import { SubmitJobPanel } from '@/components/submit-job-panel'
import { listJobs, LiveJob } from '@/lib/api'

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return `${Math.floor(diff / 3_600_000)}h ago`
}

export default function Page() {
  const [expandedJobId, setExpandedJobId] = useState<string | null>(null)
  const [liveJobs, setLiveJobs] = useState<LiveJob[]>([])

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const data = await listJobs()
        if (!cancelled) setLiveJobs(data)
      } catch { /* coordinator offline — keep showing last known state */ }
    }
    poll()
    const t = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [])

  const jobs = liveJobs.map((j) => ({
    id: j.job_id,
    submitted: relativeTime(j.updated_at),
    worker: j.worker_id || '—',
    state: j.state as any,
    duration: '—',
    logs: '',
  }))

  return (
    <div className="h-full w-full bg-[#0c0c0c] text-[#d4d4d4] flex overflow-hidden">
      <div className="w-60 border-r border-[#1f1f1f] flex flex-col">
        <WorkerNodes />
      </div>

      <div className="flex-1 flex flex-col border-r border-[#1f1f1f]">
        <JobsPanel jobs={jobs} expandedJobId={expandedJobId} onSelectJob={setExpandedJobId} />
      </div>

      <div className="w-72 flex flex-col border-l border-[#1f1f1f]">
        <SubmitJobPanel />
      </div>
    </div>
  )
}
