package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func newSealer(t *testing.T) *Sealer {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	s, err := NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := newSealer(t)
	plaintext := []byte("BQC4u1gWpS-refresh-token-value")

	sealed, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("plaintext is visible inside the ciphertext")
	}
	got, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip = %q, want %q", got, plaintext)
	}
}

// GCM authenticates. A flipped bit must fail loudly rather than yielding
// corrupt plaintext (§6).
func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	s := newSealer(t)
	sealed, err := s.Seal([]byte("client-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed[len(sealed)-1] ^= 0x01

	if _, err := s.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext opened successfully")
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	a, b := newSealer(t), newSealer(t)
	sealed, err := a.Seal([]byte("token"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("ciphertext opened under a different key")
	}
}

func TestOpenRejectsTruncatedInput(t *testing.T) {
	s := newSealer(t)
	if _, err := s.Open([]byte{0x01, 0x02}); err == nil {
		t.Fatal("truncated input opened successfully")
	}
}

// Nonce reuse under GCM is catastrophic, so sealing the same plaintext twice
// must never produce the same bytes.
func TestSealUsesFreshNonce(t *testing.T) {
	s := newSealer(t)
	plaintext := []byte("same input every time")

	first, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := s.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("identical ciphertext for identical plaintext: nonce is being reused")
	}
}

func TestNewSealerRejectsShortKey(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := NewSealer(make([]byte, n)); err != ErrKeyLength {
			t.Errorf("NewSealer(%d bytes) err = %v, want ErrKeyLength", n, err)
		}
	}
}

func TestNewSealerFromBase64(t *testing.T) {
	key, _ := GenerateKey()
	if _, err := NewSealerFromBase64(base64.StdEncoding.EncodeToString(key)); err != nil {
		t.Fatalf("valid base64 key rejected: %v", err)
	}
	if _, err := NewSealerFromBase64("not-base64!!"); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}
