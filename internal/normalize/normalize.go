// Package normalize produces the matching keys stored in records.norm_title and
// records.norm_artist.
//
// BR-7: normalisation happens in Go, never in SQL. Two reasons — it makes
// matching behave identically on SQLite and MariaDB by construction, and
// modernc.org/sqlite cannot register custom Go functions, so it would have to
// live here anyway.
//
// The transformation is deliberately aggressive and lossy. It is only ever used
// as a matching key; display always uses the original text.
package normalize

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Key reduces a title or artist credit to a comparable form:
// casefolded, accent-stripped, punctuation-removed, whitespace-collapsed.
//
//	"Björk"                  -> "bjork"
//	"Don’t Stop"             -> "dont stop"
//	"The Beatles feat. Yoko" -> "the beatles feat yoko"
func Key(s string) string {
	// Decompose, drop combining marks, recompose: "é" -> "e".
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(t, s)
	if err != nil {
		folded = s // fall back to the raw string rather than failing a match
	}

	var b strings.Builder
	b.Grow(len(folded))
	lastSpace := true // trims leading space without a second pass
	for _, r := range strings.ToLower(folded) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '/':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			// Punctuation is dropped entirely, so "Don't" and "Dont" agree.
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// TitleKey normalises a track title, additionally stripping the parenthetical
// noise that streaming services add and MusicBrainz usually does not.
//
//	"Dreams - 2004 Remaster"  -> "dreams"
//	"Everywhere (Radio Edit)" -> "everywhere"
//
// Callers keep the full Key too: a remaster is a different recording with its
// own ISRC, so this is a fallback for fuzzy comparison, never an assertion that
// two tracks are identical.
func TitleKey(s string) string {
	if i := strings.IndexAny(s, "(["); i > 0 {
		s = s[:i]
	}
	for _, sep := range []string{" - ", " – ", " — "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
			break
		}
	}
	return Key(s)
}

// DurationClose reports whether two durations match within the ±3s window
// §3.2 uses for fuzzy matching. A zero duration means "unknown", which never
// counts as a match.
func DurationClose(a, b int) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 3000
}
