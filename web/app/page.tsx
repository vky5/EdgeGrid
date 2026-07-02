import { getServerSession } from 'next-auth'
import { authOptions } from '@/lib/auth'
import { Dashboard } from '@/components/dashboard'
import { Landing } from '@/components/landing'

export default async function Page() {
  const session = await getServerSession(authOptions)
  return session ? <Dashboard /> : <Landing />
}
