package memlimit

import (
	"errors"
	"math"
	"runtime/debug"
	"testing"
)

func TestParseLimit(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  int64
		noLim bool
	}{
		{name: "v2 value", in: "268435456\n", want: 268435456},
		{name: "no trailing newline", in: "134217728", want: 134217728},
		{name: "v2 unlimited", in: "max\n", noLim: true},
		{name: "empty", in: "  \n", noLim: true},
		// cgroup v1 spells "unlimited" as a number near max int64. Treated as a
		// real limit it would produce a GOMEMLIMIT of several exabytes, which
		// is the same as no limit but arrived at by accident.
		{name: "v1 unlimited sentinel", in: "9223372036854771712", noLim: true},
		{name: "zero", in: "0", noLim: true},
		{name: "negative", in: "-1", noLim: true},
		{name: "garbage", in: "not-a-number", noLim: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseLimit(c.in)
			if c.noLim {
				if err == nil {
					t.Fatalf("parseLimit(%q) = %d, want an error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLimit(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("parseLimit(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// The headroom exists because not all of a container's memory is Go heap.
// A limit at 100% would hand the collector memory that is already spoken for.
func TestHeadroomLeavesRoom(t *testing.T) {
	if Headroom <= 0 || Headroom >= 1 {
		t.Fatalf("Headroom = %v, want a fraction strictly between 0 and 1", Headroom)
	}
	const limit = 256 << 20
	target := int64(float64(limit) * Headroom)
	if target >= limit {
		t.Errorf("target %d is not below the cgroup limit %d", target, limit)
	}
	// Enough for two concurrent Argon2 hashes at 64 MiB each, which is what
	// auth.MaxConcurrentHashes permits. Below that the collector would be
	// fighting memory the program genuinely needs live.
	if target < 128<<20 {
		t.Errorf("target %d MiB leaves no room for bounded hashing", target>>20)
	}
}

// An operator who set GOMEMLIMIT deliberately must not have it overridden.
func TestExplicitEnvironmentWins(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "123MiB")

	before := debug.SetMemoryLimit(-1) // -1 reads without setting
	msg, err := Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after := debug.SetMemoryLimit(-1)
	if after != before {
		t.Errorf("Apply changed the limit %d -> %d despite an explicit GOMEMLIMIT", before, after)
	}
	if msg == "" {
		t.Error("Apply said nothing about leaving the environment alone")
	}
}

// Outside a container there is no limit to find, and that is not a failure.
func TestNoLimitIsNotAnError(t *testing.T) {
	if !errors.Is(ErrNoLimit, ErrNoLimit) {
		t.Fatal("ErrNoLimit is not comparable")
	}
	// detect() reports either a plausible limit or ErrNoLimit — never a
	// nonsense number that would be worse than doing nothing.
	got, err := detect()
	if err != nil {
		if !errors.Is(err, ErrNoLimit) {
			t.Errorf("detect returned an unexpected error: %v", err)
		}
		return
	}
	if got <= 0 || got > math.MaxInt64/2 {
		t.Errorf("detect returned an implausible limit: %d", got)
	}
	t.Logf("this machine reports a cgroup limit of %d MiB", got>>20)
}
