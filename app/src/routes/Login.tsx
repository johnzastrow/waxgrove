// Sign in, and register against an invite.
//
// Registration is invite-only by default (§6 — a friends instance, not a public
// service), so the register form asks for a code up front rather than letting
// someone fill in four fields and then be told no.

import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import { useSession } from '../state/session'
import { ErrorNote, Field, Spinner } from '../components/bits'
import { Ring } from '../components/Ring'
import { Build } from '../components/Build'

type Mode = 'login' | 'register'

export function Login() {
  const { setUser } = useSession()
  const [mode, setMode] = useState<Mode>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [invite, setInvite] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // Whether an invite code is needed at all. The very first account on an
  // instance does not need one — asking for it anyway locks the operator out
  // of the instance they just installed, which is what this fixes.
  const [inviteRequired, setInviteRequired] = useState<boolean | null>(null)
  useEffect(() => {
    const ac = new AbortController()
    api.instance(ac.signal)
      .then((i) => setInviteRequired(i.invite_required))
      // If we cannot tell, ask for it: the server is the real gate, and a
      // spurious field beats silently dropping a code somebody needs to give.
      .catch(() => setInviteRequired(true))
    return () => ac.abort()
  }, [])

  const firstAccount = inviteRequired === false

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const user = mode === 'login'
        ? await api.login(email, password)
        : await api.register({
            email, password, display_name: displayName, invite_code: invite,
          })
      setUser(user)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'something went wrong')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="gate">
      <div className="gate-mark">
        <Ring size={56} title="Waxgrove" />
        <h1 className="wordmark">Waxgrove</h1>
        <p className="sub mono">SONIC UNWALLED GARDEN</p>
      </div>

      <form className="card" onSubmit={submit}>
        <p className="eyebrow">{mode === 'login' ? 'Sign in' : 'Join this grove'}</p>

        {mode === 'register' && firstAccount && (
          <p className="small first-account">
            <strong>This is the first account on this instance.</strong> It
            becomes the admin, and needs no invite code. Everyone after you
            will.
          </p>
        )}

        {mode === 'register' && !firstAccount && (
          <Field
            label="Invite code" value={invite} required
            autoComplete="off" spellCheck={false}
            onChange={(e) => setInvite(e.target.value)}
            placeholder="from whoever runs this instance"
          />
        )}

        <Field
          label="Email" type="email" value={email} required
          autoComplete={mode === 'login' ? 'username' : 'email'}
          onChange={(e) => setEmail(e.target.value)}
        />

        {mode === 'register' && (
          <Field
            label="Display name" value={displayName} required
            autoComplete="nickname"
            onChange={(e) => setDisplayName(e.target.value)}
          />
        )}

        <Field
          label="Password" type="password" value={password} required
          autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
          onChange={(e) => setPassword(e.target.value)}
        />

        <ErrorNote error={error} />

        <div className="row-actions">
          <button type="submit" className="btn block" disabled={busy}>
            {busy && <Spinner />}
            {mode === 'login' ? 'Sign in' : 'Create account'}
          </button>
        </div>

        <p className="small muted switch">
          {mode === 'login'
            ? (firstAccount ? 'Nobody has claimed this instance yet. ' : 'Got an invite code? ')
            : 'Already a member? '}
          <button
            type="button" className="linkish"
            onClick={() => { setMode(mode === 'login' ? 'register' : 'login'); setError(null) }}
          >
            {mode === 'login' ? (firstAccount ? 'Create the admin account' : 'Register') : 'Sign in'}
          </button>
        </p>
      </form>

      <p className="small muted gate-foot">
        Waxgrove stores metadata about songs — never audio. What you listen to
        stays with your streaming service.
        <br />
        <Build />
      </p>
    </div>
  )
}
