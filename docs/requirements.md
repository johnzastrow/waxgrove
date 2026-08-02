# Waxgrove — Requirements & Architecture

**Date:** 2026-08-02
**Status:** Requirements draft. Three decisions open (§8); D1 resolved. No code written yet.
**Inputs:** `waxgroveapp.md` (objectives), `waxgrove-naming.md` (name — confirmed Waxgrove)

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

Requirement 3 asks for two-way playlist and song exchange with Apple Music, Spotify, and
Amazon Music "at a minimum." **That requirement cannot be met as written.** Verified today:

| Service | API exists | Who can get credentials | Playlist read/write | Verdict |
|---|---|---|---|---|
| **Spotify** | Yes — Web API | Anyone, but **Development Mode only**. Extended Quota needs a registered *organization* with ≥250k MAU; individuals have been ineligible since 2025-05-15. | Yes — `GET/POST/PUT/DELETE /playlists/{id}/items` | **Usable, with a hard user cap** |
| **Apple Music** | Yes — Apple Music API / MusicKit | Requires a paid **Apple Developer Program** membership, **$99/yr** | Yes | **Usable, at a cost per self-hoster** |
| **Amazon Music** | Web API exists but is **closed beta** | Approved partners only — docs say to "contact your Amazon Music point of contact." No open application path. | n/a | **Blocked. Not obtainable.** |

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
2. **The Spotify 5-user cap is an instance-level ceiling**, and it lands right at "friends"
   scale. Above 5 Spotify-linked users, an instance must shard across additional Client IDs —
   workable (25 available) but ugly, and it should be surfaced honestly in the UI.
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

### 3.1 Metadata sources — the MetaBrainz stack

Objective 4 asks for fuzzy search across records *not* stored in the app. The MetaBrainz family
covers this with **no API key, no approval, and no paid membership** — which makes it the only
search backend that can be relied on unconditionally, given Spotify search is now capped at 10
results and Amazon is unavailable.

| Source | Provides | Auth | Role in Waxgrove |
|---|---|---|---|
| **MusicBrainz** | Recordings, releases, artists, works; ISRC ↔ MBID mapping | None (User-Agent + 1 req/sec) | **Primary.** Canonical identity and remote search (F5). |
| **ListenBrainz MBID Mapper** | Fuzzy `(artist, title)` → recording MBID | None | **Primary resolver.** See below. |
| **Cover Art Archive** | Release artwork by MBID | None | Album art for F2. Keyed off MBID, so it comes free once a record resolves. |
| **ListenBrainz** | JSPF playlist hosting, import/export, recommendations | Token for writes | JSPF interop target (F8); a neutral place to publish a playlist. |
| **Discogs** | Pressing/edition depth, physical releases | Token, rate-limited | Optional enrichment. Fits the "wax" framing but not v1-critical. |
| **AcoustID** | Audio fingerprint → MBID | Key | **Not applicable.** Waxgrove never touches audio files. |

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

---

## 4. Functional requirements

### Must (v1)
- **F1** — Store canonical song records (metadata only; never audio).
- **F2** — Store album and artist metadata attached to song records.
- **F3** — Create, edit, reorder, and delete playlists of canonical records.
- **F4** — Fuzzy search records held in the instance.
- **F5** — Fuzzy search remote sources (MusicBrainz first) and import results as records.
- **F6** — Import a playlist from a connected service into canonical records.
- **F7** — Export a playlist to a connected service, resolving each record to that service.
- **F8** — Import/export playlists as **JSPF** files, with no connector attached.
- **F9** — Share a playlist or song with another user on the instance.
- **F10** — Per-user connection of a streaming account via OAuth, with revocation.
- **F11** — Deep-link out to a song on the user's preferred service for playback.

### Should (v1.x)
- **F12** — Disambiguation UI for low-confidence matches.
- **F13** — Setup wizard for instance operators to enter their own provider credentials.
- **F14** — Re-resolve a record whose provider link has gone dead.

### Won't (explicitly out)
- Storing, hosting, transcoding, or serving audio files.
- Circumventing DRM or provider terms of service.
- Scraping provider endpoints not covered by a public API. *(Relevant to Amazon Music — see §8.)*

---

## 5. Non-functional requirements

| # | Requirement | Implication |
|---|---|---|
| N1 | **Low resources** | Must run comfortably on a Raspberry Pi or a $5 VPS. Rules out a JVM-scale runtime and a multi-container default deployment. |
| N2 | **Self-hostable by a non-expert** | Single artifact, zero external service dependencies to boot. `docker run` or one binary. |
| N3 | **Mobile-first** | Primary interaction is a phone. Design for touch and small screens first, desktop second. |
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
| **Instance credentials** | Provider Client Secrets come from the operator's environment/secret manager. No secrets in the image, repo, or config template. |
| **Auth** | Invite-only registration — this is a friends app, not a public service. Passwords **Argon2id**. Sessions in `HttpOnly` + `Secure` + `SameSite=Lax` cookies. CSRF tokens on state-changing routes. |
| **Authorization** | Checked on every request, including playlist and share access. A shared playlist link must not be a bearer of unlimited authority. |
| **Data classification** | Song metadata: public. Listening/sharing activity + provider tokens: **confidential**. |
| **Rate limiting** | On the app's own API, and a client-side limiter respecting MusicBrainz's 1 req/sec and provider quotas. |
| **Compliance** | Assumed none. To confirm. |

---

## 7. Recommended stack

Presented as a recommendation, not a decision — see §8.

| Layer | Recommendation | Reasoning |
|---|---|---|
| **Language** | **Go** | Confirms the existing lean and is the right call for N1/N2: one static binary, cross-compiles to ARM for a Pi, ~20MB idle. Strong stdlib HTTP and OAuth2. The one real cost is a thinner audio-metadata ecosystem than Python's — but Waxgrove reads no audio files, so that cost mostly evaporates. |
| **Data store** | **SQLite** (`modernc.org/sqlite`, pure Go) | Zero-admin, single-file backup, no daemon. Pure-Go driver keeps static cross-compilation trivial (no cgo). Postgres stays possible behind a repository interface if a large instance ever appears. |
| **Search** | **SQLite FTS5** + trigram tokenizer | Satisfies F4's fuzzy search with no extra service. No Elasticsearch, no Meilisearch — both violate N1. |
| **Client** | **PWA** served by the same binary | One artifact, no app store, installable to home screen, works on iOS/Android/desktop. Best fit for N2+N3. Trade-off: no native Apple MusicKit playback — though MusicKit JS covers web playback for subscribers. |
| **Auth** | Local accounts, invite-only; optional OIDC later | Small trusted groups don't need an identity provider. Keeping OIDC optional avoids forcing a dependency. |

---

## 8. Open decisions

### D1 — Amazon Music — **RESOLVED 2026-08-02**

Cut from v1. Manual import/export is acceptable to the project owner, so Amazon Music (and any
other service without an obtainable API) is reached through **JSPF file export/import**, with
the user moving the file themselves. No reverse-engineered endpoints — they break constantly
and put the project crosswise with Amazon's terms.

This resolution has a wider effect: since manual file exchange is an acceptable path for *any*
service, the JSPF layer (F8) is promoted from a convenience feature to **the universal
fallback that guarantees every objective remains reachable even with zero connectors**. It
should be built first and treated as the reference implementation of sharing; connectors are
then optimizations that remove manual steps, not prerequisites.

### D2 — Client shape

PWA vs. native (React Native / Flutter) vs. responsive web only. **Recommend: PWA.**

### D3 — Sharing model

Does a friend group share **one instance** (simplest; the Spotify 5-user cap bites at the
instance level), or do **separate instances federate**? **Recommend: one instance for v1**, with
signed JSPF export/import covering cross-instance sharing. Real federation is a large lift and
should be deferred until the core works.

### D4 — Stack confirmation

Confirm Go + SQLite + PWA per §7, or discuss alternatives.

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
- [Amazon Music Web API — overview](https://developer.amazon.com/docs/music/API_web_overview.html)
- [Amazon Music Web API — playlists](https://developer.amazon.com/docs/music/API_web_playlist.html)
- [Apple — Generating developer tokens](https://developer.apple.com/documentation/applemusicapi/generating-developer-tokens)
- [MusicBrainz — JSPF](https://musicbrainz.org/doc/jspf)
- [MusicBrainz — API](https://musicbrainz.org/doc/MusicBrainz_API)
- [MusicBrainz — Recording-URL relationship types](https://musicbrainz.org/relationships/recording-url)
- [MusicBrainz — "Free streaming" relationship type](https://musicbrainz.org/relationship/08445ccf-7b99-4438-9f9a-fb9ac18099ee)
- [ListenBrainz — Playlists API](https://listenbrainz.readthedocs.io/en/latest/users/api/playlist.html)
- [ListenBrainz — MBID mapping](https://listenbrainz.readthedocs.io/en/latest/maintainers/mapping.html)
- [ListenBrainz — MBID Mapping documentation (MusicBrainz wiki)](https://musicbrainz.org/doc/ListenBrainz/MBIDMappingDocumentation)
- [ListenBrainz Labs API — dataset hoster](https://labs.api.listenbrainz.org/)
