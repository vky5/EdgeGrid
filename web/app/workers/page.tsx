'use client'

import Link from 'next/link'
import { useEffect, useState } from 'react'
import { listWorkers, LiveWorker } from '@/lib/api'

function HardwareBar({ used, total }: { used: number; total: number }) {
  const pct = total > 0 ? Math.min((used / total) * 100, 100) : 0
  const warn = pct > 80
  return (
    <div className="h-1 bg-[#1f1f1f] w-full">
      <div
        className="h-full transition-all"
        style={{ width: `${pct}%`, backgroundColor: warn ? '#ef4444' : '#f59e0b' }}
      />
    </div>
  )
}

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return `${Math.floor(diff / 3_600_000)}h ago`
}

const STATE_COLOR = { free: '#22c55e', busy: '#f59e0b', dead: '#ef4444' } as const
const STATE_LABEL = { free: 'IDLE', busy: 'BUSY', dead: 'OFFLINE' } as const

export default function WorkersPage() {
  const [workers, setWorkers] = useState<LiveWorker[]>([])
  const [connected, setConnected] = useState<boolean | null>(null)

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const data = await listWorkers()
        if (!cancelled) { setWorkers(data); setConnected(true) }
      } catch {
        if (!cancelled) setConnected(false)
      }
    }
    poll()
    const t = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [])

  const online = workers.filter((w) => w.state !== 'dead').length
  const busy   = workers.filter((w) => w.state === 'busy').length

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-6 shrink-0">
        <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">
          WORKERS // {workers.length} REGISTERED
        </span>
        {connected === false ? (
          <span className="font-mono text-[9px] text-[#ef4444]">COORDINATOR OFFLINE</span>
        ) : (
          <>
            <span className="font-mono text-[10px] text-[#22c55e]">{online} ONLINE</span>
            <span className="font-mono text-[10px] text-[#f59e0b]">{busy} BUSY</span>
            <span className="font-mono text-[10px] text-[#ef4444]">{workers.length - online} OFFLINE</span>
          </>
        )}
      </div>

      {/* Grid */}
      <div className="flex-1 overflow-y-auto p-6">
        {workers.length === 0 && connected !== false && (
          <div className="text-[#3f3f3f] font-mono text-xs text-center pt-16">
            no workers connected
          </div>
        )}
        <div className="grid grid-cols-2 xl:grid-cols-3 gap-4">
          {workers.map((worker) => {
            const state = worker.state as 'free' | 'busy' | 'dead'
            const color = STATE_COLOR[state] ?? '#6b7280'
            const info = worker.info
            const stats = worker.stats ?? { ram_used_gb: 0, disk_used_gb: 0, disk_total_gb: 0 }

            return (
              <Link
                key={info.id}
                href={`/workers/${info.id}`}
                className="border border-[#1f1f1f] hover:border-[#f59e0b]/40 transition-colors group"
                style={{ borderLeftColor: color, borderLeftWidth: 2 }}
              >
                {/* Card header */}
                <div className="px-4 pt-4 pb-3 border-b border-[#1f1f1f] flex items-center justify-between">
                  <span className="font-mono text-sm text-[#d4d4d4] group-hover:text-[#f59e0b] transition-colors truncate">
                    {info.id}
                  </span>
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="font-mono text-[10px]" style={{ color }}>
                      {STATE_LABEL[state]}
                    </span>
                    <span
                      className={`w-2 h-2 rounded-full ${state === 'busy' ? 'animate-pulse' : ''}`}
                      style={{ backgroundColor: color }}
                    />
                  </div>
                </div>

                {/* Hardware */}
                <div className="px-4 py-3 space-y-3 text-[11px]">
                  {info.has_gpu ? (
                    <div>
                      <div className="flex justify-between mb-1">
                        <span className="text-[#6b7280]">{info.gpu_name}</span>
                        <span className="font-mono text-[#d4d4d4]">{info.gpu_vram_gb} GB VRAM</span>
                      </div>
                      <HardwareBar used={0} total={info.gpu_vram_gb} />
                    </div>
                  ) : (
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">CPU ONLY</span>
                    </div>
                  )}

                  <div>
                    <div className="flex justify-between mb-1">
                      <span className="text-[#6b7280]">RAM</span>
                      <span className="font-mono text-[#d4d4d4]">
                        {stats.ram_used_gb.toFixed(1)} / {info.ram_gb} GB
                      </span>
                    </div>
                    <HardwareBar used={stats.ram_used_gb} total={info.ram_gb} />
                  </div>

                  <div>
                    <div className="flex justify-between mb-1">
                      <span className="text-[#6b7280]">DISK FREE</span>
                      <span className="font-mono text-[#d4d4d4]">{info.disk_free_gb} GB</span>
                    </div>
                    <HardwareBar
                      used={stats.disk_used_gb}
                      total={stats.disk_total_gb > 0 ? stats.disk_total_gb : info.disk_free_gb}
                    />
                  </div>
                </div>

                {/* Footer */}
                <div className="px-4 py-2 border-t border-[#1f1f1f] flex items-center justify-between">
                  <span className="font-mono text-[10px] text-[#6b7280]">
                    {worker.job ? (
                      <span className="text-[#f59e0b]">↻ {worker.job.id}</span>
                    ) : (
                      `seen ${relativeTime(worker.last_seen)}`
                    )}
                  </span>
                </div>
              </Link>
            )
          })}
        </div>
      </div>
    </div>
  )
}
