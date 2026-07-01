import { NextRequest, NextResponse } from 'next/server'
import { getServerSession } from 'next-auth'
import { authOptions } from '@/lib/auth'

const COORD = (process.env.COORDINATOR_URL ?? 'http://localhost:8080').replace(/\/$/, '')

export async function POST(req: NextRequest) {
  const session = await getServerSession(authOptions)
  const login = (session?.user as any)?.login as string | undefined
  if (!login) {
    return NextResponse.json({ error: 'unauthorized' }, { status: 401 })
  }
  const body = await req.text()
  const res = await fetch(`${COORD}/jobs`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Submitted-By': login,
    },
    body,
  })
  return new Response(await res.text(), { status: res.status })
}
