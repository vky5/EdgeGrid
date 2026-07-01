'use client'

import { signIn } from 'next-auth/react'
import { Suspense } from 'react'
import { useSearchParams } from 'next/navigation'

function LoginForm() {
  const params = useSearchParams()
  const callbackUrl = params.get('callbackUrl') ?? '/'

  return (
    <div className="h-full flex items-center justify-center bg-[#0c0c0c]">
      <div className="border border-[#1f1f1f] bg-[#0f0f0f] p-10 flex flex-col items-center gap-6 w-80">
        <span className="font-mono text-[10px] font-bold text-[#f59e0b] tracking-widest">EDGEGRID</span>
        <div className="text-center space-y-1">
          <p className="font-mono text-sm text-[#d4d4d4]">Sign in to access the grid</p>
          <p className="font-mono text-[10px] text-[#6b7280]">GitHub account required</p>
        </div>
        <button
          onClick={() => signIn('github', { callbackUrl })}
          className="w-full font-mono text-[10px] tracking-widest text-[#d4d4d4] border border-[#1f1f1f] py-3 hover:border-[#6b7280] hover:bg-[#1a1a1a] transition-colors"
        >
          CONTINUE WITH GITHUB →
        </button>
      </div>
    </div>
  )
}

export default function LoginPage() {
  return (
    <Suspense>
      <LoginForm />
    </Suspense>
  )
}
