// Service worker registration and the update prompt.
//
// registerType is 'prompt', not 'autoUpdate', on purpose: swapping the running
// app out mid-edit is exactly how someone loses a half-built playlist. The new
// version waits until the user says so.
//
// Nothing from /api is cached. An offline shell that served a week-old playlist
// would be worse than an honest failure — the app is metadata about shared
// state, and stale shared state reads as data loss.

import { useRegisterSW } from 'virtual:pwa-register/react'

export function UpdatePrompt() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW({
    onRegisterError(err: unknown) {
      // A failed registration must never break the app; it just means no
      // offline shell this session.
      console.warn('[waxgrove] service worker registration failed', err)
    },
  })

  if (!needRefresh) return null

  return (
    <div className="toast update" role="status">
      A new version of Waxgrove is ready.
      <button type="button" className="btn sm" onClick={() => void updateServiceWorker(true)}>
        Reload
      </button>
      <button type="button" className="btn sm ghost" onClick={() => setNeedRefresh(false)}>
        Later
      </button>
    </div>
  )
}
