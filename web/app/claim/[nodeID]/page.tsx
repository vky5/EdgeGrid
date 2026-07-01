'use client'

import { useEffect, useState } from 'react'
import { useSession, signIn } from 'next-auth/react'
import { useParams } from 'next/navigation'

type State = 'idle' | 'claiming' | 'done' | 'error'

export default function ClaimPage() {
  const { data: session, status } = useSession()
  const params = useParams()
  const nodeID = params.nodeID as string
  const [claimState, setClaimState] = useState<State>('idle')
  const [msg, setMsg] = useState('')

  useEffect(() => {
    if (status === 'authenticated' && claimState === 'idle') {
      setClaimState('claiming')
      fetch(`/api/claim/${nodeID}`, { method: 'POST' })
        .then(async (res) => {
          const text = await res.text()
          if (res.ok) {
            setClaimState('done')
            setMsg('Node claimed! Waiting for admin approval.')
          } else {
            setClaimState('error')
            setMsg(text || `Error ${res.status}`)
          }
        })
        .catch((e) => {
          setClaimState('error')
          setMsg(e.message)
        })
    }
  }, [status, claimState, nodeID])

  return (
    <div className="h-full flex items-center justify-center bg-[#0c0c0c]">
      <div className="border border-[#1f1f1f] bg-[#0f0f0f] p-10 flex flex-col items-center gap-6 max-w-sm w-full">
        <span className="font-mono text-[10px] font-bold text-[#f59e0b] tracking-widest">CLAIM NODE</span>
        <div className="font-mono text-[10px] text-[#6b7280] break-all text-center">{nodeID}</div>

        {status === 'loading' && (
          <span className="w-1.5 h-1.5 rounded-full bg-[#f59e0b] animate-pulse inline-block" />
        )}

        {status === 'unauthenticated' && (
          <>
            <p className="font-mono text-xs text-[#d4d4d4] text-center">
              Sign in to link your GitHub account to this node
            </p>
            <button
              onClick={() => signIn('github', { callbackUrl: `/claim/${nodeID}` })}
              className="w-full font-mono text-[10px] tracking-widest text-[#d4d4d4] border border-[#1f1f1f] py-3 hover:border-[#6b7280] hover:bg-[#1a1a1a] transition-colors"
            >
              CONTINUE WITH GITHUB →
            </button>
          </>
        )}

        {status === 'authenticated' && claimState === 'claiming' && (
          <div className="flex items-center gap-2">
            <span className="w-1.5 h-1.5 rounded-full bg-[#f59e0b] animate-pulse inline-block" />
            <span className="font-mono text-[10px] text-[#6b7280]">claiming as @{(session?.user as any)?.login}...</span>
          </div>
        )}

        {claimState === 'done' && (
          <p className="font-mono text-xs text-[#22c55e] text-center">{msg}</p>
        )}

        {claimState === 'error' && (
          <p className="font-mono text-xs text-[#ef4444] text-center">{msg}</p>
        )}
      </div>
    </div>
  )
}
