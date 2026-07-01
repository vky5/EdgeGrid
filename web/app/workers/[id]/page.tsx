'use client'

import Link from 'next/link'
import { useParams } from 'next/navigation'
import { getWorker, JOB_HISTORY_BY_WORKER, stateColor, WorkerState } from '@/lib/mock-data'

function HardwareBar({ used, total, label, unit = 'GB' }: { used: number; total: number; label: string; unit?: string }) {
  const pct = total > 0 ? Math.min((used / total) * 100, 100) : 0
  const warn = pct > 80
  return (
    <div className="space-y-1">
      <div className="flex justify-between text-xs">
        <span className="text-[#6b7280] font-mono">{label}</span>
        <span className="font-mono text-[#d4d4d4]">
          {used.toFixed(1)} / {total} {unit}
        </span>
      </div>
      <div className="h-1.5 bg-[#1f1f1f]">
        <div
          className="h-full"
          style={{ width: `${pct}%`, backgroundColor: warn ? '#ef4444' : '#f59e0b' }}
        />
      </div>
      <div className="text-[10px] text-[#6b7280] font-mono">{pct.toFixed(1)}% used</div>
    </div>
  )
}

const STATE_COLOR: Record<WorkerState, string> = {
  healthy: '#22c55e',
  busy: '#f59e0b',
  dead: '#ef4444',
}

const STATE_LABEL: Record<WorkerState, string> = {
  healthy: 'IDLE',
  busy: 'BUSY',
  dead: 'OFFLINE',
}

export default function WorkerDetailPage() {
  const { id } = useParams<{ id: string }>()
  const worker = getWorker(id)
  const jobs = JOB_HISTORY_BY_WORKER[id] ?? []

  if (!worker) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c] text-[#6b7280] font-mono text-sm">
        WORKER {id} NOT FOUND
      </div>
    )
  }

  const color = STATE_COLOR[worker.state]

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-4 shrink-0">
        <Link
          href="/workers"
          className="terminal-label text-[#6b7280] hover:text-[#d4d4d4] transition-colors text-[9px]"
        >
          ← WORKERS
        </Link>
        <span className="text-[#1f1f1f]">/</span>
        <span className="font-mono text-xs text-[#d4d4d4]">{worker.id}</span>
        <span
          className="font-mono text-[10px] px-1.5 py-0.5 border"
          style={{ color, borderColor: color, backgroundColor: `${color}15` }}
        >
          {STATE_LABEL[worker.state]}
        </span>
      </div>

      {/* Body */}
      <div className="flex-1 flex overflow-hidden">
        {/* LEFT: hardware panel */}
        <div className="w-80 border-r border-[#1f1f1f] shrink-0 overflow-y-auto">
          {/* Status card */}
          <div className="p-4 border-b border-[#1f1f1f]">
            <div className="terminal-label mb-3">STATUS</div>
            <div className="flex items-center gap-3">
              <span
                className={`w-3 h-3 rounded-full ${worker.state === 'busy' ? 'animate-pulse' : ''}`}
                style={{ backgroundColor: color }}
              />
              <div>
                <div className="font-mono text-sm" style={{ color }}>
                  {STATE_LABEL[worker.state]}
                </div>
                <div className="font-mono text-[10px] text-[#6b7280]">last seen {worker.lastSeen}</div>
              </div>
            </div>
            {worker.currentJob && (
              <div className="mt-3 border border-[#1f1f1f] p-2">
                <div className="terminal-label text-[9px] mb-1">RUNNING JOB</div>
                <Link
                  href={`/jobs/${worker.currentJob}`}
                  className="font-mono text-xs text-[#f59e0b] hover:text-[#fbbf24] transition-colors"
                >
                  {worker.currentJob} →
                </Link>
              </div>
            )}
          </div>

          {/* Hardware */}
          <div className="p-4 border-b border-[#1f1f1f] space-y-4">
            <div className="terminal-label">HARDWARE</div>

            {worker.gpu ? (
              <>
                <div className="border border-[#1f1f1f] p-3">
                  <div className="terminal-label text-[9px] mb-2">GPU</div>
                  <div className="font-mono text-sm text-[#d4d4d4] mb-2">{worker.gpu}</div>
                  <HardwareBar used={worker.gpuUsed} total={worker.gpuTotal} label="VRAM" />
                </div>
              </>
            ) : (
              <div className="border border-[#1f1f1f] p-3">
                <div className="terminal-label text-[9px]">CPU ONLY</div>
                <div className="font-mono text-[10px] text-[#6b7280] mt-1">No GPU detected</div>
              </div>
            )}

            <HardwareBar used={worker.ramUsed} total={worker.ram} label="RAM" />
            <HardwareBar
              used={worker.disk - worker.diskFree}
              total={worker.disk}
              label="DISK USED"
            />
          </div>

          {/* Stats */}
          <div className="p-4">
            <div className="terminal-label mb-3">LIFETIME</div>
            <div className="grid grid-cols-2 gap-3">
              <div className="border border-[#1f1f1f] p-2">
                <div className="terminal-label text-[9px]">JOBS DONE</div>
                <div className="font-mono text-xl text-[#f59e0b] mt-1">{worker.jobsCompleted}</div>
              </div>
              <div className="border border-[#1f1f1f] p-2">
                <div className="terminal-label text-[9px]">DISK FREE</div>
                <div className="font-mono text-xl text-[#d4d4d4] mt-1">{worker.diskFree}GB</div>
              </div>
            </div>
            <div className="border border-[#1f1f1f] p-2 mt-3">
              <div className="terminal-label text-[9px]">REGISTERED</div>
              <div className="font-mono text-xs text-[#6b7280] mt-1">
                {new Date(worker.registeredAt).toLocaleString()}
              </div>
            </div>
          </div>
        </div>

        {/* RIGHT: job history */}
        <div className="flex-1 flex flex-col min-w-0">
          <div className="h-10 flex items-center px-6 border-b border-[#1f1f1f] shrink-0">
            <span className="terminal-label">JOB HISTORY // {jobs.length} JOBS</span>
          </div>

          {jobs.length === 0 ? (
            <div className="flex items-center justify-center flex-1 text-[#6b7280] font-mono text-xs">
              NO JOBS RUN ON THIS WORKER
            </div>
          ) : (
            <div className="flex-1 overflow-y-auto">
              {/* Column headers */}
              <div className="grid grid-cols-[1fr_100px_100px_80px_1fr] gap-4 px-6 h-9 items-center border-b border-[#1f1f1f] sticky top-0 bg-[#0c0c0c]">
                <span className="terminal-label text-[9px]">JOB ID</span>
                <span className="terminal-label text-[9px]">STATE</span>
                <span className="terminal-label text-[9px]">DURATION</span>
                <span className="terminal-label text-[9px]">GPU</span>
                <span className="terminal-label text-[9px]">SUBMITTED</span>
              </div>

              {jobs.map((job) => {
                const jColor = stateColor(job.state)
                return (
                  <div
                    key={job.id}
                    className="grid grid-cols-[1fr_100px_100px_80px_1fr] gap-4 px-6 h-10 items-center border-b border-[#1f1f1f] hover:bg-[#1a1a1a] transition-colors group"
                  >
                    <Link
                      href={`/jobs/${job.id}`}
                      className="font-mono text-xs text-[#f59e0b] hover:text-[#fbbf24] truncate"
                    >
                      {job.id}
                    </Link>
                    <span className="font-mono text-xs" style={{ color: jColor }}>
                      {job.state}
                    </span>
                    <span className="font-mono text-xs text-[#6b7280]">{job.duration}</span>
                    <span className="font-mono text-xs text-[#6b7280]">
                      {job.requiresGpu ? 'YES' : 'NO'}
                    </span>
                    <span className="font-mono text-xs text-[#6b7280]">{job.submittedAt}</span>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
