package config

import (
	"errors"
	"testing"
)

func TestLoadRequiresSecretKey(t *testing.T) {
	t.Setenv("WAXGROVE_SECRET_KEY", "")
	if _, err := Load(); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("err = %v, want ErrMissingKey", err)
	}
}

// Open registration on a friends instance is almost certainly a mistake (§6).
func TestLoadRefusesOpenRegistrationInProduction(t *testing.T) {
	t.Setenv("WAXGROVE_SECRET_KEY", "aaaa")
	t.Setenv("WAXGROVE_ENV", "production")
	t.Setenv("WAXGROVE_BASE_URL", "https://waxgrove.example")
	t.Setenv("WAXGROVE_INVITE_ONLY", "false")

	if _, err := Load(); err == nil {
		t.Fatal("production accepted open registration")
	}
}

// OAuth redirect URIs must not be plaintext http in production.
func TestLoadRefusesPlaintextBaseURLInProduction(t *testing.T) {
	t.Setenv("WAXGROVE_SECRET_KEY", "aaaa")
	t.Setenv("WAXGROVE_ENV", "production")
	t.Setenv("WAXGROVE_BASE_URL", "http://waxgrove.example")

	if _, err := Load(); err == nil {
		t.Fatal("production accepted an http base URL")
	}
}

func TestRedactedOmitsSecretMaterial(t *testing.T) {
	c := &Config{SecretKeyB64: "SUPERSECRETKEYMATERIAL", Addr: "127.0.0.1:8080"}
	out := c.Redacted()
	if contains(out, "SUPERSECRETKEYMATERIAL") {
		t.Fatalf("Redacted leaked the key: %s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
