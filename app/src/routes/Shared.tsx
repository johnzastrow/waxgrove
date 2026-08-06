// Everyone else's playlists (F9, D8).
//
// Playlists are shared by reference across the instance, so this is simply
// everything that is not yours. There is no visibility toggle because there is
// no private playlist — an instance is a group of friends who chose to be one,
// and a permissions model nobody asked for is a feature to maintain forever.

import { useEffect, useState } from 'react'
import { ApiError, api, discover } from '../api/client'
import type { PlaylistSummary } from '../api/types'
import { Empty, ErrorNote, Loading } from '../components/bits'
import { Ring } from '../components/Ring'
import { Link, navigate, useToast } from '../router'

export function Shared() {
  const toast = useToast()
  const [items, setItems] = useState<PlaylistSummary[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const fork = async (id: string) => {
    setBusy(true)
    try {
      const copy = await api.fork(id)
      toast({ message: `Copied into your own "${copy.title}".` })
      navigate(`/playlists/${copy.id}`)
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not copy that', bad: true })
    } finally {
      setBusy(false)
    }
  }

  useEffect(() => {
    const ac = new AbortController()
    discover.shared(ac.signal)
      .then((r) => setItems(r.playlists ?? []))
      .catch((err) => {
        if (err instanceof DOMException) return
        setError(err instanceof ApiError ? err.message : 'could not load shared playlists')
      })
    return () => ac.abort()
  }, [])

  return (
    <>
      <p className="eyebrow">The grove</p>
      <h2>Shared with you</h2>
      <p className="small muted">
        Everything anyone else here has made. Open one to rate it, tag it or
        say something — none of which changes what they made.
      </p>

      <ErrorNote error={error} />
      {items === null && !error && <Loading what="Looking around…" />}

      {items !== null && items.length === 0 && (
        <Empty title="Nobody else has made a playlist yet">
          When they do, it will show up here.
        </Empty>
      )}

      {items !== null && items.length > 0 && (
        <ul className="shelf">
          {items.map((p) => (
            <li key={p.id} className="shelf-row">
              <Link to={`/playlists/${p.id}`} className="shelf-item">
                <Ring size={30} title="" />
                <span className="meta">
                  <span className="ti">{p.title}</span>
                  <span className="ar">
                    {p.owner} · {p.track_count} {p.track_count === 1 ? 'track' : 'tracks'}
                    {p.rating && p.rating.count > 0 &&
                      ` · ★ ${p.rating.average.toFixed(1)}`}
                  </span>
                </span>
              </Link>
              <button
                type="button" className="btn sm ghost" disabled={busy}
                aria-label={`Copy ${p.title} into your own playlists`}
                onClick={() => void fork(p.id)}
              >
                Copy
              </button>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
