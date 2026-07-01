'use client'

import Link from 'next/link'
import { useState } from 'react'
import { submitJob } from '@/lib/api'

type Status = 'idle' | 'loading' | 'success' | 'error'

export function SubmitJobPanel() {
  const [script, setScript] = useState(`import torch
import torch.nn as nn

model = nn.Linear(784, 10)
optimizer = torch.optim.SGD(
  model.parameters(), lr=0.01
)`)
  const [requiresGpu, setRequiresGpu] = useState(true)
  const [minRam, setMinRam] = useState(16)
  const [minVram, setMinVram] = useState(8)
  const [status, setStatus] = useState<Status>('idle')
  const [submittedJobID, setSubmittedJobID] = useState<string | null>(null)
  const [errorMsg, setErrorMsg] = useState<string | null>(null)

  const handleDispatch = async () => {
    if (!script.trim()) return
    setStatus('loading')
    setErrorMsg(null)
    try {
      const res = await submitJob({
        training_script: script,
        requirements: '',
        dataset_type: 'hf',
        dataset_ref: 'mock-dataset',
        base_model_type: 'hf',
        base_model_ref: '',
        training_config_json: '{}',
        requires_gpu: requiresGpu,
        min_ram_gb: minRam,
        min_vram_gb: requiresGpu ? minVram : 0,
        min_disk_gb: 0,
      })
      setSubmittedJobID(res.job_id)
      setStatus('success')
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : 'unknown error')
      setStatus('error')
    }
  }

  const reset = () => {
    setStatus('idle')
    setSubmittedJobID(null)
    setErrorMsg(null)
  }

  return (
    <div className="flex flex-col h-full">
      {/* Submit Form Section */}
      <div className="flex-1 flex flex-col border-b border-[#1f1f1f] overflow-y-auto">
        <div className="p-4 border-b border-[#1f1f1f] sticky top-0 bg-[#0c0c0c]">
          <div className="terminal-label">DISPATCH JOB</div>
        </div>

        {status === 'success' && submittedJobID ? (
          <div className="flex-1 p-4 flex flex-col gap-3">
            <div className="border border-[#22c55e]/40 bg-[#22c55e]/5 p-3">
              <div className="terminal-label text-[9px] text-[#22c55e] mb-2">JOB QUEUED</div>
              <div className="font-mono text-xs text-[#f59e0b]">{submittedJobID}</div>
            </div>
            <Link
              href={`/jobs/${submittedJobID}`}
              className="block w-full font-mono text-[10px] text-[#d4d4d4] border border-[#1f1f1f] px-3 py-2 text-center hover:border-[#f59e0b] hover:text-[#f59e0b] transition-colors tracking-widest"
            >
              VIEW JOB LOGS →
            </Link>
            <button
              onClick={reset}
              className="w-full font-mono text-[10px] text-[#6b7280] border border-[#1f1f1f] px-3 py-2 hover:text-[#d4d4d4] transition-colors tracking-widest"
            >
              DISPATCH ANOTHER
            </button>
          </div>
        ) : (
          <div className="flex-1 p-4 space-y-4 overflow-y-auto">
            {/* Script */}
            <div className="space-y-1.5">
              <label className="terminal-label">SCRIPT</label>
              <textarea
                value={script}
                onChange={(e) => setScript(e.target.value)}
                className="w-full h-32 bg-black border border-[#1f1f1f] rounded-none p-2 font-mono text-xs text-[#d4d4d4] focus:outline-none focus:border-[#f59e0b] focus:ring-0 resize-none"
                placeholder="# Python training script"
              />
            </div>

            {/* Requirements */}
            <div className="space-y-2">
              <label className="terminal-label">REQUIREMENTS</label>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-[#d4d4d4]">REQUIRES GPU</span>
                  <button
                    onClick={() => setRequiresGpu(!requiresGpu)}
                    className={`relative inline-flex h-5 w-9 rounded-none transition-colors ${
                      requiresGpu ? 'bg-[#f59e0b]' : 'bg-[#1f1f1f]'
                    }`}
                  >
                    <span
                      className={`inline-block h-5 w-5 transform rounded-none bg-[#0c0c0c] transition-transform ${
                        requiresGpu ? 'translate-x-4' : 'translate-x-0'
                      }`}
                    />
                  </button>
                </div>

                <div className="flex items-center justify-between gap-2">
                  <label className="text-xs text-[#6b7280]">MIN RAM</label>
                  <div className="flex items-center gap-1">
                    <input
                      type="number"
                      value={minRam}
                      onChange={(e) => setMinRam(parseInt(e.target.value) || 0)}
                      min="4" max="256"
                      className="w-12 bg-[#1a1a1a] border border-[#1f1f1f] rounded-none p-1 font-mono text-xs text-[#d4d4d4] text-right focus:outline-none focus:border-[#f59e0b]"
                    />
                    <span className="text-xs text-[#6b7280]">GB</span>
                  </div>
                </div>

                {requiresGpu && (
                  <div className="flex items-center justify-between gap-2">
                    <label className="text-xs text-[#6b7280]">MIN VRAM</label>
                    <div className="flex items-center gap-1">
                      <input
                        type="number"
                        value={minVram}
                        onChange={(e) => setMinVram(parseInt(e.target.value) || 0)}
                        min="0" max="96"
                        className="w-12 bg-[#1a1a1a] border border-[#1f1f1f] rounded-none p-1 font-mono text-xs text-[#d4d4d4] text-right focus:outline-none focus:border-[#f59e0b]"
                      />
                      <span className="text-xs text-[#6b7280]">GB</span>
                    </div>
                  </div>
                )}
              </div>
            </div>

            {status === 'error' && errorMsg && (
              <div className="border border-[#ef4444]/40 bg-[#ef4444]/5 p-2">
                <div className="terminal-label text-[9px] text-[#ef4444] mb-1">DISPATCH FAILED</div>
                <div className="font-mono text-[10px] text-[#ef4444]">{errorMsg}</div>
              </div>
            )}

            <button
              onClick={handleDispatch}
              disabled={status === 'loading' || !script.trim()}
              className="w-full bg-[#f59e0b] text-black font-mono text-xs font-medium py-2 rounded-none hover:bg-[#fbbf24] transition-colors uppercase tracking-wider mt-4 disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {status === 'loading' ? 'DISPATCHING...' : 'DISPATCH JOB →'}
            </button>
          </div>
        )}
      </div>

      {/* Stats Section */}
      <div className="flex-1 flex flex-col border-t border-[#1f1f1f]">
        <div className="p-4 border-b border-[#1f1f1f]">
          <div className="terminal-label">NETWORK STATS</div>
        </div>

        <div className="flex-1 p-4 space-y-4 overflow-y-auto">
          <div className="grid grid-cols-2 gap-3">
            <div className="border border-[#1f1f1f] p-2">
              <div className="terminal-label text-[9px]">JOBS TODAY</div>
              <div className="font-mono text-lg text-[#f59e0b] mt-1">47</div>
            </div>
            <div className="border border-[#1f1f1f] p-2">
              <div className="terminal-label text-[9px]">RUNNING</div>
              <div className="font-mono text-lg text-[#f59e0b] mt-1">3</div>
            </div>
            <div className="border border-[#1f1f1f] p-2">
              <div className="terminal-label text-[9px]">QUEUED</div>
              <div className="font-mono text-lg text-[#6b7280] mt-1">1</div>
            </div>
            <div className="border border-[#1f1f1f] p-2">
              <div className="terminal-label text-[9px]">FAILED</div>
              <div className="font-mono text-lg text-[#ef4444] mt-1">2</div>
            </div>
          </div>

          <div className="border border-[#1f1f1f] p-2">
            <div className="terminal-label text-[9px]">AVG DURATION</div>
            <div className="font-mono text-lg text-[#d4d4d4] mt-1">14m</div>
          </div>

          <div className="border border-[#1f1f1f] p-3">
            <div className="terminal-label text-[9px] mb-2">JOBS/HOUR (24H)</div>
            <svg viewBox="0 0 120 30" className="w-full h-12">
              <polyline
                points="0,25 5,22 10,20 15,18 20,16 25,14 30,12 35,10 40,12 45,15 50,18 55,16 60,14 65,12 70,15 75,18 80,20 85,22 90,24 95,26 100,24 105,22 110,20 115,18 120,20"
                fill="none" stroke="#f59e0b" strokeWidth="1.2"
              />
            </svg>
          </div>
        </div>
      </div>
    </div>
  )
}
