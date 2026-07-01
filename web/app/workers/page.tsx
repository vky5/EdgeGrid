import Link from 'next/link'
import { WORKERS, stateColor, WorkerState } from '@/lib/mock-data'

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

const STATE_LABEL: Record<WorkerState, string> = {
  healthy: 'IDLE',
  busy: 'BUSY',
  dead: 'OFFLINE',
}

export default function WorkersPage() {
  const online = WORKERS.filter((w) => w.state !== 'dead').length
  const busy = WORKERS.filter((w) => w.state === 'busy').length

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-6 shrink-0">
        <span className="terminal-label">WORKERS // {WORKERS.length} REGISTERED</span>
        <span className="font-mono text-[10px] text-[#22c55e]">{online} ONLINE</span>
        <span className="font-mono text-[10px] text-[#f59e0b]">{busy} BUSY</span>
        <span className="font-mono text-[10px] text-[#ef4444]">
          {WORKERS.length - online} OFFLINE
        </span>
      </div>

      {/* Grid */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="grid grid-cols-2 xl:grid-cols-3 gap-4">
          {WORKERS.map((worker) => {
            const color = stateColor(worker.state)
            return (
              <Link
                key={worker.id}
                href={`/workers/${worker.id}`}
                className="border border-[#1f1f1f] hover:border-[#f59e0b]/40 transition-colors group"
                style={{ borderLeftColor: color, borderLeftWidth: 2 }}
              >
                {/* Card header */}
                <div className="px-4 pt-4 pb-3 border-b border-[#1f1f1f] flex items-center justify-between">
                  <span className="font-mono text-sm text-[#d4d4d4] group-hover:text-[#f59e0b] transition-colors">
                    {worker.id}
                  </span>
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-[10px]" style={{ color }}>
                      {STATE_LABEL[worker.state]}
                    </span>
                    <span
                      className={`w-2 h-2 rounded-full ${worker.state === 'busy' ? 'animate-pulse' : ''}`}
                      style={{ backgroundColor: color }}
                    />
                  </div>
                </div>

                {/* Hardware */}
                <div className="px-4 py-3 space-y-3 text-[11px]">
                  {worker.gpu && (
                    <div>
                      <div className="flex justify-between mb-1">
                        <span className="text-[#6b7280]">{worker.gpu}</span>
                        <span className="font-mono text-[#d4d4d4]">
                          {worker.gpuUsed.toFixed(1)} / {worker.gpuTotal} GB VRAM
                        </span>
                      </div>
                      <HardwareBar used={worker.gpuUsed} total={worker.gpuTotal} />
                    </div>
                  )}
                  {!worker.gpu && (
                    <div className="flex items-center gap-2">
                      <span className="terminal-label text-[9px]">CPU ONLY</span>
                    </div>
                  )}

                  <div>
                    <div className="flex justify-between mb-1">
                      <span className="text-[#6b7280]">RAM</span>
                      <span className="font-mono text-[#d4d4d4]">
                        {worker.ramUsed.toFixed(1)} / {worker.ram} GB
                      </span>
                    </div>
                    <HardwareBar used={worker.ramUsed} total={worker.ram} />
                  </div>

                  <div>
                    <div className="flex justify-between mb-1">
                      <span className="text-[#6b7280]">DISK FREE</span>
                      <span className="font-mono text-[#d4d4d4]">
                        {worker.diskFree} / {worker.disk} GB
                      </span>
                    </div>
                    <HardwareBar used={worker.disk - worker.diskFree} total={worker.disk} />
                  </div>
                </div>

                {/* Footer */}
                <div className="px-4 py-2 border-t border-[#1f1f1f] flex items-center justify-between">
                  <span className="font-mono text-[10px] text-[#6b7280]">
                    {worker.currentJob ? (
                      <span className="text-[#f59e0b]">↻ {worker.currentJob}</span>
                    ) : (
                      `↻ ${worker.lastSeen}`
                    )}
                  </span>
                  <span className="font-mono text-[10px] text-[#6b7280]">
                    {worker.jobsCompleted} jobs done
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
