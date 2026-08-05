// The playlists you own, plus the two doors in and out: create, and JSPF import.

import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Playlist } from '../api/types'
import { Empty, ErrorNote, Loading } from '../components/bits'
import { Link, useToast } from '../router'
import { Ring } from '../components/Ring'

export function Playlists() {
  const toast = useToast()
  const [items, setItems] = useState<Playlist[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const load = () => {
    api.playlists()
      .then((r) => { setItems(r.playlists ?? []); setError(null) })
      .catch((err) => setError(err instanceof ApiError ? err.message : 'could not load playlists'))
  }
  useEffect(load, [])

  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!title.trim()) return
    setBusy(true)
    try {
      const p = await api.createPlaylist(title.trim(), '')
      setTitle('')
      setItems((prev) => [p, ...(prev ?? [])])
      toast({ message: `Created "${p.title}".` })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not create', bad: true })
    } finally {
      setBusy(false)
    }
  }

  const importFile = async (file: File) => {
    setBusy(true)
    try {
      const res = await api.importJSPF(file)
      load()
      const missed = res.unresolved?.length ?? 0
      toast({
        message: missed === 0
          ? `Imported ${res.imported} tracks into "${res.playlist.title}".`
          : `Imported ${res.imported}; ${missed} need a decision inside the playlist.`,
      })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'import failed', bad: true })
    } finally {
      setBusy(false)
      if (fileInput.current) fileInput.current.value = ''
    }
  }

  return (
    <>
      <p className="eyebrow">Your shelf</p>
      <h2>Playlists</h2>

      <form className="card newlist" onSubmit={create}>
        <label>
          <span className="lbl">New playlist</span>
          <input
            type="text" value={title} onChange={(e) => setTitle(e.target.value)}
            placeholder="Sunday morning, slow" aria-label="New playlist title"
          />
        </label>
        <div className="row-actions">
          <button type="submit" className="btn" disabled={busy || !title.trim()}>Create</button>
          <button
            type="button" className="btn ghost" disabled={busy}
            onClick={() => fileInput.current?.click()}
          >
            Import JSPF
          </button>
          <input
            ref={fileInput} type="file" accept=".jspf,application/json,application/jspf+json"
            className="hidden-input" tabIndex={-1} aria-hidden="true"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) void importFile(f)
            }}
          />
        </div>
      </form>

      <ErrorNote error={error} />
      {items === null && !error && <Loading what="Reading the shelf…" />}

      {items !== null && items.length === 0 && (
        <Empty title="Nothing on the shelf yet">
          Make a playlist above, or import one you exported from another Waxgrove.
        </Empty>
      )}

      {items !== null && items.length > 0 && (
        <ul className="shelf">
          {items.map((p) => (
            <li key={p.id}>
              <Link to={`/playlists/${p.id}`} className="shelf-item">
                <Ring size={30} title="" />
                <span className="meta">
                  <span className="ti">{p.title}</span>
                  <span className="ar">
                    {p.tracks?.length ?? 0} {(p.tracks?.length ?? 0) === 1 ? 'track' : 'tracks'}
                    {' · rev '}{p.revision}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
