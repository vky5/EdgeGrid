import { NextResponse } from 'next/server'
import { getServerSession } from 'next-auth'
import { authOptions, isAdmin } from '@/lib/auth'

const COORD = (process.env.COORDINATOR_URL ?? 'http://localhost:8080').replace(/\/$/, '')
const ADMIN_TOKEN = process.env.COORDINATOR_ADMIN_TOKEN ?? ''

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ nodeID: string; action: string }> }
) {
  const session = await getServerSession(authOptions)
  if (!isAdmin((session?.user as any)?.login)) {
    return NextResponse.json({ error: 'forbidden' }, { status: 403 })
  }
  const { nodeID, action } = await params
  if (action !== 'approve' && action !== 'reject') {
    return NextResponse.json({ error: 'invalid action' }, { status: 400 })
  }
  const res = await fetch(`${COORD}/admin/join/${nodeID}/${action}`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${ADMIN_TOKEN}` },
  })
  return new Response(await res.text(), { status: res.status })
}
