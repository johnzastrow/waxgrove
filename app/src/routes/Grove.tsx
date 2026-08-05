// Search: the shared catalogue first, the metadata source second.
//
// The ordering is the point (§3.0). Everything the group has ever resolved is
// already local and free to search; the network is only consulted for what the
// grove has not seen. An instance with no metadata source configured is a
// normal, supported state, not an error — so that case says so plainly (N6).
//
// Staging goes to the crate (F16), not to component state: it persists, it
// survives this browser, and it is where a doubtful match gets settled before
// any playlist exists.

import { useEffect, useRef, useState } from 'react'
import { api, ApiError, candidateFromRecord, connect, crate, jobsApi } from '../api/client'
import type { Candidate, SongRecord } from '../api/types'
import { Empty, ErrorNote, Loading, SongRow } from '../components/bits'
import { Link, navigate, useToast } from '../router'

/** Two candidates are the same staged item when they agree on identity. */
function key(c: Candidate): string {
  return c.mbid || c.isrc || `${c.title ?? ''}|${c.artist ?? ''}`.toLowerCase()
}

export function Grove() {
  const toast = useToast()
  const [q, setQ] = useState('')
  const [local, setLocal] = useState<SongRecord[]>([])
  const [remote, setRemote] = useState<Candidate[]>([])
  const [remoteNote, setRemoteNote] = useState<string | null>(null)
  const [searching, setSearching] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [searched, setSearched] = useState(false)

  const [crateCount, setCrateCount] = useState(0)
  const [justStaged, setJustStaged] = useState<Set<string>>(new Set())

  const [spotifyReady, setSpotifyReady] = useState(false)
  const [link, setLink] = useState('')
  const [importing, setImporting] = useState(false)

  useEffect(() => {
    crate.list()
      .then((r) => setCrateCount(r.total))
      .catch(() => { /* the count is a nicety; search still works */ })
    // Absent connector, or an unconnected account: the import box stays hidden
    // rather than offering something that cannot work (N6).
    connect.status()
      .then((st) => setSpotifyReady(!!st?.connected))
      .catch(() => setSpotifyReady(false))
  }, [])

  const stage = async (c: Candidate) => {
    try {
      const r = await crate.add([c])
      setCrateCount(r.total)
      setJustStaged((prev) => new Set(prev).add(key(c)))
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not stage that', bad: true })
    }
  }
  const isStaged = (c: Candidate) => justStaged.has(key(c))

  const importFromSpotify = async (e: React.FormEvent) => {
    e.preventDefault()
    setImporting(true)
    try {
      await jobsApi.importSpotify(link.trim())
      setLink('')
      toast({ message: 'Importing. Follow it on the Jobs screen.' })
      navigate('/jobs')
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not start the import', bad: true })
    } finally {
      setImporting(false)
    }
  }

  // Debounced search. The ref lets a newer keystroke abort an in-flight
  // request, so results can never arrive out of order.
  const inflight = useRef<AbortController | null>(null)
  useEffect(() => {
    const term = q.trim()
    if (term.length < 2) {
      setLocal([]); setRemote([]); setRemoteNote(null); setSearched(false)
      return
    }
    const t = setTimeout(() => {
      inflight.current?.abort()
      const ac = new AbortController()
      inflight.current = ac
      setSearching(true); setError(null)

      // Both halves run together; the remote failing must not blank the local
      // results, which are the more useful half anyway.
      Promise.allSettled([
        api.searchRecords(term, ac.signal),
        api.searchRemote(term, ac.signal),
      ]).then(([l, r]) => {
        if (ac.signal.aborted) return
        if (l.status === 'fulfilled') setLocal(l.value.records ?? [])
        else if (!(l.reason instanceof DOMException)) {
          setError(l.reason instanceof ApiError ? l.reason.message : 'search failed')
        }
        if (r.status === 'fulfilled') {
          setRemote(r.value.candidates ?? [])
          setRemoteNote(r.value.note ?? null)
        } else {
          setRemote([])
          setRemoteNote('the metadata source is unavailable right now')
        }
        setSearching(false); setSearched(true)
      })
    }, 300)
    return () => clearTimeout(t)
  }, [q])

  const stageButton = (c: Candidate) => (
    <button
      type="button" className={isStaged(c) ? 'btn sm ghost' : 'btn sm'}
      disabled={isStaged(c)}
      onClick={() => void stage(c)}
    >
      {isStaged(c) ? 'In crate' : 'Stage'}
    </button>
  )

  return (
    <>
      <p className="eyebrow">The grove</p>
      <h2>Find a song</h2>

      <input
        type="search" value={q} onChange={(e) => setQ(e.target.value)}
        placeholder="Title, artist, or ISRC" aria-label="Search for a song"
        autoComplete="off" className="search-input"
      />

      {crateCount > 0 && (
        <p className="small crate-note">
          <Link to="/crate">
            {crateCount} {crateCount === 1 ? 'song' : 'songs'} waiting in your crate →
          </Link>
        </p>
      )}

      {spotifyReady && (
        <form className="card paste" onSubmit={importFromSpotify}>
          <p className="eyebrow">Bring a playlist in</p>
          <label>
            <span className="lbl">Spotify playlist link</span>
            <input
              type="text" value={link} onChange={(e) => setLink(e.target.value)}
              placeholder="https://open.spotify.com/playlist/…"
              autoComplete="off" spellCheck={false}
            />
          </label>
          <p className="small muted">
            In Spotify, use Share then Copy link to playlist. Spotify no longer
            lets an app list your playlists, so pasting the link is the way in.
          </p>
          <div className="row-actions">
            <button type="submit" className="btn" disabled={importing || !link.trim()}>
              {importing ? 'Starting…' : 'Import'}
            </button>
            <Link to="/jobs" className="btn ghost">See transfers</Link>
          </div>
        </form>
      )}

      <ErrorNote error={error} />
      {searching && <Loading what="Searching the grove…" />}

      {local.length > 0 && (
        <section className="card">
          <p className="eyebrow">In the grove · {local.length}</p>
          <ul className="rows">
            {local.map((r) => (
              <SongRow
                key={r.id} record={r} confidence={1}
                badge={<span className="badge g">local</span>}
                action={stageButton(candidateFromRecord(r))}
              />
            ))}
          </ul>
        </section>
      )}

      {remoteNote && (
        <p className="small muted note">
          {remoteNote === 'no metadata source configured'
            ? 'This instance runs with no metadata source. The shared catalogue and JSPF import still work.'
            : remoteNote}
        </p>
      )}

      {remote.length > 0 && (
        <section className="card">
          <p className="eyebrow">Wider catalogue · {remote.length}</p>
          <ul className="rows">
            {remote.map((c, i) => (
              <SongRow
                key={key(c) + i} candidate={c}
                badge={<span className="badge">new to the grove</span>}
                action={stageButton(c)}
              />
            ))}
          </ul>
        </section>
      )}

      {searched && !searching && local.length === 0 && remote.length === 0 && (
        <Empty title="Nothing found">
          Try fewer words, or the artist name on its own. You can also paste a
          list straight into <Link to="/crate">your crate</Link>.
        </Empty>
      )}

      {!searched && !searching && (
        <Empty title="Search the shared catalogue">
          Everything anyone here has ever added is already searchable, instantly.
          Anything else is looked up from the metadata source.
        </Empty>
      )}
    </>
  )
}
