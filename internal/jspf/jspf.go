// Package jspf reads and writes JSPF — MusicBrainz's JSON flavour of XSPF.
//
// This is the universal fallback (D1, F8): any service without a usable API is
// reached by exchanging a file, and a Waxgrove playlist stays portable even if
// Waxgrove itself disappears. It is built first and treated as the reference
// implementation of sharing.
//
// Spec: https://musicbrainz.org/doc/jspf
package jspf

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// Document is the top-level JSPF envelope.
type Document struct {
	Playlist Playlist `json:"playlist"`
}

type Playlist struct {
	Title      string  `json:"title,omitempty"`
	Creator    string  `json:"creator,omitempty"`
	Annotation string  `json:"annotation,omitempty"`
	Date       string  `json:"date,omitempty"`
	Track      []Track `json:"track"`
}

type Track struct {
	Title      string   `json:"title,omitempty"`
	Creator    string   `json:"creator,omitempty"`
	Album      string   `json:"album,omitempty"`
	Duration   int      `json:"duration,omitempty"` // milliseconds
	Identifier []string `json:"identifier,omitempty"`
	Meta       []Meta   `json:"meta,omitempty"`
}

// Meta carries key/value pairs the core spec has no field for — ISRC in
// particular, which JSPF does not model directly.
type Meta map[string]string

const (
	mbRecordingPrefix = "https://musicbrainz.org/recording/"
	isrcMetaKey       = "https://musicbrainz.org/doc/ISRC"
)

// Export serialises a playlist. MBIDs go in `identifier` as MusicBrainz URLs,
// which is the convention ListenBrainz uses; ISRCs go in `meta` because JSPF
// has no field for them.
func Export(p *domain.Playlist, creator string) *Document {
	doc := &Document{Playlist: Playlist{
		Title:      p.Title,
		Creator:    creator,
		Annotation: p.Description,
		Track:      make([]Track, 0, len(p.Tracks)),
	}}
	for _, t := range p.Tracks {
		tr := Track{
			Title:    t.Record.Title,
			Creator:  t.Record.ArtistCredit,
			Album:    t.Record.Album,
			Duration: t.Record.DurationMS,
		}
		if t.Record.MBID != "" {
			tr.Identifier = append(tr.Identifier, mbRecordingPrefix+t.Record.MBID)
		}
		if len(t.Record.ISRCs) > 0 {
			// Every ISRC is exported, not just one — a recording legitimately
			// has several, and dropping them would lose identity on re-import.
			tr.Meta = append(tr.Meta, Meta{isrcMetaKey: strings.Join(t.Record.ISRCs, ",")})
		}
		doc.Playlist.Track = append(doc.Playlist.Track, tr)
	}
	return doc
}

// Write emits the document as indented JSON.
func Write(w io.Writer, doc *Document) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// MaxSize caps how large an uploaded playlist file may be. JSPF is small text;
// anything larger is either a mistake or an attempt to exhaust memory.
const MaxSize = 8 << 20 // 8 MiB

// Parse reads a JSPF document, returning the playlist title and one candidate
// per track ready for the resolution ladder.
func Parse(r io.Reader) (title string, candidates []domain.Candidate, err error) {
	var doc Document
	dec := json.NewDecoder(io.LimitReader(r, MaxSize))
	if err := dec.Decode(&doc); err != nil {
		return "", nil, fmt.Errorf("jspf: malformed document: %w", err)
	}
	if doc.Playlist.Track == nil && doc.Playlist.Title == "" {
		return "", nil, fmt.Errorf("jspf: no playlist found")
	}

	for _, t := range doc.Playlist.Track {
		c := domain.Candidate{
			Title:      strings.TrimSpace(t.Title),
			Artist:     strings.TrimSpace(t.Creator),
			Album:      strings.TrimSpace(t.Album),
			DurationMS: t.Duration,
			SourceRef:  "jspf",
		}
		for _, id := range t.Identifier {
			if strings.HasPrefix(id, mbRecordingPrefix) {
				c.MBID = strings.TrimPrefix(id, mbRecordingPrefix)
				break
			}
		}
		for _, m := range t.Meta {
			if v, ok := m[isrcMetaKey]; ok && v != "" {
				// A track may carry several; the first is enough to resolve,
				// and the rest are learned from MusicBrainz.
				c.ISRC = strings.TrimSpace(strings.Split(v, ",")[0])
				break
			}
		}
		// A track with nothing identifying it is not importable, but it is also
		// not an error — keep the raw text so nothing is silently dropped.
		if c.Title == "" && c.Artist == "" && c.ISRC == "" && c.MBID == "" {
			c.Raw = "(empty track entry)"
		}
		candidates = append(candidates, c)
	}
	return doc.Playlist.Title, candidates, nil
}
