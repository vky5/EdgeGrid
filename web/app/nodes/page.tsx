'use client'

import { useEffect, useState } from 'react'
import { useSession } from 'next-auth/react'
import { isAdmin } from '@/lib/auth'

interface JoinRequest {
  node_id: string
  role: string
  hostname: string
  github_username?: string
  status: string
  requested_at: string
  updated_at: string
}

interface ApprovedUser {
  github_username: string
  approved_at: string
  approved_via: string
}

const STATUS_COLOR: Record<string, string> = {
  pending: '#f59e0b',
  approved: '#22c55e',
  rejected: '#ef4444',
}

function describeVia(via: string): string {
  if (via === 'admin') return 'direct grant'
  if (via.startsWith('node:')) return `via node ${via.slice(5, 13)}…`
  return via
}

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return new Date(iso).toLocaleString()
}

export default function NodesPage() {
  const { data: session } = useSession()
  const login = (session?.user as any)?.login as string | undefined
  const admin = isAdmin(login)

  const [requests, setRequests] = useState<JoinRequest[]>([])
  const [users, setUsers] = useState<ApprovedUser[]>([])
  const [connected, setConnected] = useState<boolean | null>(null)
  const [acting, setActing] = useState<string | null>(null)
  const [grantInput, setGrantInput] = useState('')
  const [granting, setGranting] = useState(false)

  const poll = async () => {
    try {
      const [reqRes, usersRes] = await Promise.all([
        fetch('/api/admin/join'),
        fetch('/api/admin/users'),
      ])
      if (!reqRes.ok) throw new Error()
      setRequests((await reqRes.json()) ?? [])
      setUsers(usersRes.ok ? (await usersRes.json()) ?? [] : [])
      setConnected(true)
    } catch {
      setConnected(false)
    }
  }

  useEffect(() => {
    if (!admin) return
    poll()
    const t = setInterval(poll, 5_000)
    return () => clearInterval(t)
  }, [admin])

  const act = async (nodeID: string, action: 'approve' | 'reject') => {
    setActing(nodeID + action)
    try {
      await fetch(`/api/admin/join/${nodeID}/${action}`, { method: 'POST' })
      await poll()
    } catch (e) {
      console.error(e)
    } finally {
      setActing(null)
    }
  }

  const grantAccess = async () => {
    const username = grantInput.trim().replace(/^@/, '')
    if (!username) return
    setGranting(true)
    try {
      await fetch(`/api/admin/users/${encodeURIComponent(username)}/approve`, { method: 'POST' })
      setGrantInput('')
      await poll()
    } catch (e) {
      console.error(e)
    } finally {
      setGranting(false)
    }
  }

  if (!admin) {
    return (
      <div className="h-full flex items-center justify-center bg-[#0c0c0c]">
        <p className="font-mono text-xs text-[#6b7280]">admin access required</p>
      </div>
    )
  }

  const pending = requests.filter((r) => r.status === 'pending')
  const resolved = requests.filter((r) => r.status !== 'pending')

  return (
    <div className="h-full flex flex-col bg-[#0c0c0c] text-[#d4d4d4]">
      {/* Header */}
      <div className="border-b border-[#1f1f1f] px-6 h-11 flex items-center gap-4 shrink-0">
        <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">NODE ACCESS</span>
        {connected === false && (
          <span className="font-mono text-[9px] text-[#ef4444]">COORDINATOR OFFLINE</span>
        )}
        {connected === true && (
          <span className="w-1.5 h-1.5 rounded-full bg-[#22c55e] animate-pulse inline-block" />
        )}
        <span className="font-mono text-[10px] text-[#6b7280] ml-auto">{pending.length} pending</span>
      </div>

      <div className="flex-1 overflow-y-auto">
        {/* Pending requests */}
        {pending.length > 0 && (
          <div>
            <div className="px-6 py-3 border-b border-[#1f1f1f]">
              <span className="font-mono text-[9px] tracking-widest text-[#f59e0b]">PENDING APPROVAL</span>
            </div>
            {pending.map((req) => (
              <div
                key={req.node_id}
                className="px-6 py-4 border-b border-[#1f1f1f] flex items-center gap-6"
              >
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-1">
                    <span
                      className="font-mono text-[9px] px-1.5 py-0.5 border"
                      style={{ color: '#f59e0b', borderColor: '#f59e0b', backgroundColor: '#f59e0b15' }}
                    >
                      {req.role.toUpperCase()}
                    </span>
                    <span className="font-mono text-xs text-[#d4d4d4]">{req.hostname || '—'}</span>
                    {req.github_username && (
                      <span className="font-mono text-[10px] text-[#6b7280]">@{req.github_username}</span>
                    )}
                  </div>
                  <div className="font-mono text-[10px] text-[#6b7280] break-all">{req.node_id}</div>
                  <div className="font-mono text-[10px] text-[#3f3f3f] mt-0.5">
                    requested {relativeTime(req.requested_at)}
                  </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <button
                    disabled={acting !== null}
                    onClick={() => act(req.node_id, 'approve')}
                    className="font-mono text-[10px] text-[#22c55e] border border-[#22c55e] px-3 py-1.5 hover:bg-[#22c55e]/10 transition-colors tracking-widest disabled:opacity-40"
                  >
                    APPROVE →
                  </button>
                  <button
                    disabled={acting !== null}
                    onClick={() => act(req.node_id, 'reject')}
                    className="font-mono text-[10px] text-[#ef4444] border border-[#ef4444] px-3 py-1.5 hover:bg-[#ef4444]/10 transition-colors tracking-widest disabled:opacity-40"
                  >
                    REJECT
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Resolved */}
        {resolved.length > 0 && (
          <div>
            <div className="px-6 py-3 border-b border-[#1f1f1f]">
              <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">RESOLVED</span>
            </div>
            {resolved.map((req) => {
              const color = STATUS_COLOR[req.status] ?? '#6b7280'
              return (
                <div
                  key={req.node_id}
                  className="px-6 py-3 border-b border-[#1f1f1f] flex items-center gap-4"
                >
                  <span
                    className="font-mono text-[9px] px-1.5 py-0.5 border shrink-0"
                    style={{ color, borderColor: color, backgroundColor: `${color}15` }}
                  >
                    {req.role.toUpperCase()}
                  </span>
                  <span className="font-mono text-[10px] text-[#6b7280] truncate flex-1">
                    {req.github_username ? `@${req.github_username}` : (req.hostname || req.node_id)}
                  </span>
                  <span className="font-mono text-[10px] shrink-0" style={{ color }}>
                    {req.status.toUpperCase()}
                  </span>
                  <span className="font-mono text-[10px] text-[#3f3f3f] shrink-0">
                    {relativeTime(req.updated_at)}
                  </span>
                </div>
              )
            })}
          </div>
        )}

        {connected !== null && requests.length === 0 && (
          <div className="px-6 py-8 text-[#3f3f3f] font-mono text-xs text-center">
            no join requests yet
          </div>
        )}

        {/* Grid access — job-submission grants, separate from node approval */}
        <div>
          <div className="px-6 py-3 border-b border-t border-[#1f1f1f] flex items-center gap-3">
            <span className="font-mono text-[9px] tracking-widest text-[#6b7280]">GRID ACCESS</span>
            <span className="font-mono text-[9px] text-[#3f3f3f]">who can submit jobs</span>
          </div>

          <div className="px-6 py-4 border-b border-[#1f1f1f] flex items-center gap-2">
            <input
              value={grantInput}
              onChange={(e) => setGrantInput(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && grantAccess()}
              placeholder="github username"
              className="flex-1 bg-black border border-[#1f1f1f] px-3 py-1.5 font-mono text-xs text-[#d4d4d4] focus:outline-none focus:border-[#6b7280]"
            />
            <button
              disabled={granting || !grantInput.trim()}
              onClick={grantAccess}
              className="font-mono text-[10px] text-[#22c55e] border border-[#22c55e] px-3 py-1.5 hover:bg-[#22c55e]/10 transition-colors tracking-widest disabled:opacity-40"
            >
              GRANT DIRECTLY →
            </button>
          </div>

          {users.length === 0 ? (
            <div className="px-6 py-6 text-[#3f3f3f] font-mono text-xs text-center">
              no one has grid access yet
            </div>
          ) : (
            users.map((u) => (
              <div
                key={u.github_username}
                className="px-6 py-3 border-b border-[#1f1f1f] flex items-center gap-4"
              >
                <span className="font-mono text-xs text-[#d4d4d4] flex-1">@{u.github_username}</span>
                <span className="font-mono text-[10px] text-[#6b7280]">{describeVia(u.approved_via)}</span>
                <span className="font-mono text-[10px] text-[#3f3f3f] shrink-0">
                  {relativeTime(u.approved_at)}
                </span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
