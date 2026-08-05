// Annotations: what people say about a playlist (F18, §3.4).
//
// The distinction this component has to make legible is D8: none of this
// alters the playlist. Anyone can rate, tag and comment on something shared
// with them; only the owner changes what is in it. So annotations live in
// their own panel, visually separate from the track list, and nothing here
// moves the revision number (BR-3).

import { useEffect, useState } from 'react'
import { ApiError, annotations as api } from '../api/client'
import type { Annotations as Data } from '../api/types'
import { ErrorNote, Loading } from './bits'
import { useToast } from '../router'

export function Annotations({ playlistID }: { playlistID: string }) {
  const toast = useToast()
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [tag, setTag] = useState('')
  const [shared, setShared] = useState(true)
  const [comment, setComment] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    api.get(playlistID, ac.signal)
      .then(setData)
      .catch((err) => {
        if (err instanceof DOMException) return
        setError(err instanceof ApiError ? err.message : 'could not load annotations')
      })
    return () => ac.abort()
  }, [playlistID])

  const run = async (fn: () => Promise<Data>) => {
    setBusy(true)
    try {
      setData(await fn())
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'that did not work', bad: true })
    } finally {
      setBusy(false)
    }
  }

  if (error) return <ErrorNote error={error} />
  if (!data) return <Loading what="Reading what people think…" />

  const { rating, tags, comments } = data

  return (
    <div className="annotations">
      <section className="card">
        <p className="eyebrow">Rating</p>
        <div className="stars" role="group" aria-label="Rate this playlist">
          {[1, 2, 3, 4, 5].map((n) => (
            <button
              key={n} type="button" disabled={busy}
              className={n <= rating.mine ? 'star on' : 'star'}
              aria-label={`${n} out of 5`}
              aria-pressed={n === rating.mine}
              onClick={() => void run(() =>
                // Clicking your current rating withdraws it, which is the only
                // sensible meaning of pressing the button that is already on.
                n === rating.mine ? api.unrate(playlistID) : api.rate(playlistID, n))}
            >
              ★
            </button>
          ))}
          <span className="small muted aggregate">
            {rating.count === 0
              ? 'nobody has rated this yet'
              : `${rating.average.toFixed(1)} from ${rating.count} ${rating.count === 1 ? 'person' : 'people'}`}
          </span>
        </div>
        <p className="small muted">
          Yours is yours. Ratings never change the playlist or its history.
        </p>
      </section>

      <section className="card">
        <p className="eyebrow">Tags</p>
        {tags.length === 0 && <p className="small muted">No tags yet.</p>}
        <ul className="taglist">
          {tags.map((t) => (
            <li key={t.id} className={t.visibility === 'private' ? 'tag private' : 'tag'}>
              <span>{t.name}</span>
              {t.visibility === 'private' && <span className="only-you">only you</span>}
              {t.mine && (
                <button
                  type="button" aria-label={`Remove the tag ${t.name}`} disabled={busy}
                  onClick={() => void api.removeTag(t.id)
                    .then(() => api.get(playlistID))
                    .then(setData)
                    .catch(() => toast({ message: 'could not remove that tag', bad: true }))}
                >
                  ×
                </button>
              )}
            </li>
          ))}
        </ul>

        <form
          className="tag-add"
          onSubmit={(e) => {
            e.preventDefault()
            if (!tag.trim()) return
            void run(() => api.addTag(playlistID, tag.trim(), shared ? 'shared' : 'private'))
              .then(() => setTag(''))
          }}
        >
          <label>
            <span className="lbl">Add a tag</span>
            <input
              type="text" value={tag} onChange={(e) => setTag(e.target.value)}
              placeholder="late night" autoComplete="off"
            />
          </label>
          <label className="check">
            <input type="checkbox" checked={shared} onChange={(e) => setShared(e.target.checked)} />
            <span>Everyone can see it</span>
          </label>
          <div className="row-actions">
            <button type="submit" className="btn sm" disabled={busy || !tag.trim()}>Add</button>
          </div>
        </form>
      </section>

      <section className="card">
        <p className="eyebrow">Comments</p>
        {comments.length === 0 && <p className="small muted">Nothing said yet.</p>}
        <ul className="comments">
          {comments.map((c) => (
            <li key={c.id}>
              <div className="comment-head">
                <span className="who">{c.author}</span>
                <span className="when mono">{new Date(c.created_at).toLocaleDateString()}</span>
                {c.mine && (
                  <button
                    type="button" className="btn sm ghost" disabled={busy}
                    onClick={() => void api.deleteComment(c.id)
                      .then(() => api.get(playlistID))
                      .then(setData)
                      .catch(() => toast({ message: 'could not delete that', bad: true }))}
                  >
                    Delete
                  </button>
                )}
              </div>
              <p className="body">{c.body}</p>
            </li>
          ))}
        </ul>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!comment.trim()) return
            void run(() => api.comment(playlistID, comment.trim())).then(() => setComment(''))
          }}
        >
          <label>
            <span className="lbl">Say something</span>
            <textarea
              value={comment} onChange={(e) => setComment(e.target.value)}
              rows={3} maxLength={2000} placeholder="track 3 is the one"
            />
          </label>
          <div className="row-actions">
            <button type="submit" className="btn sm" disabled={busy || !comment.trim()}>Post</button>
          </div>
        </form>
      </section>
    </div>
  )
}
