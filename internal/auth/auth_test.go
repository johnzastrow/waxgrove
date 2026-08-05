package auth

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword(t.Context(), "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := VerifyPassword(t.Context(), "correct-horse-battery-staple", h); err != nil {
		t.Errorf("correct password rejected: %v", err)
	}
	if err := VerifyPassword(t.Context(), "wrong-horse-battery-staple", h); err != ErrMismatch {
		t.Errorf("wrong password err = %v, want ErrMismatch", err)
	}
}

// The stored hash must never contain the password, and must be salted so two
// hashes of the same password differ.
func TestHashIsSaltedAndOpaque(t *testing.T) {
	pw := "correct-horse-battery-staple"
	a, _ := HashPassword(t.Context(), pw)
	b, _ := HashPassword(t.Context(), pw)
	if a == b {
		t.Fatal("identical hashes for the same password: salt is not random")
	}
	if strings.Contains(a, pw) {
		t.Fatal("hash contains the plaintext password")
	}
	if !strings.HasPrefix(a, "$argon2id$") {
		t.Errorf("not an argon2id hash: %s", a[:20])
	}
}

func TestShortPasswordRejected(t *testing.T) {
	if _, err := HashPassword(t.Context(), "short"); err != ErrWeak {
		t.Errorf("err = %v, want ErrWeak", err)
	}
	// The floor counts runes, not bytes, so multi-byte passwords are not
	// wrongly accepted as long enough.
	if _, err := HashPassword(t.Context(), strings.Repeat("é", 5)); err != ErrWeak {
		t.Errorf("5 multi-byte runes accepted: %v", err)
	}
}

// A malformed stored hash must be an explicit error, never an accidental pass.
func TestMalformedHashNeverVerifies(t *testing.T) {
	for _, bad := range []string{
		"", "not-a-hash", "$argon2id$", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
	} {
		if err := VerifyPassword(t.Context(), "anything", bad); err == nil {
			t.Errorf("malformed hash %q verified successfully", bad)
		}
	}
}

func TestNewTokenIsUniqueAndLong(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if len(tok) < 40 {
			t.Fatalf("token too short (%d chars): %q", len(tok), tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token generated")
		}
		seen[tok] = true
	}
}

// EqualiseTiming must actually do the argon2 work; a malformed placeholder
// would return instantly and defeat the purpose.
func TestEqualiseTimingUsesARealHash(t *testing.T) {
	if err := VerifyPassword(t.Context(), "anything", dummyHash); err != ErrMismatch {
		t.Fatalf("placeholder hash is not well-formed: %v", err)
	}
	EqualiseTiming(t.Context(), "anything") // must not panic
}
