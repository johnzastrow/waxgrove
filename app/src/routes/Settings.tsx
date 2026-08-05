// Account, invites, and what this instance actually stores.

import { useState } from 'react'
import { api, ApiError } from '../api/client'
import { useSession } from '../state/session'
import { ErrorNote } from '../components/bits'
import { useToast } from '../router'

export function Settings() {
  const { user, signOut } = useSession()
  const toast = useToast()
  const [invite, setInvite] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (!user) return null

  const makeInvite = async () => {
    setBusy(true); setError(null)
    try {
      const res = await api.createInvite()
      setInvite(res.code)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'could not create an invite')
    } finally {
      setBusy(false)
    }
  }

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast({ message: 'Invite code copied.' })
    } catch {
      // Clipboard access can be refused; the code is on screen either way.
      toast({ message: 'Copy it from the screen — the browser blocked the clipboard.', bad: true })
    }
  }

  return (
    <>
      <p className="eyebrow">You</p>
      <h2>{user.display_name}</h2>

      <section className="card">
        <dl className="facts">
          <dt>Email</dt><dd>{user.email}</dd>
          <dt>Role</dt><dd>{user.role}</dd>
        </dl>
        <div className="row-actions">
          <button type="button" className="btn ghost" onClick={() => void signOut()}>
            Sign out
          </button>
        </div>
      </section>

      {user.role === 'admin' && (
        <section className="card">
          <p className="eyebrow">Invites</p>
          <h3>Bring someone into the grove</h3>
          <p className="small muted">
            Registration is invite-only. Each code works once, and expires.
          </p>
          {invite && (
            <p className="invite mono">
              {invite}
              <button type="button" className="btn sm ghost" onClick={() => void copy(invite)}>
                Copy
              </button>
            </p>
          )}
          <ErrorNote error={error} />
          <div className="row-actions">
            <button type="button" className="btn" disabled={busy} onClick={() => void makeInvite()}>
              {busy ? 'Minting…' : 'Create an invite code'}
            </button>
          </div>
        </section>
      )}

      <section className="card">
        <p className="eyebrow">What this holds</p>
        <p className="small muted">
          Waxgrove stores metadata only: titles, artists, identifiers, and the
          order you put them in. No audio, ever. Your listening stays with your
          streaming service, and this instance never sees it.
        </p>
      </section>
    </>
  )
}
