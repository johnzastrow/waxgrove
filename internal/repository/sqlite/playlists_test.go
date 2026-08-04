package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

func seedUser(t *testing.T, s *Store, id string) {
	t.Helper()
	mustExec(t, s, `INSERT INTO users (id, email, display_name, role, created_at)
	                VALUES (?, ?, 'Ana', 'member', '2026-08-04T00:00:00Z')`, id, id+"@example.test")
}

func seedRecord(t *testing.T, s *Store, title, artist, isrc string) *domain.Record {
	t.Helper()
	rec, err := s.Records().Upsert(context.Background(), domain.Candidate{
		Title: title, Artist: artist, ISRC: isrc,
	}, domain.TierCurated)
	if err != nil {
		t.Fatalf("upsert %s: %v", title, err)
	}
	return rec
}

// The rule from §3.3: a crate commit of many tracks is ONE authored event.
// If this ever writes one revision per track, blame becomes unreadable.
func TestAddRecordsWritesExactlyOneRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")

	ids := []string{}
	for _, tr := range [][3]string{
		{"Pink Moon", "Nick Drake", "GBAYE0601498"},
		{"Northern Sky", "Nick Drake", "GBAYE0601499"},
		{"Place to Be", "Nick Drake", "GBAYE0601500"},
	} {
		ids = append(ids, seedRecord(t, s, tr[0], tr[1], tr[2]).ID)
	}

	p, err := s.Playlists().Create(ctx, "u1", "Porch, July", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Playlists().AddRecords(ctx, p.ID, "u1", ids); err != nil {
		t.Fatalf("add: %v", err)
	}

	hist, err := s.Playlists().History(ctx, p.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	// create + one add = 2, regardless of the three tracks.
	if len(hist) != 2 {
		t.Fatalf("history has %d revisions, want 2 (create + one add)", len(hist))
	}
	if hist[0].Op != domain.OpAdd {
		t.Errorf("newest op = %q, want add", hist[0].Op)
	}
}

// Two services' different ISRCs for one recording must converge on one record
// even when they arrive as separate imports (BR-1, end to end through Upsert).
func TestUpsertConvergesOnOneRecordAcrossServices(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Spotify's import: ISRC only, no MBID yet.
	fromSpotify, err := s.Records().Upsert(ctx, domain.Candidate{
		Title: "Dreams", Artist: "Fleetwood Mac", ISRC: "USWB10101368",
	}, domain.TierCurated)
	if err != nil {
		t.Fatalf("spotify upsert: %v", err)
	}
	// MusicBrainz later supplies the MBID for that same ISRC.
	withMBID, err := s.Records().Upsert(ctx, domain.Candidate{
		Title: "Dreams", Artist: "Fleetwood Mac",
		ISRC: "USWB10101368", MBID: "248cc9d1-97ea-493e-84d4-4c5ec718683b",
	}, domain.TierCurated)
	if err != nil {
		t.Fatalf("mbid upsert: %v", err)
	}
	if withMBID.ID != fromSpotify.ID {
		t.Fatalf("learning the MBID forked the record: %s vs %s", fromSpotify.ID, withMBID.ID)
	}
	if withMBID.MBID == "" {
		t.Error("MBID was not folded into the existing record")
	}

	// Apple's import: a DIFFERENT ISRC for the same recording, plus the MBID.
	fromApple, err := s.Records().Upsert(ctx, domain.Candidate{
		Title: "Dreams", Artist: "Fleetwood Mac",
		ISRC: "USWB19900178", MBID: "248cc9d1-97ea-493e-84d4-4c5ec718683b",
	}, domain.TierCurated)
	if err != nil {
		t.Fatalf("apple upsert: %v", err)
	}
	if fromApple.ID != fromSpotify.ID {
		t.Fatalf("Spotify and Apple produced different records (%s vs %s) — dedup is broken",
			fromSpotify.ID, fromApple.ID)
	}
	if len(fromApple.ISRCs) != 2 {
		t.Errorf("record holds %d ISRCs, want 2 (both services')", len(fromApple.ISRCs))
	}
}

// Ambient records stay out of search until deliberately used (BR-6, F24).
func TestAmbientRecordsAreHiddenFromSearchUntilPromoted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Records().Upsert(ctx, domain.Candidate{
		Title: "Road", Artist: "Nick Drake", ISRC: "GBAYE0601501",
	}, domain.TierAmbient); err != nil {
		t.Fatalf("ambient upsert: %v", err)
	}
	hits, err := s.Records().Search(ctx, "Road", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("ambient record appeared in search (%d hits)", len(hits))
	}

	// Deliberate use promotes it.
	if _, err := s.Records().Upsert(ctx, domain.Candidate{
		Title: "Road", Artist: "Nick Drake", ISRC: "GBAYE0601501",
	}, domain.TierCurated); err != nil {
		t.Fatalf("promote: %v", err)
	}
	hits, err = s.Records().Search(ctx, "Road", 10)
	if err != nil {
		t.Fatalf("search after promote: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("promoted record not searchable (%d hits)", len(hits))
	}
}

// FTS input is user-supplied; a stray operator must not error or change meaning.
func TestSearchHandlesHostileInput(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedRecord(t, s, "Pink Moon", "Nick Drake", "GBAYE0601498")

	for _, q := range []string{`pink"`, `NEAR(`, `moon OR *`, `"`, `  `, `pink AND`} {
		if _, err := s.Records().Search(ctx, q, 10); err != nil {
			t.Errorf("Search(%q) errored: %v", q, err)
		}
	}
}

func TestRemoveAtClosesTheGap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")

	var ids []string
	for _, tr := range [][3]string{
		{"A", "X", "AA00000000001"}, {"B", "X", "AA00000000002"}, {"C", "X", "AA00000000003"},
	} {
		ids = append(ids, seedRecord(t, s, tr[0], tr[1], tr[2]).ID)
	}
	p, _ := s.Playlists().Create(ctx, "u1", "L", "")
	if _, err := s.Playlists().AddRecords(ctx, p.ID, "u1", ids); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := s.Playlists().RemoveAt(ctx, p.ID, "u1", 1) // remove "B"
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(got.Tracks) != 2 {
		t.Fatalf("have %d tracks, want 2", len(got.Tracks))
	}
	for i, tr := range got.Tracks {
		if tr.Position != i {
			t.Errorf("track %d has position %d — gap not closed", i, tr.Position)
		}
	}
	if got.Tracks[1].Record.Title != "C" {
		t.Errorf("position 1 is %q, want C", got.Tracks[1].Record.Title)
	}
}

func TestReorderRewritesPositionsAndBumpsRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")

	var ids []string
	for _, tr := range [][3]string{
		{"A", "X", "AA10000000001"}, {"B", "X", "AA10000000002"}, {"C", "X", "AA10000000003"},
	} {
		ids = append(ids, seedRecord(t, s, tr[0], tr[1], tr[2]).ID)
	}
	p, _ := s.Playlists().Create(ctx, "u1", "L", "")
	if _, err := s.Playlists().AddRecords(ctx, p.ID, "u1", ids); err != nil {
		t.Fatal(err)
	}

	reversed := []string{ids[2], ids[1], ids[0]}
	got, err := s.Playlists().Reorder(ctx, p.ID, "u1", reversed)
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if got.Tracks[0].Record.Title != "C" || got.Tracks[2].Record.Title != "A" {
		t.Errorf("order not applied: %s,%s,%s",
			got.Tracks[0].Record.Title, got.Tracks[1].Record.Title, got.Tracks[2].Record.Title)
	}
	if got.CurrentRev != 3 { // create, add, reorder
		t.Errorf("revision = %d, want 3", got.CurrentRev)
	}
}

func TestRenameIsAContentRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")
	p, _ := s.Playlists().Create(ctx, "u1", "Old", "")

	got, err := s.Playlists().Rename(ctx, p.ID, "u1", "New")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Title != "New" {
		t.Errorf("title = %q", got.Title)
	}
	hist, _ := s.Playlists().History(ctx, p.ID)
	if hist[0].Op != domain.OpRename {
		t.Errorf("newest op = %q, want rename", hist[0].Op)
	}
}

func TestHistoryIsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")
	p, _ := s.Playlists().Create(ctx, "u1", "L", "")
	if _, err := s.Playlists().Rename(ctx, p.ID, "u1", "L2"); err != nil {
		t.Fatal(err)
	}
	hist, _ := s.Playlists().History(ctx, p.ID)
	if len(hist) != 2 || hist[0].Rev <= hist[1].Rev {
		t.Errorf("history not newest-first: %+v", hist)
	}
}

func TestDeleteCascadesRevisionsAndTracks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")
	rec := seedRecord(t, s, "A", "X", "AA20000000001")
	p, _ := s.Playlists().Create(ctx, "u1", "L", "")
	if _, err := s.Playlists().AddRecords(ctx, p.ID, "u1", []string{rec.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.Playlists().Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM playlist_tracks WHERE playlist_id = ?`,
		`SELECT COUNT(*) FROM playlist_revisions WHERE playlist_id = ?`,
	} {
		var n int
		if err := s.Reader().QueryRowContext(ctx, q, p.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("rows survived playlist delete: %s -> %d", q, n)
		}
	}
	// The record itself is shared and must NOT be removed (§3.0).
	if _, err := s.Records().Get(ctx, rec.ID); err != nil {
		t.Errorf("deleting a playlist removed a shared catalogue record: %v", err)
	}
}

func TestMutatingAMissingPlaylistIsNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")
	for name, err := range map[string]error{
		"add":    firstErr(s.Playlists().AddRecords(ctx, "nope", "u1", []string{"x"})),
		"remove": firstErr(s.Playlists().RemoveAt(ctx, "nope", "u1", 0)),
		"rename": firstErr(s.Playlists().Rename(ctx, "nope", "u1", "t")),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on missing playlist = %v, want ErrNotFound", name, err)
		}
	}
	if err := s.Playlists().Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing = %v", err)
	}
}

func firstErr(_ *domain.Playlist, err error) error { return err }

func TestGetMissingRecordIsNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Records().Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListOwnedOnlyReturnsOwnPlaylists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedUser(t, s, "u1")
	seedUser(t, s, "u2")
	if _, err := s.Playlists().Create(ctx, "u1", "mine", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Playlists().Create(ctx, "u2", "theirs", ""); err != nil {
		t.Fatal(err)
	}
	mine, err := s.Playlists().ListOwned(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].Title != "mine" {
		t.Errorf("ListOwned returned %d playlists: %+v", len(mine), mine)
	}
}
