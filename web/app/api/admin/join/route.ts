import { NextResponse } from 'next/server'
import { coordFetch, currentUser } from '@/lib/coordinator'

export async function GET() {
  const user = await currentUser()
  if (!user?.admin) {
    return NextResponse.json({ error: 'forbidden' }, { status: 403 })
  }
  const res = await coordFetch('/admin/join')
  const text = await res.text()
  if (!res.ok) return new Response(text, { status: res.status })
  return NextResponse.json(JSON.parse(text), { status: res.status })
}
