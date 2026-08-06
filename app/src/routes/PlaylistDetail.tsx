// One playlist: its tracks, its append-only history, and the way out (JSPF).
//
// Any member may read a playlist — they are shared by reference (D8) — but only
// the owner may change one, and the server answers a non-owner's write with 404
// rather than 403 so it does not confirm the playlist exists. The UI mirrors
// that: it hides the controls it knows will fail, and still handles the failure.

import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, connect, jobsApi } from '../api/client'
import type { Playlist, Revision, Sync } from '../api/types'
import { Empty, ErrorNote, Loading, SongRow } from '../components/bits'
import { Link, navigate, useToast } from '../router'
import { useSession } from '../state/session'
import { Annotations } from '../components/Annotations'

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
  const [tab, setTab] = useState<'tracks' | 'notes' | 'history'>('tracks')
  const [history, setHistory] = useState<Revision[] | null>(null)
  const [editing, setEditing] = useState(false)
  const [spotifyReady, setSpotifyReady] = useState(false)
  const [syncs, setSyncs] = useState<Sync[]>([])

  useEffect(() => {
    connect.status()
      .then((st) => setSpotifyReady(!!st?.connected))
      .catch(() => setSpotifyReady(false))
    jobsApi.syncs(id).then((r) => setSyncs(r.syncs ?? [])).catch(() => setSyncs([]))
  }, [id])

  const exportToSpotify = async (force = false) => {
    setBusy(true)
    try {
      await jobsApi.exportSpotify(id, force)
      toast({ message: 'Sending. Follow it on the Jobs screen.' })
      navigate('/jobs')
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not start the export', bad: true })
    } finally {
      setBusy(false)
    }
  }

  const fork = async () => {
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

  const spotifySync = syncs.find((s) => s.service === 'spotify')

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

  // Move-up/down rather than drag-and-drop. On a phone, drag competes with
  // scrolling and needs a library to feel right; buttons work on touch, with a
  // mouse, and from the keyboard, for a fraction of the code.
  const move = async (from: number, to: number) => {
    if (!playlist || to < 0 || to >= playlist.tracks.length) return
    const ids = playlist.tracks.map((t) => t.record.id)
    const [moved] = ids.splice(from, 1)
    if (moved === undefined) return
    ids.splice(to, 0, moved)

    setBusy(true)
    try {
      setPlaylist(await api.reorderTracks(id, ids))
      setHistory(null)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        // Someone else changed it underneath us; showing the truth beats
        // retrying against a list that no longer exists.
        toast({ message: 'This playlist changed elsewhere — reloading.', bad: true })
        load()
      } else {
        toast({ message: err instanceof ApiError ? err.message : 'could not reorder', bad: true })
      }
    } finally {
      setBusy(false)
    }
  }

  const rename = async (title: string) => {
    setBusy(true)
    try {
      setPlaylist(await api.renamePlaylist(id, title))
      setHistory(null)
      setEditing(false)
      toast({ message: 'Renamed.' })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not rename', bad: true })
    } finally {
      setBusy(false)
    }
  }

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
      {editing ? (
        <form
          className="rename"
          onSubmit={(e) => {
            e.preventDefault()
            const value = new FormData(e.currentTarget).get('title')
            if (typeof value === 'string' && value.trim()) void rename(value.trim())
          }}
        >
          <label>
            <span className="lbl">Playlist title</span>
            <input name="title" defaultValue={playlist.title} autoFocus required />
          </label>
          <div className="row-actions">
            <button type="submit" className="btn" disabled={busy}>Save</button>
            <button type="button" className="btn ghost" onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
        </form>
      ) : (
        <h2>{playlist.title}</h2>
      )}
      {playlist.description && !editing && <p className="muted">{playlist.description}</p>}

      {playlist.forked_from && (
        <p className="small muted provenance">
          Copied from{' '}
          <Link to={`/playlists/${playlist.forked_from.id}`}>
            {playlist.forked_from.title}
          </Link>{' '}
          by {playlist.forked_from.owner}
        </p>
      )}

      {spotifySync && (
        <p className={spotifySync.diverged ? 'sync-state diverged' : 'sync-state'}>
          {spotifySync.diverged ? (
            <>
              <strong>This was edited on Spotify.</strong> Updating from here
              would replace what you did over there.{' '}
              {owned && (
                <button
                  type="button" className="linkish" disabled={busy}
                  onClick={() => {
                    if (window.confirm(
                      'Replace the Spotify copy with this playlist?\n\n' +
                      'Whatever you changed on Spotify will be lost.')) {
                      void exportToSpotify(true)
                    }
                  }}
                >
                  Replace it anyway
                </button>
              )}
            </>
          ) : spotifySync.behind > 0 ? (
            <>The Spotify copy is {spotifySync.behind}{' '}
            {spotifySync.behind === 1 ? 'revision' : 'revisions'} behind.</>
          ) : (
            <>The Spotify copy is up to date.</>
          )}
        </p>
      )}

      <div className="row-actions">
        <a className="btn ghost" href={api.exportURL(playlist.id)} download>
          Export JSPF
        </a>
        <Link to="/" className="btn ghost">Add songs</Link>
        {owned && spotifyReady && (
          <button type="button" className="btn ghost" disabled={busy}
                  onClick={() => void exportToSpotify()}>
            {spotifySync ? 'Update on Spotify' : 'Send to Spotify'}
          </button>
        )}
        {/* Forking is offered on anyone's playlist, including your own —
            "make me a variant of this to mess with" is a real thing to want. */}
        <button type="button" className="btn ghost" disabled={busy} onClick={() => void fork()}>
          Copy to mine
        </button>
        {owned && !editing && (
          <button type="button" className="btn ghost" onClick={() => setEditing(true)}>
            Rename
          </button>
        )}
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
          type="button" role="tab" aria-selected={tab === 'notes'}
          className={tab === 'notes' ? 'tab on' : 'tab'}
          onClick={() => setTab('notes')}
        >
          Notes
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
            {playlist.tracks.map((t, i) => (
              <SongRow
                key={`${t.position}-${t.record.id}`}
                record={t.record}
                badge={<span className="pos mono">{String(t.position + 1).padStart(2, '0')}</span>}
                {...(owned ? {
                  action: (
                    <span className="track-actions">
                      <button
                        type="button" className="btn sm ghost icon"
                        disabled={busy || i === 0}
                        aria-label={`Move ${t.record.title} up`}
                        onClick={() => void move(i, i - 1)}
                      >
                        ↑
                      </button>
                      <button
                        type="button" className="btn sm ghost icon"
                        disabled={busy || i === playlist.tracks.length - 1}
                        aria-label={`Move ${t.record.title} down`}
                        onClick={() => void move(i, i + 1)}
                      >
                        ↓
                      </button>
                      <button
                        type="button" className="btn sm ghost" disabled={busy}
                        aria-label={`Remove ${t.record.title}`}
                        onClick={() => void removeTrack(t.position)}
                      >
                        Remove
                      </button>
                    </span>
                  ),
                } : {})}
              />
            ))}
          </ul>
        )
      )}

      {tab === 'notes' && <Annotations playlistID={id} />}

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
