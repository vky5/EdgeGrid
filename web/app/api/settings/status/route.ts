import { NextResponse } from 'next/server'
import { coordFetch, currentUser, userStatus } from '@/lib/coordinator'

interface JoinRequest {
  node_id: string
  role: string
  hostname: string
  github_username?: string
  status: string
  requested_at: string
  updated_at: string
}

// GET /api/settings/status — the signed-in user's own grid status: whether
// they have dashboard/job-submission access, how they got it, and which
// nodes (if any) they've claimed. Fetches the full node list server-side
// (same coordinator call the admin panel uses) but only ever returns the
// caller's own nodes to the browser — never the full list.
export async function GET() {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })

  const status = await userStatus(user.login)

  let myNodes: JoinRequest[] = []
  const res = await coordFetch('/admin/join')
  if (res.ok) {
    const all = (await res.json()) as JoinRequest[] | null
    myNodes = (all ?? []).filter((r) => r.github_username === user.login)
  }

  return NextResponse.json({
    login: user.login,
    admin: user.admin,
    approved: user.admin || status.approved,
    approved_via: status.approved_via,
    approved_at: status.approved_at,
    nodes: myNodes,
  })
}
