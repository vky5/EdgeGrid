'use client'

import Link from 'next/link'
import { useParams } from 'next/navigation'
import { useEffect, useState } from 'react'
import { listWorkers, LiveWorker } from '@/lib/api'

function HardwareBar({ used, total, label }: { used: number; total: number; label: string }) {
  const pct = total > 0 ? Math.min((used / total) * 100, 100) : 0
  const warn = pct > 80
  return (
    <div className="space-y-1">
      <div className="flex justify-between text-xs">
        <span className="text-[#6b7280] font-mono">{label}</span>
        <span className="font-mono text-[#d4d4d4]">
          {used.toFixed(1)} / {total.toFixed(1)} GB
        </span>
      </div>
      <div className="h-1.5 bg-[#1f1f1f]">
        <div
          className="h-full transition-all duration-500"
          style={{ width: `${pct}%`, backgroundColor: warn ? '#ef4444' : '#f59e0b' }}
        />
      </div>
      <div className="text-[10px] text-[#6b7280] font-mono">{pct.toFixed(1)}% used</div>
    </div>
  )
}

function relativeTime(iso: string): string {
  if (!iso) return '?'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 10_000) return 'just now'
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return `${Math.floor(diff / 3_600_000)}h ago`
}

const STATE_COLOR = { free: '#22c55e', busy: '#f59e0b', dead: '#ef4444' } as const
const STATE_LABEL = { free: 'IDLE', busy: 'BUSY', dead: 'OFFLINE' } as const

export default function WorkerDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [worker, setWorker] = useState<LiveWorker | null | undefined>(undefined)

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const all = await listWorkers()
        if (!cancelled) {
          setWorker(all.find((w) => w.info?.id === id) ?? null)
        }
      } catch {
        if (!cancelled) setWorker(null)
      }
    }
    poll()
    const t = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [id])

  if (worker === undefined) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c] text-[#6b7280] font-mono text-sm">
        connecting...
      </div>
    )
  }

  if (worker === null) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c] text-[#6b7280] font-mono text-sm">
        WORKER {id} NOT FOUND
      </div>
    )
  }

  const state = worker.state as 'free' | 'busy' | 'dead'
  const color = STATE_COLOR[state]
  const info = worker.info
  const stats = worker.stats ?? { ram_used_gb: 0, disk_used_gb: 0, disk_total_gb: 0 }

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
        <span className="font-mono text-xs text-[#d4d4d4]">{info.id}</span>
        <span
          className="font-mono text-[10px] px-1.5 py-0.5 border"
          style={{ color, borderColor: color, backgroundColor: `${color}15` }}
        >
          {STATE_LABEL[state]}
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
                className={`w-3 h-3 rounded-full ${state === 'busy' ? 'animate-pulse' : ''}`}
                style={{ backgroundColor: color }}
              />
              <div>
                <div className="font-mono text-sm" style={{ color }}>{STATE_LABEL[state]}</div>
                <div className="font-mono text-[10px] text-[#6b7280]">last seen {relativeTime(worker.last_seen)}</div>
              </div>
            </div>
            {worker.job && (
              <div className="mt-3 border border-[#1f1f1f] p-2">
                <div className="terminal-label text-[9px] mb-1">RUNNING JOB</div>
                <Link
                  href={`/jobs/${worker.job.id}`}
                  className="font-mono text-xs text-[#f59e0b] hover:text-[#fbbf24] transition-colors"
                >
                  {worker.job.id} →
                </Link>
              </div>
            )}
          </div>

          {/* Hardware */}
          <div className="p-4 border-b border-[#1f1f1f] space-y-4">
            <div className="terminal-label">HARDWARE</div>

            {info.has_gpu ? (
              <div className="border border-[#1f1f1f] p-3 space-y-3">
                <div className="terminal-label text-[9px]">GPU</div>
                <div className="font-mono text-sm text-[#d4d4d4]">{info.gpu_name}</div>
                <HardwareBar used={0} total={info.gpu_vram_gb} label="VRAM" />
              </div>
            ) : (
              <div className="border border-[#1f1f1f] p-3">
                <div className="terminal-label text-[9px]">CPU ONLY</div>
                <div className="font-mono text-[10px] text-[#6b7280] mt-1">No GPU detected</div>
              </div>
            )}

            <HardwareBar
              used={stats.ram_used_gb}
              total={info.ram_gb}
              label="RAM"
            />
            <HardwareBar
              used={stats.disk_used_gb}
              total={stats.disk_total_gb > 0 ? stats.disk_total_gb : info.disk_free_gb}
              label="DISK USED"
            />
          </div>

          {/* Stats */}
          <div className="p-4">
            <div className="terminal-label mb-3">CAPACITY</div>
            <div className="grid grid-cols-2 gap-3">
              <div className="border border-[#1f1f1f] p-2">
                <div className="terminal-label text-[9px]">TOTAL RAM</div>
                <div className="font-mono text-xl text-[#f59e0b] mt-1">{info.ram_gb.toFixed(0)}GB</div>
              </div>
              <div className="border border-[#1f1f1f] p-2">
                <div className="terminal-label text-[9px]">DISK FREE</div>
                <div className="font-mono text-xl text-[#d4d4d4] mt-1">{info.disk_free_gb.toFixed(0)}GB</div>
              </div>
            </div>
            {info.has_gpu && (
              <div className="border border-[#1f1f1f] p-2 mt-3">
                <div className="terminal-label text-[9px]">GPU VRAM</div>
                <div className="font-mono text-xl text-[#d4d4d4] mt-1">{info.gpu_vram_gb.toFixed(0)}GB</div>
              </div>
            )}
          </div>
        </div>

        {/* RIGHT: placeholder for job history (not yet in API) */}
        <div className="flex-1 flex flex-col min-w-0">
          <div className="h-10 flex items-center px-6 border-b border-[#1f1f1f] shrink-0">
            <span className="terminal-label">JOB HISTORY</span>
          </div>
          <div className="flex items-center justify-center flex-1 text-[#6b7280] font-mono text-xs">
            job history per worker not yet available via API
          </div>
        </div>
      </div>
    </div>
  )
}
