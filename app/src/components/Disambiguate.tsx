// The disambiguation surface (§3.2, F12).
//
// This is the screen the whole resolution ladder exists to feed. The server
// deliberately refuses to guess below the confidence threshold, so the only
// correct behaviour here is to show the user exactly what was ambiguous, offer
// the alternatives the resolver actually found, and let them decide — including
// deciding not to. Nothing on this screen may quietly discard an item.

import { useState } from 'react'
import type { Candidate, Unresolved } from '../api/types'
import { SongRow, subtitle } from './bits'
import { Ring } from './Ring'

interface Props {
  items: Unresolved[]
  /** Called with the alternative the user picked; it goes back down the ladder. */
  onChoose: (chosen: Candidate) => void | Promise<void>
  onDismiss: () => void
}

const METHOD_NOTE: Record<string, string> = {
  fuzzy: 'matched on normalised text only',
  mapper: 'came from the MusicBrainz mapper',
  isrc: 'matched by ISRC',
  mbid: 'matched by MusicBrainz ID',
}

export function Disambiguate({ items, onChoose, onDismiss }: Props) {
  if (items.length === 0) return null
  return (
    <section className="card warn" aria-labelledby="disambig-h">
      <p className="eyebrow">Needs your call</p>
      <h3 id="disambig-h">
        {items.length} {items.length === 1 ? 'track' : 'tracks'} could not be placed
      </h3>
      <p className="small muted">
        Waxgrove will not guess below {Math.round(0.85 * 100)}% confidence. These are
        still here — nothing was dropped.
      </p>
      <ul className="unresolved">
        {items.map((item, i) => (
          <UnresolvedItem key={i} item={item} onChoose={onChoose} />
        ))}
      </ul>
      <div className="row-actions">
        <button type="button" className="btn ghost" onClick={onDismiss}>Deal with it later</button>
      </div>
    </section>
  )
}

function UnresolvedItem({ item, onChoose }: { item: Unresolved; onChoose: Props['onChoose'] }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const alts = item.alternatives ?? []
  const label = item.candidate.title || item.candidate.raw || 'Unknown track'
  const note = item.method ? METHOD_NOTE[item.method] : undefined

  const choose = async (c: Candidate) => {
    setBusy(true)
    try { await onChoose(c) } finally { setBusy(false) }
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
            {Math.round(item.confidence * 100)}% confident
            {note ? ` · ${note}` : ' · no match found'}
          </span>
        </div>
        {alts.length > 0 && (
          <button
            type="button" className="btn sm ghost"
            aria-expanded={open}
            onClick={() => setOpen((v) => !v)}
          >
            {open ? 'Hide' : `${alts.length} option${alts.length === 1 ? '' : 's'}`}
          </button>
        )}
      </div>

      {open && (
        <ul className="rows alts">
          {alts.map((alt, i) => (
            <SongRow
              key={i}
              candidate={alt}
              action={
                <button
                  type="button" className="btn sm" disabled={busy}
                  onClick={() => void choose(alt)}
                >
                  This one
                </button>
              }
            />
          ))}
        </ul>
      )}

      {alts.length === 0 && (
        <p className="small muted">
          Nothing in the grove or at the metadata source looked close enough. Try
          searching for it directly.
        </p>
      )}
    </li>
  )
}
