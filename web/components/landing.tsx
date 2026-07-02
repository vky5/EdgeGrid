'use client'

import { signIn } from 'next-auth/react'

function FeatureCard({
  icon,
  title,
  desc,
}: {
  icon: React.ReactNode
  title: string
  desc: string
}) {
  return (
    <div className="terminal-panel p-5 flex flex-col gap-3">
      <div className="w-8 h-8 flex items-center justify-center border border-[#1f1f1f] text-[#f59e0b]">
        {icon}
      </div>
      <div className="terminal-label text-[#d4d4d4]">{title}</div>
      <p className="text-xs text-[#6b7280] leading-relaxed">{desc}</p>
    </div>
  )
}

const STEPS = [
  { n: '01', text: 'Sign in with GitHub' },
  { n: '02', text: 'Submit a training job or join as a worker node' },
  { n: '03', text: 'The coordinator matches jobs to available hardware over Raft-backed consensus' },
  { n: '04', text: 'Watch logs stream live, pull the checkpoint when training completes' },
]

export function Landing() {
  return (
    <div className="h-full w-full overflow-y-auto bg-[#0c0c0c] text-[#d4d4d4]">
      <div className="max-w-5xl mx-auto px-6 py-20 flex flex-col gap-20">
        {/* Hero */}
        <section className="text-center flex flex-col items-center gap-6">
          <span className="font-mono text-[10px] font-bold text-[#f59e0b] tracking-[0.3em]">
            EDGEGRID
          </span>
          <h1 className="font-mono text-2xl md:text-4xl font-semibold text-[#d4d4d4] max-w-2xl leading-tight text-balance">
            A decentralized grid for training ML models on GPUs people already own
          </h1>
          <p className="font-mono text-sm text-[#6b7280] max-w-xl leading-relaxed">
            Submit training jobs to a pool of volunteer GPU workers, or contribute your own
            idle GPU to the grid. No cloud bill, no cluster to manage.
          </p>
          <button
            onClick={() => signIn('github', { callbackUrl: '/' })}
            className="font-mono text-[10px] tracking-widest text-[#f59e0b] border border-[#f59e0b] px-6 py-3 hover:bg-[#f59e0b]/10 transition-colors"
          >
            CONTINUE WITH GITHUB →
          </button>
        </section>

        {/* Feature grid */}
        <section className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <FeatureCard
            title="SUBMIT JOBS"
            desc="Push a training script, dataset ref, and hardware requirements. The grid places your job on a matching worker automatically."
            icon={
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                <rect x="1" y="3" width="14" height="2" stroke="currentColor" strokeWidth="1.2" />
                <rect x="1" y="7" width="14" height="2" stroke="currentColor" strokeWidth="1.2" />
                <rect x="1" y="11" width="14" height="2" stroke="currentColor" strokeWidth="1.2" />
              </svg>
            }
          />
          <FeatureCard
            title="CONTRIBUTE GPU"
            desc="Join as a worker node with a single command. Approve or reject jobs before they run on your hardware."
            icon={
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                <rect x="1" y="2" width="14" height="9" stroke="currentColor" strokeWidth="1.2" />
                <path d="M5 11v3M11 11v3M3 14h10" stroke="currentColor" strokeWidth="1.2" />
                <circle cx="8" cy="6.5" r="1.5" stroke="currentColor" strokeWidth="1.2" />
              </svg>
            }
          />
          <FeatureCard
            title="LIVE MONITORING"
            desc="Stream logs in real time, track job state, and download checkpoints the moment training finishes."
            icon={
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="1.2" />
                <path d="M8 5v3l2 2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
              </svg>
            }
          />
        </section>

        {/* How it works */}
        <section className="terminal-panel p-6">
          <div className="terminal-label mb-5">HOW IT WORKS</div>
          <ol className="flex flex-col gap-4">
            {STEPS.map((s) => (
              <li key={s.n} className="flex items-start gap-4 font-mono text-xs text-[#d4d4d4]">
                <span className="text-[#f59e0b] shrink-0">{s.n}</span>
                <span className="text-[#a3a3a3]">{s.text}</span>
              </li>
            ))}
          </ol>
        </section>

        <footer className="text-center font-mono text-[9px] text-[#3f3f3f] tracking-widest">
          EDGEGRID — DISTRIBUTED ML TRAINING
        </footer>
      </div>
    </div>
  )
}
