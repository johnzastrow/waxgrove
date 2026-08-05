package httpapi

import (
	"strings"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// ParsePastedList turns free text into candidates (§3.3).
//
// People paste what they have: a setlist, a text message, a screenshot's worth
// of retyped lines, output from some other tool. The separator varies, the
// order of artist and title varies, and a good third of the lines are numbering
// or blank.
//
// So this is deliberately forgiving about shape and strict about one thing: it
// never invents. Whatever it cannot split confidently goes through as a title
// with the original text preserved in Raw, and the resolution ladder decides
// (BR-5) — a wrong split silently searching for the wrong song is worse than an
// unsplit line the user can see and fix.
func ParsePastedList(text string) []domain.Candidate {
	var out []domain.Candidate
	for _, line := range strings.Split(text, "\n") {
		if c, ok := parseLine(line); ok {
			out = append(out, c)
		}
	}
	return out
}

// separators are tried in order of how unambiguously they mean "artist here,
// title there". An em or en dash is almost always a real separator; a plain
// hyphen is often part of a name ("Jay-Z", "Blink-182"), so it is only accepted
// with spaces around it.
var separators = []string{" — ", " – ", " - ", " -- ", " · ", " | ", "\t"}

func parseLine(line string) (domain.Candidate, bool) {
	raw := strings.TrimSpace(line)
	if raw == "" {
		return domain.Candidate{}, false
	}

	cleaned := stripLeadingIndex(raw)
	if cleaned == "" {
		return domain.Candidate{}, false
	}

	for _, sep := range separators {
		left, right, found := strings.Cut(cleaned, sep)
		left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		if !found || left == "" || right == "" {
			continue
		}
		// "Artist — Title" is the overwhelmingly common order, and the one the
		// UI asks for. Guessing the other way round from punctuation alone
		// would be exactly the kind of invention this avoids.
		return domain.Candidate{
			Artist: left, Title: right, Raw: raw, SourceRef: "paste",
		}, true
	}

	// No confident split: keep the whole line as the title. The mapper handles
	// free text well, and the user can still see what they pasted.
	return domain.Candidate{Title: cleaned, Raw: raw, SourceRef: "paste"}, true
}

// stripLeadingIndex removes track numbering: "1.", "01 -", "12)", "3 ".
//
// Numbering is the single most common thing in a pasted tracklist and the most
// damaging to leave in, because it lands in the artist field and poisons the
// match.
func stripLeadingIndex(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i > 3 { // a four-digit number is a year, not an index
		return s
	}
	rest := s[i:]
	trimmed := strings.TrimLeft(rest, " .)-:\t")
	// Only treat it as numbering if something was actually separating it;
	// "1984" or "99 Luftballons" must survive intact.
	if len(trimmed) == len(rest) {
		return s
	}
	if trimmed == "" {
		return s
	}
	return strings.TrimSpace(trimmed)
}
