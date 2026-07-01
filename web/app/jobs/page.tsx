'use client'

import Link from 'next/link'
import { useState } from 'react'
import { JOBS, JobState, stateColor } from '@/lib/mock-data'

const FILTERS: (JobState | 'ALL')[] = ['ALL', 'RUNNING', 'QUEUED', 'COMPLETED', 'FAILED', 'CANCELLED']

function StateDot({ state }: { state: JobState }) {
  const color = stateColor(state)
  const pulse = state === 'RUNNING'
  return (
    <span
      className={`inline-block w-1.5 h-1.5 rounded-full ${pulse ? 'animate-pulse' : ''}`}
      style={{ backgroundColor: color }}
    />
  )
}

export default function JobsPage() {
  const [filter, setFilter] = useState<JobState | 'ALL'>('ALL')

  const visible = filter === 'ALL' ? JOBS : JOBS.filter((j) => j.state === filter)

  const counts = FILTERS.reduce(
    (acc, f) => {
      acc[f] = f === 'ALL' ? JOBS.length : JOBS.filter((j) => j.state === f).length
      return acc
    },
    {} as Record<string, number>,
  )

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center justify-between shrink-0">
        <span className="terminal-label">JOBS // {JOBS.length} TOTAL</span>
        <Link
          href="/"
          className="terminal-label text-[#f59e0b] hover:text-[#fbbf24] transition-colors text-[9px]"
        >
          + DISPATCH →
        </Link>
      </div>

      {/* Filter tabs */}
      <div className="border-b border-[#1f1f1f] px-6 flex gap-0 shrink-0">
        {FILTERS.map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            className={`h-9 px-3 font-mono text-[10px] tracking-widest border-b-2 transition-colors ${
              filter === f
                ? 'border-[#f59e0b] text-[#f59e0b]'
                : 'border-transparent text-[#6b7280] hover:text-[#d4d4d4]'
            }`}
          >
            {f}
            <span className="ml-1.5 text-[#2d2d2d]">{counts[f]}</span>
          </button>
        ))}
      </div>

      {/* Table */}
      <div className="flex-1 overflow-y-auto">
        {/* Table header */}
        <div className="grid grid-cols-[1fr_1fr_1fr_80px_90px_80px_60px] gap-4 px-6 h-9 items-center border-b border-[#1f1f1f] sticky top-0 bg-[#0c0c0c] z-10">
          <span className="terminal-label text-[9px]">JOB ID</span>
          <span className="terminal-label text-[9px]">WORKER</span>
          <span className="terminal-label text-[9px]">SUBMITTED</span>
          <span className="terminal-label text-[9px]">STATE</span>
          <span className="terminal-label text-[9px]">DURATION</span>
          <span className="terminal-label text-[9px]">GPU / RAM</span>
          <span className="terminal-label text-[9px]"></span>
        </div>

        {visible.length === 0 ? (
          <div className="flex items-center justify-center h-32 text-[#6b7280] font-mono text-xs">
            NO JOBS MATCH FILTER
          </div>
        ) : (
          visible.map((job) => (
            <div
              key={job.id}
              className="grid grid-cols-[1fr_1fr_1fr_80px_90px_80px_60px] gap-4 px-6 h-10 items-center border-b border-[#1f1f1f] hover:bg-[#1a1a1a] transition-colors group"
            >
              <span className="font-mono text-xs text-[#f59e0b] truncate">{job.id}</span>
              <span className="font-mono text-xs text-[#d4d4d4] truncate">{job.worker ?? '—'}</span>
              <span className="font-mono text-xs text-[#6b7280]">{job.submittedAt}</span>
              <span className="flex items-center gap-1.5">
                <StateDot state={job.state} />
                <span className="font-mono text-xs" style={{ color: stateColor(job.state) }}>
                  {job.state}
                </span>
              </span>
              <span className="font-mono text-xs text-[#6b7280]">{job.duration}</span>
              <span className="font-mono text-[10px] text-[#6b7280]">
                {job.requiresGpu ? `GPU / ${job.minRam}GB` : `CPU / ${job.minRam}GB`}
              </span>
              <Link
                href={`/jobs/${job.id}`}
                className="font-mono text-[10px] text-[#6b7280] hover:text-[#f59e0b] transition-colors opacity-0 group-hover:opacity-100"
              >
                VIEW →
              </Link>
            </div>
          ))
        )}
      </div>
    </div>
  )
}
