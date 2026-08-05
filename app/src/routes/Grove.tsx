// Search: the shared catalogue first, the metadata source second.
//
// The ordering is the point (§3.0). Everything the group has ever resolved is
// already local and free to search; the network is only consulted for what the
// grove has not seen. An instance with no metadata source configured is a
// normal, supported state, not an error — so that case says so plainly (N6).

import { useEffect, useRef, useState } from 'react'
import { api, ApiError, candidateFromRecord } from '../api/client'
import type { Candidate, Playlist, SongRecord } from '../api/types'
import { Empty, ErrorNote, Loading, SongRow } from '../components/bits'
import { Disambiguate } from '../components/Disambiguate'
import type { Unresolved } from '../api/types'
import { useToast } from '../router'

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

  // Staged picks. Deliberately session-scoped for M1: the persistent crate
  // needs server storage that does not exist yet, and a crate that silently
  // lived only in one browser would be worse than none.
  const [staged, setStaged] = useState<Candidate[]>([])

  const [playlists, setPlaylists] = useState<Playlist[]>([])
  const [target, setTarget] = useState('')
  const [adding, setAdding] = useState(false)
  const [unresolved, setUnresolved] = useState<Unresolved[]>([])

  useEffect(() => {
    api.playlists()
      .then((r) => setPlaylists(r.playlists ?? []))
      .catch(() => { /* the picker just stays empty; search still works */ })
  }, [])

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

  const stage = (c: Candidate) => {
    setStaged((prev) => prev.some((p) => key(p) === key(c)) ? prev : [...prev, c])
  }
  const unstage = (c: Candidate) => {
    setStaged((prev) => prev.filter((p) => key(p) !== key(c)))
  }
  const isStaged = (c: Candidate) => staged.some((p) => key(p) === key(c))

  const addStaged = async (candidates: Candidate[] = staged) => {
    if (!target || candidates.length === 0) return
    setAdding(true)
    try {
      const res = await api.addTracks(target, candidates)
      // Only the items that actually landed leave the staging list; anything
      // unresolved stays put so it cannot be lost by being "added" (BR-5).
      const stillUnresolved = new Set((res.unresolved ?? []).map((u) => key(u.candidate)))
      setStaged((prev) => prev.filter((c) => stillUnresolved.has(key(c))))
      setUnresolved(res.unresolved ?? [])
      const name = playlists.find((p) => p.id === target)?.title ?? 'the playlist'
      toast({ message: `Added ${res.added} to ${name}.` })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not add tracks', bad: true })
    } finally {
      setAdding(false)
    }
  }

  return (
    <>
      <p className="eyebrow">The grove</p>
      <h2>Find a song</h2>

      <input
        type="search" value={q} onChange={(e) => setQ(e.target.value)}
        placeholder="Title, artist, or ISRC" aria-label="Search for a song"
        autoComplete="off" className="search-input"
      />

      {staged.length > 0 && (
        <section className="card good staged" aria-label="Staged songs">
          <p className="eyebrow">Staged · {staged.length}</p>
          <ul className="rows">
            {staged.map((c) => (
              <SongRow
                key={key(c)} candidate={c}
                action={
                  <button type="button" className="btn sm ghost" onClick={() => unstage(c)}>
                    Remove
                  </button>
                }
              />
            ))}
          </ul>
          <div className="row-actions">
            <label className="picker">
              <span className="lbl">Add to</span>
              <select value={target} onChange={(e) => setTarget(e.target.value)}>
                <option value="">Choose a playlist…</option>
                {playlists.map((p) => (
                  <option key={p.id} value={p.id}>{p.title}</option>
                ))}
              </select>
            </label>
            <button
              type="button" className="btn"
              disabled={!target || adding}
              onClick={() => void addStaged()}
            >
              {adding ? 'Adding…' : `Add ${staged.length}`}
            </button>
          </div>
          {playlists.length === 0 && (
            <p className="small muted">
              You have no playlists yet — make one first and these will still be here.
            </p>
          )}
        </section>
      )}

      {unresolved.length > 0 && (
        <Disambiguate
          items={unresolved}
          onChoose={async (chosen) => { await addStaged([chosen]) }}
          onDismiss={() => setUnresolved([])}
        />
      )}

      <ErrorNote error={error} />
      {searching && <Loading what="Searching the grove…" />}

      {local.length > 0 && (
        <section className="card">
          <p className="eyebrow">In the grove · {local.length}</p>
          <ul className="rows">
            {local.map((r) => {
              const c = candidateFromRecord(r)
              return (
                <SongRow
                  key={r.id} record={r} confidence={1}
                  badge={<span className="badge g">local</span>}
                  action={
                    <button
                      type="button" className={isStaged(c) ? 'btn sm ghost' : 'btn sm'}
                      onClick={() => isStaged(c) ? unstage(c) : stage(c)}
                    >
                      {isStaged(c) ? 'Staged' : 'Stage'}
                    </button>
                  }
                />
              )
            })}
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
                action={
                  <button
                    type="button" className={isStaged(c) ? 'btn sm ghost' : 'btn sm'}
                    onClick={() => isStaged(c) ? unstage(c) : stage(c)}
                  >
                    {isStaged(c) ? 'Staged' : 'Stage'}
                  </button>
                }
              />
            ))}
          </ul>
        </section>
      )}

      {searched && !searching && local.length === 0 && remote.length === 0 && (
        <Empty title="Nothing found">
          Try fewer words, or the artist name on its own.
        </Empty>
      )}

      {!searched && !searching && staged.length === 0 && (
        <Empty title="Search the shared catalogue">
          Everything anyone here has ever added is already searchable, instantly.
          Anything else is looked up from the metadata source.
        </Empty>
      )}
    </>
  )
}
