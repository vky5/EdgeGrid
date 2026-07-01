import { NextResponse } from 'next/server'
import { getServerSession } from 'next-auth'
import { authOptions } from '@/lib/auth'

const COORD = (process.env.COORDINATOR_URL ?? 'http://localhost:8080').replace(/\/$/, '')

export async function POST(
  _req: Request,
  { params }: { params: Promise<{ nodeID: string }> }
) {
  const session = await getServerSession(authOptions)
  const login = (session?.user as any)?.login as string | undefined
  if (!login) {
    return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  }
  const { nodeID } = await params
  const res = await fetch(`${COORD}/join/claim/${nodeID}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ github_username: login }),
  })
  return new Response(await res.text(), { status: res.status })
}
