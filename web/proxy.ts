import { withAuth } from 'next-auth/middleware'

export default withAuth({
  pages: {
    signIn: '/login',
  },
})

export const config = {
  // Root ("/") is intentionally excluded: it renders a public landing page to
  // signed-out visitors and the dashboard to signed-in ones (see app/page.tsx).
  matcher: ['/jobs/:path*', '/workers/:path*', '/nodes/:path*', '/settings/:path*'],
}
