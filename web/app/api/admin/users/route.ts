import { NextResponse } from 'next/server'
import { coordFetch, currentUser } from '@/lib/coordinator'

// GET /api/admin/users — list everyone with grid access (admin only).
export async function GET() {
  const user = await currentUser()
  if (!user?.admin) {
    return NextResponse.json({ error: 'forbidden' }, { status: 403 })
  }
  const res = await coordFetch('/admin/users')
  const text = await res.text()
  if (!res.ok) return new Response(text, { status: res.status })
  return NextResponse.json(JSON.parse(text), { status: res.status })
}
