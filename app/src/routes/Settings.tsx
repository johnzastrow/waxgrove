// Account, invites, and what this instance actually stores.

import { useEffect, useState } from 'react'
import { api, ApiError, connect } from '../api/client'
import type { ConnectStatus } from '../api/types'
import { useSession } from '../state/session'
import { ErrorNote, Loading } from '../components/bits'
import { Connect } from '../components/Connect'
import { navigate, useToast } from '../router'

export function Settings() {
  const { user, signOut } = useSession()
  const toast = useToast()
  const [invite, setInvite] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // null means this instance has no connector at all, which is a supported
  // configuration rather than a failure (N6). undefined means still loading.
  const [spotify, setSpotify] = useState<ConnectStatus | null | undefined>(undefined)
  useEffect(() => {
    const ac = new AbortController()
    connect.status(ac.signal)
      .then(setSpotify)
      .catch(() => setSpotify(null))
    return () => ac.abort()
  }, [])

  // The OAuth callback redirects back here with its outcome in the query, since
  // the browser arrives from Spotify rather than from the app's own fetch.
  useEffect(() => {
    const result = new URLSearchParams(window.location.search).get('spotify')
    if (!result) return
    const messages: Record<string, { message: string; bad?: boolean }> = {
      connected: { message: 'Spotify connected.' },
      denied: { message: 'Spotify authorisation was declined.', bad: true },
      expired: { message: 'That connection attempt expired — try again.', bad: true },
      failed: { message: 'Spotify refused the connection. Check your Client ID and Secret.', bad: true },
    }
    const m = messages[result]
    if (m) toast(m)
    // Clear the query so a refresh does not repeat the message.
    navigate('/settings', { replace: true })
    connect.status().then(setSpotify).catch(() => {})
  }, [toast])

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

      {spotify === undefined && <Loading what="Checking connections…" />}
      {spotify && <Connect status={spotify} onChange={setSpotify} />}

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
