interface Job {
  id: string
  submitted: string
  worker: string
  state: 'RUNNING' | 'COMPLETED' | 'FAILED' | 'QUEUED'
  duration: string
  logs: string
}

interface JobsPanelProps {
  jobs: Job[]
  expandedJobId: string | null
  onSelectJob: (id: string) => void
}

export function JobsPanel({ jobs, expandedJobId, onSelectJob }: JobsPanelProps) {
  const getStateColor = (state: string) => {
    switch (state) {
      case 'RUNNING':
        return 'text-[#f59e0b]'
      case 'COMPLETED':
        return 'text-[#22c55e]'
      case 'FAILED':
        return 'text-[#ef4444]'
      case 'QUEUED':
        return 'text-[#6b7280]'
      default:
        return 'text-[#d4d4d4]'
    }
  }

  const getStateDot = (state: string) => {
    switch (state) {
      case 'RUNNING':
        return <span className="inline-block w-1.5 h-1.5 bg-[#f59e0b] rounded-full animate-pulse"></span>
      case 'COMPLETED':
        return <span className="inline-block w-1.5 h-1.5 bg-[#22c55e] rounded-full"></span>
      case 'FAILED':
        return <span className="inline-block w-1.5 h-1.5 bg-[#ef4444] rounded-full"></span>
      case 'QUEUED':
        return <span className="inline-block w-1.5 h-1.5 bg-[#6b7280] rounded-full"></span>
      default:
        return null
    }
  }

  const expandedJob = jobs.find((j) => j.id === expandedJobId)

  return (
    <div className="flex flex-col h-full">
      {/* Jobs Table */}
      <div className="flex-1 flex flex-col min-h-0 overflow-hidden">
        <div className="border-b border-[#1f1f1f]">
          {/* Table Header */}
          <div className="flex items-center text-xs terminal-label h-10 px-4 gap-4 border-b border-[#1f1f1f]">
            <div className="w-32">JOB ID</div>
            <div className="w-24">SUBMITTED</div>
            <div className="flex-1">WORKER</div>
            <div className="w-20">STATE</div>
            <div className="w-24">DURATION</div>
          </div>

          {/* Table Rows */}
          <div className="overflow-y-auto">
            {jobs.map((job) => (
              <div
                key={job.id}
                onClick={() => onSelectJob(job.id)}
                className={`flex items-center text-xs h-9 px-4 gap-4 cursor-pointer border-b border-[#1f1f1f] hover:bg-[#1a1a1a] transition-colors ${
                  expandedJobId === job.id ? 'bg-[#1a1a1a]' : ''
                }`}
              >
                <div className="w-32 font-mono text-[#f59e0b]">{job.id}</div>
                <div className="w-24 text-[#6b7280]">{job.submitted}</div>
                <div className="flex-1 text-[#d4d4d4]">{job.worker}</div>
                <div className={`w-20 flex items-center gap-1.5 ${getStateColor(job.state)}`}>
                  {getStateDot(job.state)}
                  <span className="font-mono text-xs">{job.state}</span>
                </div>
                <div className="w-24 text-[#6b7280]">{job.duration}</div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Log Viewer */}
      {expandedJob && (
        <div className="border-t border-[#1f1f1f] flex flex-col h-96 min-h-0">
          {/* Log Header */}
          <div className="flex items-center justify-between h-10 px-4 bg-black border-b border-[#1f1f1f] gap-2">
            <div className="terminal-label flex items-center gap-2">
              LOGS // {expandedJob.id} // {expandedJob.worker}
              <span
                className={`inline-block w-1.5 h-1.5 rounded-full ${
                  expandedJob.state === 'RUNNING'
                    ? 'bg-[#f59e0b] animate-pulse'
                    : expandedJob.state === 'COMPLETED'
                      ? 'bg-[#22c55e]'
                      : expandedJob.state === 'FAILED'
                        ? 'bg-[#ef4444]'
                        : 'bg-[#6b7280]'
                }`}
              ></span>
              <span className="text-[#6b7280]">{expandedJob.state}</span>
            </div>
          </div>

          {/* Log Content */}
          <div className="flex-1 overflow-y-auto bg-black font-mono text-[11px] leading-relaxed p-3 space-y-0">
            {expandedJob.logs.split('\n').map((line, idx) => {
              let lineColor = 'text-[#d4d4d4]'
              let highlightColor = ''

              if (line.includes('Epoch')) {
                highlightColor = 'bg-[#f59e0b]/10'
              } else if (line.toLowerCase().includes('error') || line.toLowerCase().includes('failed')) {
                lineColor = 'text-[#ef4444]'
              }

              // Extract timestamp
              const timestampMatch = line.match(/^\[(\d{2}:\d{2}:\d{2})\]/)
              const timestamp = timestampMatch ? timestampMatch[1] : null
              const messageStart = timestampMatch ? line.indexOf(']') + 1 : 0

              return (
                <div key={idx} className={`whitespace-pre-wrap break-words ${highlightColor}`}>
                  {timestamp && <span className="text-[#6b7280]">[{timestamp}]</span>}
                  <span className={lineColor}>{line.substring(messageStart || 0)}</span>
                </div>
              )
            })}
            {expandedJob.state === 'RUNNING' && (
              <div className="text-[#f59e0b] animate-pulse">▌</div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
