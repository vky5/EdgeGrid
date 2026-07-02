'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { useSession, signOut } from 'next-auth/react'

const NAV_ITEMS = [
  {
    href: '/',
    label: 'DASHBOARD',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
        <rect x="1" y="1" width="6" height="6" stroke="currentColor" strokeWidth="1.2" />
        <rect x="9" y="1" width="6" height="6" stroke="currentColor" strokeWidth="1.2" />
        <rect x="1" y="9" width="6" height="6" stroke="currentColor" strokeWidth="1.2" />
        <rect x="9" y="9" width="6" height="6" stroke="currentColor" strokeWidth="1.2" />
      </svg>
    ),
  },
  {
    href: '/jobs',
    label: 'JOBS',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
        <rect x="1" y="3" width="14" height="2" stroke="currentColor" strokeWidth="1.2" />
        <rect x="1" y="7" width="14" height="2" stroke="currentColor" strokeWidth="1.2" />
        <rect x="1" y="11" width="14" height="2" stroke="currentColor" strokeWidth="1.2" />
      </svg>
    ),
  },
  {
    href: '/workers',
    label: 'WORKERS',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
        <rect x="1" y="2" width="14" height="9" stroke="currentColor" strokeWidth="1.2" />
        <path d="M5 11v3M11 11v3M3 14h10" stroke="currentColor" strokeWidth="1.2" />
        <circle cx="8" cy="6.5" r="1.5" stroke="currentColor" strokeWidth="1.2" />
      </svg>
    ),
  },
  {
    href: '/nodes',
    label: 'NODE ACCESS',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
        <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="1.2" />
        <path d="M8 5v3l2 2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
        <path d="M4 2.5L2 1M12 2.5L14 1M4 13.5L2 15M12 13.5L14 15" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
      </svg>
    ),
  },
  {
    href: '/settings',
    label: 'SETTINGS',
    icon: (
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
        <circle cx="8" cy="8" r="2.5" stroke="currentColor" strokeWidth="1.2" />
        <path
          d="M8 1.5v1.8M8 12.7v1.8M14.5 8h-1.8M3.3 8H1.5M12.5 3.5l-1.3 1.3M4.8 11.2l-1.3 1.3M12.5 12.5l-1.3-1.3M4.8 4.8L3.5 3.5"
          stroke="currentColor"
          strokeWidth="1.2"
          strokeLinecap="round"
        />
      </svg>
    ),
  },
]

export function Nav() {
  const pathname = usePathname()
  const { data: session } = useSession()
  const login = (session?.user as any)?.login as string | undefined

  if (!session) return null

  const isActive = (href: string) => {
    if (href === '/') return pathname === '/'
    return pathname.startsWith(href)
  }

  const avatarLetter = login ? login[0].toUpperCase() : '?'

  return (
    <nav className="w-11 flex flex-col border-r border-[#1f1f1f] bg-[#0c0c0c] shrink-0">
      {/* Logo */}
      <div className="h-11 flex items-center justify-center border-b border-[#1f1f1f]">
        <span className="font-mono text-[10px] font-bold text-[#f59e0b] tracking-widest">EG</span>
      </div>

      {/* Nav items */}
      <div className="flex flex-col items-center gap-1 pt-2">
        {NAV_ITEMS.map((item) => (
          <Link
            key={item.href}
            href={item.href}
            title={item.label}
            className={`group relative w-9 h-9 flex items-center justify-center transition-colors ${
              isActive(item.href)
                ? 'text-[#f59e0b] bg-[#1a1a1a]'
                : 'text-[#6b7280] hover:text-[#d4d4d4] hover:bg-[#1a1a1a]'
            }`}
          >
            {isActive(item.href) && (
              <span className="absolute left-0 top-0 bottom-0 w-0.5 bg-[#f59e0b]" />
            )}
            {item.icon}
            {/* Tooltip */}
            <span className="absolute left-11 bg-[#1a1a1a] border border-[#1f1f1f] px-2 py-1 font-mono text-[10px] text-[#d4d4d4] tracking-widest whitespace-nowrap opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-50">
              {item.label}
            </span>
          </Link>
        ))}
      </div>

      {/* User / logout at bottom */}
      {session && (
        <div className="mt-auto mb-2 flex flex-col items-center gap-1">
          <div
            title={`@${login}`}
            className="w-9 h-9 flex items-center justify-center text-[#6b7280]"
          >
            <span className="w-5 h-5 rounded-full bg-[#1f1f1f] border border-[#3f3f3f] flex items-center justify-center font-mono text-[9px] text-[#d4d4d4]">
              {avatarLetter}
            </span>
          </div>
          <button
            title={`@${login} — sign out`}
            onClick={() => signOut({ callbackUrl: '/login' })}
            className="group relative w-9 h-9 flex items-center justify-center text-[#6b7280] hover:text-[#ef4444] hover:bg-[#1a1a1a] transition-colors"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path d="M6 2H3a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
              <path d="M10.5 5L14 8l-3.5 3M14 8H6" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span className="absolute left-11 bottom-0 bg-[#1a1a1a] border border-[#1f1f1f] px-2 py-1 font-mono text-[10px] text-[#d4d4d4] tracking-widest whitespace-nowrap opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none z-50">
              @{login} · SIGN OUT
            </span>
          </button>
        </div>
      )}
    </nav>
  )
}
