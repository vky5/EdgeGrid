'use client'

import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import { submitJob, listJobs, LiveJob } from '@/lib/api'

type Status = 'idle' | 'loading' | 'success' | 'error'

function Label({ children }: { children: React.ReactNode }) {
  return <div className="font-mono text-[9px] tracking-widest text-[#6b7280] mb-1.5">{children}</div>
}

function UploadButton({ accept, onLoad }: { accept: string; onLoad: (text: string) => void }) {
  const ref = useRef<HTMLInputElement>(null)
  return (
    <>
      <input
        ref={ref} type="file" accept={accept}
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (!file) return
          const reader = new FileReader()
          reader.onload = (ev) => {
            onLoad(ev.target?.result as string)
            if (ref.current) ref.current.value = ''
          }
          reader.readAsText(file)
        }}
      />
      <button
        type="button"
        onClick={() => ref.current?.click()}
        className="font-mono text-[9px] tracking-widest text-[#f59e0b] border border-[#f59e0b]/50 px-2 py-1 hover:bg-[#f59e0b]/10 transition-colors"
      >
        ↑ FILE
      </button>
    </>
  )
}

function FieldHeader({ label, accept, onLoad }: { label: string; accept: string; onLoad: (t: string) => void }) {
  return (
    <div className="flex items-center justify-between mb-1.5">
      <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">{label}</span>
      <UploadButton accept={accept} onLoad={onLoad} />
    </div>
  )
}

function Select({ value, onChange, options }: {
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string }[]
}) {
  return (
    <select
      value={value} onChange={(e) => onChange(e.target.value)}
      className="bg-black border border-[#1f1f1f] p-2 font-mono text-xs text-[#d4d4d4] focus:outline-none focus:border-[#f59e0b]"
    >
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  )
}

function StatBox({ label, value, color = '#d4d4d4' }: { label: string; value: number; color?: string }) {
  return (
    <div className="border border-[#1f1f1f] p-2">
      <div className="font-mono text-[9px] tracking-widest text-[#6b7280]">{label}</div>
      <div className="font-mono text-lg mt-0.5" style={{ color }}>{value}</div>
    </div>
  )
}

function deriveStats(jobs: LiveJob[]) {
  return {
    total:   jobs.length,
    running: jobs.filter((j) => j.state === 'running').length,
    queued:  jobs.filter((j) => j.state === 'queued' || j.state === 'pending').length,
    failed:  jobs.filter((j) => j.state === 'failed'  || j.state === 'error').length,
  }
}

export function SubmitJobPanel() {
  const [script,       setScript]       = useState('')
  const [requirements, setRequirements] = useState('')
  const [datasetType,  setDatasetType]  = useState('hf')
  const [datasetRef,   setDatasetRef]   = useState('')
  const [requiresGpu,  setRequiresGpu]  = useState(true)
  const [minRam,       setMinRam]       = useState(16)
  const [minVram,      setMinVram]      = useState(8)
  const [minDisk,      setMinDisk]      = useState(0)
  const [status,       setStatus]       = useState<Status>('idle')
  const [submittedID,  setSubmittedID]  = useState<string | null>(null)
  const [errorMsg,     setErrorMsg]     = useState<string | null>(null)
  const [jobs,         setJobs]         = useState<LiveJob[]>([])

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const data = await listJobs()
        if (!cancelled) setJobs(data)
      } catch { /* ignore */ }
    }
    poll()
    const t = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [])

  const stats = deriveStats(jobs)

  const handleDispatch = async () => {
    if (!script.trim()) return
    setStatus('loading')
    setErrorMsg(null)
    try {
      const res = await submitJob({
        training_script:      script,
        requirements:         requirements,
        dataset_type:         datasetType,
        dataset_ref:          datasetRef,
        base_model_type:      '',
        base_model_ref:       '',
        training_config_json: '{}',
        requires_gpu:         requiresGpu,
        min_ram_gb:           minRam,
        min_vram_gb:          requiresGpu ? minVram : 0,
        min_disk_gb:          minDisk,
      })
      setSubmittedID(res.job_id)
      setStatus('success')
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : 'unknown error')
      setStatus('error')
    }
  }

  const reset = () => { setStatus('idle'); setSubmittedID(null); setErrorMsg(null) }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="px-4 h-11 border-b border-[#1f1f1f] flex items-center shrink-0">
        <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">DISPATCH JOB</span>
      </div>

      <div className="flex-1 overflow-y-auto">
        {status === 'success' && submittedID ? (
          <div className="p-4 flex flex-col gap-3">
            <div className="border border-[#22c55e]/40 bg-[#22c55e]/5 p-3">
              <div className="font-mono text-[9px] tracking-widest text-[#22c55e] mb-2">JOB QUEUED</div>
              <div className="font-mono text-xs text-[#f59e0b] break-all">{submittedID}</div>
            </div>
            <Link
              href={`/jobs/${submittedID}`}
              className="block w-full font-mono text-[10px] text-[#d4d4d4] border border-[#1f1f1f] px-3 py-2 text-center hover:border-[#f59e0b] hover:text-[#f59e0b] transition-colors tracking-widest"
            >
              VIEW JOB LOGS →
            </Link>
            <button onClick={reset} className="w-full font-mono text-[10px] text-[#6b7280] border border-[#1f1f1f] px-3 py-2 hover:text-[#d4d4d4] transition-colors tracking-widest">
              DISPATCH ANOTHER
            </button>
          </div>
        ) : (
          <div className="p-4 space-y-5">

            {/* Script */}
            <div>
              <FieldHeader label="TRAINING SCRIPT (.py)" accept=".py,text/x-python" onLoad={setScript} />
              <textarea
                value={script}
                onChange={(e) => setScript(e.target.value)}
                rows={12}
                placeholder={`import time\n\nfor epoch in range(5):\n    print(f"[epoch {epoch+1}/5] loss=...")\n    time.sleep(0.5)`}
                className="w-full bg-black border border-[#1f1f1f] p-2 font-mono text-xs text-[#d4d4d4] focus:outline-none focus:border-[#f59e0b] resize-none placeholder:text-[#3f3f3f]"
              />
            </div>

            {/* Requirements */}
            <div>
              <FieldHeader label="REQUIREMENTS.TXT (optional)" accept=".txt,text/plain" onLoad={setRequirements} />
              <textarea
                value={requirements}
                onChange={(e) => setRequirements(e.target.value)}
                rows={4}
                placeholder={`torch>=2.0\ntransformers\ndatasets`}
                className="w-full bg-black border border-[#1f1f1f] p-2 font-mono text-xs text-[#d4d4d4] focus:outline-none focus:border-[#f59e0b] resize-none placeholder:text-[#3f3f3f]"
              />
            </div>

            {/* Dataset */}
            <div>
              <Label>DATASET (optional)</Label>
              <div className="flex gap-2">
                <Select
                  value={datasetType}
                  onChange={setDatasetType}
                  options={[
                    { value: 'hf',     label: 'HF' },
                    { value: 's3',     label: 'S3' },
                    { value: 'custom', label: 'URL' },
                  ]}
                />
                <input
                  type="text"
                  value={datasetRef}
                  onChange={(e) => setDatasetRef(e.target.value)}
                  placeholder={
                    datasetType === 'hf'     ? 'wikitext/wikitext-2-raw-v1' :
                    datasetType === 's3'     ? 's3://bucket/path' :
                                               'https://...'
                  }
                  className="flex-1 bg-black border border-[#1f1f1f] p-2 font-mono text-xs text-[#d4d4d4] focus:outline-none focus:border-[#f59e0b] placeholder:text-[#3f3f3f]"
                />
              </div>
              <p className="font-mono text-[9px] text-[#3f3f3f] mt-1">
                leave empty if your script generates its own data
              </p>
            </div>

            {/* Resources */}
            <div>
              <Label>RESOURCES</Label>
              <div className="space-y-2.5">
                <div className="flex items-center justify-between">
                  <span className="font-mono text-xs text-[#d4d4d4]">REQUIRES GPU</span>
                  <button
                    onClick={() => setRequiresGpu(!requiresGpu)}
                    className={`relative inline-flex h-5 w-9 transition-colors ${requiresGpu ? 'bg-[#f59e0b]' : 'bg-[#1f1f1f]'}`}
                  >
                    <span className={`inline-block h-5 w-5 bg-[#0c0c0c] transition-transform ${requiresGpu ? 'translate-x-4' : 'translate-x-0'}`} />
                  </button>
                </div>

                {[
                  { label: 'MIN RAM',  value: minRam,  set: setMinRam,  max: 512, show: true },
                  { label: 'MIN VRAM', value: minVram, set: setMinVram, max: 96,  show: requiresGpu },
                  { label: 'MIN DISK', value: minDisk, set: setMinDisk, max: 2000, show: true },
                ].map(({ label, value, set, max, show }) => show && (
                  <div key={label} className="flex items-center justify-between gap-2">
                    <label className="font-mono text-xs text-[#6b7280]">{label}</label>
                    <div className="flex items-center gap-1">
                      <input
                        type="number" value={value}
                        onChange={(e) => set(parseInt(e.target.value) || 0)}
                        min="0" max={max}
                        className="w-14 bg-[#1a1a1a] border border-[#1f1f1f] p-1 font-mono text-xs text-[#d4d4d4] text-right focus:outline-none focus:border-[#f59e0b]"
                      />
                      <span className="font-mono text-xs text-[#6b7280]">GB</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {status === 'error' && errorMsg && (
              <div className="border border-[#ef4444]/40 bg-[#ef4444]/5 p-2">
                <div className="font-mono text-[9px] tracking-widest text-[#ef4444] mb-1">DISPATCH FAILED</div>
                <div className="font-mono text-[10px] text-[#ef4444]">{errorMsg}</div>
              </div>
            )}

            <button
              onClick={handleDispatch}
              disabled={status === 'loading' || !script.trim()}
              className="w-full bg-[#f59e0b] text-black font-mono text-xs font-medium py-2.5 hover:bg-[#fbbf24] transition-colors uppercase tracking-wider disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {status === 'loading' ? 'DISPATCHING...' : 'DISPATCH JOB →'}
            </button>

            {/* Live stats */}
            <div>
              <Label>NETWORK STATS</Label>
              <div className="grid grid-cols-2 gap-2">
                <StatBox label="TOTAL"   value={stats.total} />
                <StatBox label="RUNNING" value={stats.running} color="#f59e0b" />
                <StatBox label="QUEUED"  value={stats.queued}  color="#6b7280" />
                <StatBox label="FAILED"  value={stats.failed}  color={stats.failed > 0 ? '#ef4444' : '#6b7280'} />
              </div>
            </div>

          </div>
        )}
      </div>
    </div>
  )
}
