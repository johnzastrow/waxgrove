# Changelog

Notable changes, newest first. Versions are [semantic](https://semver.org);
pre-1.0, so a minor bump may still change behaviour.

The running version is shown at the bottom of **You**, and at
`GET /api/version` without signing in — the first question when something
behaves oddly is which build you are looking at, and needing an account to
answer that makes it useless during a bad deploy.

---

## 0.5.1 — 2026-08-09

### Fixed

- **A field-scoped search was flattened into free text before reaching
  MusicBrainz.** Searching for an artist returned anything mentioning the word
  anywhere — titles included — which reads exactly like the scoping is broken.
  The local half was always correct; the remote half threw the scope away.

  MusicBrainz indexes recordings with a query syntax of its own, so the fields
  now travel as `artist:(...)`, `recording:(...)`, `release:(...)` and
  `firstreleasedate:`, with the values escaped so a title containing a colon
  searches rather than failing to parse.

  It only showed up on an artist absent from the local catalogue, where the
  grove half is empty and every visible result comes from the metadata source.

---

## 0.5.0 — 2026-08-09

Making the catalogue legible: you could search it, but not read it.

### Added

- **Browse the catalogue.** An empty search box now lists what is actually
  there instead of an empty state — you cannot search for a song you have
  forgotten you added. Filter to what you contributed, sort by artist or by
  newest, and page through it. `Store.Records().List` behind it.
- **Search particular fields.** The box still searches everything; title,
  artist, album and year narrow it, and they compose — "anything mentioning
  moon, by Drake, from 1972" is one query rather than three to intersect by
  hand. A year alone is a valid search.
- **Songs are labelled everywhere**, including remote results: a table with
  column headings on a wide screen, and each value carrying its own label on a
  phone. A dotted run of "Nick Drake · Pink Moon · 1972 · 2:08" makes the
  reader work out which is the album and which is the year, and gets it wrong
  for a band named after a number.
- **The version is on screen at all times**, in the app chrome on every route
  as well as on the sign-in page.

---

## 0.4.0 — 2026-08-07

First deployment to a real instance, and the round of fixes that followed from
running it against real Spotify rather than a stub.

### Added

- **Change your password** (`POST /api/me/password`, **You → Password**). It did
  not exist at all; an instance where the first password you type is permanent
  is not one you can hand to friends. It requires the current password even
  though you are signed in, and ends every session including your own.
- **Version and build**, visible in the app and at `GET /api/version`, plus this
  changelog.
- `GET /api/instance`, so the sign-in screen knows whether an invite code is
  needed before asking for one.

### Fixed

- **The register form demanded an invite code for the very first account**,
  which locked the operator out of the instance they had just installed. The
  server was always correct; the browser blocked the request before it was sent.
- **Spotify renamed a playlist's contents from `tracks` to `items`**, and each
  entry's payload from `track` to `item`. One rename produced three symptoms
  that each looked like something else: a 403 on the old collection path, a
  reported total of zero on a playlist with songs in it, and an empty result
  from a `fields` projection. Both spellings are now read, and entries that are
  not songs — a playlist can hold podcast episodes now — are skipped.
- **Creating a playlist moved to `POST /me/playlists`.** The old
  `/users/{id}/playlists` returns 403 for a token carrying both modify scopes,
  which reads exactly like a permissions problem and is not one. Both are tried.
- **Provider errors kept no evidence.** A 403 logged as `Forbidden` with no
  URL, method or reason, which made a real failure undiagnosable — three
  confident diagnoses were wrong before the request and response body were being
  recorded. They are now.
- The market was never captured, because `user-read-private` was not requested.
  Every connection recorded an empty storefront and resolved with no market,
  though availability is regional.
- A failed job's detail pane said "Everything made it across" underneath the
  error explaining that nothing had.
- A cancelled search logged as a warning. The app debounces typing and aborts
  the previous request on each keystroke, so that was ordinary use burying real
  failures in noise.
- `ListShared` scanned a nullable description into a plain string and panicked
  on any playlist without one.

---

## 0.3.0 — 2026-08-05

**M3 — the crate, annotations, discovery, and the privacy pair.**

### Added

- **The crate** (F16): a persistent per-user staging area. Songs accumulate from
  search, a pasted list or an import, over as long as you like, and commit as
  one playlist — one authored event rather than twenty. Anything that cannot be
  identified stays in the crate rather than being dropped or guessed at.
- **Disambiguation before commit** (F12). Settling a match records it as
  *chosen*, distinct from the automatic methods, so an audit can tell a decision
  from a guess.
- **Annotations** (F18): per-user ratings with a derived aggregate, private and
  shared tags, comments. None of it moves a playlist's revision — a rating that
  did would make every exported copy look out of date.
- **Pasted lists**: free text to candidates, forgiving about separators and
  track numbering, and strict about never inventing a split it is unsure of.
- **Shared playlists** (F9) and **forking with provenance** (F20).
- **Export your data** (F25) and **erase your account** (F26). The export
  carries no secrets by construction; an export is a file that travels.
- **Re-sync** rather than duplicating: a second export updates the playlist
  Waxgrove already made. A copy edited on Spotify is detected and refused rather
  than silently overwritten, with the choice offered to the user.

---

## 0.2.0 — 2026-08-05

**M2 — the Spotify connector.**

### Added

- **Connect your own Spotify app** (D6). Spotify caps an app at five users, so
  an app shipped with Waxgrove would cap the instance; each person registers
  their own instead. Credentials are AES-256-GCM at rest.
- **Import** a playlist from a pasted link — Spotify removed the endpoint that
  lists your playlists, so a link is the way in.
- **Export** a playlist to Spotify, resolving cheapest-first: a cached provider
  id, then ISRC, then text. Both outcomes are cached, including the negatives.
- **Background jobs** with visible progress, surviving a restart. Partial
  success is reported honestly: an export that placed most of a playlist says
  which tracks did not make it and why.

### Fixed

- A connection-pool deadlock. Concurrent searches each held a cursor while
  waiting for a second connection, so past the pool size nothing completed
  again — static files kept serving while every database request hung.
- An unauthenticated OOM: `/api/login` costs 64 MiB of Argon2 per request, so
  concurrency was a memory multiplier anyone could pull. Hashing is bounded.

---

## 0.1.0 — 2026-08-04

**M1 — the neutral core, with no connectors at all.**

### Added

- Canonical record model keyed on MusicBrainz ID, with ISRCs as a many-to-one
  set — a recording carries several, and two services will hand you different
  ones for the same song.
- Playlists with an append-only revision history; reorder, rename, remove.
- Local full-text search, and MusicBrainz search and import.
- JSPF import and export, so a library outlives this app.
- Invite-only accounts, Argon2id passwords, sessions.
- The PWA: mobile-first with full desktop parity, installable, served from the
  same binary.
