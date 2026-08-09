// Small shared pieces. Kept in one file because each is a handful of lines and
// splitting them would cost more navigation than it saves.
//
// No inline style attributes anywhere in this app: the CSP is `default-src
// 'self'` with no 'unsafe-inline', and keeping presentation in the stylesheet
// means that stays true without anyone having to remember why.

import type { InputHTMLAttributes, ReactNode } from 'react'
import type { Candidate, SongRecord } from '../api/types'
import { Ring } from './Ring'

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return <span className="spinner" role="status" aria-label={label} />
}

export function Loading({ what }: { what: string }) {
  return <p className="muted small inline-note"><Spinner /> {what}</p>
}

export function ErrorNote({ error }: { error: string | null }) {
  if (!error) return null
  return <p className="err" role="alert">{error}</p>
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <Ring size={44} title="" />
      <h3>{title}</h3>
      {children && <p className="small">{children}</p>}
    </div>
  )
}

export function Field(
  { label, ...input }: { label: string } & InputHTMLAttributes<HTMLInputElement>,
) {
  return (
    <label>
      <span className="lbl">{label}</span>
      <input {...input} />
    </label>
  )
}

/** mm:ss, or nothing at all when the source did not give us a duration. */
export function duration(ms: number): string {
  if (!ms || ms < 0) return ''
  const total = Math.round(ms / 1000)
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`
}

/** The one-line description of a song, where a full row will not fit. */
export function subtitle(
  x: { artist?: string; album?: string; year?: number; duration_ms?: number },
): string {
  return [x.artist, x.album, x.year || null, duration(x.duration_ms ?? 0) || null]
    .filter(Boolean).join(' · ')
}

/**
 * SongList wraps rows and supplies the column headings.
 *
 * The headings are what make the row a table rather than a run of text — every
 * value under a name, so "1972" is visibly a year and not a duration. On a
 * phone there is no room for a header row, so each cell carries its own label
 * instead (see the CSS); the markup is the same either way.
 */
export function SongList({ children, numbered = false }: { children: ReactNode; numbered?: boolean }) {
  return (
    <ul className={numbered ? 'rows songs numbered' : 'rows songs'}>
      <li className="song-head" aria-hidden="true">
        <span className="c-ring" />
        <span className="c-title">Title</span>
        <span className="c-artist">Artist</span>
        <span className="c-album">Album</span>
        <span className="c-year">Year</span>
        <span className="c-len">Length</span>
        <span className="c-act" />
      </li>
      {children}
    </ul>
  )
}

interface RowProps {
  record?: SongRecord
  candidate?: Candidate
  confidence?: number
  badge?: ReactNode
  action?: ReactNode
}

/**
 * SongRow is one song, however it is identified.
 *
 * Every field is labelled — by the column heading on a wide screen, by its own
 * label on a narrow one. A dotted run of "Nick Drake · Pink Moon · 1972 · 2:08"
 * makes the reader work out which is the album and which is the year, and gets
 * it wrong when a band is named after a year.
 */
export function SongRow({ record, candidate, confidence, badge, action }: RowProps) {
  const src = record ?? candidate ?? {}
  const title = record?.title || candidate?.title || candidate?.raw || 'Unknown track'
  const artist = (record?.artist ?? candidate?.artist) || ''
  const album = (record?.album ?? candidate?.album) || ''
  const year = record?.year ?? candidate?.year ?? 0
  const len = duration(('duration_ms' in src ? src.duration_ms : 0) ?? 0)
  const isrcs = record?.isrcs ?? (candidate?.isrc ? [candidate.isrc] : [])

  return (
    <li>
      <span className="c-ring">
        <Ring size={24} {...(confidence !== undefined ? { confidence } : {})} />
      </span>

      <span className="c-title" data-label="Title">
        <span className="v ti">{title}</span>
        {isrcs.length > 0 && (
          <span className="isrc">
            {isrcs[0]}{isrcs.length > 1 && ` +${isrcs.length - 1}`}
          </span>
        )}
      </span>

      <span className="c-artist" data-label="Artist">
        {artist ? <span className="v">{artist}</span> : <span className="v none">unknown</span>}
      </span>

      <span className="c-album" data-label="Album">
        {album ? <span className="v">{album}</span> : <span className="v none">—</span>}
      </span>

      <span className="c-year" data-label="Year">
        {year ? <span className="v mono">{year}</span> : <span className="v none">—</span>}
      </span>

      <span className="c-len" data-label="Length">
        {len ? <span className="v mono">{len}</span> : <span className="v none">—</span>}
      </span>

      <span className="c-act">
        {badge}
        {action}
      </span>
    </li>
  )
}
