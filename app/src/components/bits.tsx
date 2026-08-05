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

/** The one-line description of a song, used for both records and candidates. */
export function subtitle(
  x: { artist?: string; album?: string; year?: number; duration_ms?: number },
): string {
  return [x.artist, x.album, x.year || null, duration(x.duration_ms ?? 0) || null]
    .filter(Boolean).join(' · ')
}

interface RowProps {
  record?: SongRecord
  candidate?: Candidate
  confidence?: number
  badge?: ReactNode
  action?: ReactNode
}

/** One song, however it is identified. The ring carries confidence when known. */
export function SongRow({ record, candidate, confidence, badge, action }: RowProps) {
  const title = record?.title || candidate?.title || candidate?.raw || 'Unknown track'
  const meta = record
    ? subtitle({
        artist: record.artist, album: record.album,
        year: record.year, duration_ms: record.duration_ms,
      })
    : subtitle(candidate ?? {})
  const isrcs = record?.isrcs ?? (candidate?.isrc ? [candidate.isrc] : [])

  return (
    <li>
      <Ring size={24} {...(confidence !== undefined ? { confidence } : {})} />
      <span className="meta">
        <span className="ti">{title}</span>
        <span className="ar">{meta || <em className="muted">no artist given</em>}</span>
        {isrcs.length > 0 && (
          <span className="isrc">
            {isrcs[0]}{isrcs.length > 1 && ` +${isrcs.length - 1} more`}
          </span>
        )}
      </span>
      {badge}
      {action}
    </li>
  )
}
