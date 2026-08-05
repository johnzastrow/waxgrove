// The Spotify connect wizard (F13).
//
// Three steps, in this order because each genuinely depends on the last:
// register your own app, paste its credentials, authorise. It is more setup
// than a normal OAuth button, and that is the deliberate cost of BYO (D6) —
// Spotify's Development Mode caps an app at 5 users, so an operator-owned app
// would put a hard ceiling on how many friends could use the instance.
//
// The wizard therefore explains *why*, once, rather than presenting three
// unexplained fields.

import { useState } from 'react'
import { ApiError, connect } from '../api/client'
import type { ConnectStatus } from '../api/types'
import { ErrorNote, Field } from './bits'
import { useToast } from '../router'

interface Props {
  status: ConnectStatus
  onChange: (s: ConnectStatus | null) => void
}

export function Connect({ status, onChange }: Props) {
  const toast = useToast()
  const [clientID, setClientID] = useState(status.client_id ?? '')
  const [clientSecret, setClientSecret] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  const copyRedirect = async () => {
    try {
      await navigator.clipboard.writeText(status.redirect_uri)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast({ message: 'Copy it from the screen — the browser blocked the clipboard.', bad: true })
    }
  }

  const saveApp = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      onChange(await connect.saveApp(clientID.trim(), clientSecret.trim()))
      setClientSecret('') // never keep it in a form field longer than needed
      toast({ message: 'Saved. Now authorise Waxgrove with Spotify.' })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'could not save those credentials')
    } finally {
      setBusy(false)
    }
  }

  const authorise = async () => {
    setBusy(true)
    try {
      const { authorize_url } = await connect.begin()
      // A full navigation, not a popup: the user authenticates on Spotify's
      // own page and must be able to see that they are doing so.
      window.location.assign(authorize_url)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'could not start the connection')
      setBusy(false)
    }
  }

  const disconnect = async () => {
    if (!window.confirm(
      'Disconnect Spotify? Waxgrove will forget your tokens. Your playlists stay here.',
    )) return
    setBusy(true)
    try {
      await connect.disconnect()
      onChange({ ...status, connected: false, app_configured: false, client_id: '' })
      setClientID('')
      toast({ message: 'Spotify disconnected.' })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not disconnect', bad: true })
    } finally {
      setBusy(false)
    }
  }

  if (status.connected) {
    return (
      <section className="card good">
        <p className="eyebrow">Spotify</p>
        <h3>Connected</h3>
        <p className="small muted">
          {status.storefront
            ? `Resolving against the ${status.storefront} catalogue. `
            : ''}
          You can import a playlist by pasting its link, and push playlists back out.
        </p>
        <div className="row-actions">
          <button type="button" className="btn danger" disabled={busy} onClick={() => void disconnect()}>
            Disconnect
          </button>
        </div>
      </section>
    )
  }

  return (
    <section className="card">
      <p className="eyebrow">Spotify</p>
      <h3>Connect your account</h3>
      <p className="small muted">
        Spotify caps each app at five users, so Waxgrove does not ship one —
        you register your own, and it stays yours. It is free and takes a
        couple of minutes.
      </p>

      <ol className="wizard">
        <li>
          <span className="step">1</span>
          <div>
            <strong>Create an app</strong>
            <p className="small muted">
              Open the{' '}
              <a href="https://developer.spotify.com/dashboard" target="_blank" rel="noreferrer noopener">
                Spotify developer dashboard
              </a>{' '}
              and create an app. Any name will do.
            </p>
          </div>
        </li>

        <li>
          <span className="step">2</span>
          <div>
            <strong>Add this redirect URI</strong>
            <p className="small muted">
              Paste this into the app's settings, exactly as it appears. Spotify
              compares it character for character.
            </p>
            <p className="redirect mono">
              <span>{status.redirect_uri}</span>
              <button type="button" className="btn sm ghost" onClick={() => void copyRedirect()}>
                {copied ? 'Copied' : 'Copy'}
              </button>
            </p>
          </div>
        </li>

        <li>
          <span className="step">3</span>
          <div>
            <strong>Paste the app's credentials</strong>
            <form onSubmit={saveApp}>
              <Field
                label="Client ID" value={clientID} required
                autoComplete="off" spellCheck={false}
                onChange={(e) => setClientID(e.target.value)}
              />
              <Field
                label="Client Secret" type="password" value={clientSecret}
                required={!status.app_configured}
                autoComplete="off" spellCheck={false}
                placeholder={status.app_configured ? 'stored — leave blank to keep' : ''}
                onChange={(e) => setClientSecret(e.target.value)}
              />
              <p className="small muted">
                Both are encrypted before they touch the disk. The secret is never
                shown again — if you lose it, Spotify will issue a new one.
              </p>
              <ErrorNote error={error} />
              <div className="row-actions">
                <button type="submit" className="btn" disabled={busy || !clientID.trim()}>
                  Save
                </button>
              </div>
            </form>
          </div>
        </li>

        <li className={status.app_configured ? '' : 'pending'}>
          <span className="step">4</span>
          <div>
            <strong>Authorise</strong>
            <p className="small muted">
              You will sign in on Spotify's own page. Waxgrove never sees your
              Spotify password.
            </p>
            <div className="row-actions">
              <button
                type="button" className="btn"
                disabled={busy || !status.app_configured}
                onClick={() => void authorise()}
              >
                Authorise with Spotify
              </button>
            </div>
          </div>
        </li>
      </ol>
    </section>
  )
}
