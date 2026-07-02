import { NextResponse } from 'next/server'
import { authorizeJob, coordFetch, currentUser } from '@/lib/coordinator'

// POST /api/jobs/{id}/approve — approve a job pending worker review (owner or admin).
export async function POST(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const { id } = await params
  const authz = await authorizeJob(id, user)
  if (!authz.ok) return NextResponse.json({ error: 'forbidden' }, { status: authz.status })
  const res = await coordFetch(`/jobs/${encodeURIComponent(id)}/approve`, { method: 'POST' })
  return new Response(await res.text(), { status: res.status })
}
