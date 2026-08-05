// The API client. One place that knows about credentials, error shape, and
// what a 401 means, so no view has to reimplement any of it.

import type {
  AddTracksResponse, Annotations, Candidate, CommitResponse, ConnectStatus,
  CrateItem, CrateResponse, HistoryResponse, ImportResponse, InviteResponse, Job,
  JobsResponse, Playlist, PlaylistsResponse, RecordsResponse, RemoteResponse,
  SharedResponse, User,
} from './types'

/**
 * ApiError carries the server's message, which is always safe to display:
 * internal/httpapi returns generic text and keeps detail in its own logs (§6).
 */
export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
    this.name = 'ApiError'
  }
  /** True when the session is absent or expired. */
  get unauthenticated() { return this.status === 401 }
}

/** Fires when any request comes back 401, so the shell can bounce to login. */
type Listener = () => void
const unauthorizedListeners = new Set<Listener>()
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => { unauthorizedListeners.delete(fn) }
}

interface Options {
  method?: string
  body?: unknown
  signal?: AbortSignal
}

async function request<T>(path: string, opts: Options = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method: opts.method ?? 'GET',
      // The session is an HttpOnly cookie: JavaScript cannot read it, and
      // 'same-origin' is what makes the browser attach it.
      credentials: 'same-origin',
      headers: opts.body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: opts.body === undefined ? null : JSON.stringify(opts.body),
      ...(opts.signal ? { signal: opts.signal } : {}),
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    // Offline, DNS failure, server down — distinct from an API rejection.
    throw new ApiError(0, 'cannot reach Waxgrove — check your connection')
  }

  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn())
    throw new ApiError(401, 'your session has expired')
  }
  if (res.status === 204) return undefined as T

  const text = await res.text()
  let parsed: unknown = null
  if (text) {
    try { parsed = JSON.parse(text) } catch { parsed = null }
  }

  if (!res.ok) {
    const msg =
      parsed && typeof parsed === 'object' && 'error' in parsed && typeof parsed.error === 'string'
        ? parsed.error
        : `request failed (${res.status})`
    throw new ApiError(res.status, msg)
  }
  return parsed as T
}

export const api = {
  // --- auth ------------------------------------------------------------------
  login: (email: string, password: string) =>
    request<User>('/api/login', { method: 'POST', body: { email, password } }),

  register: (body: {
    email: string; display_name: string; password: string; invite_code: string
  }) => request<User>('/api/register', { method: 'POST', body }),

  logout: () => request<void>('/api/logout', { method: 'POST' }),

  me: (signal?: AbortSignal) => request<User>('/api/me', signal ? { signal } : {}),

  createInvite: () => request<InviteResponse>('/api/invites', { method: 'POST' }),

  // --- catalogue -------------------------------------------------------------
  /** Search the shared local catalogue. Ambient records stay out of this (F24). */
  searchRecords: (q: string, signal?: AbortSignal) =>
    request<RecordsResponse>(`/api/records?q=${encodeURIComponent(q)}`, signal ? { signal } : {}),

  /** Search the configured metadata source. Returns an empty set when none is (N6). */
  searchRemote: (q: string, signal?: AbortSignal) =>
    request<RemoteResponse>(`/api/records/remote?q=${encodeURIComponent(q)}`, signal ? { signal } : {}),

  // --- playlists -------------------------------------------------------------
  playlists: () => request<PlaylistsResponse>('/api/playlists'),

  createPlaylist: (title: string, description: string) =>
    request<Playlist>('/api/playlists', { method: 'POST', body: { title, description } }),

  playlist: (id: string, signal?: AbortSignal) =>
    request<Playlist>(`/api/playlists/${encodeURIComponent(id)}`, signal ? { signal } : {}),

  renamePlaylist: (id: string, title: string) =>
    request<Playlist>(`/api/playlists/${encodeURIComponent(id)}`, {
      method: 'PATCH', body: { title },
    }),

  /**
   * Replace the ordering. The list must hold exactly the records the playlist
   * already has — the server answers 409 otherwise rather than recording a
   * change that is not a reorder.
   */
  reorderTracks: (id: string, recordIDs: string[]) =>
    request<Playlist>(`/api/playlists/${encodeURIComponent(id)}/tracks`, {
      method: 'PUT', body: { record_ids: recordIDs },
    }),

  deletePlaylist: (id: string) =>
    request<void>(`/api/playlists/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  addTracks: (id: string, candidates: Candidate[]) =>
    request<AddTracksResponse>(`/api/playlists/${encodeURIComponent(id)}/tracks`, {
      method: 'POST', body: { candidates },
    }),

  removeTrack: (id: string, position: number) =>
    request<Playlist>(
      `/api/playlists/${encodeURIComponent(id)}/tracks/${position}`, { method: 'DELETE' }),

  history: (id: string, signal?: AbortSignal) =>
    request<HistoryResponse>(
      `/api/playlists/${encodeURIComponent(id)}/history`, signal ? { signal } : {}),

  exportURL: (id: string) => `/api/playlists/${encodeURIComponent(id)}/export.jspf`,

  /**
   * Import a JSPF file. The body is the file itself, not JSON-wrapped — the
   * server parses the raw stream under a size cap.
   */
  importJSPF: async (file: File): Promise<ImportResponse> => {
    const res = await fetch('/api/playlists/import', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: file,
    })
    if (res.status === 401) {
      unauthorizedListeners.forEach((fn) => fn())
      throw new ApiError(401, 'your session has expired')
    }
    const parsed = await res.json().catch(() => null)
    if (!res.ok) {
      const msg = parsed && typeof parsed.error === 'string' ? parsed.error : 'import failed'
      throw new ApiError(res.status, msg)
    }
    return parsed as ImportResponse
  },
}

// --- M3: the crate -----------------------------------------------------------

export const crate = {
  list: (signal?: AbortSignal) =>
    request<CrateResponse>('/api/crate', signal ? { signal } : {}),

  /** Stage candidates. Each is run down the resolution ladder on the way in. */
  add: (candidates: Candidate[]) =>
    request<CrateResponse>('/api/crate', { method: 'POST', body: { candidates } }),

  /** Stage a pasted list — one song per line, ideally "Artist — Title". */
  paste: (text: string) =>
    request<CrateResponse>('/api/crate/paste', { method: 'POST', body: { text } }),

  /** Settle an item the resolver would not guess at (F12). */
  resolve: (itemID: string, choice: { record_id?: string; candidate?: Candidate }) =>
    request<CrateItem>(`/api/crate/${encodeURIComponent(itemID)}/resolve`, {
      method: 'POST', body: choice,
    }),

  remove: (itemID: string) =>
    request<void>(`/api/crate/${encodeURIComponent(itemID)}`, { method: 'DELETE' }),

  clear: () => request<void>('/api/crate', { method: 'DELETE' }),

  /** Commit the resolved part. Whatever still needs a decision stays staged. */
  commit: (title: string, description = '') =>
    request<CommitResponse>('/api/crate/commit', {
      method: 'POST', body: { title, description },
    }),
}

// --- M3: annotations and discovery -------------------------------------------

export const annotations = {
  get: (playlistID: string, signal?: AbortSignal) =>
    request<Annotations>(`/api/playlists/${encodeURIComponent(playlistID)}/annotations`,
      signal ? { signal } : {}),

  rate: (playlistID: string, value: number) =>
    request<Annotations>(`/api/playlists/${encodeURIComponent(playlistID)}/rating`, {
      method: 'PUT', body: { value },
    }),

  unrate: (playlistID: string) =>
    request<Annotations>(`/api/playlists/${encodeURIComponent(playlistID)}/rating`,
      { method: 'DELETE' }),

  addTag: (playlistID: string, name: string, visibility: 'private' | 'shared') =>
    request<Annotations>(`/api/playlists/${encodeURIComponent(playlistID)}/tags`, {
      method: 'POST', body: { name, visibility },
    }),

  removeTag: (tagID: string) =>
    request<void>(`/api/tags/${encodeURIComponent(tagID)}`, { method: 'DELETE' }),

  comment: (playlistID: string, body: string) =>
    request<Annotations>(`/api/playlists/${encodeURIComponent(playlistID)}/comments`, {
      method: 'POST', body: { body },
    }),

  deleteComment: (commentID: string) =>
    request<void>(`/api/comments/${encodeURIComponent(commentID)}`, { method: 'DELETE' }),
}

export const discover = {
  shared: (signal?: AbortSignal) =>
    request<SharedResponse>('/api/shared', signal ? { signal } : {}),
}

export const privacy = {
  exportURL: () => '/api/me/export',
  /** Erasure takes the account's own email as confirmation. Not undoable. */
  erase: (confirm: string) =>
    request<void>('/api/me', { method: 'DELETE', body: { confirm } }),
}

// --- M2: streaming connectors ------------------------------------------------

export const connect = {
  /**
   * Connection state. A 404 means this instance runs with no connector at
   * all, which is a supported configuration rather than an error (N6).
   */
  status: async (signal?: AbortSignal): Promise<ConnectStatus | null> => {
    try {
      return await request<ConnectStatus>('/api/connect/spotify', signal ? { signal } : {})
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) return null
      throw err
    }
  },

  /** Store the user's own app (D6). The secret goes in and never comes back. */
  saveApp: (client_id: string, client_secret: string) =>
    request<ConnectStatus>('/api/connect/spotify/app', {
      method: 'PUT', body: { client_id, client_secret },
    }),

  begin: () =>
    request<{ authorize_url: string }>('/api/connect/spotify/begin', { method: 'POST' }),

  disconnect: () => request<void>('/api/connect/spotify', { method: 'DELETE' }),
}

export const jobsApi = {
  /** Import a playlist the user pasted a link to. Returns the job, not a result. */
  importSpotify: (link: string) =>
    request<Job>('/api/import/spotify', { method: 'POST', body: { link } }),

  exportSpotify: (playlistID: string) =>
    request<Job>(`/api/playlists/${encodeURIComponent(playlistID)}/export/spotify`,
      { method: 'POST' }),

  list: (signal?: AbortSignal) =>
    request<JobsResponse>('/api/jobs', signal ? { signal } : {}),

  get: (id: string, signal?: AbortSignal) =>
    request<Job>(`/api/jobs/${encodeURIComponent(id)}`, signal ? { signal } : {}),

  cancel: (id: string) =>
    request<void>(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
}

/** Turn a record into the candidate shape the add-tracks endpoint accepts. */
export function candidateFromRecord(r: {
  mbid: string; title: string; artist: string; album: string
  duration_ms: number; year: number; isrcs: string[] | null
}): Candidate {
  return {
    mbid: r.mbid,
    title: r.title,
    artist: r.artist,
    album: r.album,
    duration_ms: r.duration_ms,
    year: r.year,
    ...(r.isrcs && r.isrcs.length > 0 ? { isrc: r.isrcs[0] } : {}),
  }
}
