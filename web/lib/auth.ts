import type { NextAuthOptions } from 'next-auth'
import GitHubProvider from 'next-auth/providers/github'

export const authOptions: NextAuthOptions = {
  secret:
    process.env.NEXTAUTH_SECRET ??
    (process.env.NODE_ENV !== 'production' ? 'dev-secret-change-in-prod' : undefined),
  providers: [
    GitHubProvider({
      clientId: process.env.GITHUB_CLIENT_ID!,
      clientSecret: process.env.GITHUB_CLIENT_SECRET!,
      httpOptions: { timeout: 10000 },
    }),
  ],
  callbacks: {
    async jwt({ token, profile }) {
      // Persist GitHub login (username) in the JWT so session can expose it.
      if (profile) {
        token.login = (profile as { login: string }).login
      }
      return token
    },
    async session({ session, token }) {
      if (session.user) {
        (session.user as { login?: string }).login = token.login as string
      }
      return session
    },
  },
  pages: {
    signIn: '/login',
  },
}

export function isAdmin(login: string | undefined): boolean {
  // ADMIN_GITHUB_USERNAME is read server-side only; on the client it will be
  // undefined so we fall back to NEXT_PUBLIC_ADMIN_GITHUB_USERNAME.
  const adminLogin =
    process.env.ADMIN_GITHUB_USERNAME ??
    process.env.NEXT_PUBLIC_ADMIN_GITHUB_USERNAME ??
    ''
  return !!login && !!adminLogin && login === adminLogin
}
