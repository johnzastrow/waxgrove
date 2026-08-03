# Waxgrove — Requirements & Architecture

**Date:** 2026-08-03
**Status:** Requirements draft. All decisions in §8 resolved (D1–D10). The goals added to
`objectives.md` are reconciled into this document as of 2026-08-03. No code written yet.
**Inputs:** `objectives.md` (objectives + goals), `naming.md` (name — confirmed Waxgrove)

---

## 1. What Waxgrove is

A mobile-first, self-hostable app for sharing songs and playlists between friends who use
different streaming services. It stores **metadata only** — never audio files — and acts as a
neutral meeting point between walled gardens.

**Objectives (from `waxgroveapp.md`):** open source · self-hostable · low resources ·
standards-based where standards exist.

### Scope correction

The earlier planning round assumed a local music-library manager (scan files, read tags,
dedupe). That is the wrong shape. Waxgrove does not manage audio files, so the milestone
options previously floated — library scanning, tag cleanup, file dedupe — are out of scope.
The unit of work is a **song record**, not a file.

---

## 2. The feasibility problem (read this first)

> **Companion document:** the full connector mechanics — authorisation flows, import and export
> per service, storefronts, rate limits, and what the user actually touches inside Spotify and
> Apple Music — are in **[`streaming-integration.md`](streaming-integration.md)**. This section
> records the constraints and the decisions; that document records how it works.

The original brief asked for two-way playlist and song exchange with several services "at a
minimum." **That requirement cannot be met as written.** Verified 2026-08-02:

| Service | API exists | Who can get credentials | Playlist read/write | Verdict |
|---|---|---|---|---|
| **Spotify** | Yes — Web API | Anyone, but **Development Mode only**. Extended Quota needs a registered *organization* with ≥250k MAU; individuals have been ineligible since 2025-05-15. | Yes — `GET/POST/PUT/DELETE /playlists/{id}/items` | **Usable, with a hard user cap** |
| **Apple Music** | Yes — Apple Music API / MusicKit | Requires a paid **Apple Developer Program** membership, **$99/yr** | Yes | **Usable, at a cost per self-hoster** |

**Supported services are Spotify and Apple Music.** Any service without an obtainable API is
out of scope as a connector — see D1. That is not a gap in the product: the canonical layer
(§3) plus JSPF (F8) means records remain portable regardless of which services are wired up,
and Waxgrove is required to be fully useful with **zero** connectors attached (N6).

### Spotify's constraints in detail

- **5 authenticated users per app.** Current docs state Development Mode allows "up to 5
  authenticated Spotify users," each added to an allowlist manually. (This was 25 for years —
  re-verify before building against it.) Non-allowlisted users get `403`.
- **The app owner must hold Spotify Premium**, effective 2026-03-09.
- **Search returns max 10 results** in Development Mode, down from 50.
- **Quota is pooled per developer account**, not per app — 25 Client IDs are allowed per
  account, but they share one budget.
- The February 2026 migration removed batch fetch (`GET /tracks`, `GET /albums`, …),
  `GET /users/{id}/playlists`, browse/discovery, and artist top-tracks from Development Mode.
  Playlist item management and `GET /search` survive.

### What this forces

1. **Waxgrove cannot ship with embedded credentials.** There is no central "Waxgrove app" that
   could ever be approved at scale. Every instance must register **its own** Spotify app and
   supply **its own** Apple developer key. Waxgrove ships a setup wizard, not secrets.
2. **The Spotify 5-user cap is per-app, not per-instance — and that is the way out.**
   The cap only becomes an instance ceiling if a single operator-owned app serves everyone.
   Waxgrove therefore takes a **BYO-first** approach (D6): each user registers their *own*
   Spotify app — free, one-time, ~5 minutes — so every app has exactly one allowlisted user
   and the cap never binds at any instance size. This also sidesteps the pooled-quota problem:
   quota is budgeted per developer account, so BYO gives each user their own full Development
   Mode budget instead of making friends contend for one.

   An **operator-provided app remains available as a fallback**, capped at its 5 slots. Because
   the Premium requirement falls on whoever *owns* the app, those slots are best reserved for
   users who cannot do BYO — free-tier users and the less technical. The instance must track
   and surface remaining slots honestly in the UI.

   **This model does not transfer to Apple Music.** BYO works for Spotify because app
   registration is free; Apple requires a $99/yr Developer Program membership, so per-user BYO
   would mean every user paying $99. Apple Music stays operator-provided — a per-instance cost.
3. **Connectors are hostile infrastructure.** They are rate-limited, revocable, and change terms
   annually. They cannot be load-bearing. **Waxgrove must be fully useful with zero connectors
   attached** — that is a hard requirement, not a fallback.

---

## 3. The architectural centre: canonical identity

The durable asset is not the connectors. It is a **platform-neutral catalog**. Provider IDs are
the wall; canonical identity is the hole in it.

**Every song is stored as a canonical record**, keyed in priority order:

| Key | Source | Role |
|---|---|---|
| **ISRC** | International Standard Recording Code | Primary. A real standard (objective #4). Both Spotify and Apple expose it per track. |
| **MusicBrainz Recording MBID** | MusicBrainz | Secondary, richer — links to release, work, artist credits. |
| Normalized `(artist, title, album, duration)` | Derived | Fallback for fuzzy matching when neither ID is present. |

Provider IDs (Spotify track URI, Apple catalog ID) attach to a canonical record as a
**resolution cache** — never as primary identity. A record whose Spotify link goes dead is
still a valid record.

**Playlists** are ordered lists of canonical records, serialized as **JSPF** — MusicBrainz's
JSON flavor of XSPF, already used by ListenBrainz for exactly this purpose. This satisfies the
standards-based objective and means a Waxgrove playlist stays portable even if Waxgrove dies.

**Sharing between friends** = exchanging canonical records. The recipient's client resolves
them against *their own* connected service, on demand. Nobody's platform is privileged.

### 3.0 The catalog is instance-wide

**Once a song is added to Waxgrove, every user on the instance can use it.** Canonical records
live in one shared catalog, not in per-user libraries. Consequences:

- **Deduplication is global.** If two users import the same song — from different services, at
  different times — it resolves to one record. Who arrived first is irrelevant.
- **Resolution gets cheaper over time.** A song resolved once never needs the MBID Mapper
  again. A long-running instance has a warm catalog where most additions hit step 1 or 2 of
  §3.2 immediately, which conserves scarce provider quota.
- **Every import enriches everyone.** Local search (F4) returns songs contributed by any user,
  including songs from services the searcher does not use.

This is consistent with §6's data classification: song metadata is **public**, so a shared
catalog is safe. What stays **confidential** is activity — who added or played what — and
provider credentials. Annotations (§3.4) layer per-user meaning on top of shared records.

### 3.1 Metadata sources — the MetaBrainz stack

Objective 4 asks for fuzzy search across records *not* stored in the app. The MetaBrainz family
covers this with **no API key, no approval, and no paid membership** — which makes it the only
search backend that can be relied on unconditionally, given Spotify search is now capped at 10
results in Development Mode.

| Source | Provides | Auth | Role in Waxgrove |
|---|---|---|---|
| **MusicBrainz** | Recordings, releases, artists, works; ISRC ↔ MBID mapping | None (User-Agent + 1 req/sec) | **Primary.** Canonical identity and remote search (F5). |
| **ListenBrainz MBID Mapper** | Fuzzy `(artist, title)` → recording MBID | None | **Primary resolver.** See below. |
| **Cover Art Archive** | Release artwork by MBID | None | Album art for F2. Keyed off MBID, so it comes free once a record resolves. |
| **ListenBrainz** | JSPF playlist hosting, import/export, recommendations | Token for writes | JSPF interop target (F8); a neutral place to publish a playlist. |
| **Deezer** | Track/album/artist search returning **ISRC** | **None** for search | **Secondary resolver.** A keyless second opinion returning the exact key §3 is built on. Strongest where MusicBrainz coverage lags — very new or purely commercial releases. |
| **iTunes Search API** | Apple catalog IDs, artwork, previews | **None** | **Apple resolution without the $99/yr membership** — see below. Read/deep-link only. |
| **Last.fm** | Tags, genres, similar artists, popularity | Free key | Enrichment. Feeds the "extended information" goal and any future recommendation work. |
| **Wikidata** | Cross-platform ID mappings (SPARQL) | None | Supplementary ID mapping, same spirit as MusicBrainz URL relationships. Stronger on artists/albums than individual recordings. |
| **Discogs** | Pressing/edition depth, physical releases | Token, rate-limited | Optional enrichment. Fits the "wax" framing but not v1-critical. |
| **AcoustID** | Audio fingerprint → MBID | Key | **Not applicable.** Waxgrove never touches audio files. |

**These are resolvers and enrichers, never identity schemes.** Everything they return maps into
ISRC or MBID. Adding a second identity namespace would undo the point of §3.

**Third finding: the Apple *read* path does not require the Developer Program.** The $99/yr
membership is needed for MusicKit — playlist writes and playback. The public iTunes Search API
needs no key at all. So an instance that never pays Apple can still resolve records to Apple
catalog IDs, show Apple artwork, and deep-link out to Apple Music (F11). Only "sync a playlist
*into* Apple Music" (F7) requires the paid membership. This makes the entry cost materially
lower than §2 implies — an operator can serve Apple users usefully for free, and pay only when
someone needs playlist writes.

**Considered and declined for v1:** Odesli/Songlink (one ID or URL resolved to links across
many services). Useful for deep-linking (F11) to services Waxgrove has no connector for, but it
is a third-party service that could disappear or start charging, and the design should not lean
on it.

**Post-v1 candidate: Discogs.** Pressing and edition depth, physical releases — it fits the
"wax" framing and would enrich records meaningfully, but it needs a token, is rate-limited, and
adds nothing to v1's core loop. Worth adding once the catalog and annotation model are settled.

Each additional source is a dependency, a rate limit, and a failure mode — N1 and N2 argue for
restraint. All of them must degrade cleanly: MusicBrainz alone is sufficient for correctness.
**Terms of use need verifying at implementation time** — the iTunes Search API is nominally an
affiliate endpoint, and §2 shows how quickly provider terms move.

Two findings worth building on:

**The MBID Mapper is the fuzzy matcher, already built.** ListenBrainz runs a hosted fuzzy
resolver that takes a free-text artist and track name and returns a MusicBrainz recording MBID
— the exact problem described below, solved by people with the full MusicBrainz corpus to train
against. Endpoints live at `labs.api.listenbrainz.org` (`recording-search`, `acr-lookup`,
`explain-mbid-mapping`, which shows *why* a match was made). Waxgrove should call it first and
keep a local matcher only as an offline fallback. That removes the single largest chunk of
v1 engineering risk.

**MusicBrainz already stores streaming links.** Recordings and releases carry URL relationships
to streaming services — `free streaming` (UUID `08445ccf-7b99-4438-9f9a-fb9ac18099ee`, used for
Spotify) and a separate subscription-streaming type (Tidal, Apple Music). So for records with
good community coverage, **Waxgrove can resolve a song across platforms without calling any
provider API at all.** Coverage is community-contributed and therefore incomplete, so this is
a first-pass accelerator rather than a replacement for the connectors — but it is free,
unrevocable, and improves over time on its own.

### 3.2 Matching is the product

Resolution quality is where tools in this category live or die. The rule: **never silently
mismatch.**

1. ISRC exact match → high confidence, automatic.
2. MBID match → high confidence, automatic.
3. **ListenBrainz MBID Mapper** on `(artist, title)` → confidence from the mapper.
4. Local fuzzy `(normalized artist, normalized title, duration ±3s)` → scored confidence.
5. Below threshold → **surface a disambiguation UI**. Do not guess.

Once an MBID is known, check MusicBrainz URL relationships for provider links **before**
spending a provider API call — this conserves Spotify's pooled quota, which is scarce.

Every match stores its method and confidence score, so bad matches are auditable and
re-resolvable later.

### 3.3 Playlist creation — one pipeline, every source

Goal: *create playlists from all possible sources of songs and searches.* The trap is building
one import flow per source, which makes mixing sources impossible and leaves nowhere to review
match quality before a playlist exists. Instead, a single pipeline:

```
Source adapters → Candidates → Resolution ladder (§3.2) → Crate → Commit → Playlist revision
```

**Source adapters** all satisfy one interface, `Fetch(ctx) → []Candidate`:

| Source | Arrives carrying | Resolution cost |
|---|---|---|
| Local catalog search (F4) | canonical record | none |
| Another playlist, or a song shared by a user (F9) | canonical record | none |
| MusicBrainz search (F5) | MBID | none — already canonical |
| JSPF import (F8) | ISRC / MBID | trivial |
| Provider playlist import (F6) | ISRC + provider ID | cheap — ISRC exact match |
| Provider search | ISRC + provider ID | cheap |
| Pasted share URL (Spotify / Apple) | provider ID | MusicBrainz URL relationship first; provider API only if needed |
| Pasted text, CSV, M3U, iTunes XML | free text only | full ladder → MBID Mapper |

The canonical-identity architecture pays off here: **only free-text sources need the expensive
fuzzy path.** Everything else short-circuits at step 1 or 2 of §3.2, conserving provider quota.

**The crate** is a persistent, per-user staging area — items accumulate from any source, in any
order, over any span of time, and become a playlist only on commit.

- **Multi-source by construction.** Catalog search, a pasted text list, tracks pulled from a
  friend's playlist, and a provider import can all land in one crate, committed once. This is
  what makes "as few clicks as possible" real for composed playlists.
- **Persistent across sessions.** Mobile-first (N3) means a crate is built over days. It is a
  table, not client state.
- **Deduplication is free.** Everything resolves to canonical identity, so the same song
  arriving from two sources collapses automatically (§3.0).
- **Confidence travels per item.** Method and score from §3.2, so low-confidence items surface
  the disambiguation UI (F12) *in place, before commit* — not after a bad playlist exists.
- **Nothing is silently lost.** Unresolved candidates remain in the crate as raw text awaiting
  a decision, honoring §3.2's "never silently mismatch."
- **Commit is one authored event.** Committing writes exactly one playlist revision (§3.4), so
  blame stays meaningful — a crate commit is one entry, not twenty individual track-adds.

Sketch:

```
crate       (id, user_id, created_at)
crate_item  (crate_id, position, canonical_record_id NULL,
             raw_candidate JSON, source_ref,
             resolution_method, confidence, status)
```

`canonical_record_id` is nullable by design — that is what allows an unresolved item to sit in
the crate as raw text rather than being dropped.

**The crate is not mandatory.** For the simple case — importing one provider playlist wholesale
where every track resolves at high confidence — staging adds a step for no benefit. Import
offers a **direct-to-playlist fast path**, diverting into the crate only when items need
disambiguation or the user explicitly chooses to stage. The crate is for composition and
review, not a toll booth on every import.

### 3.4 Playlists, revisions, and annotations

A playlist is **owned by one user and shared by reference** — not copied. Other users on the
instance view it, export it to their own service, and annotate it, all against the same object.
If the owner edits it, everyone sees the update. Diverging requires an explicit fork.

Two kinds of state attach to a playlist, and **they must not share a history**:

| | Playlist content | Annotations |
|---|---|---|
| What | Ordered canonical records | Ratings, tags, comments |
| Versioned | **Yes** — append-only revisions with actor, timestamp, operation | **No** |
| Who writes | Owner (and forks) | Any user with access |

**Why the split matters.** In the reference flow (§3.5), User B rates and tags a playlist that
User A owns. That is not a content change. If annotations wrote to the revision log, A's
history would fill with B's tags and blame would become meaningless. Content revisions and
annotations are therefore separate tables from the start — cheap now, expensive to retrofit.

- **Ratings** are **per-user**, with an aggregate displayed. B's rating of A's playlist is B's
  own; it never overwrites A's.
- **Tags** come in two kinds: **private** (visible only to their author, for personal
  organization) and **shared** (visible instance-wide, attributed).
- **Comments** are shared and attributed.

### 3.5 Reference flow — cross-service sharing

The use case the design must serve, end to end:

> User A (Spotify) syncs a playlist up to Waxgrove. User B (Apple Music) sees the playlist and
> its metadata, syncs it down to Apple Music to play it, then rates it and adds tags and
> comments in Waxgrove.

| Step | Mechanism | Notes |
|---|---|---|
| A imports from Spotify | F6 | Spotify tracks carry ISRC → §3.2 step 1, exact, automatic. Records join the shared catalog (§3.0). |
| B views playlist + metadata | — | **No connector required to view** (N6). Album art free via Cover Art Archive on MBID. |
| B exports to Apple Music | F7 | Check MusicBrainz URL relationships before spending provider calls (§3.2), then Apple ISRC lookup. |
| B rates, tags, comments | §3.4 | Annotations on A's playlist. No revision written. |

**Export is lossy by nature, and that must be visible.** Some tracks will not exist on the
target service — regional licensing, exclusives, delistings. "42 of 45 exported; 3 unavailable
on Apple Music" is the *normal* outcome, not an error. Each record therefore carries a per-service
export status, and the user is shown what did not transfer rather than quietly receiving a
shorter playlist (F15).

**Cost note:** this flow needs *both* connectors — an Apple Developer membership ($99/yr, paid
by the operator) and a Spotify app (BYO per user, or an operator slot). It is the most
connector-expensive path in the product, and self-hosters should be told so up front.

### 3.6 Provider resolution, storefronts, and why sync is a job

**Import is the easy direction.** Spotify returns `external_ids.isrc` on every track, so an
imported playlist lands at §3.2 step 1 — exact, automatic, no disambiguation. Apple library
resources do not always carry an ISRC, so resolution may need to walk from the library item to
its catalog resource to obtain one, costing an extra call per track.

**Export is the hard direction.** For each record, resolve cheapest-first:

| # | Step | Cost |
|---|---|---|
| 1 | Cached `provider_ref` for this record, service **and storefront** | free |
| 2 | MusicBrainz URL relationship (§3.1) | free — spends no provider quota |
| 3 | Provider ISRC lookup — Apple has a first-class `filter[isrc]`; Spotify only a query form | 1 call |
| 4 | Fuzzy text search — constrained by Spotify Development Mode's 10-result cap | 1 call |
| 5 | No match → record unavailable for that service (F15) | — |

#### Provider IDs are per-storefront

**A recording's Apple Music catalog ID differs between storefronts** (`us` vs `gb` and so on),
and availability differs with it. Spotify has the analogous problem through `available_markets`.
The resolution cache therefore **cannot** be keyed `(record, service)`. It must be:

```
provider_ref(record_id, service, storefront, external_id, status, checked_at)
```

Two consequences:

1. **F15's export status is per-track *and* per-storefront.** "Unavailable" is never a global
   fact about a record — it is a fact about a record in a storefront. A friend group spread
   across countries will see genuinely different results from the same playlist.
2. A single-country friend group masks this entirely, which makes it exactly the kind of
   assumption that survives development and fails in production. Key the cache correctly from
   the first migration.

#### Rate limits make sync a background job, not a request

| Source | Limit |
|---|---|
| MusicBrainz | **1 req/sec, hard** — a 45-track cold-cache playlist is 45s of MusicBrainz alone |
| Spotify (Dev Mode) | quota pooled per developer account — which is what BYO-first (D6) fixes |
| Apple | rate-limited per developer token |

So a cold export can exceed a minute. It **must** run as a resumable background job with
progress, not inside a request/response — the UI counterpart to §7.2's rule that network I/O
never happens inside a database transaction. Per-service token-bucket limiters are required
(N-level concern, see §6 rate limiting), and remaining quota should be visible to the user,
because under Development Mode it is a scarce resource they can actually exhaust.

*API specifics above should be re-verified at implementation time. §2 already records Spotify
moving its user cap from 25 to 5; treat exact filter syntax and limits as provisional.*

**See [`streaming-integration.md`](streaming-integration.md)** for the full treatment: the
authorisation flows, the per-service import asymmetry (Spotify cannot list playlists, so import
is paste-a-link; Apple can, so it is pick-from-list), the complete user journeys, what is not
possible, and the six interface consequences that follow from these constraints.

---

## 4. Functional requirements

### Must (v1)
- **F1** — Store canonical song records (metadata only; never audio), in one **instance-wide
  shared catalog** (§3.0).
- **F2** — Store album and artist metadata attached to song records.
- **F3** — Create, edit, reorder, and delete playlists of canonical records.
- **F4** — Fuzzy search records held in the instance.
- **F5** — Fuzzy search remote sources (MusicBrainz first) and import results as records.
- **F6** — Import a playlist from a connected service into canonical records.
- **F7** — Export a playlist to a connected service, resolving each record to that service.
- **F8** — Import/export playlists as **JSPF** files, with no connector attached.
- **F9** — Share a playlist or song with another user on the instance, **by reference** (§3.4).
- **F10** — Per-user connection of a streaming account via OAuth, with revocation.
- **F11** — Deep-link out to a song on the user's preferred service for playback.
- **F15** — Report **per-service export status** per record; show the user what did not
  transfer rather than silently delivering a shorter playlist (§3.5).
- **F16** — **The crate**: a persistent per-user staging area accumulating candidates from any
  source, committed to a playlist as one authored revision (§3.3). Includes the
  direct-to-playlist fast path that bypasses staging for clean high-confidence imports.
- **F17** — **Playlist revision history** — append-only, with actor, timestamp, and operation,
  supporting version tracking and blame (§3.4).
- **F19** — **BYO provider credentials**: a user may supply their own Spotify Client ID and
  Secret; the instance falls back to an operator-provided app where one is configured and a
  slot is free (§2, D6). Remaining operator slots are surfaced in the UI.
- **F21** — **Tracked sync state** per `(playlist, service, storefront)`: when it last synced,
  how many revisions the provider copy is behind, and a re-sync action (D10). Divergence caused
  by editing on the provider side must be **detected and surfaced, never silently overwritten**.
- **F22** — **Provider operations run as resumable background jobs** with visible progress, not
  inside a request/response (§3.6). Includes per-service rate limiting and surfacing remaining
  provider quota, which is genuinely exhaustible under Spotify Development Mode.

### Should (v1.x)
- **F12** — Disambiguation UI for low-confidence matches, surfaced inside the crate before
  commit.
- **F13** — Setup wizard — **per user** for BYO provider credentials, and **per operator** for
  instance-level credentials.
- **F14** — Re-resolve a record whose provider link has gone dead.
- **F18** — **Annotations UI**: per-user playlist ratings with aggregate, private and shared
  tags, and attributed comments (§3.4). *The schema for these ships in v1 (D7); only the UI is
  deferred.*
- **F20** — Fork a shared playlist into the user's own, with provenance back to the original.

> **Schema-now, UI-later.** F17 and F18 shape the data model and are therefore built into the
> v1 schema even where their interface lands in v1.x. Retrofitting revisions and blame onto
> mutable playlists after real data exists is the expensive path.

### Won't (explicitly out)
- Storing, hosting, transcoding, or serving audio files.
- Circumventing DRM or provider terms of service.
- Scraping provider endpoints not covered by a public API, or any service with no obtainable
  public API (D1).

---

## 5. Non-functional requirements

| # | Requirement | Implication |
|---|---|---|
| N1 | **Low resources** | Must run comfortably on a Raspberry Pi or a $5 VPS. Rules out a JVM-scale runtime and a multi-container default deployment. |
| N2 | **Self-hostable by a non-expert** | Single artifact, zero external service dependencies to boot. `docker run` or one binary. |
| N3 | **Mobile-first, not mobile-only** | The phone is the primary interaction and sets the design order — touch targets and small screens are solved first. **Desktop is a first-class target, not a fallback**: the PWA installs and runs on desktop too, and some tasks are genuinely better there (bulk crate curation, disambiguating a long import, editing a large playlist). Layouts must scale up deliberately — a stretched phone column is not a desktop design. |
| N4 | **Open source** | License decision pending. AGPL-3.0 is the usual choice for self-hosted social software; MIT if wide adoption matters more. |
| N5 | **Standards-based** | ISRC, MusicBrainz MBID, JSPF/XSPF, OAuth 2.0 + PKCE, OIDC where auth is delegated. |
| N6 | **Degrades to zero connectors** | Full local + JSPF functionality with no provider linked. |

---

## 6. Security profile (draft — for §8 confirmation)

Profile is **production**. The dominant concern is not the app's own data — song metadata is
public — it is the **provider OAuth tokens**, which are crown-jewel secrets granting write
access to a user's real music library.

| Area | Requirement |
|---|---|
| **Token storage** | Provider refresh/access tokens encrypted at rest with **AES-256-GCM**; key from environment or secret manager, never in the DB or repo. Never logged, never rendered, never included in exports. |
| **OAuth flows** | Authorization Code + **PKCE** for every provider. Strict `redirect_uri` allowlist. `state` validated. |
| **Instance credentials** | Operator-provided Client Secrets come from the environment/secret manager. No secrets in the image, repo, or config template. |
| **Per-user credentials** | **Changed by D6.** BYO-first means user-supplied Spotify Client Secrets are stored in the **database**, not the environment. They get the same treatment as OAuth tokens: **AES-256-GCM** at rest, key from the environment, never logged, rendered, or exported. This is now a primary path, not an edge case — design it in from the start. |
| **Auth** | **Local accounts are the default** — invite-only registration, passwords **Argon2id**. **OIDC is supported but optional** (D5), configured per instance; when enabled, no local password is stored for OIDC users. Invite-only applies to both paths as an *authorization* gate, not merely a registration one. Sessions in `HttpOnly` + `Secure` + `SameSite=Lax` cookies. CSRF tokens on state-changing routes. |
| **Authorization** | Checked on every request, including playlist, share, and annotation access. A shared playlist link must not be a bearer of unlimited authority. **Private tags are readable only by their author** — enforced server-side, never by client filtering. |
| **Data classification** | Song metadata and the shared catalog: **public**. Listening/sharing activity, private tags, provider tokens, and per-user Client Secrets: **confidential**. |
| **Rate limiting** | On the app's own API, and a client-side limiter respecting MusicBrainz's 1 req/sec and provider quotas. |
| **Compliance** | Assumed none. To confirm. |

---

## 7. Stack

| Layer | Decision | Reasoning |
|---|---|---|
| **Language** | **Go** — see §7.1 | One static binary, ARM cross-compile for a Pi, ~20MB idle. Directly serves N1/N2, the project's hardest constraints. |
| **Data store** | **SQLite (dev) → MariaDB (prod)** — see §7.2 | Confirmed by project owner. Sequencing and dialect-parity costs discussed below. |
| **Search** | **SQLite FTS5** / **InnoDB FULLTEXT** | Satisfies F4 with no extra service. No Elasticsearch or Meilisearch — both violate N1. Dual-dialect cost noted in §7.2. |
| **Client** | **PWA** served by the same binary — *confirmed* | One artifact, no app store, installs to home screen on iOS/Android, works on desktop. Best fit for N2+N3. Trade-off accepted: no native MusicKit playback, though MusicKit JS covers web playback for Apple subscribers. |
| **Auth** | **Local accounts (default) + optional OIDC** — both in v1 (D5) | Requiring OIDC would force every self-hoster to run or register an identity provider before their friends could log in, which collides head-on with N2. Local invite-only accounts keep the zero-dependency boot; OIDC is opt-in configuration for those who already have an IdP. Cost is one auth abstraction, not an auth rewrite. |

### 7.1 Go vs. Python — where Python's ecosystem would actually help

The question is fair: Python's library ecosystem is far larger, and much of the open music-data
world is written in it. Assessed against features Waxgrove might plausibly want:

| Candidate feature | Python advantage | Verdict |
|---|---|---|
| **Recommendations / semantic search** ("more like this", auto-generated playlists) | **Decisive.** scikit-learn, `implicit`, sentence-transformers, PyTorch. Go has no comparable ecosystem. | **The only decisive case.** Not in the current requirements. |
| **MetaBrainz interop** | MetaBrainz's own stack is Python — ListenBrainz is Flask, and **Troi**, their playlist-generation toolkit, is Python. Native integration if Waxgrove is Python too. | Real but modest — the APIs are plain REST/JSON. |
| **Local fuzzy matching** | `rapidfuzz`, `jellyfish`, `recordlinkage` are excellent. Go has thinner equivalents. | **Mostly neutralized** — the ListenBrainz MBID Mapper does the heavy lifting remotely (§3.1). The local fallback is Jaro-Winkler over normalized strings: a few hundred lines in any language. |
| **Audio tag reading** (`mutagen`) | Best-in-class, no Go equal. | **N/A** — Waxgrove never reads audio files. Only relevant if "assist in playing local files" ever grows into folder scanning. |
| **Audio fingerprinting** (AcoustID) | `pyacoustid` is mature. | **N/A** — same reason. |
| **Odd-format imports** (iTunes XML, M3U, CSV) | `pandas` + `lxml` are more pleasant. | Minor. Go's stdlib is adequate. |

**Where Go wins, and why it wins here:** connector sync is a long-running, concurrent,
rate-limited workload against several hostile APIs — goroutines plus `golang.org/x/time/rate`
model that more cleanly than asyncio. More importantly, N1 and N2 are the *stated* priorities:
a self-hoster running `docker run` or dropping one binary on a Pi is the target experience, and
a Python deployment costs a runtime or a fatter container plus several times the idle memory.

**Recommendation: Go, with a deliberate seam for Python later.** Recommendations and semantic
search are the one place Python is genuinely irreplaceable — and they are also *optional,
separable* features. Design the core to call an **optional recommendation service over HTTP**,
which can be a Python sidecar if and when that feature is built. Default deployment stays a
single binary; self-hosters who don't want recommendations never install it. This gets Python's
one decisive advantage without paying its deployment cost on day one.

### 7.2 Can SQLite handle the expected concurrency?

**Question asked:** fewer than 10 users total, but 3+ concurrent users is realistic. Is SQLite
sufficient?

**Answer: yes, comfortably — by two or three orders of magnitude.** This workload is not close
to any SQLite limit.

In **WAL mode**, SQLite supports an **unlimited number of concurrent readers plus one writer**,
and readers do not block the writer or each other. So concurrent *reads* — browsing playlists,
searching the catalog, which is nearly all of Waxgrove's traffic — are genuinely parallel and
uncontended.

The real constraint is that **only one write transaction runs at a time**. Concurrent writers
serialize: with `busy_timeout` set, a second writer waits rather than failing with
`SQLITE_BUSY`. For Waxgrove's writes (save a playlist, add a track, update a record) that wait
is sub-millisecond. Three concurrent users writing simultaneously is a non-event.

**The risk is transaction *duration*, not user count.** The one way to make this hurt is a
long-running write transaction — e.g. a Spotify playlist sync writing 500 tracks while holding
the write lock, blocking everyone else's saves. Design rules that follow:

- **All network I/O happens outside transactions.** Fetch from the provider, resolve, *then*
  open a short transaction to write. Never hold the write lock across an HTTP call.
- **Batch long syncs into chunks** — commit every N records rather than one giant transaction.
- **Use two `*sql.DB` instances**: a write pool with `SetMaxOpenConns(1)` and a separate read
  pool with a higher limit. Go's `database/sql` opens multiple connections by default, and each
  gets its own SQLite lock — which manufactures `SQLITE_BUSY` contention that wouldn't otherwise
  exist. Forcing writes through one connection serializes them cleanly at the Go layer instead.
- **Required DSN pragmas on both pools:** `journal_mode=WAL`, `busy_timeout=5000`,
  `foreign_keys=ON`, `synchronous=NORMAL` (safe under WAL).

**Consequence for the MariaDB requirement:** load is not a reason to add MariaDB here. If it
stays on the roadmap it should be because of existing infrastructure, backup tooling, or
preference — all legitimate, but worth naming, because §7.3 shows the dual-dialect cost is not
trivial. **Recommendation: ship SQLite everywhere for v1**, keep the repository seam clean, and
add MariaDB only if a concrete need appears.

### 7.3 SQLite (dev) + MariaDB (prod) — the cost of dual dialects

Requested by the project owner; sequencing confirmed as **SQLite first, MariaDB adapter at M2**.
Two costs to go in with eyes open about:

1. **Full-text search does not port.** SQLite FTS5 and MariaDB InnoDB `FULLTEXT ... MATCH/AGAINST`
   have different syntax, different tokenizers, and different relevance ranking. F4 is a core
   feature, so this means **two search implementations that must return comparable results**.
   This is the single largest cost of dual-dialect support.
2. **Dev/prod parity risk.** SQLite and MariaDB differ in type affinity, upsert syntax,
   `RETURNING` support, transaction/locking semantics, and — most dangerously here — **string
   collation and case sensitivity**. Artist and title comparison is central to matching, so a
   collation difference is exactly the class of bug that passes in dev and fails in prod.

**Recommended mitigations:**

- All persistence behind a **repository interface**; no dialect-specific SQL outside a dialect
  adapter package.
- **Normalize aggressively in Go, not in SQL** — store a pre-computed `normalized_artist` and
  `normalized_title` (casefolded, accent-stripped, punctuation-removed) as plain columns, and
  match on those. This makes matching behavior identical on both engines by construction, and
  sidesteps a known limitation of `modernc.org/sqlite`: it does not cleanly support registering
  custom Go functions with SQLite, so custom scoring must live in Go anyway.
- **Run the integration test suite against both engines in CI.** Non-negotiable — it is the only
  thing that keeps parity honest.
- **Sequence it:** build SQLite-first, add the MariaDB adapter once the schema stabilizes.
  Maintaining two dialects while the schema is still churning doubles the churn on the
  fastest-moving part of the codebase.

*Worth noting for later:* the friends-scale workload (5–25 users, read-heavy) is well within
SQLite-in-WAL-mode territory — it is what Navidrome and similar self-hosted apps ship in
production. If the MariaDB requirement stems from existing infrastructure or backup tooling,
that is a good reason; if it stems from expected load, it is likely unnecessary.

---

## 8. Open decisions

### D1 — Services without an obtainable API — **RESOLVED 2026-08-02, narrowed 2026-08-03**

**Connector support is limited to Spotify and Apple Music.** Any service whose API is closed,
partner-only, or otherwise unobtainable by a self-hoster is **out of scope** — no connector, no
reverse-engineered endpoints. Scraped endpoints break constantly and put the project crosswise
with provider terms (§4, Won't).

*(2026-08-03: Amazon Music was previously carried here as a manual-import case. It has been
dropped from requirements entirely rather than tracked as a gap.)*

Such services are reached, if at all, through **JSPF file export/import** — the user moves the
file themselves — or a deep-link out (F11) if a link can be resolved without a connector.

This resolution has a wider effect that outlives any individual service: since manual file
exchange is an acceptable path for *any* service, the JSPF layer (F8) is promoted from a
convenience feature to **the universal fallback that guarantees every objective remains
reachable even with zero connectors**. It should be built first and treated as the reference
implementation of sharing; connectors are then optimizations that remove manual steps, not
prerequisites.

### D2 — Client shape — **RESOLVED 2026-08-02**

**PWA**, served by the same binary. Accepted trade-off: no native MusicKit playback.

### D3 — Sharing model — **RESOLVED 2026-08-02**

**One shared instance** per friend group. Federation is deferred; cross-instance sharing is
covered by JSPF export/import (see D1). Consequence to surface in the UI: the Spotify 5-user
cap applies at the instance level, so an instance with more than 5 Spotify-linked users must
shard across additional Client IDs.

### D4 — Stack — **RESOLVED 2026-08-02**

**Go core**, with a deliberate seam for an **optional Python sidecar** serving future
recommendation / semantic-search features over HTTP (§7.1). Default deployment stays a single
binary.

**SQLite first; MariaDB adapter deferred to M2** (§7.3). The load question that prompted the
MariaDB requirement is answered in §7.2 — SQLite in WAL mode handles this workload with orders
of magnitude to spare, so MariaDB is now optional rather than required. Left open, and worth
deciding before M2 rather than now: whether to build the MariaDB adapter at all.

### D5 — Auth model — **RESOLVED 2026-08-03**

**Local accounts by default, OIDC optional — both in v1.** `objectives.md` lists "OIDC logins"
as a goal, which appeared to conflict with §7's local-accounts design. It is a false dilemma:
*requiring* OIDC would force every self-hoster to stand up or register an identity provider
before their friends could log in, breaking N2. Supporting both satisfies the goal at the cost
of one auth abstraction. Argon2id remains for local accounts; invite-only becomes an
authorization gate covering both paths.

### D6 — Spotify credentials — **RESOLVED 2026-08-03**

**BYO-first, operator app as fallback.** The Development Mode cap is 5 users *per app*, so it
only becomes an instance ceiling if one operator app serves everyone. Each user registering
their own free app removes the ceiling at any instance size and gives each user their own
quota rather than contending for a pooled one. The operator app remains available for its 5
slots; because the Premium requirement falls on the app *owner*, those slots are best reserved
for users who cannot do BYO. Does not transfer to Apple Music, where registration costs
$99/yr — Apple stays operator-provided (§2).

### D7 — Annotation scope — **RESOLVED 2026-08-03**

**Revision schema in v1; annotation UI in v1.x.** Version tracking and blame determine the
shape of the playlist tables, so they are built now (F17). Tagging, comments, and ratings ship
their interface later (F18) against a schema that already accommodates them. Retrofitting an
append-only revision model onto mutable playlists after real data exists is the expensive path.

### D8 — Playlist sharing and annotation model — **RESOLVED 2026-08-03**

**Playlists are shared by reference, not copied** — one object, owned by one user, viewable,
exportable, and annotatable by others; forking is explicit (F20). **Ratings are per-user with
an aggregate displayed.** **Tags come in two kinds — private and shared.** Annotations never
write to the playlist revision history (§3.4).

### D9 — Metadata sources — **RESOLVED 2026-08-03**

**MusicBrainz remains the identity authority.** Added as resolvers and enrichers only:
**Deezer** and the **iTunes Search API** (both keyless), plus **Last.fm** and **Wikidata** for
enrichment. **Odesli/Songlink declined** — useful but a third-party dependency the design
should not lean on. **Discogs deferred** as a post-v1 enrichment candidate. Everything these
return maps into ISRC or MBID; none introduces a competing identity namespace (§3.1).

### D10 — Sync semantics — **RESOLVED 2026-08-03**

**Tracked one-way sync.** Waxgrove remembers the provider playlist it created
(`playlist_sync(playlist_id, service, storefront, provider_playlist_id, last_synced_rev,
last_synced_at)`) and can re-push when the source playlist moves ahead. Rejected alternatives:

| Option | Why not |
|---|---|
| **Snapshot** (push once, forget) | Breaks the reference flow in §3.5 — when Ana revises the playlist, Ben has no signal that his Apple Music copy is stale, and no path to refresh it beyond exporting again by hand. |
| **Two-way sync** | Requires conflict resolution, deletion semantics, and continuous polling of both services. Ruinous against Development Mode quota, and far beyond a friends-scale app. |

Consequences to design for:

- **Sync state is user-visible**: last synced, and how many revisions behind the provider copy
  now is (F21). "Synced" is not a boolean.
- **Divergence policy needed.** If the user edits the playlist *on the provider side*, a re-sync
  would overwrite it. Waxgrove must detect divergence and ask rather than silently clobbering —
  the same "never silently mismatch" principle §3.2 applies to matching.
- Sync is **per (playlist, service, storefront)**, following §3.6 — the same playlist can be
  synced to two services, at different revisions.
- One-way means the provider copy is a **projection**, never a source of truth. Waxgrove's
  revision history (F17) remains authoritative.

---

## 11. Resume here

> **RECONCILED 2026-08-03.** The "Goals" section added to `objectives.md` has been absorbed
> into this document. Record of where each goal landed:
>
> | New goal | Resolution |
> |---|---|
> | **OIDC logins** | **D5** — local accounts remain the default, OIDC is optional; both ship in v1. The apparent conflict with §7 was a false dilemma: *requiring* OIDC would break N2. §6 and §7 updated. |
> | **User tagging, shared tagging, comments, version tracking, blame** | **D7 + D8** — revision schema (F17) and annotation model (F18) in the v1 schema; annotation UI in v1.x. Content revisions and annotations are deliberately separate histories (§3.4). |
> | **Minimal-click cross-service sync** | Handled by §3.3's crate pipeline and §3.5's reference flow. Not new architecture — F6 + resolution + F7 composed, plus F15 for honest partial-export reporting. |
> | **Rating playlists** | **D8** — per-user ratings with an aggregate displayed (§3.4, F18). |
> | **"Creation of playlists inside the app from…"** | Completed as *"from all possible sources of songs and searches"* → §3.3, the source-adapter pipeline and the crate (F16). |
>
> Goals 6 and 7 in `objectives.md` remain empty placeholders — fill or delete them.

**Next step: scaffold the Go project.** Module layout, repository interface + SQLite adapter
with the §7.2 pragmas and dual read/write pools, the canonical record schema including
revisions (F17) and annotations (F18), and the M1 feature set from §9 — playlist CRUD, FTS5
search, MusicBrainz + MBID Mapper import, JSPF import/export, and local-accounts auth with the
OIDC seam from D5.

**Toolchain confirmed on the Windows workstation:** Go 1.26.5, `CGO_ENABLED=0` — which suits
the pure-Go `modernc.org/sqlite` driver chosen in §7.

**Decisions still to make, but none blocking M1:**

1. **License** — AGPL-3.0 vs MIT (§5, N4).
2. **Whether to build the MariaDB adapter at all** — revisit at M2 (§7.3).
3. **Compliance** — assumed none; confirm (§6).
4. **Prohibited patterns** — none recorded yet for this project.

**Security profile to re-confirm at scaffold time** (§6): production profile; provider OAuth
tokens are the crown jewels and must be AES-256-GCM encrypted at rest with a key from the
environment; invite-only registration; Argon2id passwords; PKCE on every OAuth flow.

---

## 9. Proposed first milestone

**M1 — The neutral core, with no connectors at all.**

Canonical record model · playlist CRUD · FTS5 fuzzy local search · MusicBrainz remote search
and import · JSPF import/export · invite-only auth.

The reason to sequence it this way: it delivers a fully working product that no provider can
revoke, it forces the canonical identity layer to be correct before any connector can bias it
toward one platform's ID scheme, and it makes the Spotify connector (M2) a plug-in rather than
a foundation.

---

## 10. Sources

- [Spotify — Quota modes](https://developer.spotify.com/documentation/web-api/concepts/quota-modes)
- [Spotify — February 2026 Web API Dev Mode migration guide](https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide)
- [Spotify — Web API quota updates, July 2026](https://developer.spotify.com/blog/2026-07-23-web-api-quota-updates)
- [Spotify — Updating the criteria for Web API extended access](https://developer.spotify.com/blog/2025-04-15-updating-the-criteria-for-web-api-extended-access)
- [Apple — iTunes Search API](https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/iTuneSearchAPI/)
- [Deezer — API reference](https://developers.deezer.com/api)
- [Last.fm — API](https://www.last.fm/api)
- [Wikidata — SPARQL query service](https://query.wikidata.org/)
- [Apple — Generating developer tokens](https://developer.apple.com/documentation/applemusicapi/generating-developer-tokens)
- [MusicBrainz — JSPF](https://musicbrainz.org/doc/jspf)
- [MusicBrainz — API](https://musicbrainz.org/doc/MusicBrainz_API)
- [MusicBrainz — Recording-URL relationship types](https://musicbrainz.org/relationships/recording-url)
- [MusicBrainz — "Free streaming" relationship type](https://musicbrainz.org/relationship/08445ccf-7b99-4438-9f9a-fb9ac18099ee)
- [ListenBrainz — Playlists API](https://listenbrainz.readthedocs.io/en/latest/users/api/playlist.html)
- [ListenBrainz — MBID mapping](https://listenbrainz.readthedocs.io/en/latest/maintainers/mapping.html)
- [ListenBrainz — MBID Mapping documentation (MusicBrainz wiki)](https://musicbrainz.org/doc/ListenBrainz/MBIDMappingDocumentation)
- [ListenBrainz Labs API — dataset hoster](https://labs.api.listenbrainz.org/)
