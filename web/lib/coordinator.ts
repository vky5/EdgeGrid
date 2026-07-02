// Server-only helpers for talking to the coordinator. These are imported only
// from route handlers (web/app/api/**), which never ship to the browser, so the
// backend token never reaches the client.
import { getServerSession } from 'next-auth'
import { authOptions, isAdmin } from './auth'

const COORD = (process.env.COORDINATOR_URL ?? 'http://localhost:8080').replace(/\/$/, '')
const TOKEN = process.env.COORDINATOR_ADMIN_TOKEN ?? ''

// coordFetch forwards a request to the coordinator with the shared backend
// token. This token authenticates the Next.js backend to the coordinator; the
// coordinator rejects any request without it (except node bootstrap endpoints).
export function coordFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers)
  if (TOKEN) headers.set('Authorization', `Bearer ${TOKEN}`)
  return fetch(`${COORD}${path}`, { ...init, headers, cache: 'no-store' })
}

export interface CurrentUser {
  login: string
  admin: boolean
}

// currentUser returns the authenticated GitHub user, or null if not signed in.
export async function currentUser(): Promise<CurrentUser | null> {
  const session = await getServerSession(authOptions)
  const login = (session?.user as { login?: string } | undefined)?.login
  if (!login) return null
  return { login, admin: isAdmin(login) }
}

export type JobAuthz =
  | { ok: true; job: Record<string, unknown> }
  | { ok: false; status: number }

// authorizeJob loads a job and verifies the user may access it: admins may
// access any job, other users only their own. Jobs with no recorded owner
// (submitted before ownership tracking) are treated as accessible to any
// signed-in user.
export async function authorizeJob(id: string, user: CurrentUser): Promise<JobAuthz> {
  const res = await coordFetch(`/jobs/${encodeURIComponent(id)}`)
  if (res.status === 404) return { ok: false, status: 404 }
  if (!res.ok) return { ok: false, status: 502 }
  const job = (await res.json()) as Record<string, unknown>
  const owner = job.submitted_by as string | undefined
  if (!user.admin && owner && owner !== user.login) {
    return { ok: false, status: 403 }
  }
  return { ok: true, job }
}

export interface UserStatus {
  github_username: string
  approved: boolean
  approved_via?: string // "node:<nodeID>" | "admin"
  approved_at?: string
}

// userStatus asks the coordinator whether `username` has been granted
// dashboard access (job submission) — separate from whether any of their
// nodes are approved to join NATS.
export async function userStatus(username: string): Promise<UserStatus> {
  const res = await coordFetch(`/users/${encodeURIComponent(username)}/status`)
  if (!res.ok) return { github_username: username, approved: false }
  return res.json()
}

// isApprovedUser is the gate used before letting a user submit a job:
// admins always pass; everyone else needs a grid-access grant.
export async function isApprovedUser(user: CurrentUser): Promise<boolean> {
  if (user.admin) return true
  const status = await userStatus(user.login)
  return status.approved
}
