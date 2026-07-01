import { NextResponse } from 'next/server'
import { getServerSession } from 'next-auth'
import { authOptions, isAdmin } from '@/lib/auth'

const COORD = (process.env.COORDINATOR_URL ?? 'http://localhost:8080').replace(/\/$/, '')
const ADMIN_TOKEN = process.env.COORDINATOR_ADMIN_TOKEN ?? ''

export async function GET() {
  const session = await getServerSession(authOptions)
  if (!isAdmin((session?.user as any)?.login)) {
    return NextResponse.json({ error: 'forbidden' }, { status: 403 })
  }
  const res = await fetch(`${COORD}/admin/join`, {
    headers: { Authorization: `Bearer ${ADMIN_TOKEN}` },
    cache: 'no-store',
  })
  const text = await res.text()
  if (!res.ok) return new Response(text, { status: res.status })
  return NextResponse.json(JSON.parse(text), { status: res.status })
}
