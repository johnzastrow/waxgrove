// Package auth provides password hashing, session tokens and invite codes.
//
// Local accounts are the default and OIDC is optional (D5); this package covers
// the local half. Argon2id per §6.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. Deliberately explicit rather than tuned to whatever
// machine happens to build this: a self-hoster may be on a small shared VPS
// (N1), so memory is kept modest while staying well above the OWASP floor.
//
// 64 MiB is per hash, and concurrent logins multiply it. That is the intended
// cost of a memory-hard KDF, not a leak — but it is also the single largest
// allocation this program makes, so see scavenge.go for what happens to it
// afterwards.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

var (
	ErrInvalidHash = errors.New("auth: password hash is malformed")
	ErrMismatch    = errors.New("auth: password does not match")
	ErrWeak        = errors.New("auth: password must be at least 12 characters")
)

// MinPasswordLen matches the complexity floor used elsewhere in the project.
const MinPasswordLen = 12

// HashPassword returns a PHC-formatted Argon2id hash, salt included.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLen {
		return "", ErrWeak
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	noteHash() // see scavenge.go: 64 MiB of garbage that nothing else will collect

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a stored hash in constant time.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrInvalidHash
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrInvalidHash
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	noteHash()
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

// NewToken returns a high-entropy, URL-safe opaque token for sessions and
// invite codes. crypto/rand only — never math/rand for anything security
// relevant (§6).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// dummyHash is a real Argon2id hash, used only by EqualiseTiming.
var dummyHash string

func init() {
	// Generated once at startup so it is guaranteed well-formed and matches the
	// current cost parameters. A hand-written constant risks being malformed,
	// which would make the comparison fast and defeat the purpose.
	h, err := HashPassword("waxgrove-timing-equalisation-placeholder")
	if err != nil {
		panic("auth: cannot build timing placeholder: " + err.Error())
	}
	dummyHash = h
}

// EqualiseTiming spends roughly the same work as a real password check.
//
// Call it on the "no such account" path so a failed lookup and a failed
// password take comparable time, and the response cannot be used to enumerate
// which addresses are registered.
func EqualiseTiming(password string) {
	_ = VerifyPassword(password, dummyHash)
}
