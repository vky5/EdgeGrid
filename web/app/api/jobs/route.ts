import { NextRequest, NextResponse } from 'next/server'
import { coordFetch, currentUser, isApprovedUser } from '@/lib/coordinator'

// GET /api/jobs — list jobs. Admins see everything; other users see only their
// own (the coordinator filters by the ?user= param we attach here).
export async function GET() {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const path = user.admin ? '/jobs' : `/jobs?user=${encodeURIComponent(user.login)}`
  const res = await coordFetch(path)
  return new Response(await res.text(), {
    status: res.status,
    headers: { 'Content-Type': 'application/json' },
  })
}

// POST /api/jobs — submit a training job. X-Submitted-By is set from the session
// so ownership can't be spoofed by the caller. Requires grid access — admins
// always have it; everyone else needs to have contributed (or been granted)
// an approved node. See docs/security/known-gaps.md history for why this
// wasn't gated before.
export async function POST(req: NextRequest) {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  if (!(await isApprovedUser(user))) {
    return NextResponse.json({ error: 'grid access pending admin approval' }, { status: 403 })
  }
  const body = await req.text()
  const res = await coordFetch('/jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Submitted-By': user.login },
    body,
  })
  return new Response(await res.text(), { status: res.status })
}
