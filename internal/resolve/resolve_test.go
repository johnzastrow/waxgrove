package resolve

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/musicbrainz"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
)

func newCatalog(t *testing.T) *sqlite.RecordRepo {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.Records()
}

// stubRemote records what was asked so tests can assert the network was NOT
// consulted when the local catalog already had the answer.
type stubRemote struct {
	calls   int
	byISRC  map[string]domain.Candidate
	byMBID  map[string]domain.Candidate
	mapped  map[string]domain.Candidate // key: artist|title
	failAll bool
}

func (s *stubRemote) LookupISRC(_ context.Context, isrc string) (domain.Candidate, error) {
	s.calls++
	if s.failAll {
		return domain.Candidate{}, errors.New("network down")
	}
	c, ok := s.byISRC[isrc]
	if !ok {
		return domain.Candidate{}, musicbrainz.ErrNotFound
	}
	return c, nil
}
func (s *stubRemote) LookupRecording(_ context.Context, mbid string) (domain.Candidate, error) {
	s.calls++
	if s.failAll {
		return domain.Candidate{}, errors.New("network down")
	}
	c, ok := s.byMBID[mbid]
	if !ok {
		return domain.Candidate{}, musicbrainz.ErrNotFound
	}
	return c, nil
}
func (s *stubRemote) MapRecording(_ context.Context, artist, title string) (domain.Candidate, error) {
	s.calls++
	if s.failAll {
		return domain.Candidate{}, errors.New("network down")
	}
	c, ok := s.mapped[artist+"|"+title]
	if !ok {
		return domain.Candidate{}, musicbrainz.ErrNotFound
	}
	return c, nil
}

// A song already in the catalog must resolve without touching the network.
// This is what makes a warm instance fast and conserves scarce quota (§3.0).
func TestKnownISRCCostsNoNetwork(t *testing.T) {
	cat := newCatalog(t)
	ctx := context.Background()
	if _, err := cat.Upsert(ctx, domain.Candidate{
		Title: "Dreams", Artist: "Fleetwood Mac", ISRC: "USWB10101368",
	}, domain.TierCurated); err != nil {
		t.Fatal(err)
	}

	remote := &stubRemote{}
	m, err := New(cat, remote).Resolve(ctx, domain.Candidate{ISRC: "USWB10101368"}, domain.TierCurated)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if m.Method != domain.MatchISRC || !m.Resolved() {
		t.Errorf("method=%s conf=%.2f, want exact ISRC", m.Method, m.Confidence)
	}
	if remote.calls != 0 {
		t.Errorf("network called %d times for a locally-known ISRC", remote.calls)
	}
}

// Free text goes to the MBID Mapper before any local fuzzy guessing (§3.1).
func TestFreeTextUsesTheMapper(t *testing.T) {
	cat := newCatalog(t)
	remote := &stubRemote{
		mapped: map[string]domain.Candidate{
			"Cat Stevens|The Wind": {Title: "The Wind", Artist: "Cat Stevens",
				MBID: "abc-123"},
		},
		byMBID: map[string]domain.Candidate{
			"abc-123": {Title: "The Wind", Artist: "Cat Stevens", MBID: "abc-123",
				ISRC: "GBAAA7100123", DurationMS: 102000},
		},
	}
	m, err := New(cat, remote).Resolve(context.Background(),
		domain.Candidate{Title: "The Wind", Artist: "Cat Stevens"}, domain.TierCurated)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if m.Method != domain.MatchMapper {
		t.Fatalf("method = %s, want mapper", m.Method)
	}
	// Enrichment must have folded in the ISRC the mapper itself did not return.
	if m.Record.ISRCs == nil || m.Record.ISRCs[0] != "GBAAA7100123" {
		t.Errorf("ISRC not enriched from MusicBrainz: %+v", m.Record.ISRCs)
	}
}

// Nothing matched is a RESULT, not an error — the caller keeps the raw text
// so the user can decide (BR-5).
func TestUnmatchedIsNotAnError(t *testing.T) {
	cat := newCatalog(t)
	remote := &stubRemote{}
	m, err := New(cat, remote).Resolve(context.Background(),
		domain.Candidate{Title: "jonny", Artist: "sparklehorse??", Raw: "jonny — sparklehorse??"},
		domain.TierCurated)
	if err != nil {
		t.Fatalf("unmatched returned an error: %v", err)
	}
	if m.Resolved() || m.Record != nil {
		t.Errorf("expected no match, got %+v", m)
	}
	if m.Method != domain.MatchNone {
		t.Errorf("method = %q, want none", m.Method)
	}
}

// N6: with no connector at all, identity-bearing candidates must still resolve.
func TestWorksWithNoRemoteAtAll(t *testing.T) {
	cat := newCatalog(t)
	m, err := New(cat, nil).Resolve(context.Background(),
		domain.Candidate{Title: "Pink Moon", Artist: "Nick Drake", ISRC: "GBAYE0601498"},
		domain.TierCurated)
	if err != nil {
		t.Fatalf("resolve with nil remote: %v", err)
	}
	if !m.Resolved() || m.Record.Title != "Pink Moon" {
		t.Fatalf("did not resolve locally: %+v", m)
	}
}

// A network failure must not prevent storing a candidate that already carries
// identity — connectors are never load-bearing (N6).
func TestRemoteFailureDegradesQuietly(t *testing.T) {
	cat := newCatalog(t)
	remote := &stubRemote{failAll: true}
	m, err := New(cat, remote).Resolve(context.Background(),
		domain.Candidate{Title: "Pink Moon", Artist: "Nick Drake", ISRC: "GBAYE0601498"},
		domain.TierCurated)
	if err != nil {
		t.Fatalf("network failure became a hard error: %v", err)
	}
	if !m.Resolved() {
		t.Fatalf("candidate with an ISRC failed to resolve when the network was down")
	}
}

// Ambiguity must surface alternatives rather than silently picking (F12, §3.2).
func TestLowConfidenceOffersAlternatives(t *testing.T) {
	cat := newCatalog(t)
	ctx := context.Background()
	// Two records that normalise identically but differ in duration.
	for _, d := range []struct {
		isrc string
		dur  int
	}{{"AA00000000001", 102000}, {"AA00000000002", 178000}} {
		if _, err := cat.Upsert(ctx, domain.Candidate{
			Title: "The Wind", Artist: "Cat Stevens", ISRC: d.isrc, DurationMS: d.dur,
		}, domain.TierCurated); err != nil {
			t.Fatal(err)
		}
	}
	remote := &stubRemote{} // mapper finds nothing, so we fall to local fuzzy
	m, err := New(cat, remote).Resolve(ctx,
		domain.Candidate{Title: "The Wind", Artist: "Cat Stevens"}, domain.TierCurated)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if m.Method != domain.MatchFuzzy {
		t.Fatalf("method = %s, want fuzzy", m.Method)
	}
	if len(m.Alternatives) < 2 {
		t.Errorf("ambiguous match offered %d alternatives, want >= 2", len(m.Alternatives))
	}
	if m.Resolved() {
		t.Error("an ambiguous match reported itself as resolved")
	}
}

// Duration agreement is what lifts a fuzzy match over the threshold (§3.2).
func TestDurationLiftsFuzzyConfidence(t *testing.T) {
	cat := newCatalog(t)
	ctx := context.Background()
	if _, err := cat.Upsert(ctx, domain.Candidate{
		Title: "Harvest Moon", Artist: "Neil Young", ISRC: "AA00000000009", DurationMS: 303000,
	}, domain.TierCurated); err != nil {
		t.Fatal(err)
	}
	m, err := New(cat, &stubRemote{}).Resolve(ctx,
		domain.Candidate{Title: "Harvest Moon", Artist: "Neil Young", DurationMS: 304500},
		domain.TierCurated)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !m.Resolved() {
		t.Errorf("duration-corroborated fuzzy match scored %.2f, below threshold", m.Confidence)
	}
}
