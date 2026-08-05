// Wire types. These mirror the view functions in internal/httpapi/api.go —
// userView, recordView, playlistView — and domain.Candidate's JSON tags.
//
// They are hand-maintained rather than generated: the view layer is deliberately
// small and explicit on the Go side, and a generator would be more machinery
// than it saves. When a view function changes, change the matching type here.

export type Role = 'member' | 'admin'

export interface User {
  id: string
  email: string
  display_name: string
  role: Role
}

/**
 * A canonical song. Identity is the MBID; ISRCs are a set (BR-1).
 * Mirrors domain.Record. Named SongRecord, not Record, so it does not shadow
 * the built-in TS Record<K, V> utility type wherever it is imported.
 */
export interface SongRecord {
  id: string
  mbid: string
  title: string
  artist: string
  album: string
  duration_ms: number
  year: number
  isrcs: string[] | null
}

/** An unresolved song reference, from any source. Mirrors domain.Candidate. */
export interface Candidate {
  title?: string
  artist?: string
  album?: string
  duration_ms?: number
  isrc?: string
  mbid?: string
  year?: number
  raw?: string
  source_ref?: string
}

export interface Track {
  position: number
  record: SongRecord
  added_in_rev: number
}

export interface Playlist {
  id: string
  title: string
  description: string
  owner_id: string
  revision: number
  tracks: Track[]
}

export type MatchMethod = 'isrc' | 'mbid' | 'mapper' | 'fuzzy' | ''

/**
 * A candidate the resolver refused to place. Never dropped, never guessed —
 * the UI must offer the alternatives rather than pick one (§3.2, BR-5).
 */
export interface Unresolved {
  candidate: Candidate
  method?: MatchMethod
  confidence: number
  alternatives: Candidate[] | null
}

export type RevisionOp = 'create' | 'add' | 'remove' | 'reorder' | 'rename'

export interface Revision {
  rev: number
  op: RevisionOp
  /** Display name, or "a departed member" once the author was erased (BR-4). */
  actor: string
  detail: string
  created_at: string
}

// --- response envelopes ------------------------------------------------------

export interface RecordsResponse {
  records: SongRecord[]
}

export interface RemoteResponse {
  candidates: Candidate[] | null
  /** Present when the instance runs with no metadata source at all (N6). */
  note?: string
}

export interface PlaylistsResponse {
  playlists: Playlist[]
}

export interface AddTracksResponse {
  playlist: Playlist
  added: number
  unresolved: Unresolved[]
}

export interface ImportResponse {
  playlist: Playlist
  imported: number
  unresolved: Unresolved[]
}

export interface HistoryResponse {
  revisions: Revision[]
}

export interface InviteResponse {
  code: string
  expires_in: number
}

/** The confidence at or above which the server applies a match unattended. */
export const CONFIDENCE_THRESHOLD = 0.85
