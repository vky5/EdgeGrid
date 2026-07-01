'use client'

import Link from 'next/link'
import { useParams, useRouter } from 'next/navigation'
import { useState } from 'react'
import { getJob, stateColor } from '@/lib/mock-data'
import { approveJob, rejectJob, cancelJob } from '@/lib/api'

function MetaRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-start gap-3 py-2 border-b border-[#1f1f1f]">
      <span className="terminal-label text-[9px] w-24 shrink-0 pt-0.5">{label}</span>
      <span className="font-mono text-xs text-[#d4d4d4] break-all">{value}</span>
    </div>
  )
}

export default function JobDetailPage() {
  const { id } = useParams<{ id: string }>()
  const router = useRouter()
  const [acting, setActing] = useState(false)
  const job = getJob(id)

  const act = async (fn: () => Promise<void>) => {
    setActing(true)
    try { await fn() } catch (e) { console.error(e) } finally { setActing(false); router.refresh() }
  }

  if (!job) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c] text-[#6b7280] font-mono text-sm">
        JOB {id} NOT FOUND
      </div>
    )
  }

  const color = stateColor(job.state)

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-4 shrink-0">
        <Link href="/jobs" className="terminal-label text-[#6b7280] hover:text-[#d4d4d4] transition-colors text-[9px]">
          ← JOBS
        </Link>
        <span className="text-[#1f1f1f]">/</span>
        <span className="font-mono text-xs text-[#f59e0b]">{job.id}</span>
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
          {(job.state === 'RUNNING' || job.state === 'QUEUED' || job.state === 'PENDING_REVIEW') && (
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

      {/* Body: two columns */}
      <div className="flex-1 flex overflow-hidden">
        {/* LEFT: metadata */}
        <div className="w-72 border-r border-[#1f1f1f] flex flex-col shrink-0 overflow-y-auto">
          <div className="p-4 border-b border-[#1f1f1f]">
            <div className="terminal-label">JOB METADATA</div>
          </div>
          <div className="px-4 pb-4">
            <MetaRow label="JOB ID" value={job.id} />
            <MetaRow label="STATE" value={<span style={{ color }}>{job.state}</span>} />
            <MetaRow label="WORKER" value={job.worker ?? <span className="text-[#6b7280]">unassigned</span>} />
            <MetaRow label="SUBMITTED" value={job.submittedAt} />
            <MetaRow label="STARTED" value={job.startedAt ?? <span className="text-[#6b7280]">—</span>} />
            <MetaRow label="FINISHED" value={job.finishedAt ?? <span className="text-[#6b7280]">—</span>} />
            <MetaRow label="DURATION" value={job.duration} />
          </div>

          <div className="px-4 pb-4">
            <div className="terminal-label text-[9px] mb-3 pt-2">REQUIREMENTS</div>
            <MetaRow label="GPU" value={job.requiresGpu ? 'YES' : 'NO'} />
            <MetaRow label="MIN RAM" value={`${job.minRam} GB`} />
            <MetaRow label="MIN VRAM" value={job.requiresGpu ? `${job.minVram} GB` : '—'} />
          </div>

          {job.checkpointKey && (
            <div className="px-4 pb-4">
              <div className="terminal-label text-[9px] mb-3 pt-2">CHECKPOINT</div>
              <div className="bg-black border border-[#1f1f1f] p-2 font-mono text-[10px] text-[#22c55e] break-all">
                {job.checkpointKey}
              </div>
            </div>
          )}

          {job.worker && (
            <div className="px-4 pb-4 mt-auto pt-4 border-t border-[#1f1f1f]">
              <Link
                href={`/workers/${job.worker}`}
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
            <span className="font-mono text-[10px] text-[#6b7280]">{job.id}</span>
          </div>
          {job.state === 'PENDING_REVIEW' && (
            <div className="border-b border-[#8b5cf6]/30 bg-[#8b5cf6]/5 px-4 py-2 flex items-center gap-3">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-[#8b5cf6] animate-pulse" />
              <span className="font-mono text-[10px] text-[#8b5cf6] tracking-widest">
                AWAITING WORKER APPROVAL — worker-gpu-02 must approve before execution starts
              </span>
            </div>
          )}
          <div className="flex-1 overflow-y-auto bg-black font-mono text-[11px] leading-relaxed p-4 space-y-0">
            {job.logs.split('\n').map((line, idx) => {
              const isEpoch = line.includes('Epoch') || line.includes('epoch')
              const isError =
                line.toLowerCase().includes('error') || line.toLowerCase().includes('failed')
              const timestampMatch = line.match(/^\[(\d{2}:\d{2}:\d{2})\]/)
              const timestamp = timestampMatch?.[1]
              const message = timestampMatch ? line.slice(line.indexOf(']') + 1) : line

              return (
                <div
                  key={idx}
                  className={`whitespace-pre-wrap break-words py-0.5 ${isEpoch ? 'bg-[#f59e0b]/10' : ''}`}
                >
                  {timestamp && <span className="text-[#6b7280]">[{timestamp}]</span>}
                  <span className={isError ? 'text-[#ef4444]' : 'text-[#d4d4d4]'}>{message}</span>
                </div>
              )
            })}
            {job.state === 'RUNNING' && (
              <div className="text-[#f59e0b] animate-pulse mt-1">▌</div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
