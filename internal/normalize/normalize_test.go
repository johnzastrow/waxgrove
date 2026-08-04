package normalize

import "testing"

func TestKey(t *testing.T) {
	cases := map[string]string{
		"Björk":                  "bjork",
		"Sigur Rós":              "sigur ros",
		"Don’t Stop":             "dont stop",
		"Don't Stop":             "dont stop", // both apostrophes agree
		"The Beatles feat. Yoko": "the beatles feat yoko",
		"  Pink   Moon  ":        "pink moon",
		"AC/DC":                  "ac dc",
		"Café del Mar":           "cafe del mar",
		"Motörhead":              "motorhead",
		"":                       "",
	}
	for in, want := range cases {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

// The two apostrophe forms are the single most common reason a pasted track
// name fails to match a catalog entry.
func TestKeyUnifiesApostrophes(t *testing.T) {
	if Key("Don’t Stop") != Key("Don't Stop") {
		t.Fatal("curly and straight apostrophes normalise differently")
	}
}

func TestTitleKeyStripsProviderNoise(t *testing.T) {
	cases := map[string]string{
		"Dreams - 2004 Remaster":       "dreams",
		"Everywhere (Radio Edit)":      "everywhere",
		"The Wind [Live]":              "the wind",
		"Go Your Own Way – Remastered": "go your own way",
		"Pink Moon":                    "pink moon",
	}
	for in, want := range cases {
		if got := TitleKey(in); got != want {
			t.Errorf("TitleKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// A leading parenthesis is part of the title, not noise.
func TestTitleKeyKeepsLeadingParenthetical(t *testing.T) {
	if got := TitleKey("(Don't Fear) The Reaper"); got == "" {
		t.Fatalf("TitleKey stripped the whole title: %q", got)
	}
}

func TestDurationClose(t *testing.T) {
	cases := []struct {
		a, b int
		want bool
	}{
		{254000, 254000, true},
		{254000, 256000, true},  // +2s, inside the window
		{254000, 257000, true},  // +3s, on the boundary
		{254000, 257001, false}, // just outside
		{254000, 178000, false},
		{0, 254000, false}, // unknown duration never matches
		{254000, 0, false},
	}
	for _, c := range cases {
		if got := DurationClose(c.a, c.b); got != c.want {
			t.Errorf("DurationClose(%d, %d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
