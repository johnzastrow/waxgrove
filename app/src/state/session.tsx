// Who is signed in.
//
// The session itself is an HttpOnly cookie the browser holds and JavaScript
// cannot read (internal/httpapi sets it). So "am I logged in?" is answered by
// asking the server once at boot, not by inspecting storage.

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { api, onUnauthorized } from '../api/client'
import type { User } from '../api/types'

interface SessionValue {
  user: User | null
  /** True until the initial /api/me settles; routes wait rather than flash login. */
  loading: boolean
  setUser: (u: User | null) => void
  signOut: () => Promise<void>
}

const SessionContext = createContext<SessionValue>({
  user: null, loading: true, setUser: () => {}, signOut: async () => {},
})

export function SessionProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const ac = new AbortController()
    api.me(ac.signal)
      .then(setUser)
      .catch(() => setUser(null)) // 401 at boot is the normal signed-out state
      .finally(() => { if (!ac.signal.aborted) setLoading(false) })
    return () => ac.abort()
  }, [])

  // Any request that comes back 401 mid-session drops us to signed-out, so an
  // expired session surfaces immediately rather than as repeated failures.
  useEffect(() => onUnauthorized(() => setUser(null)), [])

  const signOut = useCallback(async () => {
    try { await api.logout() } catch { /* clearing local state matters more */ }
    setUser(null)
  }, [])

  const value = useMemo(
    () => ({ user, loading, setUser, signOut }),
    [user, loading, signOut],
  )
  return <SessionContext value={value}>{children}</SessionContext>
}

export const useSession = () => useContext(SessionContext)
