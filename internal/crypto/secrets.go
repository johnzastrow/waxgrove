// Package crypto handles the secrets Waxgrove must store but must never leak:
// provider OAuth tokens and, under BYO-first (D6), per-user Spotify Client
// Secrets. These are the crown jewels — they grant write access to a user's
// real music library (docs/requirements.md §6).
//
// AES-256-GCM, key from the environment, never from the database.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// KeyLen is the required key length: AES-256.
const KeyLen = 32

var (
	ErrKeyLength  = errors.New("crypto: key must be 32 bytes (AES-256)")
	ErrCiphertext = errors.New("crypto: ciphertext malformed or authentication failed")
)

// Sealer encrypts and decrypts secrets at rest.
type Sealer struct{ aead cipher.AEAD }

// NewSealer builds a Sealer from a raw 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeyLen {
		return nil, ErrKeyLength
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// NewSealerFromBase64 builds a Sealer from a standard base64 key, the form the
// operator supplies via the environment.
func NewSealerFromBase64(s string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto: key is not valid base64: %w", err)
	}
	return NewSealer(key)
}

// Seal encrypts plaintext, returning nonce||ciphertext. A fresh random nonce is
// generated per call — reusing one under GCM is catastrophic.
func (s *Sealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. GCM authenticates, so tampering fails rather than
// returning corrupt plaintext.
func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrCiphertext
	}
	out, err := s.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, ErrCiphertext
	}
	return out, nil
}

// GenerateKey produces a fresh AES-256 key for operators bootstrapping an
// instance. Uses crypto/rand — never math/rand.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}
