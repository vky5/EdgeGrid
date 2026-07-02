import { NextResponse } from 'next/server'
import { authorizeJob, coordFetch, currentUser } from '@/lib/coordinator'

export const dynamic = 'force-dynamic'

// GET /api/jobs/{id}/artifact — proxy the checkpoint download (owner or admin).
export async function GET(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const { id } = await params
  const authz = await authorizeJob(id, user)
  if (!authz.ok) return NextResponse.json({ error: 'forbidden' }, { status: authz.status })

  const upstream = await coordFetch(`/jobs/${encodeURIComponent(id)}/artifact`)
  if (!upstream.ok || !upstream.body) {
    return NextResponse.json({ error: 'artifact not found' }, { status: upstream.status || 404 })
  }
  return new Response(upstream.body, {
    status: 200,
    headers: {
      'Content-Type': 'application/octet-stream',
      'Content-Disposition': `attachment; filename="checkpoint-${id}.tar"`,
    },
  })
}
