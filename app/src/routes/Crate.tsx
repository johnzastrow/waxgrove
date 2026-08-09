// The crate (F16, §3.3) — the persistent staging area.
//
// This is where "build a playlist from anything" actually happens: songs
// accumulate here from search, from a paste, from wherever, over as long as it
// takes, and commit as one playlist.
//
// The screen's real job is the items that need a decision. Everything resolved
// can be scrolled past; what matters is the handful the resolver refused to
// guess at, because settling those *before* the playlist exists is the whole
// point of F12.

import { useCallback, useEffect, useState } from 'react'
import { ApiError, api, crate } from '../api/client'
import type { CrateItem, Candidate, SongRecord } from '../api/types'
import { Empty, ErrorNote, Loading, SongList, SongRow, subtitle } from '../components/bits'
import { Ring } from '../components/Ring'
import { navigate, useToast } from '../router'

export function Crate() {
  const toast = useToast()
  const [items, setItems] = useState<CrateItem[] | null>(null)
  const [needs, setNeeds] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [title, setTitle] = useState('')
  const [paste, setPaste] = useState('')
  const [showPaste, setShowPaste] = useState(false)

  const apply = useCallback((r: { items: CrateItem[]; needs_decision: number }) => {
    setItems(r.items ?? [])
    setNeeds(r.needs_decision ?? 0)
    setError(null)
  }, [])

  const load = useCallback((signal?: AbortSignal) => {
    crate.list(signal)
      .then(apply)
      .catch((err) => {
        if (err instanceof DOMException) return
        setError(err instanceof ApiError ? err.message : 'could not read your crate')
      })
  }, [apply])

  useEffect(() => {
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])

  const stagePaste = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      apply(await crate.paste(paste))
      setPaste('')
      setShowPaste(false)
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not read that list', bad: true })
    } finally {
      setBusy(false)
    }
  }

  const remove = async (id: string) => {
    try {
      await crate.remove(id)
      load()
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not remove', bad: true })
    }
  }

  const commit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const res = await crate.commit(title.trim())
      setTitle('')
      load()
      toast({
        message: res.left_in_crate > 0
          // Said plainly, rather than leaving the user to notice their crate
          // is not empty and wonder why.
          ? `Made "${res.playlist.title}". ${res.left_in_crate} still need a decision and stayed in your crate.`
          : `Made "${res.playlist.title}".`,
      })
      navigate(`/playlists/${res.playlist.id}`)
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not commit', bad: true })
    } finally {
      setBusy(false)
    }
  }

  const empty = items !== null && items.length === 0
  const resolved = (items ?? []).filter((i) => i.status === 'resolved')
  const undecided = (items ?? []).filter((i) => i.status !== 'resolved')

  return (
    <>
      <p className="eyebrow">Staging</p>
      <h2>Your crate</h2>
      <p className="small muted">
        Songs collect here from anywhere — search, a pasted list, an import —
        for as long as you like. Commit them and they become one playlist.
      </p>

      <div className="row-actions">
        <button type="button" className="btn ghost" onClick={() => setShowPaste((v) => !v)}>
          {showPaste ? 'Cancel' : 'Paste a list'}
        </button>
        {items !== null && items.length > 0 && (
          <button
            type="button" className="btn danger" disabled={busy}
            onClick={() => {
              if (window.confirm('Empty your crate? Nothing committed is affected.')) {
                void crate.clear().then(() => load())
              }
            }}
          >
            Empty it
          </button>
        )}
      </div>

      {showPaste && (
        <form className="card" onSubmit={stagePaste}>
          <label>
            <span className="lbl">One song per line</span>
            <textarea
              value={paste} onChange={(e) => setPaste(e.target.value)}
              rows={6} autoFocus
              placeholder={'Nick Drake — Pink Moon\nFleetwood Mac — Dreams'}
            />
          </label>
          <p className="small muted">
            "Artist — Title" reads best, but a hyphen, a pipe or a tab all work,
            and track numbers are stripped. Anything Waxgrove cannot split it
            leaves alone rather than guessing.
          </p>
          <div className="row-actions">
            <button type="submit" className="btn" disabled={busy || !paste.trim()}>
              Add to crate
            </button>
          </div>
        </form>
      )}

      <ErrorNote error={error} />
      {items === null && !error && <Loading what="Opening the crate…" />}

      {empty && (
        <Empty title="Your crate is empty">
          Search the grove and stage a few songs, or paste a list.
        </Empty>
      )}

      {undecided.length > 0 && (
        <section className="card warn">
          <p className="eyebrow">Needs your call · {needs}</p>
          <h3>Waxgrove will not guess these</h3>
          <p className="small muted">
            Settle them here, before the playlist exists — it is much easier
            than fixing one you have already shared.
          </p>
          <ul className="unresolved">
            {undecided.map((item) => (
              <Undecided key={item.id} item={item} onDone={load} onRemove={remove} />
            ))}
          </ul>
        </section>
      )}

      {resolved.length > 0 && (
        <section className="card good">
          <p className="eyebrow">Ready · {resolved.length}</p>
          <SongList>
            {resolved.map((item) => (
              <SongRow
                key={item.id}
                {...(item.record ? { record: item.record } : { candidate: item.candidate })}
                confidence={item.confidence}
                action={
                  <button type="button" className="btn sm ghost" onClick={() => void remove(item.id)}>
                    Remove
                  </button>
                }
              />
            ))}
          </SongList>

          <form className="commit" onSubmit={commit}>
            <label>
              <span className="lbl">Make a playlist from the ready ones</span>
              <input
                type="text" value={title} onChange={(e) => setTitle(e.target.value)}
                placeholder="Sunday morning, slow" required
              />
            </label>
            <div className="row-actions">
              <button type="submit" className="btn" disabled={busy || !title.trim()}>
                Commit {resolved.length}
              </button>
            </div>
          </form>
        </section>
      )}
    </>
  )
}

// Undecided is one item waiting on a human (F12).
function Undecided({ item, onDone, onRemove }: {
  item: CrateItem
  onDone: () => void
  onRemove: (id: string) => void
}) {
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [results, setResults] = useState<SongRecord[] | null>(null)
  const [busy, setBusy] = useState(false)

  const label = item.candidate.title || item.candidate.raw || 'Unknown track'

  const search = async () => {
    setOpen(true)
    if (results !== null) return
    const q = [item.candidate.artist, item.candidate.title].filter(Boolean).join(' ')
    try {
      const [local, remote] = await Promise.all([
        api.searchRecords(q),
        api.searchRemote(q).catch(() => ({ candidates: [] })),
      ])
      setResults(local.records ?? [])
      setRemote(remote.candidates ?? [])
    } catch {
      setResults([])
    }
  }
  const [remote, setRemote] = useState<Candidate[]>([])

  const choose = async (choice: { record_id?: string; candidate?: Candidate }) => {
    setBusy(true)
    try {
      await crate.resolve(item.id, choice)
      onDone()
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not settle that', bad: true })
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="unresolved-item">
      <div className="unresolved-head">
        <Ring size={26} confidence={item.confidence} />
        <div className="meta">
          <span className="ti">{label}</span>
          <span className="ar">
            {subtitle(item.candidate) || <em className="muted">nothing but the raw text</em>}
          </span>
          <span className="isrc">
            {item.status === 'unresolved'
              ? 'no match found'
              : `${Math.round(item.confidence * 100)}% confident — not enough to apply`}
          </span>
        </div>
        <button type="button" className="btn sm ghost" onClick={() => void search()}>
          Find it
        </button>
        <button type="button" className="btn sm ghost" onClick={() => onRemove(item.id)}>
          Drop
        </button>
      </div>

      {open && (
        results === null ? <Loading what="Looking…" /> : (
          <SongList>
            {results.map((r) => (
              <SongRow
                key={r.id} record={r} confidence={1}
                action={
                  <button type="button" className="btn sm" disabled={busy}
                          onClick={() => void choose({ record_id: r.id })}>
                    This one
                  </button>
                }
              />
            ))}
            {remote.map((c, i) => (
              <SongRow
                key={`r${i}`} candidate={c}
                badge={<span className="badge">new to the grove</span>}
                action={
                  <button type="button" className="btn sm" disabled={busy}
                          onClick={() => void choose({ candidate: c })}>
                    This one
                  </button>
                }
              />
            ))}
            {results.length === 0 && remote.length === 0 && (
              <li><span className="small muted">Nothing found. Try the Grove search directly.</span></li>
            )}
          </SongList>
        )
      )}
    </li>
  )
}
