// The brand mark doing double duty as the match-confidence indicator.
//
// Concentric rings read as record grooves and as tree rings; here the outermost
// groove is also an arc showing how sure the resolver is. Above the threshold
// it closes into a full green ring, below it stays an open copper arc — so
// "this needs a human" is visible at a glance, at any size, without a legend.

import { CONFIDENCE_THRESHOLD } from '../api/types'

interface Props {
  /** 0..1. Omit for the plain mark with no confidence reading. */
  confidence?: number
  size?: number
  title?: string
}

export function Ring({ confidence, size = 26, title }: Props) {
  const r = 11
  const circumference = 2 * Math.PI * r
  const conf = confidence ?? null
  const certain = conf !== null && conf >= CONFIDENCE_THRESHOLD
  const stroke = conf === null ? '#7A9670' : certain ? '#7A9670' : '#C07B45'
  const label = title ?? (conf === null ? 'Waxgrove' : `${Math.round(conf * 100)}% confident`)

  return (
    <svg
      width={size} height={size} viewBox="0 0 28 28"
      role="img" aria-label={label} className="ring"
    >
      <title>{label}</title>
      {/* Inner grooves: constant, they are the mark itself. */}
      <circle cx="14" cy="14" r="3"   fill="none" stroke="#31362B" strokeWidth="1.4" />
      <circle cx="14" cy="14" r="6.5" fill="none" stroke="#31362B" strokeWidth="1.4" />
      {/* Outer groove: the track, then the arc. */}
      <circle cx="14" cy="14" r={r} fill="none" stroke="#242820" strokeWidth="2" />
      {conf !== null && (
        <circle
          cx="14" cy="14" r={r} fill="none" stroke={stroke} strokeWidth="2"
          strokeLinecap="round"
          strokeDasharray={`${circumference * Math.max(0, Math.min(1, conf))} ${circumference}`}
          transform="rotate(-90 14 14)"
        />
      )}
      {conf === null && (
        <circle cx="14" cy="14" r={r} fill="none" stroke={stroke} strokeWidth="2" />
      )}
      <circle cx="14" cy="14" r="1.2" fill={stroke} />
    </svg>
  )
}
