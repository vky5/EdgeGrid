export function WorkerNodes() {
  const workers = [
    {
      id: 'worker-gpu-01',
      state: 'busy' as const,
      gpu: 'RTX 3080',
      gpuUsage: 0.85,
      gpuTotal: 10,
      ram: 32,
      ramUsage: 0.65,
      disk: 120,
      diskUsage: 0.25,
      currentJob: 'job-a1b2c3d4',
      lastSeen: '2s ago',
    },
    {
      id: 'worker-gpu-02',
      state: 'healthy' as const,
      gpu: 'RTX 4090',
      gpuUsage: 0,
      gpuTotal: 24,
      ram: 64,
      ramUsage: 0.12,
      disk: 500,
      diskUsage: 0.08,
      currentJob: null,
      lastSeen: '1s ago',
    },
    {
      id: 'worker-gpu-03',
      state: 'dead' as const,
      gpu: 'RTX 2080 Ti',
      gpuUsage: 0,
      gpuTotal: 11,
      ram: 32,
      ramUsage: 0,
      disk: 256,
      diskUsage: 0,
      currentJob: null,
      lastSeen: '45m ago',
    },
    {
      id: 'worker-cpu-01',
      state: 'healthy' as const,
      gpu: 'CPU only',
      gpuUsage: 0,
      gpuTotal: 0,
      ram: 128,
      ramUsage: 0.34,
      disk: 1000,
      diskUsage: 0.42,
      currentJob: null,
      lastSeen: '3s ago',
    },
  ]

  const healthyCount = workers.filter((w) => w.state !== 'dead').length

  const getStatusColor = (state: string) => {
    switch (state) {
      case 'healthy':
        return 'bg-[#22c55e]'
      case 'busy':
        return 'bg-[#f59e0b]'
      case 'dead':
        return 'bg-[#ef4444]'
      default:
        return 'bg-gray-500'
    }
  }

  const getAccentColor = (state: string) => {
    switch (state) {
      case 'healthy':
        return 'border-l-[#22c55e]'
      case 'busy':
        return 'border-l-[#f59e0b]'
      case 'dead':
        return 'border-l-[#ef4444]'
      default:
        return 'border-l-gray-500'
    }
  }

  const renderBar = (used: number, total: number) => {
    const percent = total > 0 ? (used / total) * 100 : 0
    return (
      <div className="h-1 bg-[#1f1f1f] rounded-none overflow-hidden">
        <div className="h-full bg-[#f59e0b]" style={{ width: `${percent}%` }}></div>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Header */}
      <div className="p-4 border-b border-[#1f1f1f]">
        <div className="terminal-label">NODES // {healthyCount} ONLINE</div>
      </div>

      {/* Nodes List */}
      <div className="flex-1 overflow-y-auto">
        {workers.map((worker) => (
          <div
            key={worker.id}
            className={`border-l-2 border-b border-[#1f1f1f] p-3 ${getAccentColor(worker.state)}`}
          >
            {/* ID and Status */}
            <div className="flex items-center justify-between mb-2">
              <span className="font-mono text-xs text-[#d4d4d4]">{worker.id}</span>
              <div className={`w-2 h-2 rounded-full ${getStatusColor(worker.state)}`}></div>
            </div>

            {/* Hardware bars */}
            <div className="space-y-1.5 text-[10px] mb-2">
              {worker.gpu !== 'CPU only' && (
                <div>
                  <div className="flex justify-between mb-0.5">
                    <span className="text-[#6b7280]">GPU {worker.gpu}</span>
                    <span className="text-[#d4d4d4]">{worker.gpuUsage.toFixed(1)}GB VRAM</span>
                  </div>
                  {renderBar(worker.gpuUsage, worker.gpuTotal)}
                </div>
              )}

              <div>
                <div className="flex justify-between mb-0.5">
                  <span className="text-[#6b7280]">RAM</span>
                  <span className="text-[#d4d4d4]">{(worker.ram * worker.ramUsage).toFixed(1)}GB</span>
                </div>
                {renderBar(worker.ram * worker.ramUsage, worker.ram)}
              </div>

              <div>
                <div className="flex justify-between mb-0.5">
                  <span className="text-[#6b7280]">DISK</span>
                  <span className="text-[#d4d4d4]">
                    {(worker.disk * (1 - worker.diskUsage)).toFixed(0)}GB free
                  </span>
                </div>
                {renderBar(worker.disk * worker.diskUsage, worker.disk)}
              </div>
            </div>

            {/* Job status and last seen */}
            <div className="text-[9px] space-y-0.5">
              <div className="text-[#6b7280]">
                {worker.currentJob ? (
                  <span className="font-mono text-[#f59e0b]">{worker.currentJob}</span>
                ) : (
                  <span>IDLE</span>
                )}
              </div>
              <div className="text-[#6b7280]">↻ {worker.lastSeen}</div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
