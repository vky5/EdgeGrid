'use client'

import { useEffect, useState } from 'react'
import { useSession } from 'next-auth/react'

interface NodeRecord {
  node_id: string
  role: string
  hostname: string
  status: string
  requested_at: string
  updated_at: string
}

interface SettingsStatus {
  login: string
  admin: boolean
  approved: boolean
  approved_via?: string
  approved_at?: string
  nodes: NodeRecord[]
}

const STATUS_COLOR: Record<string, string> = {
  pending: '#f59e0b',
  approved: '#22c55e',
  rejected: '#ef4444',
}

function relativeTime(iso?: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return new Date(iso).toLocaleString()
}

function describeVia(via?: string): string {
  if (!via) return ''
  if (via === 'admin') return 'granted directly by admin'
  if (via.startsWith('node:')) return `via node ${via.slice(5)}`
  return via
}

export default function SettingsPage() {
  const { data: session } = useSession()
  const login = (session?.user as any)?.login as string | undefined
  const [status, setStatus] = useState<SettingsStatus | null>(null)
  const [error, setError] = useState(false)

  useEffect(() => {
    let cancelled = false
    const poll = async () => {
      try {
        const res = await fetch('/api/settings/status')
        if (!res.ok) throw new Error()
        const data = await res.json()
        if (!cancelled) { setStatus(data); setError(false) }
      } catch {
        if (!cancelled) setError(true)
      }
    }
    poll()
    const t = setInterval(poll, 5_000)
    return () => { cancelled = true; clearInterval(t) }
  }, [])

  const accessColor = status?.approved ? '#22c55e' : '#8b5cf6'
  const accessLabel = status?.admin ? 'ADMIN' : status?.approved ? 'APPROVED' : 'PENDING APPROVAL'

  return (
    <div className="h-full overflow-y-auto bg-[#0c0c0c] text-[#d4d4d4]">
      <div className="max-w-2xl mx-auto px-6 py-10 flex flex-col gap-8">
        {/* Header */}
        <div className="flex items-center gap-3">
          <span className="w-8 h-8 rounded-full bg-[#1f1f1f] border border-[#3f3f3f] flex items-center justify-center font-mono text-xs text-[#d4d4d4]">
            {login ? login[0].toUpperCase() : '?'}
          </span>
          <div>
            <div className="font-mono text-sm text-[#d4d4d4]">@{login ?? '—'}</div>
            <div className="terminal-label text-[9px]">SETTINGS</div>
          </div>
        </div>

        {error && (
          <div className="terminal-panel p-4 text-[#ef4444] text-xs">
            couldn't reach the coordinator — try again shortly
          </div>
        )}

        {/* Grid access status */}
        <section className="terminal-panel p-6 flex flex-col gap-3">
          <div className="terminal-label">GRID ACCESS</div>
          <div className="flex items-center gap-3">
            <span
              className="font-mono text-[10px] px-2 py-1 border tracking-widest"
              style={{ color: accessColor, borderColor: accessColor, backgroundColor: `${accessColor}15` }}
            >
              {accessLabel}
            </span>
            {status?.approved_via && (
              <span className="font-mono text-[10px] text-[#6b7280]">
                {describeVia(status.approved_via)} · {relativeTime(status.approved_at)}
              </span>
            )}
          </div>

          {status && !status.admin && !status.approved && (
            <div className="mt-2 flex flex-col gap-3 text-xs text-[#a3a3a3] leading-relaxed">
              <p>
                You can sign in and see this page, but <span className="text-[#d4d4d4]">job submission is
                gated</span> until an admin grants you grid access. That happens automatically once you
                run a worker node, claim it below, and it gets approved — or an admin can grant access
                directly without a node.
              </p>
              <div className="border-t border-[#1f1f1f] pt-3 flex flex-col gap-1.5">
                <div className="terminal-label text-[9px] mb-1">TO CONTRIBUTE A WORKER</div>
                <code className="bg-black border border-[#1f1f1f] px-3 py-2 text-[10px] text-[#22c55e] block overflow-x-auto">
                  ./edgegrid -client -join &lt;coordinator-url&gt; -dashboard &lt;this-site-url&gt; -executor training -worker-id my-worker
                </code>
                <p className="text-[#6b7280]">
                  On boot it prints a claim link (<code className="text-[#d4d4d4]">/claim/&lt;node-id&gt;</code>) —
                  open it, sign in with GitHub, then wait for an admin to approve it here.
                </p>
              </div>
            </div>
          )}
        </section>

        {/* Claimed nodes */}
        <section className="terminal-panel p-6 flex flex-col gap-3">
          <div className="terminal-label">YOUR NODES</div>
          {!status || status.nodes.length === 0 ? (
            <p className="text-xs text-[#3f3f3f]">
              no node linked to your account yet
            </p>
          ) : (
            <div className="flex flex-col gap-2">
              {status.nodes.map((n) => {
                const color = STATUS_COLOR[n.status] ?? '#6b7280'
                return (
                  <div
                    key={n.node_id}
                    className="flex items-center gap-4 border border-[#1f1f1f] px-3 py-2.5"
                  >
                    <span
                      className="font-mono text-[9px] px-1.5 py-0.5 border shrink-0"
                      style={{ color, borderColor: color, backgroundColor: `${color}15` }}
                    >
                      {n.status.toUpperCase()}
                    </span>
                    <div className="flex-1 min-w-0">
                      <div className="font-mono text-xs text-[#d4d4d4]">
                        {n.role.toUpperCase()} · {n.hostname || '—'}
                      </div>
                      <div className="font-mono text-[10px] text-[#6b7280] break-all">{n.node_id}</div>
                    </div>
                    <span className="font-mono text-[10px] text-[#3f3f3f] shrink-0">
                      {relativeTime(n.updated_at)}
                    </span>
                  </div>
                )
              })}
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
