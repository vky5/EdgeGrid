import { NextResponse } from 'next/server'
import { coordFetch, currentUser } from '@/lib/coordinator'

// GET /api/workers — list all workers. All signed-in users may see the fleet.
export async function GET() {
  const user = await currentUser()
  if (!user) return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  const res = await coordFetch('/workers')
  return new Response(await res.text(), {
    status: res.status,
    headers: { 'Content-Type': 'application/json' },
  })
}
