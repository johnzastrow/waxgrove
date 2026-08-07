# Waxgrove — Streaming Integration Design

**Date:** 2026-08-03
**Status:** Design draft. No code written yet.
**Parent document:** [`requirements.md`](requirements.md) — this document expands §2, §3.1, §3.2
and §3.6 of that document and does not restate decisions recorded there.

> **Everything in this document is provisional and must be re-verified at implementation time.**
> `requirements.md` §2 already records Spotify moving its Development Mode user cap from 25 to 5
> and removing endpoints in a single migration. Treat every endpoint, filter syntax and limit
> below as a starting point for verification, not as settled fact.

---

## 1. Scope

How Waxgrove actually exchanges playlists with Spotify and Apple Music: which credentials exist,
who holds them, what the user touches, and what the mechanics force onto the UI.

Supported connectors are **Spotify** and **Apple Music** only (D1). Waxgrove must remain fully
useful with **zero** connectors attached (N6) — everything here is an optimisation that removes
manual steps, never a prerequisite.

---

## 2. Credentials — who holds what

| | Spotify | Apple Music |
|---|---|---|
| **Read / resolve** | User's own app (BYO-first, D6) or an operator slot | **iTunes Search API — no key, no cost** (§3.1) |
| **Write playlists** | Same OAuth token | **MusicKit — requires the $99/yr membership** |
| **Token model** | OAuth 2.0 + PKCE: access (~1h) + refresh | Developer JWT (ES256, ≤6 months) **plus** a per-user Music User Token |
| **Who pays** | Nobody — app registration is free | Operator, once per instance |
| **Scaling limit** | 5 users per app → solved by BYO (D6) | None beyond the membership |

**The asymmetry that matters:** Apple's write path needs *three* things simultaneously — the
operator's paid membership, a per-user MusicKit authorisation, **and** an active Apple Music
subscription on that user's account. The developer token alone gets you nothing user-facing.

**The asymmetry that saves money:** Apple *reading* is free. Resolving records to Apple catalog
IDs, fetching artwork and deep-linking out (F11) all work through the public iTunes Search API
with no key. An instance that never pays Apple still serves Apple users usefully — the $99 buys
playlist **writes** and nothing else.

Storage requirements for all of the above are in `requirements.md` §6: per-user Client Secrets
and all tokens are AES-256-GCM at rest, key from the environment, never logged or exported.

---

## 3. Authorisation flows

### Spotify — Authorization Code + PKCE

1. If BYO (D6), the user first pastes their own Client ID and Secret into Waxgrove, and copies
   Waxgrove's redirect URI into their Spotify app settings.
2. Waxgrove redirects to `accounts.spotify.com` with a `state` value and PKCE challenge.
3. The user authenticates **on Spotify's own page** and approves scopes.
4. Waxgrove exchanges the code for an access token (~1h) and a refresh token.

Scopes needed: playlist read (public and private as applicable) and playlist modification for
export. Request the minimum that supports F6/F7 — nothing broader.

### Apple Music — MusicKit

1. Waxgrove signs a developer token (ES256 JWT, ≤6 months) with the operator's key.
2. MusicKit JS renders **Apple's own authorisation sheet in the browser**.
3. The user signs in with their Apple ID and approves.
4. Waxgrove receives a Music User Token scoped to that user.

Note this is Apple UI, but it is *not* the Apple Music app — the user never leaves the browser.

---

## 4. Import — service → Waxgrove

The easy direction. Both services return an **ISRC** per track, so imported records land at
step 1 of the §3.2 resolution ladder: exact, automatic, no disambiguation. This is why a
straight provider import produces almost entirely high-confidence records.

### Spotify — paste a link

The February 2026 Development Mode migration **removed `GET /users/{id}/playlists`**, so
**Waxgrove cannot list a user's playlists.** `GET /playlists/{id}` and its items survive.

Therefore the user must copy the playlist link inside the Spotify app and paste it into
Waxgrove. This is not a UX preference; it is forced by the quota mode.

### Apple Music — pick from a list

`GET /v1/me/library/playlists` works, so Waxgrove can present the user's library playlists
directly. Two caveats:

- **Library playlists only.** A playlist someone shared that the user never added to their
  library is invisible to the API.
- Library resources do not always carry an ISRC. Resolution may need to walk from the library
  item to its catalog resource to obtain one — **an extra call per track**.

### The asymmetry, and why it is a screen requirement

| Service | Import gesture |
|---|---|
| Spotify | **Paste a link** — because the API cannot list playlists |
| Apple Music | **Pick from a list** — because it can |

The Import surface therefore needs **two distinct modes selected per service**, not one generic
flow. This is an API footnote that turns into an interface requirement, and it is easy to lose
between documents — hence recording it here.

---

## 5. Export — Waxgrove → service

The hard direction. For each record, resolve **cheapest first**:

| # | Step | Cost |
|---|---|---|
| 1 | Cached `provider_ref` for this record, service **and storefront** | free |
| 2 | MusicBrainz URL relationship (§3.1) | free — spends no provider quota |
| 3 | Provider ISRC lookup — Apple offers a first-class `filter[isrc]`; Spotify only a query form | 1 call |
| 4 | Fuzzy text search — constrained by Spotify's 10-result Development Mode cap | 1 call |
| 5 | No match → mark unavailable for that service and storefront (F15) | — |

Writing: Spotify creates a playlist then adds tracks in batches of 100. Apple creates a library
playlist with track relationships.

**Partial success is the normal outcome, not an error.** Regional licensing, exclusives and
delistings mean some tracks will not exist on the target. F15 requires showing the user exactly
which ones failed and why, rather than silently delivering a shorter playlist.

---

## 6. Storefronts — provider IDs are regional

**A recording's Apple Music catalog ID differs between storefronts** (`us` vs `gb` and so on),
and availability differs with it. Spotify has the analogous problem via `available_markets`.

The resolution cache therefore cannot be keyed `(record, service)`. It must be:

```
provider_ref(record_id, service, storefront, external_id, status, checked_at)
```

Consequences:

1. **"Unavailable" is never a global fact about a record** — it is a fact about a record *in a
   storefront*. A friend group spread across countries will legitimately see different results
   from the same playlist.
2. A single-country friend group **masks this entirely**, which makes it exactly the class of
   assumption that survives development and fails in production. Key the cache correctly in the
   first migration.

Full treatment in `requirements.md` §3.6.

---

## 7. Rate limits — why sync is a job, not a request

| Source | Limit |
|---|---|
| MusicBrainz | **~1 req/sec averaged, implemented as a burst bucket.** Live responses carry `x-ratelimit-limit: 1200`, `x-ratelimit-remaining` and a reset timestamp — respect the headers rather than assuming a hard per-call gate. User-Agent required. |
| Spotify (Dev Mode) | Quota pooled **per developer account** — precisely what BYO-first (D6) fixes |
| Apple | Rate-limited per developer token |

**Requests are album-shaped, not track-shaped.** One release lookup with
`inc=recordings+isrcs+artist-credits` returns the whole tracklist and every ISRC — measured at
11 tracks, 11KB, 0.39s. A 45-track playlist drawn from a dozen albums therefore costs roughly a
dozen requests, not forty-five, and D11 keeps the surplus tracks as ambient records so later
resolutions cost nothing at all.

**Watch for the multi-ISRC trap.** A single recording carries several ISRCs — *Dreams* by
Fleetwood Mac has seven. Spotify and Apple may each return a *different* one for the same
recording, so match on **ISRC set membership**, never on a single stored ISRC column, or the two
services' imports will never converge. See `requirements.md` §3.

A cold export can exceed a minute. It must run as a **resumable background job with visible
progress** (F22), never inside a request/response. This is the UI counterpart to §7.2's rule
that network I/O never happens inside a database transaction.

Two further requirements follow:

- **Per-service token-bucket limiters**, respecting MusicBrainz's 1 req/sec absolutely.
- **Remaining quota must be visible to the user.** Under Development Mode this is a scarce
  resource a user can genuinely exhaust, and silently failing halfway through a sync is the
  worst possible outcome.

The shared catalog (§3.0) is the main mitigation: a song resolved once never needs resolving
again, so a warm instance spends far less quota than a cold one.

---

## 8. Sync semantics — tracked one-way (D10)

Waxgrove remembers the playlist it created and can re-push when the source moves ahead:

```
playlist_sync(playlist_id, service, storefront, provider_playlist_id,
              last_synced_rev, last_synced_at)
```

- **Sync state is user-visible** (F21): last synced, and how many revisions behind the provider
  copy now is. "Synced" is not a boolean.
- **Divergence must be detected, not clobbered.** If the user edited the playlist on the
  provider side, a re-sync would overwrite their work. Waxgrove asks — the same "never silently
  mismatch" principle §3.2 applies to matching.
- Sync is per `(playlist, service, storefront)`. The same playlist may be synced to two services
  at different revisions.
- One-way means **the provider copy is a projection, never a source of truth.** Waxgrove's
  revision history (F17) remains authoritative.

Rationale for rejecting snapshot and two-way sync is recorded in `requirements.md` D10.

---

## 9. User journeys — what the user actually touches

Across a complete round trip the user performs **two** actions inside a streaming app: copy a
link once, and add-to-library once. Everything else happens in Waxgrove.

### Spotify → Waxgrove

| Step | Where | Action |
|---|---|---|
| 1 | Waxgrove | Connect Spotify (BYO: paste Client ID + Secret, copy redirect URI over) |
| 2 | accounts.spotify.com | Log in, approve scopes |
| 3 | **Spotify app** | ⋯ → Share → **Copy link to playlist** |
| 4 | Waxgrove | Import → Spotify → paste the link |
| 5 | Waxgrove | Tracks canonicalise via ISRC |

### Apple Music → Waxgrove

| Step | Where | Action |
|---|---|---|
| 0 | **Apple Music app** | If not already saved: **Add to Library** — the API cannot see un-saved playlists |
| 1 | Waxgrove | Connect Apple Music |
| 2 | Apple's MusicKit sheet (in browser) | Sign in, approve |
| 3 | Waxgrove | Import → Apple Music → pick from the list |
| 4 | Waxgrove | Tracks canonicalise via ISRC |

### Waxgrove → either service

| Step | Where | Action |
|---|---|---|
| 1 | Waxgrove | Playlist → Sync to Apple Music / Sync to Spotify |
| 2 | Waxgrove | Background job resolves and writes; progress visible (F22) |
| 3 | **Service app** | Playlist appears under Library → Playlists |

### The reference round trip (§3.5)

**Ana (Spotify, US) → Ben (Apple Music, US):** Ana copies the link in Spotify → pastes into
Waxgrove → 45 records canonicalise by ISRC → commits and shares → Ben opens it in Waxgrove,
authorises Apple once, hits Sync → the playlist appears in his Apple Music library.

**Ben → Ana:** Ben ensures the playlist is in his Apple Music library → picks it from the list
in Waxgrove (no link-copying; Apple can list) → Ana hits Sync to Spotify → it appears in her
Spotify library.

---

## 10. What is not possible

- **Apple Music has no export function.** There is no "download this playlist as a file". Without
  the connector the only route is typing or pasting track names — which is exactly why the crate
  (§3.3) accepts pasted free text.
- **A Spotify playlist the user cannot see** — private and not theirs — cannot be imported by
  link.
- **Neither service reliably allows pushing into an existing playlist.** Sync creates a *new*
  playlist, which is why D10's tracked sync updates the playlist Waxgrove itself created, and
  why divergence detection matters.
- **No scraping, ever** (§4, Won't). Unofficial endpoints break constantly and put the project
  crosswise with provider terms.

---

## 11. Consequences for the interface

Recorded here because each is a design requirement that originates in an API constraint:

| Constraint | Interface consequence |
|---|---|
| Spotify cannot list playlists; Apple can | Import needs **two modes**, chosen per service — paste-a-link vs pick-from-list |
| Cold sync exceeds a minute | Sync is a **job surface** with progress and resume, not a spinner (F22) |
| Provider IDs are per-storefront | Export status is **per-track and per-storefront**, not a single banner (F15) |
| Tracked one-way sync | Playlists need **sync state** — "synced 2 days ago · 3 revisions behind" (F21) |
| Dev Mode quota is exhaustible | **Remaining quota** belongs in the UI |
| BYO credentials (D6) | A real **connect wizard** is required (F13) — per user, not just per operator |

Both surfaces are designed in **[`design/connect-and-jobs.html`](design/connect-and-jobs.html)**,
alongside [`design/direction.html`](design/direction.html) (phone tier) and
[`design/desktop.html`](design/desktop.html) (desktop tier).

Two design decisions from that work are worth recording here, because they follow directly from
the constraints above rather than from taste:

- **The running job and the export result are one surface in two states.** Progress becomes
  outcome; the live rows become the per-track result rows F15 requires. They were never two
  screens.
- **Nothing is written to the provider until every record resolves.** A failed or cancelled job
  therefore leaves both the Waxgrove playlist and the provider account untouched, which is what
  makes "safe to leave" an honest claim rather than a hopeful one.

---

## 12. To verify before building

1. Whether **Apple's API can modify an existing library playlist's tracks** — historically
   limited. Materially changes §8's re-sync mechanics if it cannot.
2. ~~Whether Spotify's **removal of playlist listing** from Development Mode is still in force.~~
   **Answered in production, 2026-08-07 — and it was not a Development Mode restriction at all.**

   Spotify has **renamed a playlist's contents from `tracks` to `items`**, and each entry's
   payload from `track` to `item`. Entries now carry a `type` and an `episode` flag, so a
   playlist can hold podcast episodes as well as songs.

   The rename explains three symptoms that each looked like something else:

   - `GET /playlists/{id}/tracks` → **403**, because the collection is now at `/items`. This
     reads exactly like a permissions failure and is not one.
   - `tracks.total` → **0** on a twenty-song playlist, because the field is `items`.
   - A `fields=...tracks(items(...))` projection → **empty**, for the same reason.

   Waxgrove now reads `items` and falls back to `tracks`, takes each entry from `item` or
   `track`, skips anything whose type is not a track, and writes to `/items`. The paging URL is
   taken from the response rather than constructed, so it names whichever endpoint that account
   is actually served.

   **Method note worth keeping.** Three plausible diagnoses were wrong before the response body
   was logged: the user not being allowlisted, the playlist being editorial, and a malformed
   fields projection. Each was consistent with the symptom. None survived the evidence. The fix
   that mattered was making the failure legible — carrying the request into the error, and
   keeping the body when a response parses cleanly into nothing.
3. Current Spotify Development Mode **user cap** (5 at time of writing) and **search result cap**
   (10).
4. Exact ISRC filter syntax on both services.
5. Whether the **iTunes Search API's terms** permit this use — it is nominally an affiliate
   endpoint (§3.1).

---

## Cross-references

| Topic | Where it is decided |
|---|---|
| Which services are supported | `requirements.md` D1 |
| Spotify BYO-first credentials | `requirements.md` D6, §2 |
| Sync semantics | `requirements.md` D10 |
| Metadata sources | `requirements.md` §3.1, D9 |
| Resolution ladder and matching | `requirements.md` §3.2 |
| Storefront keying, job model | `requirements.md` §3.6 |
| Reference cross-service flow | `requirements.md` §3.5 |
| Token and secret storage | `requirements.md` §6 |
| F13, F15, F19, F21, F22 | `requirements.md` §4 |
