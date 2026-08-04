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

// --- metadata source gate ---------------------------------------------------

func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("WAXGROVE_SECRET_KEY", "aaaa")
	t.Setenv("WAXGROVE_ENV", "development")
	t.Setenv("WAXGROVE_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("WAXGROVE_METADATA_SOURCE", "")
	t.Setenv("WAXGROVE_CONTACT", "")
}

// MusicBrainz requires a way to reach a misbehaving operator, so enabling the
// connector without a contact must fail loudly rather than send junk.
func TestMusicBrainzRequiresAContact(t *testing.T) {
	baseEnv(t)
	t.Setenv("WAXGROVE_METADATA_SOURCE", "musicbrainz")
	if _, err := Load(); err == nil {
		t.Fatal("musicbrainz enabled with no contact was accepted")
	}
	t.Setenv("WAXGROVE_CONTACT", "ops@example.test")
	c, err := Load()
	if err != nil {
		t.Fatalf("valid musicbrainz config rejected: %v", err)
	}
	if !c.RemoteEnabled() {
		t.Error("RemoteEnabled() = false with musicbrainz configured")
	}
}

// N6: running with nothing attached is a supported configuration, not an error.
func TestMetadataNoneIsSupported(t *testing.T) {
	baseEnv(t)
	t.Setenv("WAXGROVE_METADATA_SOURCE", "none")
	c, err := Load()
	if err != nil {
		t.Fatalf("metadata=none rejected: %v", err)
	}
	if c.RemoteEnabled() {
		t.Error("RemoteEnabled() = true with metadata=none")
	}
}

func TestUnknownMetadataSourceIsRejected(t *testing.T) {
	baseEnv(t)
	t.Setenv("WAXGROVE_METADATA_SOURCE", "discogs")
	t.Setenv("WAXGROVE_CONTACT", "ops@example.test")
	if _, err := Load(); err == nil {
		t.Fatal("unknown metadata source accepted")
	}
}

// The default must not silently start hitting a third party without a contact.
func TestDefaultRequiresExplicitContact(t *testing.T) {
	baseEnv(t)
	if _, err := Load(); err == nil {
		t.Fatal("default config started with no contact configured")
	}
}

func TestDefaultsAreSafe(t *testing.T) {
	baseEnv(t)
	t.Setenv("WAXGROVE_METADATA_SOURCE", "none")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.InviteOnly {
		t.Error("registration defaults to open; §6 requires invite-only")
	}
	if c.Addr != "127.0.0.1:8080" {
		t.Errorf("default addr = %q — should not bind all interfaces by default", c.Addr)
	}
}

func TestRedactedShowsMetadataSource(t *testing.T) {
	c := &Config{MetadataSource: "none", SecretKeyB64: "SECRET"}
	if !contains(c.Redacted(), "metadata=none") {
		t.Errorf("Redacted omits the metadata source: %s", c.Redacted())
	}
}
