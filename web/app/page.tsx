'use client'

import { useState } from 'react'
import { WorkerNodes } from '@/components/worker-nodes'
import { JobsPanel } from '@/components/jobs-panel'
import { SubmitJobPanel } from '@/components/submit-job-panel'

export default function Page() {
  const [expandedJobId, setExpandedJobId] = useState<string | null>('job-a1b2c3d4')
  const [jobs, setJobs] = useState([
    {
      id: 'job-a1b2c3d4',
      submitted: '2 min ago',
      worker: 'worker-gpu-01',
      state: 'RUNNING',
      duration: '1m 23s',
      logs: `[14:32:15] Initializing distributed training session
[14:32:16] Connecting to parameter server...
[14:32:17] Connected. Sync point 1/10.
[14:32:18] Model loaded: 2.4GB
[14:32:19] Gradient accumulation steps: 4
[14:32:20] Starting epoch 1/20...
[14:32:45] Epoch 1: loss = 2.341 | val_accuracy = 0.764
[14:33:10] Epoch 2: loss = 1.892 | val_accuracy = 0.821
[14:33:35] Epoch 3: loss = 1.564 | val_accuracy = 0.856
[14:34:00] Computing batch norm statistics...
[14:34:05] Batch norm complete. Ready for next epoch.`,
    },
    {
      id: 'job-x7y8z9w0',
      submitted: '8 min ago',
      worker: 'worker-gpu-02',
      state: 'COMPLETED',
      duration: '5m 42s',
      logs: 'Job completed successfully.',
    },
    {
      id: 'job-m1n2o3p4',
      submitted: '15 min ago',
      worker: 'worker-gpu-03',
      state: 'FAILED',
      duration: '3m 21s',
      logs: '[14:28:00] Error: VRAM exhausted on device 0\n[14:28:01] Attempting recovery...\n[14:28:02] Recovery failed. Job terminated.',
    },
    {
      id: 'job-q5r6s7t8',
      submitted: '25 min ago',
      worker: 'worker-cpu-01',
      state: 'QUEUED',
      duration: '—',
      logs: 'Waiting for worker availability.',
    },
  ])

  return (
    <div className="h-screen w-screen bg-[#0c0c0c] text-[#d4d4d4] flex overflow-hidden">
      {/* LEFT COLUMN: Worker Nodes Panel */}
      <div className="w-60 border-r border-[#1f1f1f] flex flex-col">
        <WorkerNodes />
      </div>

      {/* CENTER COLUMN: Jobs + Log Viewer */}
      <div className="flex-1 flex flex-col border-r border-[#1f1f1f]">
        <JobsPanel jobs={jobs} expandedJobId={expandedJobId} onSelectJob={setExpandedJobId} />
      </div>

      {/* RIGHT COLUMN: Submit Job + Stats */}
      <div className="w-72 flex flex-col border-l border-[#1f1f1f]">
        <SubmitJobPanel />
      </div>
    </div>
  )
}
