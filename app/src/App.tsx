import { useCallback, useEffect, useState } from 'react'
import { Link, ToastContext, match, useRouter } from './router'
import type { Toast } from './router'
import { useSession } from './state/session'
import { Ring } from './components/Ring'
import { Loading } from './components/bits'
import { Login } from './routes/Login'
import { Grove } from './routes/Grove'
import { Playlists } from './routes/Playlists'
import { PlaylistDetail } from './routes/PlaylistDetail'
import { Settings } from './routes/Settings'
import { Jobs } from './routes/Jobs'
import { Crate } from './routes/Crate'
import { Shared } from './routes/Shared'
import { UpdatePrompt } from './pwa'

const NAV = [
  { to: '/', label: 'Grove' },
  { to: '/crate', label: 'Crate' },
  { to: '/playlists', label: 'Playlists' },
  { to: '/shared', label: 'Shared' },
  { to: '/settings', label: 'You' },
]

export function App() {
  const { path } = useRouter()
  const { user, loading } = useSession()
  const [toast, setToast] = useState<Toast | null>(null)

  const show = useCallback((t: Toast) => setToast(t), [])
  useEffect(() => {
    if (!toast) return
    const id = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(id)
  }, [toast])

  // Waiting on the boot /api/me rather than flashing the login screen at
  // someone who is already signed in.
  if (loading) {
    return <div className="boot"><Ring size={48} /><Loading what="Waxgrove" /></div>
  }
  if (!user) {
    return (
      <ToastContext value={show}>
        <Login />
      </ToastContext>
    )
  }

  return (
    <ToastContext value={show}>
      <div id="app">
        <header className="topbar">
          <span className="brand">
            <Ring size={26} title="" />
            <span>
              <h1>Waxgrove</h1>
              <span className="sub">SONIC UNWALLED GARDEN</span>
            </span>
          </span>
          <nav className="nav" aria-label="Main">
            {NAV.map((n) => {
              const current = n.to === '/' ? path === '/' : path.startsWith(n.to)
              return (
                <Link key={n.to} to={n.to} {...(current ? { 'aria-current': 'page' } : {})}>
                  <span className="dot" aria-hidden="true" />
                  {n.label}
                </Link>
              )
            })}
          </nav>
          {/* Absorbs the leftover height in the desktop rail so the nav sits
              under the brand rather than being pushed to the floor. Inert on
              phones, where the nav is a fixed bottom bar. */}
          <span className="spacer" />
        </header>

        <main>
          <Route path={path} />
        </main>

        {toast && (
          <div className={toast.bad ? 'toast bad' : 'toast'} role="status" aria-live="polite">
            {toast.message}
          </div>
        )}
        <UpdatePrompt />
      </div>
    </ToastContext>
  )
}

function Route({ path }: { path: string }) {
  if (path === '/') return <Grove />
  if (path === '/playlists') return <Playlists />
  if (path === '/crate') return <Crate />
  if (path === '/shared') return <Shared />
  if (path === '/jobs') return <Jobs />
  if (path === '/settings') return <Settings />

  const detail = match('/playlists/:id', path)
  if (detail?.id) return <PlaylistDetail id={detail.id} />

  return (
    <>
      <p className="eyebrow">Not here</p>
      <h2>No such page</h2>
      <p className="row-actions"><Link to="/" className="btn ghost">Back to the grove</Link></p>
    </>
  )
}
