---
theme: default
title: Waxgrove — how it works
info: The architecture behind cross-service playlist exchange.
class: text-left
transition: fade
mdc: true
fonts:
  provider: none
---

<div class="flex items-center gap-10">
  <img src="/mark.svg" class="w-32" alt="Waxgrove mark" />
  <div>
    <div class="eyebrow">Waxgrove</div>
    <h1 class="text-6xl">How it works</h1>
    <p class="dim mt-4">The architecture behind cross-service playlist exchange —
    and the three findings that shaped it.</p>
  </div>
</div>

---

<div class="eyebrow">01 — Scope</div>

# Metadata only, never audio

<div class="grid2 mt-8">
  <div class="card good">
    <h4>Waxgrove stores</h4>
    <p class="text-sm">Song, album and artist metadata. Playlists. Tags, ratings, comments.
    Which service each record resolves to.</p>
  </div>
  <div class="card warn">
    <h4>Waxgrove never stores</h4>
    <p class="text-sm">Audio files. It does not host, transcode or serve music, and it
    does not circumvent DRM or scrape endpoints.</p>
  </div>
</div>

<p class="text-sm mt-8">The unit of work is a <strong>song record</strong>, not a file.
Playback stays where it belongs — in the listener's own service.</p>

---

<div class="eyebrow">02 — The core inversion</div>

# Identity, not provider IDs

<div class="mt-6">
  <table class="w-full">
    <tr><th>Key</th><th>Role</th></tr>
    <tr><td class="mono">MusicBrainz MBID</td><td><strong>Primary identity.</strong> Stable, one per recording.</td></tr>
    <tr><td class="mono">ISRC</td><td>Lookup key — the recording's registered code. Many per recording.</td></tr>
    <tr><td class="mono">artist + title + duration</td><td>Normalised fallback when neither ID exists.</td></tr>
    <tr><td class="mono dim">Spotify URI / Apple ID</td><td class="dim">A <em>cache</em>. Never identity.</td></tr>
  </table>
</div>

<div class="card mt-8">
<p class="text-sm">Because provider IDs hang off the record rather than being it, a playlist
survives a connector dying — and can be resolved against a service it was never built for.
That is the entire mechanism by which a Spotify playlist becomes an Apple Music one.</p>
</div>

---

<div class="eyebrow">03 — Finding one</div>

# A recording has *many* ISRCs

<p class="text-sm">Verified against the live MusicBrainz API. The recording <em>Dreams</em> by
Fleetwood Mac carries <strong>seven</strong>:</p>

```
USRH11802580   USWB10101368   USWB10202603   USWB10400046
USWB11301111   USWB19900178   USWB22600016
```

<div class="card warn mt-6">
<h4>The bug this would have caused</h4>
<p class="text-sm">Spotify may return <code>USWB10101368</code> while Apple returns
<code>USWB19900178</code> — <strong>for the same recording</strong>. Keyed on a single ISRC
column those become two separate records, and deduplication fails silently in exactly the
cross-service case the product exists to serve.</p>
</div>

<p class="text-sm mt-6">So a record holds a <strong>set</strong> of ISRCs, and matching is set
membership. Re-releases and remasters each register their own code; MBID is what unifies them.</p>

---

<div class="eyebrow">04 — Matching</div>

# The ladder, cheapest first

<div class="mt-6">
  <table class="w-full">
    <tr><th>#</th><th>Step</th><th>Confidence</th></tr>
    <tr><td class="mono">1</td><td>ISRC set membership</td><td class="mono" style="color:var(--grove-300)">exact, automatic</td></tr>
    <tr><td class="mono">2</td><td>MBID match</td><td class="mono" style="color:var(--grove-300)">exact, automatic</td></tr>
    <tr><td class="mono">3</td><td>ListenBrainz MBID Mapper on (artist, title)</td><td class="mono">scored</td></tr>
    <tr><td class="mono">4</td><td>Local fuzzy: normalised text + duration ±3s</td><td class="mono" style="color:var(--warn)">scored</td></tr>
    <tr><td class="mono">5</td><td>Below threshold → <strong>ask the human</strong></td><td class="mono" style="color:var(--ember)">never guess</td></tr>
  </table>
</div>

<div class="grid2 mt-6">
  <div class="card">
    <h4>Why it stays fast</h4>
    <p class="text-sm">A provider import arrives carrying ISRCs, so it lands on step 1 —
    exact, no fuzzy logic, no human. Only free text needs the expensive path.</p>
  </div>
  <div class="card good">
    <h4>Every match is auditable</h4>
    <p class="text-sm">Method and score are stored, so a bad match can be found and
    re-resolved later rather than quietly persisting.</p>
  </div>
</div>

---

<div class="eyebrow">05 — Building playlists</div>

# One pipeline, every source

<div class="mono text-xs mt-4 dim">
Source adapters → Candidates → Resolution ladder → <span style="color:var(--copper)">The crate</span> → Commit → Revision
</div>

<div class="grid3 mt-6">
  <div class="card"><h4>Arrives canonical</h4>
    <p class="text-xs">Catalogue search · another playlist · a friend's shared song ·
    MusicBrainz search</p></div>
  <div class="card"><h4>Arrives with an ISRC</h4>
    <p class="text-xs">Spotify or Apple import · provider search · JSPF file ·
    a pasted share link</p></div>
  <div class="card warn"><h4>Free text only</h4>
    <p class="text-xs">Pasted "Artist — Title" lines · CSV · M3U · iTunes XML</p></div>
</div>

<div class="card mt-6">
<h4>The crate</h4>
<p class="text-sm">A persistent staging area. Add from four sources over four days, review the
match quality, then commit once. Duplicates collapse automatically because everything resolves
to canonical identity — and a commit of twenty tracks writes <strong>one</strong> revision, so
history stays readable.</p>
</div>

---

<div class="eyebrow">06 — A distinction that matters</div>

# Content and opinion are separate histories

<div class="grid2 mt-8">
  <div class="card">
    <h4>Versioned — content</h4>
    <p class="text-sm">Adding, removing, reordering, renaming. Append-only, with author and
    timestamp. You can see who changed what.</p>
  </div>
  <div class="card good">
    <h4>Not versioned — annotations</h4>
    <p class="text-sm">Ratings, tags, comments. Attached to the playlist, attributed to a
    person, outside its history.</p>
  </div>
</div>

<div class="card warn mt-8">
<p class="text-sm">Ben rating and tagging Ana's playlist is <strong>not a content change</strong>.
If annotations wrote to the revision log, Ana's history would fill with Ben's tags and blame
would become meaningless. Cheap to separate now; painful later.</p>
</div>

<p class="text-sm mt-6 dim">Ratings are per person with an aggregate shown. Tags come in two
kinds: private to you, or shared with the group.</p>

---

<div class="eyebrow">07 — Finding two</div>

# Availability is regional

<div class="grid2 mt-8">
  <div>
    <p class="text-sm">An Apple Music catalogue ID <strong>differs between storefronts</strong>,
    and availability differs with it. Spotify has the same problem via market lists.</p>
    <p class="text-sm mt-4">So the resolution cache is keyed
    <code>(record, service, storefront)</code>. "Unavailable" is never a fact about a record —
    only about a record <em>in a place</em>.</p>
  </div>
  <div class="card warn">
    <h4>Why this is easy to miss</h4>
    <p class="text-sm">A friend group in one country masks it completely. It would pass every
    test and fail the moment someone moves abroad — which is why it is keyed correctly in the
    first migration rather than retrofitted.</p>
  </div>
</div>

<p class="text-sm mt-8">It also reframes what availability <em>means</em>: for someone with only
Spotify, an Apple column isn't about their own export. It tells them
<strong>whether their Apple Music friends can play what they just made</strong>.</p>

---

<div class="eyebrow">08 — Connectors</div>

# Two services, two very different deals

<div class="grid2 mt-6">
  <div class="card">
    <h4>Spotify — free, but capped per app</h4>
    <p class="text-sm">Wider access needs an organisation with 250,000+ monthly users, which a
    friends' app can never be. Development Mode allows 5 users <em>per app</em>.</p>
    <p class="text-sm mt-3"><strong>So each user brings their own app.</strong> Free, five
    minutes, once — and the cap never binds at any size, because every app has exactly one user.</p>
  </div>
  <div class="card">
    <h4>Apple — free to read, paid to write</h4>
    <p class="text-sm">Finding songs, artwork and deep links all work through a public API with
    no key at all.</p>
    <p class="text-sm mt-3">Only <em>writing a playlist into</em> Apple Music needs the
    <strong>$99/yr</strong> developer membership — paid once by whoever runs the instance,
    not per person.</p>
  </div>
</div>

<div class="card warn mt-6">
<p class="text-sm">Connectors are treated as <strong>hostile infrastructure</strong>: rate-limited,
revocable, terms changed annually. They can never be load-bearing.</p>
</div>

---

<div class="eyebrow">09 — Finding three</div>

# Sync is a job, not a button

<div class="grid2 mt-8">
  <div>
    <p class="text-sm">MusicBrainz permits roughly <strong>one request per second</strong>. A
    cold 45-track export therefore takes over a minute — which rules out a spinner and a modal.</p>
    <p class="text-sm mt-4">So it runs as a resumable background job with real progress. Close
    the tab; switch to your phone. Nothing is written to the provider until every record
    resolves, so a failure leaves both sides untouched.</p>
  </div>
  <div class="card good">
    <h4>It gets faster on its own</h4>
    <p class="text-sm">One request returns an <strong>entire album</strong> with every ISRC, and
    the catalogue is shared instance-wide.</p>
    <p class="text-sm mt-3">A song resolved once is never resolved again — so a group's
    instance speeds up the more they use it.</p>
  </div>
</div>

---

<div class="eyebrow">10 — The floor</div>

# Useful with nothing connected

<div class="mt-8 grid2">
  <div class="card good">
    <h4>Works with zero connectors</h4>
    <p class="text-sm">Build playlists, search locally and on MusicBrainz, tag, rate, comment,
    share with friends, import and export.</p>
  </div>
  <div class="card">
    <h4>JSPF is the universal fallback</h4>
    <p class="text-sm">An open playlist standard. Any service without a usable API is reached
    by exchanging a file — and every playlist stays portable regardless.</p>
  </div>
</div>

<p class="text-sm mt-8">This is a <strong>hard requirement, not a fallback</strong>. Connectors
remove manual steps; they are never a prerequisite. It is also what makes the project survivable:
if Waxgrove disappears tomorrow, the playlists don't.</p>

---

<div class="eyebrow">11 — Stack</div>

# Small enough to run on a Pi

<div class="mt-6">
  <table class="w-full">
    <tr><th>Layer</th><th>Choice</th><th>Because</th></tr>
    <tr><td>Language</td><td class="mono">Go</td><td>One static binary, ARM cross-compile, ~20MB idle</td></tr>
    <tr><td>Store</td><td class="mono">SQLite (WAL)</td><td>Handles this workload with orders of magnitude to spare</td></tr>
    <tr><td>Search</td><td class="mono">FTS5</td><td>No extra service to run</td></tr>
    <tr><td>Client</td><td class="mono">PWA</td><td>One artifact, no app store, installs to a home screen</td></tr>
    <tr><td>Auth</td><td class="mono">local + optional OIDC</td><td>Zero dependencies to boot; OIDC when you want it</td></tr>
  </table>
</div>

<div class="grid2 mt-6">
  <div class="card"><h4>Concurrency</h4>
    <p class="text-xs">Writes forced through one connection so they serialise cleanly; readers
    unconstrained. Network I/O never inside a transaction.</p></div>
  <div class="card"><h4>Secrets</h4>
    <p class="text-xs">Provider tokens are AES-256-GCM at rest, key from the environment. They
    grant write access to a real music library — treated accordingly.</p></div>
</div>

---

<div class="flex flex-col items-center justify-center h-full text-center">
  <img src="/mark.svg" class="w-28 mb-6" alt="" />
  <h1 class="text-5xl">Wax<span style="color:var(--grove-300)">grove</span></h1>
  <p class="dim mt-4 max-w-xl">Every ring in the mark carries a gap.<br>Nothing here closes.</p>
  <p class="mono text-xs mt-10" style="color:var(--copper)">
    github.com/johnzastrow/waxgrove &nbsp;·&nbsp; MIT
  </p>
  <p class="text-xs dim mt-2">API specifics are provisional and re-verified at implementation time.</p>
</div>
