package sqlite

import (
	"context"
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
