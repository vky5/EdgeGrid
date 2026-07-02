import { NextResponse } from 'next/server'
import { authorizeJob, coordFetch, currentUser } from '@/lib/coordinator'

// GET /api/jobs/{id} — job status (owner or admin only).
export async function GET(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const { id } = await params
  const authz = await authorizeJob(id, user)
  if (!authz.ok) return NextResponse.json({ error: 'forbidden' }, { status: authz.status })
  return NextResponse.json(authz.job)
}

// DELETE /api/jobs/{id} — cancel a job (owner or admin only).
export async function DELETE(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const { id } = await params
  const authz = await authorizeJob(id, user)
  if (!authz.ok) return NextResponse.json({ error: 'forbidden' }, { status: authz.status })
  const res = await coordFetch(`/jobs/${encodeURIComponent(id)}`, { method: 'DELETE' })
  return new Response(await res.text(), { status: res.status })
}
