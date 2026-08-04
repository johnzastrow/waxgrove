-- Waxgrove initial schema.
--
-- Design rules encoded here, each traceable to docs/requirements.md:
--   * MBID is primary identity; ISRC is a many-to-one lookup key held as a SET (§3).
--   * Provider IDs are cached per (record, service, storefront) (§3.6).
--   * Playlist CONTENT is versioned; ANNOTATIONS are not — separate histories (§3.4).
--   * Every attribution column is NULLABLE so a user can be anonymised without
--     destroying shared history (§6.1, GDPR Art. 17).
--   * Normalisation is computed in Go and stored, never computed in SQL (§7.3),
--     so matching behaves identically on SQLite and MariaDB.
--
-- Timestamps are RFC3339 UTC strings for dialect portability.

-- ---------------------------------------------------------------- identity --

CREATE TABLE users (
    id             TEXT PRIMARY KEY,
    email          TEXT UNIQUE,                 -- NULL once anonymised (§6.1)
    display_name   TEXT NOT NULL,
    password_hash  TEXT,                        -- NULL for OIDC-only users (D5)
    oidc_subject   TEXT UNIQUE,                 -- NULL for local-only users (D5)
    role           TEXT NOT NULL DEFAULT 'member'
                        CHECK (role IN ('member', 'admin')),
    invited_by     TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at     TEXT NOT NULL,
    deleted_at     TEXT,
    anonymized_at  TEXT                         -- set by the GDPR erasure path (F26)
);

CREATE TABLE invites (
    code        TEXT PRIMARY KEY,
    created_by  TEXT REFERENCES users (id) ON DELETE SET NULL,
    used_by     TEXT REFERENCES users (id) ON DELETE SET NULL,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,               -- opaque, high-entropy
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX ix_sessions_user ON sessions (user_id);

-- ----------------------------------------------------------------- catalog --

CREATE TABLE records (
    id             TEXT PRIMARY KEY,
    mbid           TEXT UNIQUE,                 -- primary identity once known (§3)
    title          TEXT NOT NULL,
    artist_credit  TEXT NOT NULL,
    album          TEXT,
    duration_ms    INTEGER,
    year           INTEGER,
    -- Precomputed in Go: casefolded, accent-stripped, punctuation-removed (§7.3).
    norm_title     TEXT NOT NULL,
    norm_artist    TEXT NOT NULL,
    -- D11: 'curated' records were deliberately added and appear in search;
    -- 'ambient' arrived via an album fetch and exist only to make resolution instant.
    tier           TEXT NOT NULL DEFAULT 'ambient'
                        CHECK (tier IN ('curated', 'ambient')),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX ix_records_norm ON records (norm_artist, norm_title);
CREATE INDEX ix_records_tier ON records (tier);

-- A recording carries MANY ISRCs (verified: "Dreams" has seven), so this is a
-- set, never a column on records. Keyed on ISRC alone, Spotify and Apple would
-- create two records for one recording and dedup would silently fail (§3).
CREATE TABLE record_isrcs (
    record_id  TEXT NOT NULL REFERENCES records (id) ON DELETE CASCADE,
    isrc       TEXT NOT NULL,
    PRIMARY KEY (record_id, isrc)
);
-- One ISRC identifies one recording, so it may not appear on two records.
-- A collision here means a merge is required, not a second row.
CREATE UNIQUE INDEX ux_record_isrcs_isrc ON record_isrcs (isrc);

-- Who deliberately added a record. Only meaningful for the curated tier (D11).
CREATE TABLE record_provenance (
    id         TEXT PRIMARY KEY,
    record_id  TEXT NOT NULL REFERENCES records (id) ON DELETE CASCADE,
    user_id    TEXT REFERENCES users (id) ON DELETE SET NULL,   -- NULL = anonymised
    added_at   TEXT NOT NULL
);
CREATE INDEX ix_record_provenance_record ON record_provenance (record_id);

-- Resolution cache. Keyed by storefront because an Apple catalog ID differs
-- between storefronts and availability differs with it (§3.6). A single-country
-- friend group masks this completely.
CREATE TABLE provider_refs (
    record_id    TEXT NOT NULL REFERENCES records (id) ON DELETE CASCADE,
    service      TEXT NOT NULL CHECK (service IN ('spotify', 'apple')),
    storefront   TEXT NOT NULL,
    external_id  TEXT,
    status       TEXT NOT NULL
                      CHECK (status IN ('ok', 'absent', 'region', 'unknown')),
    checked_at   TEXT NOT NULL,
    PRIMARY KEY (record_id, service, storefront)
);

-- ------------------------------------------------------------- credentials --

-- Secrets are AES-256-GCM ciphertext; the key comes from the environment and
-- never lives in this database (§6). BYO Client ID/Secret per D6.
CREATE TABLE user_provider_credentials (
    user_id            TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    service            TEXT NOT NULL CHECK (service IN ('spotify', 'apple')),
    storefront         TEXT,
    client_id          TEXT,
    client_secret_enc  BLOB,
    access_token_enc   BLOB,
    refresh_token_enc  BLOB,
    expires_at         TEXT,
    scopes             TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    PRIMARY KEY (user_id, service)
);

-- --------------------------------------------------------------- playlists --

CREATE TABLE playlists (
    id           TEXT PRIMARY KEY,
    owner_id     TEXT REFERENCES users (id) ON DELETE SET NULL,
    title        TEXT NOT NULL,
    description  TEXT,
    current_rev  INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX ix_playlists_owner ON playlists (owner_id);

-- Current materialised state of the ordered list.
CREATE TABLE playlist_tracks (
    playlist_id   TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,
    record_id     TEXT NOT NULL REFERENCES records (id),
    added_in_rev  INTEGER NOT NULL,
    PRIMARY KEY (playlist_id, position)
);
CREATE INDEX ix_playlist_tracks_record ON playlist_tracks (record_id);

-- Append-only content history with blame (F17). actor_id is NULLABLE so an
-- erased user can be anonymised while the history other people depend on
-- survives (§6.1). A crate commit writes exactly ONE row here (§3.3).
CREATE TABLE playlist_revisions (
    id           TEXT PRIMARY KEY,
    playlist_id  TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    rev          INTEGER NOT NULL,
    actor_id     TEXT REFERENCES users (id) ON DELETE SET NULL,
    op           TEXT NOT NULL,                 -- create | add | remove | reorder | rename
    detail       TEXT,                          -- JSON payload
    created_at   TEXT NOT NULL,
    UNIQUE (playlist_id, rev)
);

-- ------------------------------------------------------------- annotations --
-- Deliberately NOT versioned. Rating or tagging someone else's playlist is not
-- a content change; if these wrote to playlist_revisions, blame would become
-- meaningless (§3.4).

CREATE TABLE ratings (
    playlist_id  TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    value        INTEGER NOT NULL CHECK (value BETWEEN 1 AND 5),
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (playlist_id, user_id)          -- per-user, aggregate is derived (D8)
);

CREATE TABLE tags (
    id           TEXT PRIMARY KEY,
    playlist_id  TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    -- 'private' is visible only to its author and must be enforced server-side (§6).
    visibility   TEXT NOT NULL CHECK (visibility IN ('private', 'shared')),
    created_at   TEXT NOT NULL,
    UNIQUE (playlist_id, user_id, name, visibility)
);
CREATE INDEX ix_tags_playlist ON tags (playlist_id, visibility);

CREATE TABLE comments (
    id           TEXT PRIMARY KEY,
    playlist_id  TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    user_id      TEXT REFERENCES users (id) ON DELETE SET NULL,   -- NULL = anonymised
    body         TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    deleted_at   TEXT
);
CREATE INDEX ix_comments_playlist ON comments (playlist_id);

-- ------------------------------------------------------------------- crate --

CREATE TABLE crates (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL
);

-- record_id is NULLABLE on purpose: an unresolved candidate stays in the crate
-- as raw text rather than being silently dropped (§3.2, §3.3).
CREATE TABLE crate_items (
    id                 TEXT PRIMARY KEY,
    crate_id           TEXT NOT NULL REFERENCES crates (id) ON DELETE CASCADE,
    position           INTEGER NOT NULL,
    record_id          TEXT REFERENCES records (id),
    raw_candidate      TEXT,                    -- JSON, for unresolved items
    source_ref         TEXT,                    -- which adapter produced it
    resolution_method  TEXT,                    -- isrc | mbid | mapper | fuzzy
    confidence         REAL,
    status             TEXT NOT NULL
                            CHECK (status IN ('resolved', 'ambiguous', 'unresolved')),
    created_at         TEXT NOT NULL
);
CREATE INDEX ix_crate_items_crate ON crate_items (crate_id, position);

-- -------------------------------------------------------------------- sync --

-- Tracked one-way sync (D10). The provider copy is a projection, never a
-- source of truth. Per (playlist, service, storefront) — the same playlist may
-- be synced to two services at different revisions.
CREATE TABLE playlist_syncs (
    playlist_id           TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    service               TEXT NOT NULL CHECK (service IN ('spotify', 'apple')),
    storefront            TEXT NOT NULL,
    provider_playlist_id  TEXT,
    last_synced_rev       INTEGER,
    last_synced_at        TEXT,
    -- Set when the playlist was edited on the provider side. Re-sync must ask
    -- rather than overwrite (D10).
    diverged              INTEGER NOT NULL DEFAULT 0 CHECK (diverged IN (0, 1)),
    PRIMARY KEY (playlist_id, service, storefront)
);

-- -------------------------------------------------------------------- jobs --

-- Long provider operations are resumable background jobs, never a
-- request/response (F22, §3.6).
CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,                 -- import | export | resolve
    state        TEXT NOT NULL
                      CHECK (state IN ('queued', 'running', 'paused',
                                       'done', 'failed', 'cancelled')),
    user_id      TEXT REFERENCES users (id) ON DELETE SET NULL,
    playlist_id  TEXT REFERENCES playlists (id) ON DELETE CASCADE,
    service      TEXT,
    storefront   TEXT,
    done         INTEGER NOT NULL DEFAULT 0,
    total        INTEGER NOT NULL DEFAULT 0,
    error        TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX ix_jobs_state ON jobs (state);

CREATE TABLE job_items (
    id        TEXT PRIMARY KEY,
    job_id    TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL,
    record_id TEXT REFERENCES records (id) ON DELETE SET NULL,
    status    TEXT NOT NULL,
    detail    TEXT
);
CREATE INDEX ix_job_items_job ON job_items (job_id, position);

-- --------------------------------------------------------------------- fts --
-- F4, satisfied without an extra service (§7). MariaDB uses InnoDB FULLTEXT
-- instead; both sit behind the repository interface (§7.3).

CREATE VIRTUAL TABLE records_fts USING fts5 (
    title,
    artist_credit,
    album,
    content = 'records',
    content_rowid = 'rowid'
);

CREATE TRIGGER trg_records_fts_ins AFTER INSERT ON records BEGIN
    INSERT INTO records_fts (rowid, title, artist_credit, album)
    VALUES (new.rowid, new.title, new.artist_credit, new.album);
END;

CREATE TRIGGER trg_records_fts_del AFTER DELETE ON records BEGIN
    INSERT INTO records_fts (records_fts, rowid, title, artist_credit, album)
    VALUES ('delete', old.rowid, old.title, old.artist_credit, old.album);
END;

CREATE TRIGGER trg_records_fts_upd AFTER UPDATE ON records BEGIN
    INSERT INTO records_fts (records_fts, rowid, title, artist_credit, album)
    VALUES ('delete', old.rowid, old.title, old.artist_credit, old.album);
    INSERT INTO records_fts (rowid, title, artist_credit, album)
    VALUES (new.rowid, new.title, new.artist_credit, new.album);
END;
