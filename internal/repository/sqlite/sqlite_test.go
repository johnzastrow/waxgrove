package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// The §7.2 pragmas are not cosmetic: without WAL the concurrency story
// collapses, and without foreign_keys the GDPR anonymisation path silently
// stops working because ON DELETE SET NULL is never applied.
func TestOpenAppliesRequiredPragmas(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for name, pool := range map[string]*sql.DB{"read": s.Reader(), "write": s.Writer()} {
		var journal string
		if err := pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatalf("%s pool journal_mode: %v", name, err)
		}
		if journal != "wal" {
			t.Errorf("%s pool journal_mode = %q, want wal", name, journal)
		}

		var fk int
		if err := pool.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("%s pool foreign_keys: %v", name, err)
		}
		if fk != 1 {
			t.Errorf("%s pool foreign_keys = %d, want 1", name, fk)
		}
	}
}

// The write pool must be a single connection, so concurrent writers serialise
// in Go rather than manufacturing SQLITE_BUSY against each other (§7.2).
func TestWritePoolIsSingleConnection(t *testing.T) {
	s := newTestStore(t)
	if got := s.Writer().Stats().MaxOpenConnections; got != 1 {
		t.Errorf("write pool MaxOpenConnections = %d, want 1", got)
	}
	if got := s.Reader().Stats().MaxOpenConnections; got <= 1 {
		t.Errorf("read pool MaxOpenConnections = %d, want > 1", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var before int
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if before == 0 {
		t.Fatal("no migrations recorded after Open")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var after int
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if before != after {
		t.Errorf("migrations re-applied: %d -> %d", before, after)
	}
}

// The correction recorded in §3: one recording carries MANY ISRCs. Spotify may
// return one of them and Apple another for the SAME recording. Both must
// resolve to a single record, or global dedup silently fails.
func TestOneRecordHoldsManyISRCs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := s.Writer().ExecContext(ctx,
		`INSERT INTO records (id, mbid, title, artist_credit, norm_title, norm_artist,
		                      tier, created_at, updated_at)
		 VALUES ('rec1', '248cc9d1-97ea-493e-84d4-4c5ec718683b', 'Dreams', 'Fleetwood Mac',
		         'dreams', 'fleetwood mac', 'curated', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert record: %v", err)
	}

	// The seven real ISRCs on this recording.
	isrcs := []string{
		"USRH11802580", "USWB10101368", "USWB10202603", "USWB10400046",
		"USWB11301111", "USWB19900178", "USWB22600016",
	}
	for _, isrc := range isrcs {
		if _, err := s.Writer().ExecContext(ctx,
			`INSERT INTO record_isrcs (record_id, isrc) VALUES (?, ?)`, "rec1", isrc); err != nil {
			t.Fatalf("insert isrc %s: %v", isrc, err)
		}
	}

	// Spotify's ISRC and Apple's ISRC must land on the same record.
	spotifyISRC, appleISRC := "USWB10101368", "USWB19900178"
	var a, b string
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT record_id FROM record_isrcs WHERE isrc = ?`, spotifyISRC).Scan(&a); err != nil {
		t.Fatalf("lookup spotify isrc: %v", err)
	}
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT record_id FROM record_isrcs WHERE isrc = ?`, appleISRC).Scan(&b); err != nil {
		t.Fatalf("lookup apple isrc: %v", err)
	}
	if a != b {
		t.Fatalf("two services resolved to different records (%s vs %s) — dedup is broken", a, b)
	}
}

// An ISRC identifies one recording, so the same ISRC may not be claimed by two
// records. A collision means a merge is needed, not a second row.
func TestISRCCannotBeClaimedByTwoRecords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	for _, id := range []string{"recA", "recB"} {
		if _, err := s.Writer().ExecContext(ctx,
			`INSERT INTO records (id, title, artist_credit, norm_title, norm_artist,
			                      tier, created_at, updated_at)
			 VALUES (?, 't', 'a', 't', 'a', 'curated', ?, ?)`, id, now, now); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	if _, err := s.Writer().ExecContext(ctx,
		`INSERT INTO record_isrcs (record_id, isrc) VALUES ('recA', 'USWB10101368')`); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := s.Writer().ExecContext(ctx,
		`INSERT INTO record_isrcs (record_id, isrc) VALUES ('recB', 'USWB10101368')`); err == nil {
		t.Fatal("second record claimed the same ISRC; unique index is missing")
	}
}

// §6.1 / GDPR Art. 17: erasing a user must anonymise attribution without
// destroying playlist history other people depend on.
func TestErasureAnonymisesButPreservesHistory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	mustExec(t, s, `INSERT INTO users (id, email, display_name, role, created_at)
	                VALUES ('u1', 'ana@example.test', 'Ana', 'member', ?)`, now)
	mustExec(t, s, `INSERT INTO playlists (id, owner_id, title, current_rev, created_at, updated_at)
	                VALUES ('p1', 'u1', 'Porch, July', 1, ?, ?)`, now, now)
	mustExec(t, s, `INSERT INTO playlist_revisions (id, playlist_id, rev, actor_id, op, created_at)
	                VALUES ('r1', 'p1', 1, 'u1', 'create', ?)`, now)
	mustExec(t, s, `INSERT INTO comments (id, playlist_id, user_id, body, created_at)
	                VALUES ('c1', 'p1', 'u1', 'track 9 is the one', ?)`, now)

	// Erasure: delete the user. ON DELETE SET NULL must anonymise, not cascade.
	mustExec(t, s, `DELETE FROM users WHERE id = 'u1'`)

	var revCount int
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM playlist_revisions WHERE playlist_id = 'p1'`).Scan(&revCount); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if revCount != 1 {
		t.Fatalf("revision history destroyed by erasure: %d rows, want 1", revCount)
	}

	var actor sql.NullString
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT actor_id FROM playlist_revisions WHERE id = 'r1'`).Scan(&actor); err != nil {
		t.Fatalf("read actor: %v", err)
	}
	if actor.Valid {
		t.Errorf("actor_id = %q after erasure, want NULL", actor.String)
	}

	var commentAuthor sql.NullString
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT user_id FROM comments WHERE id = 'c1'`).Scan(&commentAuthor); err != nil {
		t.Fatalf("read comment author: %v", err)
	}
	if commentAuthor.Valid {
		t.Errorf("comment user_id = %q after erasure, want NULL", commentAuthor.String)
	}
}

// Provider IDs are per-storefront (§3.6). The same record must be able to hold
// a different external id and availability per storefront.
func TestProviderRefsAreStorefrontScoped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	mustExec(t, s, `INSERT INTO records (id, title, artist_credit, norm_title, norm_artist,
	                                     tier, created_at, updated_at)
	                VALUES ('rec1', 'Everywhere', 'Fleetwood Mac', 'everywhere', 'fleetwood mac',
	                        'curated', ?, ?)`, now, now)

	mustExec(t, s, `INSERT INTO provider_refs (record_id, service, storefront, external_id, status, checked_at)
	                VALUES ('rec1', 'apple', 'us', '1440857781', 'ok', ?)`, now)
	mustExec(t, s, `INSERT INTO provider_refs (record_id, service, storefront, external_id, status, checked_at)
	                VALUES ('rec1', 'apple', 'gb', NULL, 'absent', ?)`, now)

	var status string
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT status FROM provider_refs WHERE record_id='rec1' AND service='apple' AND storefront='gb'`).
		Scan(&status); err != nil {
		t.Fatalf("read gb ref: %v", err)
	}
	if status != "absent" {
		t.Errorf("gb status = %q, want absent", status)
	}
}

// A crate item with no match keeps its raw text rather than being dropped
// (§3.2 "never silently mismatch", §3.3).
func TestCrateHoldsUnresolvedItems(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	mustExec(t, s, `INSERT INTO users (id, email, display_name, role, created_at)
	                VALUES ('u1', 'b@example.test', 'Ben', 'member', ?)`, now)
	mustExec(t, s, `INSERT INTO crates (id, user_id, created_at) VALUES ('cr1', 'u1', ?)`, now)
	mustExec(t, s, `INSERT INTO crate_items (id, crate_id, position, record_id, raw_candidate,
	                                         status, created_at)
	                VALUES ('ci1', 'cr1', 0, NULL, '{"text":"the wind — cat stevens"}',
	                        'unresolved', ?)`, now)

	var recordID sql.NullString
	var raw string
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT record_id, raw_candidate FROM crate_items WHERE id='ci1'`).Scan(&recordID, &raw); err != nil {
		t.Fatalf("read crate item: %v", err)
	}
	if recordID.Valid {
		t.Error("unresolved item has a record_id; it should be NULL")
	}
	if raw == "" {
		t.Error("raw candidate text was dropped")
	}
}

// FTS5 backs F4 without an extra service (§7).
func TestFullTextSearchFindsRecords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	mustExec(t, s, `INSERT INTO records (id, title, artist_credit, album, norm_title, norm_artist,
	                                     tier, created_at, updated_at)
	                VALUES ('rec1', 'Pink Moon', 'Nick Drake', 'Pink Moon', 'pink moon',
	                        'nick drake', 'curated', ?, ?)`, now, now)

	var title string
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT title FROM records WHERE rowid IN (
		    SELECT rowid FROM records_fts WHERE records_fts MATCH 'drake')`).Scan(&title); err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if title != "Pink Moon" {
		t.Errorf("fts returned %q", title)
	}
}

// Concurrent writers must queue rather than fail. §7.2 predicts sub-millisecond
// waits at this scale; this asserts only that none of them error.
func TestConcurrentWritersDoNotCollide(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "rec" + string(rune('A'+n))
			_, err := s.Writer().ExecContext(ctx,
				`INSERT INTO records (id, title, artist_credit, norm_title, norm_artist,
				                      tier, created_at, updated_at)
				 VALUES (?, 't', 'a', 't', 'a', 'ambient', ?, ?)`, id, now, now)
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	var count int
	if err := s.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != writers {
		t.Errorf("wrote %d records, want %d", count, writers)
	}
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %.60s: %v", query, err)
	}
}
