'use client'

import Link from 'next/link'
import { useEffect, useState } from 'react'
import { listWorkers, LiveWorker } from '@/lib/api'

function HardwareBar({ pct }: { pct: number }) {
  const warn = pct > 80
  return (
    <div className="h-1 bg-[#1f1f1f] rounded-none overflow-hidden">
      <div
        className="h-full"
        style={{ width: `${Math.min(pct, 100)}%`, backgroundColor: warn ? '#ef4444' : '#f59e0b' }}
      />
    </div>
  )
}

const STATE_COLOR = { free: '#22c55e', busy: '#f59e0b', dead: '#ef4444' } as const
const BORDER_COLOR = {
  free: 'border-l-[#22c55e]',
  busy: 'border-l-[#f59e0b]',
  dead: 'border-l-[#ef4444]',
} as const

function workerState(w: LiveWorker): 'free' | 'busy' | 'dead' {
  return w.state === 'free' ? 'free' : w.state === 'busy' ? 'busy' : 'dead'
}

function relativeTime(iso: string): string {
  if (!iso) return '?'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 10_000) return 'just now'
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return `${Math.floor(diff / 3_600_000)}h ago`
}

export function WorkerNodes() {
  const [workers, setWorkers] = useState<LiveWorker[]>([])
  const [connected, setConnected] = useState<boolean | null>(null)

  useEffect(() => {
    let cancelled = false

    const poll = async () => {
      try {
        const data = await listWorkers()
        if (!cancelled) {
          setWorkers(data)
          setConnected(true)
        }
      } catch {
        if (!cancelled) setConnected(false)
      }
    }

    poll()
    const id = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(id) }
  }, [])

  const onlineCount = workers.filter((w) => w.state !== 'dead').length

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="p-4 border-b border-[#1f1f1f] flex items-center justify-between">
        <div className="terminal-label">
          NODES // {connected === false ? '?' : onlineCount} ONLINE
        </div>
        {connected === false && (
          <span className="font-mono text-[9px] text-[#ef4444]">COORDINATOR OFFLINE</span>
        )}
        {connected === true && (
          <span className="w-1.5 h-1.5 rounded-full bg-[#22c55e] animate-pulse inline-block" />
        )}
      </div>

      <div className="flex-1 overflow-y-auto">
        {workers.length === 0 && connected !== false && (
          <div className="p-4 text-[#6b7280] font-mono text-[10px]">
            {connected === null ? 'connecting...' : 'no workers registered'}
          </div>
        )}

        {workers.map((worker) => {
          const state = workerState(worker)
          const color = STATE_COLOR[state]
          const info = worker.info

          return (
            <Link
              key={info?.id ?? worker.state}
              href={`/workers/${info?.id}`}
              className={`border-l-2 border-b border-[#1f1f1f] p-3 block hover:bg-[#1a1a1a] transition-colors ${BORDER_COLOR[state]}`}
            >
              {/* ID + status dot */}
              <div className="flex items-center justify-between mb-2">
                <span className="font-mono text-xs text-[#d4d4d4]">{info?.id ?? '—'}</span>
                <div
                  className={`w-2 h-2 rounded-full ${state === 'busy' ? 'animate-pulse' : ''}`}
                  style={{ backgroundColor: color }}
                />
              </div>

              {/* Hardware */}
              <div className="space-y-1.5 text-[10px] mb-2">
                {info?.has_gpu && (
                  <div>
                    <div className="flex justify-between mb-0.5">
                      <span className="text-[#6b7280]">GPU {info.gpu_name}</span>
                      <span className="text-[#d4d4d4]">{info.gpu_vram_gb.toFixed(0)}GB VRAM</span>
                    </div>
                    {/* VRAM usage not in heartbeat yet — show capacity bar at 0% */}
                    <HardwareBar pct={0} />
                  </div>
                )}

                <div>
                  <div className="flex justify-between mb-0.5">
                    <span className="text-[#6b7280]">RAM</span>
                    <span className="text-[#d4d4d4]">{info?.ram_gb.toFixed(0)}GB total</span>
                  </div>
                  <HardwareBar pct={0} />
                </div>

                <div>
                  <div className="flex justify-between mb-0.5">
                    <span className="text-[#6b7280]">DISK</span>
                    <span className="text-[#d4d4d4]">{info?.disk_free_gb.toFixed(0)}GB free</span>
                  </div>
                  <HardwareBar pct={0} />
                </div>
              </div>

              {/* Job + last seen */}
              <div className="text-[9px] space-y-0.5">
                <div className="text-[#6b7280]">
                  {worker.job ? (
                    <span className="font-mono text-[#f59e0b]">{worker.job.id}</span>
                  ) : (
                    <span>{state === 'dead' ? 'DEAD' : 'IDLE'}</span>
                  )}
                </div>
                <div className="text-[#6b7280]">↻ {relativeTime(worker.last_seen)}</div>
              </div>
            </Link>
          )
        })}
      </div>
    </div>
  )
}
