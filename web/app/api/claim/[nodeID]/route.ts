import { NextResponse } from 'next/server'
import { coordFetch, currentUser } from '@/lib/coordinator'

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ nodeID: string }> }
) {
  const user = await currentUser()
  if (!user) {
    return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  }
  const { nodeID } = await params
  const res = await coordFetch(`/join/claim/${encodeURIComponent(nodeID)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ github_username: user.login }),
  })
  return new Response(await res.text(), { status: res.status })
}
