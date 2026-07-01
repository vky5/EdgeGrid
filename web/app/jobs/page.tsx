'use client'

import Link from 'next/link'
import { useEffect, useState } from 'react'
import { listJobs, LiveJob } from '@/lib/api'

const FILTERS = ['ALL', 'RUNNING', 'PENDING_REVIEW', 'QUEUED', 'COMPLETED', 'FAILED', 'CANCELLED']

const STATE_COLOR: Record<string, string> = {
  RUNNING:        '#f59e0b',
  PENDING_REVIEW: '#8b5cf6',
  QUEUED:         '#6b7280',
  COMPLETED:      '#22c55e',
  FAILED:         '#ef4444',
  CANCELLED:      '#ef4444',
}

function StateDot({ state }: { state: string }) {
  const color = STATE_COLOR[state] ?? '#6b7280'
  return (
    <span className="flex items-center gap-1.5">
      <span
        className={`w-1.5 h-1.5 rounded-full shrink-0 ${state === 'RUNNING' ? 'animate-pulse' : ''}`}
        style={{ backgroundColor: color }}
      />
      <span style={{ color }} className="font-mono text-xs">{state}</span>
    </span>
  )
}

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return new Date(iso).toLocaleDateString()
}

export default function JobsPage() {
  const [jobs, setJobs] = useState<LiveJob[]>([])
  const [filter, setFilter] = useState('ALL')
  const [connected, setConnected] = useState<boolean | null>(null)

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const data = await listJobs()
        if (!cancelled) { setJobs(data); setConnected(true) }
      } catch {
        if (!cancelled) setConnected(false)
      }
    }
    poll()
    const t = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [])

  const visible = filter === 'ALL' ? jobs : jobs.filter((j) => j.state === filter)

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-6 shrink-0">
        <span className="terminal-label">JOBS</span>
        {connected === false && (
          <span className="font-mono text-[9px] text-[#ef4444]">COORDINATOR OFFLINE</span>
        )}
        {connected === true && (
          <span className="w-1.5 h-1.5 rounded-full bg-[#22c55e] animate-pulse inline-block" />
        )}
        <span className="font-mono text-[10px] text-[#6b7280] ml-auto">{jobs.length} total</span>
      </div>

      {/* Filter tabs */}
      <div className="border-b border-[#1f1f1f] px-6 flex items-center gap-1 h-10 overflow-x-auto shrink-0">
        {FILTERS.map((f) => {
          const count = f === 'ALL' ? jobs.length : jobs.filter((j) => j.state === f).length
          return (
            <button
              key={f}
              onClick={() => setFilter(f)}
              className={`font-mono text-[9px] px-2 py-1 tracking-widest transition-colors whitespace-nowrap ${
                filter === f
                  ? 'text-[#f59e0b] border-b-2 border-[#f59e0b]'
                  : 'text-[#6b7280] hover:text-[#d4d4d4]'
              }`}
            >
              {f} {count > 0 && <span className="opacity-60">({count})</span>}
            </button>
          )
        })}
      </div>

      {/* Table */}
      <div className="flex-1 overflow-y-auto">
        <div className="grid grid-cols-[1fr_140px_1fr_90px] gap-4 px-6 h-9 items-center border-b border-[#1f1f1f] sticky top-0 bg-[#0c0c0c]">
          <span className="terminal-label text-[9px]">JOB ID</span>
          <span className="terminal-label text-[9px]">STATE</span>
          <span className="terminal-label text-[9px]">WORKER</span>
          <span className="terminal-label text-[9px]">UPDATED</span>
        </div>

        {connected === null && (
          <div className="px-6 py-4 text-[#6b7280] font-mono text-xs">connecting...</div>
        )}

        {connected !== null && visible.length === 0 && (
          <div className="px-6 py-4 text-[#6b7280] font-mono text-xs">
            {filter === 'ALL' ? 'no jobs yet' : `no ${filter} jobs`}
          </div>
        )}

        {visible.map((job) => (
          <div
            key={job.job_id}
            className="grid grid-cols-[1fr_140px_1fr_90px] gap-4 px-6 h-10 items-center border-b border-[#1f1f1f] hover:bg-[#1a1a1a] transition-colors"
          >
            <Link
              href={`/jobs/${job.job_id}`}
              className="font-mono text-xs text-[#f59e0b] hover:text-[#fbbf24] truncate"
            >
              {job.job_id}
            </Link>
            <StateDot state={job.state} />
            <span className="font-mono text-xs text-[#6b7280] truncate">
              {job.worker_id || '—'}
            </span>
            <span className="font-mono text-[10px] text-[#6b7280]">{relativeTime(job.updated_at)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
