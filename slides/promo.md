---
theme: default
title: Waxgrove
info: Sonic Unwalled Garden Library Exchange — share playlists across streaming services.
class: text-left
transition: fade
mdc: true
# Static hosting has no URL rewrites: GitHub Pages serves its own 404 for
# unknown paths and only honours a 404.html at the SITE root, not a nested one.
# Hash routing keeps deep links working without any server rewrite.
routerMode: hash
fonts:
  provider: none
---

<div class="flex items-center gap-10">
  <img src="/mark.svg" class="w-40" alt="Waxgrove mark" />
  <div>
    <h1 class="text-7xl">Wax<span style="color:var(--grove-300)">grove</span></h1>
    <div class="eyebrow mt-4">Sonic Unwalled Garden Library Exchange</div>
    <p class="dim mt-4 text-lg">A record collection that grows in the open.</p>
  </div>
</div>

---

<div class="eyebrow">The problem</div>

# Your playlist isn't a list of songs

It's a list of **Spotify URIs**.

<div class="mt-8 card warn">
Move to another service and it doesn't move with you. Not because the songs are
gone — because the <em>names</em> for them were never yours.
</div>

<div class="mt-8 dim">
Streaming services are walled gardens for one structural reason: a song's identity
<strong>is</strong> a provider ID.
</div>

---

<div class="eyebrow">The friction</div>

# Ana has Spotify. Ben has Apple Music.

<div class="grid3 mt-10">
  <div class="card">
    <h4>Today</h4>
    <p class="text-sm">Ana screenshots the playlist. Ben retypes 45 track names. Three are
    wrong. Nobody does this twice.</p>
  </div>
  <div class="card">
    <h4>Or</h4>
    <p class="text-sm">A third-party service wants both their passwords, and sells the
    listening data.</p>
  </div>
  <div class="card good">
    <h4>Or — Waxgrove</h4>
    <p class="text-sm">Ana shares. Ben taps sync. Neither ever touches the other's
    service.</p>
  </div>
</div>

---

<div class="eyebrow">The idea</div>

# Store songs by what they *are*

<div class="grid2 mt-8">
  <div class="card good">
    <h4>Canonical identity</h4>
    <p class="text-sm"><strong>ISRC</strong> — the recording's real-world standard code.<br>
    <strong>MusicBrainz ID</strong> — the open music encyclopedia's identifier.</p>
    <p class="text-sm dim mt-3">Both are platform-neutral. Neither belongs to a company
    that can revoke them.</p>
  </div>
  <div class="card">
    <h4>Provider IDs</h4>
    <p class="text-sm">A Spotify URI is a <em>cache</em>, hanging off the record.</p>
    <p class="text-sm dim mt-3">A record whose Spotify link dies is still a perfectly
    good record. That inversion is the whole product.</p>
  </div>
</div>

<div class="mt-8 dim text-sm">
Sharing a playlist means exchanging canonical records. The recipient resolves them
against whatever <em>they</em> use. Nobody's platform is privileged.
</div>

---

<div class="eyebrow">In practice</div>

# Two friends, two services, one playlist

<div class="mt-6 grid3 items-center">
  <div class="card text-center">
    <div style="font-family:Fraunces,serif;font-size:1.5rem;color:var(--cream-100)">Ana</div>
    <div class="mono text-xs mt-1" style="color:var(--copper)">SPOTIFY · ATLANTA</div>
    <p class="text-xs dim mt-3">Copies a link. Once.</p>
  </div>
  <div class="card good text-center">
    <img src="/mark.svg" class="w-12 mx-auto mb-2" alt="" />
    <div style="font-family:Fraunces,serif;font-size:1.5rem;color:var(--cream-100)">Waxgrove</div>
    <p class="text-xs dim mt-3">Matches 45 songs by ISRC. Exactly — no guessing.</p>
  </div>
  <div class="card text-center">
    <div style="font-family:Fraunces,serif;font-size:1.5rem;color:var(--cream-100)">Ben</div>
    <div class="mono text-xs mt-1" style="color:var(--copper)">APPLE MUSIC · MAINE</div>
    <p class="text-xs dim mt-3">Taps sync. Plays it.</p>
  </div>
</div>

<div class="mt-8 flex items-center gap-8">
  <div class="big">2</div>
  <p class="text-sm">Times either of them leaves Waxgrove across the entire round trip.
  <span class="dim">Copy a link once; tap Add to Library once. No exports, no CSV files,
  no retyping.</span></p>
</div>

---

<div class="eyebrow">The hard part, done properly</div>

# It never guesses

<div class="grid2 mt-8">
  <div>
    <p class="text-sm">Matching songs across catalogues is where tools like this live or die.
    Waxgrove scores every match and shows its working.</p>
    <p class="text-sm mt-4">Below the confidence threshold it <strong>asks</strong> rather than
    picking. An unmatched line stays as text — it is never silently dropped, and never
    silently wrong.</p>
    <p class="text-sm mt-4 dim">Three tracks didn't transfer? You're told which three,
    and why.</p>
  </div>
  <div class="card">
    <h4>The mark is the mechanic</h4>
    <div class="flex gap-6 items-center justify-center my-4">
      <div class="text-center">
        <svg width="52" height="52" viewBox="0 0 100 100" fill="none">
          <circle cx="50" cy="50" r="42" stroke="#7A9670" stroke-width="6"/>
          <circle cx="50" cy="50" r="26" stroke="#7A9670" stroke-width="6"/>
          <circle cx="50" cy="50" r="5" fill="#7A9670"/></svg>
        <div class="mono text-[9px] mt-1 dim">EXACT</div>
      </div>
      <div class="text-center">
        <svg width="52" height="52" viewBox="0 0 100 100" fill="none">
          <circle cx="50" cy="50" r="42" stroke="#C9A227" stroke-width="6" stroke-dasharray="120 60"/>
          <circle cx="50" cy="50" r="26" stroke="#5c5230" stroke-width="6" stroke-dasharray="60 44"/>
          <circle cx="50" cy="50" r="5" fill="#C9A227"/></svg>
        <div class="mono text-[9px] mt-1 dim">UNSURE</div>
      </div>
      <div class="text-center">
        <svg width="52" height="52" viewBox="0 0 100 100" fill="none">
          <circle cx="50" cy="50" r="42" stroke="#A8512A" stroke-width="6" stroke-dasharray="34 96"/>
          <circle cx="50" cy="50" r="5" fill="#A8512A"/></svg>
        <div class="mono text-[9px] mt-1 dim">NO MATCH</div>
      </div>
    </div>
    <p class="text-xs dim">Grooves and tree rings are the same shape. Complete rings mean a
    certain match; fractured rings mean it needs you.</p>
  </div>
</div>

---

<div class="eyebrow">Yours, actually</div>

# Self-hosted, and cheap to run

<div class="grid2 mt-8">
  <div class="card good">
    <h4>Runs on</h4>
    <p class="text-sm">A Raspberry Pi. A $5 VPS. One static binary, or <code>docker run</code>.</p>
    <p class="text-sm dim mt-2">No account, no cloud, no company between you and your friends.</p>
  </div>
  <div class="card">
    <h4>Stores</h4>
    <p class="text-sm"><strong>Metadata only.</strong> Never audio files — nothing to host,
    nothing to take down.</p>
  </div>
</div>

<div class="grid3 mt-6">
  <div class="card"><div class="mono" style="color:var(--grove-300)">$0</div>
    <p class="text-xs dim mt-1">Waxgrove itself. Open source, MIT.</p></div>
  <div class="card"><div class="mono" style="color:var(--grove-300)">$0</div>
    <p class="text-xs dim mt-1">Spotify. Reading Apple Music too.</p></div>
  <div class="card"><div class="mono" style="color:var(--copper-br)">$99/yr</div>
    <p class="text-xs dim mt-1">Only to write into Apple Music. Once per instance.</p></div>
</div>

---

<div class="eyebrow">And when it all goes wrong</div>

# It outlives its own connectors

<div class="mt-8 grid2">
  <div>
    <p class="text-sm">Streaming APIs are rate-limited, revocable, and change terms annually.
    So Waxgrove treats them as <em>hostile infrastructure</em> and refuses to depend on them.</p>
    <p class="text-sm mt-4"><strong>With zero services connected</strong> you can still build
    playlists, search, tag, rate, comment, share with friends, and import or export
    everything as standard playlist files.</p>
  </div>
  <div class="card good">
    <h4>Portable by construction</h4>
    <p class="text-sm">Playlists serialise to <strong>JSPF</strong> — an open standard.</p>
    <p class="text-sm mt-3">Your library survives a service dying, a connector breaking,
    and Waxgrove itself.</p>
  </div>
</div>

---

<div class="flex flex-col items-center justify-center h-full text-center">
  <img src="/mark.svg" class="w-32 mb-6" alt="" />
  <h1 class="text-6xl">Wax<span style="color:var(--grove-300)">grove</span></h1>
  <p class="dim mt-4">A record collection that grows in the open.</p>
  <p class="mono text-xs mt-10" style="color:var(--copper)">
    github.com/johnzastrow/waxgrove &nbsp;·&nbsp; MIT
  </p>
  <p class="text-xs dim mt-2">Pre-release. Foundation built; features in progress.</p>
</div>
