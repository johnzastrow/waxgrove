# Waxgrove — Database Schema

**Date:** 2026-08-03
**Migration:** `internal/repository/sqlite/migrations/0001_init.sql`
**Parent:** [`requirements.md`](requirements.md) — every rule here traces to a decision there.

21 tables plus an FTS5 index. SQLite first; a MariaDB adapter sits behind the same repository
interface if it is ever built (§7.3). Timestamps are **RFC3339 UTC strings** for dialect
portability; identifiers are opaque **TEXT** rather than integers so records can be merged and
exported without renumbering.

---

## 1. Entity relationships

Split into four diagrams. One diagram of 21 tables is unreadable, and these groups map to how
the system actually divides: who you are, what the group owns, what a playlist means, and what
is in flight.

### 1.1 Identity and access

```mermaid
erDiagram
    USERS ||--o{ SESSIONS : "authenticates via"
    USERS ||--o{ INVITES : "creates"
    USERS ||--o| INVITES : "was admitted by"
    USERS ||--o{ USER_PROVIDER_CREDENTIALS : "connects"
    USERS ||--o| USERS : "invited_by"

    USERS {
        TEXT id PK
        TEXT email UK "NULL once anonymised"
        TEXT display_name
        TEXT password_hash "NULL for OIDC-only"
        TEXT oidc_subject UK "NULL for local-only"
        TEXT role "member | admin"
        TEXT invited_by FK
        TEXT deleted_at
        TEXT anonymized_at
    }
    SESSIONS {
        TEXT id PK "opaque, high entropy"
        TEXT user_id FK
        TEXT expires_at
    }
    INVITES {
        TEXT code PK
        TEXT created_by FK
        TEXT used_by FK
        TEXT expires_at
    }
    USER_PROVIDER_CREDENTIALS {
        TEXT user_id PK-FK
        TEXT service PK "spotify | apple"
        TEXT client_id "BYO, D6"
        BLOB client_secret_enc "AES-256-GCM"
        BLOB access_token_enc "AES-256-GCM"
        BLOB refresh_token_enc "AES-256-GCM"
        TEXT storefront
    }
```

### 1.2 The catalog

```mermaid
erDiagram
    RECORDS ||--o{ RECORD_ISRCS : "is known by"
    RECORDS ||--o{ RECORD_PROVENANCE : "was added by"
    RECORDS ||--o{ PROVIDER_REFS : "resolves to"
    USERS ||--o{ RECORD_PROVENANCE : "contributed"

    RECORDS {
        TEXT id PK
        TEXT mbid UK "primary identity"
        TEXT title
        TEXT artist_credit
        TEXT album
        INTEGER duration_ms
        INTEGER year
        TEXT norm_title "computed in Go"
        TEXT norm_artist "computed in Go"
        TEXT tier "curated | ambient"
    }
    RECORD_ISRCS {
        TEXT record_id PK-FK
        TEXT isrc PK "globally unique"
    }
    RECORD_PROVENANCE {
        TEXT id PK
        TEXT record_id FK
        TEXT user_id FK "NULL once anonymised"
        TEXT added_at
    }
    PROVIDER_REFS {
        TEXT record_id PK-FK
        TEXT service PK "spotify | apple"
        TEXT storefront PK "us | gb | ..."
        TEXT external_id
        TEXT status "ok|absent|region|unknown"
        TEXT checked_at
    }
```

### 1.3 Playlists, versions and annotations

Note the deliberate asymmetry: `PLAYLIST_REVISIONS` records content history; ratings, tags and
comments do not touch it (§3.4).

```mermaid
erDiagram
    USERS ||--o{ PLAYLISTS : owns
    PLAYLISTS ||--o{ PLAYLIST_TRACKS : contains
    PLAYLISTS ||--o{ PLAYLIST_REVISIONS : "content history"
    PLAYLISTS ||--o{ RATINGS : "annotated by"
    PLAYLISTS ||--o{ TAGS : "annotated by"
    PLAYLISTS ||--o{ COMMENTS : "annotated by"
    RECORDS ||--o{ PLAYLIST_TRACKS : "appears in"
    USERS ||--o{ PLAYLIST_REVISIONS : "authored"
    USERS ||--o{ RATINGS : rates
    USERS ||--o{ TAGS : tags
    USERS ||--o{ COMMENTS : writes

    PLAYLISTS {
        TEXT id PK
        TEXT owner_id FK "NULL once anonymised"
        TEXT title
        INTEGER current_rev
    }
    PLAYLIST_TRACKS {
        TEXT playlist_id PK-FK
        INTEGER position PK
        TEXT record_id FK
        INTEGER added_in_rev
    }
    PLAYLIST_REVISIONS {
        TEXT id PK
        TEXT playlist_id FK
        INTEGER rev UK "unique per playlist"
        TEXT actor_id FK "NULL once anonymised"
        TEXT op "create|add|remove|reorder|rename"
        TEXT detail "JSON"
    }
    RATINGS {
        TEXT playlist_id PK-FK
        TEXT user_id PK-FK
        INTEGER value "1..5, per user"
    }
    TAGS {
        TEXT id PK
        TEXT playlist_id FK
        TEXT user_id FK
        TEXT name
        TEXT visibility "private | shared"
    }
    COMMENTS {
        TEXT id PK
        TEXT playlist_id FK
        TEXT user_id FK "NULL once anonymised"
        TEXT body
        TEXT deleted_at
    }
```

### 1.4 Work in flight — crate, sync, jobs

```mermaid
erDiagram
    USERS ||--o{ CRATES : owns
    CRATES ||--o{ CRATE_ITEMS : stages
    RECORDS ||--o{ CRATE_ITEMS : "resolved to"
    PLAYLISTS ||--o{ PLAYLIST_SYNCS : "projected to"
    PLAYLISTS ||--o{ JOBS : "operated on by"
    JOBS ||--o{ JOB_ITEMS : "progresses through"
    RECORDS ||--o{ JOB_ITEMS : "processed as"

    CRATES {
        TEXT id PK
        TEXT user_id FK
    }
    CRATE_ITEMS {
        TEXT id PK
        TEXT crate_id FK
        INTEGER position
        TEXT record_id FK "NULL while unresolved"
        TEXT raw_candidate "JSON, survives non-match"
        TEXT resolution_method "isrc|mbid|mapper|fuzzy"
        REAL confidence
        TEXT status "resolved|ambiguous|unresolved"
    }
    PLAYLIST_SYNCS {
        TEXT playlist_id PK-FK
        TEXT service PK
        TEXT storefront PK
        TEXT provider_playlist_id
        INTEGER last_synced_rev
        INTEGER diverged "provider-side edit detected"
    }
    JOBS {
        TEXT id PK
        TEXT kind "import|export|resolve"
        TEXT state "queued|running|paused|done|failed|cancelled"
        INTEGER done
        INTEGER total
        TEXT error
    }
    JOB_ITEMS {
        TEXT id PK
        TEXT job_id FK
        INTEGER position
        TEXT record_id FK
        TEXT status
    }
```

---

## 2. Business rules

These are the rules the schema exists to enforce. Each names the decision it comes from, and
where a test guards it, the test name.

### BR-1 — MBID is identity; ISRC is a lookup key

`records.mbid` is the stable identity. `record_isrcs` is a **set** — one recording carries many
ISRCs (*Dreams* has seven). `record_isrcs.isrc` is **globally unique**, so one ISRC never points
at two recordings.

**Why:** Spotify may return `USWB10101368` and Apple `USWB19900178` for the same recording. Keyed
on a single ISRC column those become two records and global deduplication silently fails — in
exactly the cross-service case the product exists to serve. *(§3; `TestOneRecordHoldsManyISRCs`,
`TestISRCCannotBeClaimedByTwoRecords`.)*

A record may exist with `mbid IS NULL` when nothing in MusicBrainz has matched yet. It is then
identified by its ISRC set, and merges into the MBID-keyed record when one is found.

### BR-2 — Availability is per storefront, never global

`provider_refs` is keyed `(record_id, service, storefront)`. "Unavailable" is a fact about a
record *in a storefront*, not about the record.

**Why:** an Apple catalog ID differs between storefronts and availability differs with it. A
single-country friend group masks this entirely. *(§3.6; `TestProviderRefsAreStorefrontScoped`.)*

### BR-3 — Content is versioned; annotations are not

`playlist_revisions` is append-only and records `(rev, actor_id, op)`. Ratings, tags and comments
write **nothing** to it.

**Why:** rating or tagging someone else's playlist is not a content change. If annotations wrote
to the revision log, a playlist's history would fill with other people's tags and blame would
become meaningless. *(§3.4, D7, D8.)*

### BR-4 — Erasure anonymises; it does not delete history

Every attribution column is **nullable** with `ON DELETE SET NULL`: `playlist_revisions.actor_id`,
`comments.user_id`, `record_provenance.user_id`, `playlists.owner_id`, `invites.created_by`.
Deleting a user nulls attribution and leaves the rows.

Cascading deletes are reserved for data that is *only* about that user: sessions, credentials,
ratings, tags, crates.

**Why:** GDPR Art. 17 versus an append-only history. Destroying revisions would corrupt playlist
history other people depend on, and the content of a revision is playlist structure, not personal
data. *(§6.1; `TestErasureAnonymisesButPreservesHistory`.)*

> `foreign_keys=ON` is **verified at startup**, not assumed. With foreign keys off, SQLite ignores
> `ON DELETE SET NULL` and this entire rule silently stops working.

### BR-5 — Nothing is silently dropped

`crate_items.record_id` is nullable and `raw_candidate` holds the original text. An item that
resolves to nothing stays in the crate for the user to decide about.

**Why:** §3.2's "never silently mismatch", applied to ingestion. *(§3.3;
`TestCrateHoldsUnresolvedItems`.)*

### BR-6 — Two catalog tiers

`records.tier` is `curated` or `ambient`. Curated records were deliberately added and appear in
search; ambient records arrived as a side effect of an album fetch, stay out of search, and are
promoted to curated on first deliberate use.

**Why:** fetching a whole album costs almost nothing, but letting unchosen tracks into search
turns a catalog the group built into a partial mirror of MusicBrainz. *(D11, F24.)*

### BR-7 — Normalisation happens in Go, not SQL

`norm_title` and `norm_artist` are computed by the application — casefolded, accent-stripped,
punctuation-removed — and stored as plain columns.

**Why:** it makes matching behave identically on SQLite and MariaDB by construction, and
sidesteps `modernc.org/sqlite` not supporting custom Go functions. *(§7.3.)*

### BR-8 — Provider secrets are ciphertext

`client_secret_enc`, `access_token_enc` and `refresh_token_enc` are `BLOB` holding
`nonce || AES-256-GCM ciphertext`. The key comes from the environment and is never stored here.

**Why:** these are the crown jewels — they grant write access to a user's real music library.
*(§6, D6.)*

### BR-9 — The provider copy is a projection

`playlist_syncs` records where a playlist was pushed and at which revision. `diverged` marks a
provider-side edit. Sync is one-way: Waxgrove's revision history is authoritative.

**Why:** two-way sync needs conflict resolution and constant polling, ruinous against Development
Mode quota. *(D10, F21.)*

### BR-10 — Ratings are per user

`ratings` is keyed `(playlist_id, user_id)`. Any aggregate is derived, never stored.

**Why:** B rating A's playlist is B's own opinion; it must not overwrite A's. *(D8.)*

### BR-11 — Private tags are private server-side

`tags.visibility` is `private` or `shared`. Private tags are readable only by their author, and
that must be enforced in queries — never by filtering in the client.

**Why:** §6 classifies private tags as confidential.

### BR-12 — Long provider work is a job

`jobs` and `job_items` carry state and progress. Provider operations run here, not in a request.

**Why:** MusicBrainz averages one request per second, so a cold sync exceeds a minute. *(§3.6,
F22.)*

---

## 3. Data dictionary

`PK` primary key · `FK` foreign key · `UK` unique · **enc** = AES-256-GCM ciphertext.
All timestamps are RFC3339 UTC strings.

### users

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | TEXT | PK | Opaque identifier |
| `email` | TEXT | yes, UK | Nulled on erasure (BR-4) |
| `display_name` | TEXT | no | |
| `password_hash` | TEXT | yes | **Argon2id**. NULL for OIDC-only users (D5) |
| `oidc_subject` | TEXT | yes, UK | NULL for local-only users (D5) |
| `role` | TEXT | no | `member` \| `admin` |
| `invited_by` | TEXT | yes → users | `SET NULL` |
| `created_at` | TEXT | no | |
| `deleted_at` | TEXT | yes | Soft delete marker |
| `anonymized_at` | TEXT | yes | Set by the erasure path (F26) |

### sessions

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | TEXT | PK | Opaque, high-entropy; from `crypto/rand` |
| `user_id` | TEXT | no → users | `CASCADE` — sessions die with the user |
| `expires_at` | TEXT | no | |
| `created_at` | TEXT | no | |

### invites

| Column | Type | Null | Notes |
|---|---|---|---|
| `code` | TEXT | PK | Registration is invite-only (§6) |
| `created_by` | TEXT | yes → users | `SET NULL` |
| `used_by` | TEXT | yes → users | `SET NULL`; NULL while unredeemed |
| `expires_at` | TEXT | no | |
| `created_at` | TEXT | no | |

### user_provider_credentials

| Column | Type | Null | Notes |
|---|---|---|---|
| `user_id` | TEXT | PK → users | `CASCADE` |
| `service` | TEXT | PK | `spotify` \| `apple` |
| `storefront` | TEXT | yes | Drives `provider_refs` lookups (BR-2) |
| `client_id` | TEXT | yes | BYO-first (D6); NULL when using an operator app |
| `client_secret_enc` | BLOB | yes | **enc** (BR-8) |
| `access_token_enc` | BLOB | yes | **enc** |
| `refresh_token_enc` | BLOB | yes | **enc** |
| `expires_at` | TEXT | yes | Access-token expiry |
| `scopes` | TEXT | yes | Granted scopes |

### records

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | TEXT | PK | |
| `mbid` | TEXT | yes, UK | **Primary identity** when known (BR-1) |
| `title` | TEXT | no | As displayed |
| `artist_credit` | TEXT | no | MusicBrainz artist-credit string |
| `album` | TEXT | yes | |
| `duration_ms` | INTEGER | yes | Used for the ±3s fuzzy window (§3.2) |
| `year` | INTEGER | yes | |
| `norm_title` | TEXT | no | Computed in Go (BR-7) |
| `norm_artist` | TEXT | no | Computed in Go (BR-7) |
| `tier` | TEXT | no | `curated` \| `ambient`, default `ambient` (BR-6) |

### record_isrcs

| Column | Type | Null | Notes |
|---|---|---|---|
| `record_id` | TEXT | PK → records | `CASCADE` |
| `isrc` | TEXT | PK | **Globally unique** — a collision means merge, not insert (BR-1) |

### record_provenance

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | TEXT | PK | |
| `record_id` | TEXT | no → records | `CASCADE` |
| `user_id` | TEXT | yes → users | `SET NULL` (BR-4) |
| `added_at` | TEXT | no | Meaningful only for curated records |

### provider_refs

| Column | Type | Null | Notes |
|---|---|---|---|
| `record_id` | TEXT | PK → records | `CASCADE` |
| `service` | TEXT | PK | `spotify` \| `apple` |
| `storefront` | TEXT | PK | Regional scope (BR-2) |
| `external_id` | TEXT | yes | NULL when absent from that storefront |
| `status` | TEXT | no | `ok` \| `absent` \| `region` \| `unknown` |
| `checked_at` | TEXT | no | Supports re-resolution (F14) |

### playlists

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | TEXT | PK | |
| `owner_id` | TEXT | yes → users | `SET NULL` (BR-4) |
| `title` | TEXT | no | |
| `description` | TEXT | yes | |
| `current_rev` | INTEGER | no | Latest revision number |

### playlist_tracks

| Column | Type | Null | Notes |
|---|---|---|---|
| `playlist_id` | TEXT | PK → playlists | `CASCADE` |
| `position` | INTEGER | PK | Zero-based order |
| `record_id` | TEXT | no → records | No cascade: a record in use is not deletable |
| `added_in_rev` | INTEGER | no | Revision that introduced this track |

### playlist_revisions

| Column | Type | Null | Notes |
|---|---|---|---|
| `id` | TEXT | PK | |
| `playlist_id` | TEXT | no → playlists | `CASCADE` |
| `rev` | INTEGER | no | Unique per playlist |
| `actor_id` | TEXT | yes → users | `SET NULL` (BR-4) |
| `op` | TEXT | no | `create` \| `add` \| `remove` \| `reorder` \| `rename` |
| `detail` | TEXT | yes | JSON payload |

> A crate commit writes **exactly one** row here, however many tracks it carried — which is what
> keeps blame legible (§3.3).

### ratings · tags · comments

| Table | Key | Notes |
|---|---|---|
| `ratings` | `(playlist_id, user_id)` | `value` 1–5, per user; aggregate derived (BR-10) |
| `tags` | `id`, unique `(playlist_id, user_id, name, visibility)` | `visibility` `private` \| `shared` (BR-11) |
| `comments` | `id` | `user_id` nullable (BR-4); `deleted_at` for soft delete |

### crates · crate_items

| Column | Type | Null | Notes |
|---|---|---|---|
| `crates.user_id` | TEXT | no → users | `CASCADE`; one working crate per user |
| `crate_items.record_id` | TEXT | **yes** → records | NULL while unresolved (BR-5) |
| `crate_items.raw_candidate` | TEXT | yes | JSON; survives a non-match |
| `crate_items.source_ref` | TEXT | yes | Which source adapter produced it |
| `crate_items.resolution_method` | TEXT | yes | `isrc` \| `mbid` \| `mapper` \| `fuzzy` |
| `crate_items.confidence` | REAL | yes | Score from §3.2 |
| `crate_items.status` | TEXT | no | `resolved` \| `ambiguous` \| `unresolved` |

### playlist_syncs

| Column | Type | Null | Notes |
|---|---|---|---|
| `playlist_id` | TEXT | PK → playlists | `CASCADE` |
| `service` / `storefront` | TEXT | PK | Same playlist may sync to two services (BR-9) |
| `provider_playlist_id` | TEXT | yes | The playlist Waxgrove created there |
| `last_synced_rev` | INTEGER | yes | Drift = `current_rev − last_synced_rev` (F21) |
| `diverged` | INTEGER | no | `1` = edited provider-side; re-sync must ask (D10) |

### jobs · job_items

| Column | Type | Null | Notes |
|---|---|---|---|
| `jobs.kind` | TEXT | no | `import` \| `export` \| `resolve` |
| `jobs.state` | TEXT | no | `queued` \| `running` \| `paused` \| `done` \| `failed` \| `cancelled` |
| `jobs.done` / `total` | INTEGER | no | Progress (F22) |
| `jobs.error` | TEXT | yes | Internal detail — never rendered to a client (§6) |
| `job_items.status` | TEXT | no | Per-record outcome; feeds F15 |

### records_fts

FTS5 external-content index over `records(title, artist_credit, album)`, kept current by three
triggers. Backs F4 with no extra service. MariaDB would use InnoDB `FULLTEXT` behind the same
repository interface (§7.3).

### schema_migrations

| Column | Type | Notes |
|---|---|---|
| `name` | TEXT PK | Migration filename |
| `applied_at` | TEXT | Applied in lexical order, each in its own transaction |

---

## 4. Indexes

| Index | Purpose |
|---|---|
| `ux_record_isrcs_isrc` | Enforces BR-1 — one ISRC, one recording |
| `ix_records_norm` | `(norm_artist, norm_title)` for the fuzzy fallback |
| `ix_records_tier` | Filters ambient records out of search (BR-6) |
| `ix_playlist_tracks_record` | "Which playlists contain this record?" |
| `ix_tags_playlist` | `(playlist_id, visibility)` for the private/shared split |
| `ix_jobs_state` | Job queue scan |
| `ix_sessions_user`, `ix_comments_playlist`, `ix_crate_items_crate`, `ix_job_items_job`, `ix_record_provenance_record` | Foreign-key traversal |

---

## 5. Changing this schema

1. Add a new numbered file in `internal/repository/sqlite/migrations/`. Never edit an applied one.
2. Migrations run in lexical order, each in its own transaction, tracked in `schema_migrations`.
3. Keep attribution columns nullable — BR-4 depends on it, and retrofitting nullability onto
   `NOT NULL` foreign keys after real data exists is the expensive path.
4. Add a test that asserts the *rule*, not the column. The tests worth having here are the ones
   that would catch a silent dedup failure, not the ones that check a column exists.
