package jspf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

func TestRoundTripPreservesIdentity(t *testing.T) {
	p := &domain.Playlist{
		Title:       "Porch, July",
		Description: "summer",
		Tracks: []domain.Track{{
			Position: 0,
			Record: domain.Record{
				Title: "Dreams", ArtistCredit: "Fleetwood Mac", Album: "Rumours",
				DurationMS: 254000,
				MBID:       "248cc9d1-97ea-493e-84d4-4c5ec718683b",
				// A recording carries many ISRCs; all must survive the trip.
				ISRCs: []string{"USWB10101368", "USWB19900178"},
			},
		}},
	}

	var buf bytes.Buffer
	if err := Write(&buf, Export(p, "ana")); err != nil {
		t.Fatalf("write: %v", err)
	}

	title, cands, err := Parse(&buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if title != "Porch, July" {
		t.Errorf("title = %q", title)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1", len(cands))
	}
	c := cands[0]
	if c.MBID != "248cc9d1-97ea-493e-84d4-4c5ec718683b" {
		t.Errorf("MBID lost: %q", c.MBID)
	}
	if c.ISRC != "USWB10101368" {
		t.Errorf("ISRC lost: %q", c.ISRC)
	}
	if c.DurationMS != 254000 {
		t.Errorf("duration lost: %d", c.DurationMS)
	}
}

// All ISRCs are exported, not just the first — dropping them would lose
// identity that lets a different service's import converge (BR-1).
func TestExportKeepsEveryISRC(t *testing.T) {
	p := &domain.Playlist{Tracks: []domain.Track{{
		Record: domain.Record{Title: "Dreams", ArtistCredit: "Fleetwood Mac",
			ISRCs: []string{"USWB10101368", "USWB19900178", "USWB22600016"}},
	}}}
	var buf bytes.Buffer
	if err := Write(&buf, Export(p, "ana")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"USWB10101368", "USWB19900178", "USWB22600016"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("export dropped ISRC %s", want)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, in := range []string{``, `{`, `[]`, `{"nope":1}`, `null`} {
		if _, _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("Parse(%q) accepted invalid input", in)
		}
	}
}

// A hostile or accidental huge upload must not be read into memory unbounded.
func TestParseIsSizeLimited(t *testing.T) {
	huge := strings.NewReader(`{"playlist":{"title":"x","track":[` +
		strings.Repeat(`{"title":"`+strings.Repeat("a", 1000)+`"},`, 20000) + `]}}`)
	if _, _, err := Parse(huge); err == nil {
		t.Fatal("oversized document was accepted")
	}
}

// An entry with nothing identifying it is kept, not dropped (§3.2).
func TestParseKeepsEmptyTrackAsRaw(t *testing.T) {
	_, cands, err := Parse(strings.NewReader(`{"playlist":{"title":"t","track":[{}]}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 1 || cands[0].Raw == "" {
		t.Fatalf("empty track was dropped instead of kept as raw: %+v", cands)
	}
}
