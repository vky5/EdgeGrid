'use client'

import Link from 'next/link'
import { useParams, useRouter } from 'next/navigation'
import { useEffect, useState } from 'react'
import { getJob, approveJob, rejectJob, cancelJob, LiveJob } from '@/lib/api'

const STATE_COLOR: Record<string, string> = {
  RUNNING:        '#f59e0b',
  PENDING_REVIEW: '#8b5cf6',
  QUEUED:         '#6b7280',
  COMPLETED:      '#22c55e',
  FAILED:         '#ef4444',
  CANCELLED:      '#ef4444',
}

function MetaRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-start gap-3 py-2 border-b border-[#1f1f1f]">
      <span className="terminal-label text-[9px] w-24 shrink-0 pt-0.5">{label}</span>
      <span className="font-mono text-xs text-[#d4d4d4] break-all">{value}</span>
    </div>
  )
}

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return new Date(iso).toLocaleString()
}

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>()
  const router = useRouter()
  const [job, setJob] = useState<LiveJob | null | undefined>(undefined)
  const [acting, setActing] = useState(false)

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const data = await getJob(id)
        if (!cancelled) setJob(data)
      } catch {
        if (!cancelled) setJob(null)
      }
    }
    poll()
    const t = setInterval(poll, 3_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [id])

  const act = async (fn: () => Promise<void>) => {
    setActing(true)
    try { await fn() } catch (e) { console.error(e) } finally { setActing(false) }
  }

  if (job === undefined) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c] text-[#6b7280] font-mono text-sm">
        connecting...
      </div>
    )
  }

  if (job === null) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c] text-[#6b7280] font-mono text-sm">
        JOB {id} NOT FOUND
      </div>
    )
  }

  const color = STATE_COLOR[job.state] ?? '#6b7280'
  const isActive = job.state === 'RUNNING' || job.state === 'QUEUED' || job.state === 'PENDING_REVIEW'

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-4 shrink-0">
        <Link href="/jobs" className="terminal-label text-[#6b7280] hover:text-[#d4d4d4] transition-colors text-[9px]">
          ← JOBS
        </Link>
        <span className="text-[#1f1f1f]">/</span>
        <span className="font-mono text-xs text-[#f59e0b]">{job.job_id}</span>
        <span
          className="font-mono text-[10px] px-1.5 py-0.5 border"
          style={{ color, borderColor: color, backgroundColor: `${color}15` }}
        >
          {job.state}
        </span>

        <div className="ml-auto flex items-center gap-2">
          {job.state === 'PENDING_REVIEW' && (
            <>
              <button
                disabled={acting}
                onClick={() => act(() => approveJob(id))}
                className="font-mono text-[10px] text-[#22c55e] border border-[#22c55e] px-3 py-1 hover:bg-[#22c55e]/10 transition-colors tracking-widest disabled:opacity-40"
              >
                APPROVE →
              </button>
              <button
                disabled={acting}
                onClick={() => act(() => rejectJob(id))}
                className="font-mono text-[10px] text-[#ef4444] border border-[#ef4444] px-3 py-1 hover:bg-[#ef4444]/10 transition-colors tracking-widest disabled:opacity-40"
              >
                REJECT
              </button>
            </>
          )}
          {isActive && (
            <button
              disabled={acting}
              onClick={() => act(() => cancelJob(id))}
              className="font-mono text-[10px] text-[#6b7280] border border-[#1f1f1f] px-3 py-1 hover:border-[#ef4444] hover:text-[#ef4444] transition-colors tracking-widest disabled:opacity-40"
            >
              CANCEL
            </button>
          )}
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 flex overflow-hidden">
        {/* LEFT: metadata */}
        <div className="w-72 border-r border-[#1f1f1f] flex flex-col shrink-0 overflow-y-auto">
          <div className="p-4 border-b border-[#1f1f1f]">
            <div className="terminal-label">JOB METADATA</div>
          </div>
          <div className="px-4 pb-4">
            <MetaRow label="JOB ID" value={job.job_id} />
            <MetaRow label="STATE" value={<span style={{ color }}>{job.state}</span>} />
            <MetaRow
              label="WORKER"
              value={
                job.worker_id ? (
                  <Link href={`/workers/${job.worker_id}`} className="text-[#f59e0b] hover:text-[#fbbf24]">
                    {job.worker_id}
                  </Link>
                ) : (
                  <span className="text-[#6b7280]">unassigned</span>
                )
              }
            />
            <MetaRow label="UPDATED" value={relativeTime(job.updated_at)} />
            {job.error && (
              <MetaRow label="ERROR" value={<span className="text-[#ef4444]">{job.error}</span>} />
            )}
          </div>

          {job.checkpoint_key && (
            <div className="px-4 pb-4">
              <div className="terminal-label text-[9px] mb-3 pt-2">CHECKPOINT</div>
              <div className="bg-black border border-[#1f1f1f] p-2 font-mono text-[10px] text-[#22c55e] break-all">
                {job.checkpoint_key}
              </div>
              <Link
                href={`http://localhost:8080/jobs/${job.job_id}/artifact`}
                target="_blank"
                className="block mt-2 font-mono text-[9px] text-[#6b7280] border border-[#1f1f1f] px-3 py-2 text-center hover:border-[#22c55e] hover:text-[#22c55e] transition-colors tracking-widest"
              >
                DOWNLOAD →
              </Link>
            </div>
          )}

          {job.worker_id && (
            <div className="px-4 pb-4 mt-auto pt-4 border-t border-[#1f1f1f]">
              <Link
                href={`/workers/${job.worker_id}`}
                className="block w-full font-mono text-[10px] text-[#6b7280] border border-[#1f1f1f] px-3 py-2 text-center hover:border-[#f59e0b] hover:text-[#f59e0b] transition-colors tracking-widest"
              >
                VIEW WORKER →
              </Link>
            </div>
          )}
        </div>

        {/* RIGHT: log terminal */}
        <div className="flex-1 flex flex-col min-w-0">
          <div className="h-10 flex items-center px-4 border-b border-[#1f1f1f] bg-black shrink-0 gap-3">
            <span className="terminal-label text-[9px]">LOGS</span>
            {job.state === 'RUNNING' && (
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-[#f59e0b] animate-pulse" />
            )}
            <span className="font-mono text-[10px] text-[#6b7280]">{job.job_id}</span>
          </div>

          {job.state === 'PENDING_REVIEW' && (
            <div className="border-b border-[#8b5cf6]/30 bg-[#8b5cf6]/5 px-4 py-3 flex items-center gap-3">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-[#8b5cf6] animate-pulse" />
              <div>
                <div className="font-mono text-[10px] text-[#8b5cf6] tracking-widest mb-0.5">
                  AWAITING WORKER APPROVAL
                </div>
                <div className="font-mono text-[10px] text-[#6b7280]">
                  Worker <span className="text-[#d4d4d4]">{job.worker_id}</span> must approve before execution starts.
                  Will auto-reject after 60s.
                </div>
              </div>
            </div>
          )}

          {job.state === 'QUEUED' && (
            <div className="border-b border-[#6b7280]/30 bg-[#6b7280]/5 px-4 py-3 flex items-center gap-3">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-[#6b7280]" />
              <span className="font-mono text-[10px] text-[#6b7280]">
                Waiting for a free worker that meets requirements
              </span>
            </div>
          )}

          <div className="flex-1 overflow-y-auto bg-black font-mono text-[11px] leading-relaxed p-4">
            {(job.state === 'QUEUED' || job.state === 'PENDING_REVIEW') && (
              <div className="text-[#3f3f3f]">logs will appear here once the job starts running</div>
            )}
            {job.state === 'RUNNING' && (
              <div className="text-[#f59e0b] animate-pulse">▌ streaming logs — wire SSE endpoint to see live output</div>
            )}
            {job.state === 'COMPLETED' && (
              <div className="text-[#22c55e]">job completed — checkpoint key: {job.checkpoint_key || 'none'}</div>
            )}
            {job.state === 'FAILED' && (
              <div className="text-[#ef4444]">job failed: {job.error || 'unknown error'}</div>
            )}
            {job.state === 'CANCELLED' && (
              <div className="text-[#6b7280]">job was cancelled</div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
