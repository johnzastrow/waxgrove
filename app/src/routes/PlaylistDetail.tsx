// One playlist: its tracks, its append-only history, and the way out (JSPF).
//
// Any member may read a playlist — they are shared by reference (D8) — but only
// the owner may change one, and the server answers a non-owner's write with 404
// rather than 403 so it does not confirm the playlist exists. The UI mirrors
// that: it hides the controls it knows will fail, and still handles the failure.

import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Playlist, Revision } from '../api/types'
import { Empty, ErrorNote, Loading, SongRow } from '../components/bits'
import { Link, navigate, useToast } from '../router'
import { useSession } from '../state/session'

const OP_LABEL: Record<string, string> = {
  create: 'created it',
  add: 'added tracks',
  remove: 'removed a track',
  reorder: 'reordered it',
  rename: 'renamed it',
}

/**
 * Revision detail is a small JSON blob whose shape depends on the op. Render it
 * as a sentence; fall back to nothing rather than dumping raw JSON at someone
 * reading a history.
 */
function describe(op: string, detail: string): string {
  if (!detail) return ''
  let d: Record<string, unknown>
  try { d = JSON.parse(detail) } catch { return '' }

  if (op === 'add' && typeof d.count === 'number') {
    return `${d.count} ${d.count === 1 ? 'track' : 'tracks'}`
  }
  if ((op === 'create' || op === 'rename') && typeof d.title === 'string') {
    return `"${d.title}"`
  }
  if (op === 'remove' && typeof d.position === 'number') {
    return `position ${d.position + 1}`
  }
  return ''
}

export function PlaylistDetail({ id }: { id: string }) {
  const toast = useToast()
  const { user } = useSession()
  const [playlist, setPlaylist] = useState<Playlist | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState<'tracks' | 'history'>('tracks')
  const [history, setHistory] = useState<Revision[] | null>(null)

  const load = useCallback((signal?: AbortSignal) => {
    api.playlist(id, signal)
      .then((p) => { setPlaylist(p); setError(null) })
      .catch((err) => {
        if (err instanceof DOMException) return
        setError(err instanceof ApiError ? err.message : 'could not load this playlist')
      })
  }, [id])

  useEffect(() => {
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])

  useEffect(() => {
    if (tab !== 'history' || history !== null) return
    const ac = new AbortController()
    api.history(id, ac.signal)
      .then((r) => setHistory(r.revisions ?? []))
      .catch(() => setHistory([]))
    return () => ac.abort()
  }, [tab, history, id])

  const owned = !!playlist && !!user && playlist.owner_id === user.id

  const removeTrack = async (position: number) => {
    setBusy(true)
    try {
      setPlaylist(await api.removeTrack(id, position))
      setHistory(null) // the history just gained an entry
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not remove', bad: true })
    } finally {
      setBusy(false)
    }
  }

  const destroy = async () => {
    setBusy(true)
    try {
      await api.deletePlaylist(id)
      toast({ message: 'Playlist deleted.' })
      navigate('/playlists', { replace: true })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not delete', bad: true })
      setBusy(false)
    }
  }

  if (error) {
    return (
      <>
        <ErrorNote error={error} />
        <p className="row-actions"><Link to="/playlists" className="btn ghost">Back to the shelf</Link></p>
      </>
    )
  }
  if (!playlist) return <Loading what="Opening the sleeve…" />

  return (
    <>
      <p className="eyebrow">
        <Link to="/playlists">Playlists</Link> · revision {playlist.revision}
      </p>
      <h2>{playlist.title}</h2>
      {playlist.description && <p className="muted">{playlist.description}</p>}

      <div className="row-actions">
        <a className="btn ghost" href={api.exportURL(playlist.id)} download>
          Export JSPF
        </a>
        <Link to="/" className="btn ghost">Add songs</Link>
        {owned && (
          <button
            type="button" className="btn danger" disabled={busy}
            onClick={() => { if (confirmDelete(playlist.title)) void destroy() }}
          >
            Delete
          </button>
        )}
      </div>

      <div className="tabs" role="tablist">
        <button
          type="button" role="tab" aria-selected={tab === 'tracks'}
          className={tab === 'tracks' ? 'tab on' : 'tab'}
          onClick={() => setTab('tracks')}
        >
          Tracks · {playlist.tracks?.length ?? 0}
        </button>
        <button
          type="button" role="tab" aria-selected={tab === 'history'}
          className={tab === 'history' ? 'tab on' : 'tab'}
          onClick={() => setTab('history')}
        >
          History
        </button>
      </div>

      {tab === 'tracks' && (
        (playlist.tracks?.length ?? 0) === 0 ? (
          <Empty title="An empty sleeve">
            Search the grove and stage a few songs, then add them here.
          </Empty>
        ) : (
          <ul className="rows numbered">
            {playlist.tracks.map((t) => (
              <SongRow
                key={`${t.position}-${t.record.id}`}
                record={t.record}
                badge={<span className="pos mono">{String(t.position + 1).padStart(2, '0')}</span>}
                {...(owned ? {
                  action: (
                    <button
                      type="button" className="btn sm ghost" disabled={busy}
                      aria-label={`Remove ${t.record.title}`}
                      onClick={() => void removeTrack(t.position)}
                    >
                      Remove
                    </button>
                  ),
                } : {})}
              />
            ))}
          </ul>
        )
      )}

      {tab === 'history' && (
        history === null ? <Loading what="Reading the log…" /> : (
          <ol className="history">
            {history.map((rev) => (
              <li key={rev.rev}>
                <span className="mono rev">r{rev.rev}</span>
                <span className="meta">
                  <span className="ti">{OP_LABEL[rev.op] ?? rev.op}</span>
                  <span className="ar">
                    {rev.actor}
                    {describe(rev.op, rev.detail) && ` · ${describe(rev.op, rev.detail)}`}
                  </span>
                  <span className="isrc">{new Date(rev.created_at).toLocaleString()}</span>
                </span>
              </li>
            ))}
          </ol>
        )
      )}
    </>
  )
}

// Deleting a playlist is not reversible, so it asks. The native dialog is
// deliberate: it cannot be missed, and a custom modal would be more code for
// less certainty.
function confirmDelete(title: string): boolean {
  return window.confirm(
    `Delete "${title}"? Its history goes with it. Songs stay in the shared catalogue.`,
  )
}
