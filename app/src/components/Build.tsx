// Which build this is.
//
// Shown on the sign-in screen as well as inside the app: "which version am I
// looking at" is the first question when something behaves unexpectedly, and
// needing to sign in to answer it makes it useless during a bad deploy.

import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { VersionInfo } from '../api/types'

export function Build({ detailed = false }: { detailed?: boolean }) {
  const [info, setInfo] = useState<VersionInfo | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    // A version we cannot read is not worth an error message; the rest of the
    // screen is more important than this line.
    api.version(ac.signal).then(setInfo).catch(() => {})
    return () => ac.abort()
  }, [])

  if (!info) return null

  if (!detailed) {
    return <span className="build mono">v{info.full}</span>
  }

  return (
    <dl className="facts">
      <dt>Version</dt><dd className="mono">{info.version}</dd>
      {info.commit && (<><dt>Build</dt><dd className="mono">{info.commit}</dd></>)}
      {info.built_at && (
        <><dt>Built</dt><dd className="mono">{new Date(info.built_at).toLocaleString()}</dd></>
      )}
    </dl>
  )
}
