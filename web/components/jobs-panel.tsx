import Link from 'next/link'

interface Job {
  id: string
  submitted: string
  worker: string
  state: 'RUNNING' | 'COMPLETED' | 'FAILED' | 'QUEUED' | 'PENDING_REVIEW' | 'CANCELLED'
  duration: string
  logs: string
}

interface JobsPanelProps {
  jobs: Job[]
  expandedJobId: string | null
  onSelectJob: (id: string) => void
}

export function JobsPanel({ jobs, expandedJobId, onSelectJob }: JobsPanelProps) {
  const STATE_COLOR: Record<string, string> = {
    RUNNING:        '#f59e0b',
    PENDING_REVIEW: '#8b5cf6',
    COMPLETED:      '#22c55e',
    FAILED:         '#ef4444',
    CANCELLED:      '#ef4444',
    QUEUED:         '#6b7280',
  }

  const getStateColor = (state: string): string => STATE_COLOR[state] ?? '#6b7280'

  const getStateDot = (state: string) => {
    const c = STATE_COLOR[state] ?? '#6b7280'
    const pulse = state === 'RUNNING' || state === 'PENDING_REVIEW'
    return (
      <span
        className={`inline-block w-1.5 h-1.5 rounded-full ${pulse ? 'animate-pulse' : ''}`}
        style={{ backgroundColor: c }}
      />
    )
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
                <div className="w-20 flex items-center gap-1.5" style={{ color: getStateColor(job.state) }}>
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
                  expandedJob.state === 'RUNNING' || expandedJob.state === 'PENDING_REVIEW'
                    ? 'animate-pulse'
                    : ''
                }`}
                style={{ backgroundColor: getStateColor(expandedJob.state) }}
              />
              <span style={{ color: getStateColor(expandedJob.state) }}>{expandedJob.state}</span>
            </div>
          </div>

          {/* Log Content — full logs on detail page */}
          <div className="flex-1 overflow-y-auto bg-black font-mono text-[11px] leading-relaxed p-3 flex flex-col gap-2">
            <div className="text-[#3f3f3f]">
              {expandedJob.state === 'QUEUED' && 'waiting for a free worker…'}
              {expandedJob.state === 'PENDING_REVIEW' && 'awaiting worker approval…'}
              {expandedJob.state === 'RUNNING' && <span className="text-[#f59e0b] animate-pulse">▌ running</span>}
              {expandedJob.state === 'COMPLETED' && <span className="text-[#22c55e]">completed</span>}
              {expandedJob.state === 'FAILED' && <span className="text-[#ef4444]">failed</span>}
              {expandedJob.state === 'CANCELLED' && 'cancelled'}
            </div>
            <Link
              href={`/jobs/${expandedJob.id}`}
              className="font-mono text-[9px] text-[#6b7280] border border-[#1f1f1f] px-3 py-2 text-center hover:border-[#f59e0b] hover:text-[#f59e0b] transition-colors tracking-widest w-fit"
            >
              VIEW FULL LOGS →
            </Link>
          </div>
        </div>
      )}
    </div>
  )
}
