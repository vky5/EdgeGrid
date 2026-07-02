import { NextResponse } from 'next/server'
import { coordFetch, currentUser } from '@/lib/coordinator'

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ nodeID: string; action: string }> }
) {
  const user = await currentUser()
  if (!user?.admin) {
    return NextResponse.json({ error: 'forbidden' }, { status: 403 })
  }
  const { nodeID, action } = await params
  if (action !== 'approve' && action !== 'reject') {
    return NextResponse.json({ error: 'invalid action' }, { status: 400 })
  }
  const res = await coordFetch(`/admin/join/${encodeURIComponent(nodeID)}/${action}`, {
    method: 'POST',
  })
  return new Response(await res.text(), { status: res.status })
}
