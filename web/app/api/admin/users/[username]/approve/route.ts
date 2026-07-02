import { NextResponse } from 'next/server'
import { coordFetch, currentUser } from '@/lib/coordinator'

// POST /api/admin/users/{username}/approve — grant grid access directly, no
// node required (admin only).
export async function POST(
  _req: Request,
  { params }: { params: Promise<{ username: string }> }
) {
  const user = await currentUser()
  if (!user?.admin) {
    return NextResponse.json({ error: 'forbidden' }, { status: 403 })
  }
  const { username } = await params
  const res = await coordFetch(`/admin/users/${encodeURIComponent(username)}/approve`, {
    method: 'POST',
  })
  return new Response(await res.text(), { status: res.status })
}
