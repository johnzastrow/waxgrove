// Package resolve implements the matching ladder from docs/requirements.md §3.2.
//
// The governing rule is "never silently mismatch". Every step records how it
// matched and how confident it is; below the threshold the caller is expected
// to ask a human rather than pick. An unresolved candidate is returned as
// unresolved — never quietly dropped, never quietly guessed.
//
// Steps run cheapest first, and the local catalog is always consulted before
// the network. That is what makes a warm instance fast: a song resolved once
// never costs a request again (§3.0).
package resolve

import (
	"context"
	"errors"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/normalize"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
)

// Catalog is the local half of the ladder.
type Catalog interface {
	FindByISRC(ctx context.Context, isrc string) (*domain.Record, error)
	FindByMBID(ctx context.Context, mbid string) (*domain.Record, error)
	FuzzyMatch(ctx context.Context, c domain.Candidate) ([]domain.Record, error)
	Upsert(ctx context.Context, c domain.Candidate, tier domain.Tier) (*domain.Record, error)
}

// Remote is the network half. It is an interface so the ladder can run with
// connectors absent — N6 requires Waxgrove to work with nothing attached.
type Remote interface {
	LookupISRC(ctx context.Context, isrc string) (domain.Candidate, error)
	LookupRecording(ctx context.Context, mbid string) (domain.Candidate, error)
	MapRecording(ctx context.Context, artist, title string) (domain.Candidate, error)
}

// Resolver runs the ladder.
type Resolver struct {
	cat    Catalog
	remote Remote // may be nil: local-only resolution is valid
}

func New(cat Catalog, remote Remote) *Resolver {
	return &Resolver{cat: cat, remote: remote}
}

// Confidence scores per method. Steps 1 and 2 are identity matches and are
// therefore certain; everything below is a judgement and scored as such.
const (
	confISRC   = 1.00
	confMBID   = 1.00
	confMapper = 0.90 // the mapper has the full corpus; still not certainty
	confFuzzy  = 0.70 // normalised text agreement, no duration corroboration
	confFuzzyD = 0.88 // ...with the duration also inside the ±3s window
)

// Resolve runs a candidate down the ladder and returns what it found.
//
// tier controls what a newly created record is born as: a deliberate add is
// curated, an album side-effect is ambient (D11).
func (r *Resolver) Resolve(ctx context.Context, c domain.Candidate, tier domain.Tier) (domain.Match, error) {
	// --- 1. ISRC set membership, locally -------------------------------------
	if c.ISRC != "" {
		rec, err := r.cat.FindByISRC(ctx, c.ISRC)
		if err == nil {
			return domain.Match{Record: rec, Method: domain.MatchISRC, Confidence: confISRC}, nil
		}
		if !errors.Is(err, sqlite.ErrNotFound) {
			return domain.Match{}, err
		}
	}

	// --- 2. MBID, locally ----------------------------------------------------
	if c.MBID != "" {
		rec, err := r.cat.FindByMBID(ctx, c.MBID)
		if err == nil {
			return domain.Match{Record: rec, Method: domain.MatchMBID, Confidence: confMBID}, nil
		}
		if !errors.Is(err, sqlite.ErrNotFound) {
			return domain.Match{}, err
		}
	}

	// The candidate already carries identity but is not in the catalog yet, so
	// it can be stored directly — no guessing involved.
	if c.ISRC != "" || c.MBID != "" {
		enriched := r.enrichFromRemote(ctx, c)
		rec, err := r.cat.Upsert(ctx, enriched, tier)
		if err != nil {
			return domain.Match{}, err
		}
		method := domain.MatchISRC
		if c.ISRC == "" {
			method = domain.MatchMBID
		}
		return domain.Match{Record: rec, Method: method, Confidence: confISRC}, nil
	}

	// --- 3. The MBID Mapper on free text -------------------------------------
	// §3.1: this is the fuzzy matcher, already built by people with the whole
	// MusicBrainz corpus. Ask it before attempting anything locally.
	if r.remote != nil && c.Artist != "" && c.Title != "" {
		mapped, err := r.remote.MapRecording(ctx, c.Artist, c.Title)
		if err == nil && mapped.MBID != "" {
			full := r.enrichFromRemote(ctx, mapped)
			// Preserve what the caller told us where the mapper is silent.
			if full.DurationMS == 0 {
				full.DurationMS = c.DurationMS
			}
			rec, err := r.cat.Upsert(ctx, full, tier)
			if err != nil {
				return domain.Match{}, err
			}
			return domain.Match{Record: rec, Method: domain.MatchMapper, Confidence: confMapper}, nil
		}
	}

	// --- 4. Local fuzzy ------------------------------------------------------
	hits, err := r.cat.FuzzyMatch(ctx, c)
	if err != nil {
		return domain.Match{}, err
	}
	if len(hits) > 0 {
		best, conf := pickBest(hits, c)
		m := domain.Match{Record: best, Method: domain.MatchFuzzy, Confidence: conf}
		// More than one plausible record means the user must choose (F12).
		if len(hits) > 1 || conf < domain.ConfidenceThreshold {
			m.Alternatives = toCandidates(hits)
		}
		return m, nil
	}

	// --- 5. Nothing matched --------------------------------------------------
	// Deliberately not an error: the caller keeps the raw text in the crate so
	// the user can decide later (BR-5).
	return domain.Match{Method: domain.MatchNone, Confidence: 0}, nil
}

// pickBest chooses among equally-normalised hits, rewarding duration agreement.
func pickBest(hits []domain.Record, c domain.Candidate) (*domain.Record, float64) {
	best := &hits[0]
	conf := confFuzzy
	for i := range hits {
		if normalize.DurationClose(hits[i].DurationMS, c.DurationMS) {
			return &hits[i], confFuzzyD
		}
	}
	return best, conf
}

func toCandidates(recs []domain.Record) []domain.Candidate {
	out := make([]domain.Candidate, 0, len(recs))
	for _, r := range recs {
		isrc := ""
		if len(r.ISRCs) > 0 {
			isrc = r.ISRCs[0]
		}
		out = append(out, domain.Candidate{
			Title: r.Title, Artist: r.ArtistCredit, Album: r.Album,
			DurationMS: r.DurationMS, ISRC: isrc, MBID: r.MBID, Year: r.Year,
		})
	}
	return out
}

// enrichFromRemote fills in metadata MusicBrainz has and the source did not —
// notably the full ISRC set, which is what lets a later import from a different
// service converge on this record (BR-1).
//
// Enrichment is best-effort: a network failure must never prevent a candidate
// that already carries identity from being stored (N6).
func (r *Resolver) enrichFromRemote(ctx context.Context, c domain.Candidate) domain.Candidate {
	if r.remote == nil {
		return c
	}
	var found domain.Candidate
	var err error
	switch {
	case c.MBID != "":
		found, err = r.remote.LookupRecording(ctx, c.MBID)
	case c.ISRC != "":
		found, err = r.remote.LookupISRC(ctx, c.ISRC)
	default:
		return c
	}
	if err != nil {
		return c // degrade quietly; the candidate is still usable
	}

	out := c
	if out.MBID == "" {
		out.MBID = found.MBID
	}
	if out.ISRC == "" {
		out.ISRC = found.ISRC
	}
	if out.Title == "" {
		out.Title = found.Title
	}
	if out.Artist == "" {
		out.Artist = found.Artist
	}
	if out.Album == "" {
		out.Album = found.Album
	}
	if out.DurationMS == 0 {
		out.DurationMS = found.DurationMS
	}
	if out.Year == 0 {
		out.Year = found.Year
	}
	return out
}
