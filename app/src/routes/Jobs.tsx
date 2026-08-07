// The job surface (F22).
//
// Provider work is slow and partly fails by nature, so this screen exists to
// make both of those legible rather than surprising: progress while it runs,
// and afterwards the exact list of tracks that did not make it. A playlist that
// silently arrives three songs short is the worst possible outcome (F15).

import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, jobsApi } from '../api/client'
import type { Job } from '../api/types'
import { Empty, ErrorNote, Loading } from '../components/bits'
import { Link, useToast } from '../router'

const KIND_LABEL: Record<string, string> = {
  import: 'Import from Spotify',
  export: 'Export to Spotify',
}

const STATE_LABEL: Record<string, string> = {
  queued: 'Waiting',
  running: 'Working',
  paused: 'Paused',
  done: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

export function Jobs() {
  const toast = useToast()
  const [jobs, setJobs] = useState<Job[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [detail, setDetail] = useState<Job | null>(null)

  const load = useCallback((signal?: AbortSignal) => {
    jobsApi.list(signal)
      .then((r) => { setJobs(r.jobs ?? []); setError(null) })
      .catch((err) => {
        if (err instanceof DOMException) return
        setError(err instanceof ApiError ? err.message : 'could not load jobs')
      })
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])

  // Poll only while something is actually moving. A job surface that keeps
  // requesting after everything has settled is a battery drain for no reason.
  const active = (jobs ?? []).some((j) => !j.terminal)
  const timer = useRef<number | null>(null)
  useEffect(() => {
    if (!active) return
    timer.current = window.setInterval(() => load(), 1500)
    return () => { if (timer.current) window.clearInterval(timer.current) }
  }, [active, load])

  const openDetail = async (id: string) => {
    if (expanded === id) { setExpanded(null); setDetail(null); return }
    setExpanded(id)
    setDetail(null)
    try {
      setDetail(await jobsApi.get(id))
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not load that job', bad: true })
    }
  }

  const cancel = async (id: string) => {
    try {
      await jobsApi.cancel(id)
      load()
      toast({ message: 'Cancelled.' })
    } catch (err) {
      toast({ message: err instanceof ApiError ? err.message : 'could not cancel', bad: true })
    }
  }

  return (
    <>
      <p className="eyebrow">Transfers</p>
      <h2>Jobs</h2>
      <p className="small muted">
        Moving a playlist to or from a streaming service takes a while and can
        partly fail — a song licensed in one country is often missing in
        another. Everything that did not make it is listed here.
      </p>

      <ErrorNote error={error} />
      {jobs === null && !error && <Loading what="Reading the queue…" />}

      {jobs !== null && jobs.length === 0 && (
        <Empty title="Nothing has run yet">
          Import a Spotify playlist from the Grove, or export one from a
          playlist page.
        </Empty>
      )}

      {jobs !== null && jobs.length > 0 && (
        <ul className="jobs">
          {jobs.map((j) => (
            <li key={j.id} className={`job ${j.state}`}>
              <div className="job-head">
                <span className="meta">
                  <span className="ti">{KIND_LABEL[j.kind] ?? j.kind}</span>
                  <span className="ar">
                    {STATE_LABEL[j.state] ?? j.state}
                    {j.total > 0 && ` · ${j.done} of ${j.total}`}
                    {' · '}{new Date(j.updated_at).toLocaleString()}
                  </span>
                </span>
                {!j.terminal && (
                  <button type="button" className="btn sm ghost" onClick={() => void cancel(j.id)}>
                    Cancel
                  </button>
                )}
                {j.terminal && (
                  <button
                    type="button" className="btn sm ghost"
                    aria-expanded={expanded === j.id}
                    onClick={() => void openDetail(j.id)}
                  >
                    {expanded === j.id ? 'Hide' : 'Details'}
                  </button>
                )}
              </div>

              {j.total > 0 && !j.terminal && (
                <progress className="bar" value={j.done} max={j.total}>
                  {j.done} of {j.total}
                </progress>
              )}

              {j.error && <p className="err">{j.error}</p>}

              {j.state === 'done' && j.playlist_id && (
                <p className="small">
                  <Link to={`/playlists/${j.playlist_id}`}>Open the playlist</Link>
                </p>
              )}

              {expanded === j.id && (
                detail === null
                  ? <Loading what="Reading the outcome…" />
                  : <JobDetail job={detail} />
              )}
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

const PROBLEM_LABEL: Record<string, string> = {
  unavailable: 'Not on Spotify here',
  unresolved: 'Could not be identified',
  failed: 'Failed',
}

function JobDetail({ job }: { job: Job }) {
  const problems = job.problems ?? []

  // A job that failed before it processed anything has no per-track outcomes,
  // and saying "everything made it across" there is the opposite of the truth.
  // The error above already explains what happened; this pane should not
  // contradict it.
  if (job.state === 'failed' && job.total === 0 && problems.length === 0) {
    return (
      <div className="job-detail">
        <p className="small muted">
          This stopped before any track was looked at, so there is nothing
          per-track to report — the reason is above.
        </p>
      </div>
    )
  }
  if (job.state === 'cancelled' && problems.length === 0) {
    return (
      <div className="job-detail">
        <p className="small muted">
          Cancelled after {job.succeeded ?? 0} of {job.total}.
        </p>
      </div>
    )
  }

  return (
    <div className="job-detail">
      <p className="small muted">
        {job.succeeded ?? 0} of {job.total} went through
        {problems.length > 0 && ` · ${problems.length} did not`}
        {job.storefront && ` · resolved against ${job.storefront}`}
      </p>
      {problems.length === 0 ? (
        <p className="small">Everything made it across.</p>
      ) : (
        <ul className="rows">
          {problems.map((p) => (
            <li key={p.position}>
              <span className="pos mono">{String(p.position + 1).padStart(2, '0')}</span>
              <span className="meta">
                <span className="ti">{p.detail}</span>
                <span className="ar">{PROBLEM_LABEL[p.status] ?? p.status}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
