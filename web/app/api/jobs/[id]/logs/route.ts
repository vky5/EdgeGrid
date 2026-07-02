import { NextResponse } from 'next/server'
import { authorizeJob, coordFetch, currentUser } from '@/lib/coordinator'

// Never cache; this proxies a long-lived SSE stream.
export const dynamic = 'force-dynamic'

// GET /api/jobs/{id}/logs — proxy the coordinator's SSE log stream (owner or admin).
// EventSource can't send an Authorization header, so the browser hits this
// same-origin route (session cookie), and we attach the backend token upstream.
export async function GET(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const { id } = await params
  const authz = await authorizeJob(id, user)
  if (!authz.ok) return NextResponse.json({ error: 'forbidden' }, { status: authz.status })

  const upstream = await coordFetch(`/jobs/${encodeURIComponent(id)}/logs`, {
    headers: { Accept: 'text/event-stream' },
  })
  if (!upstream.ok || !upstream.body) {
    return NextResponse.json({ error: 'failed to stream logs' }, { status: 502 })
  }
  return new Response(upstream.body, {
    status: 200,
    headers: {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache, no-transform',
      Connection: 'keep-alive',
    },
  })
}
