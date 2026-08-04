// Package config loads instance configuration from the environment.
//
// Secrets come from the environment or a secret manager, never from the
// database or a file in the repo (docs/requirements.md §6). Nothing here has a
// default that would be unsafe in production: the encryption key has no
// fallback, because generating one silently would mean tokens encrypted under
// a key that vanishes on restart.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr         string // listen address
	DatabaseURL  string // SQLite path for v1 (§7.3)
	SecretKeyB64 string // base64 AES-256 key for sealing tokens (§6)
	BaseURL      string // public URL, used to build OAuth redirect URIs
	Environment  string // "development" or "production"
	InviteOnly   bool
}

var ErrMissingKey = errors.New(
	"WAXGROVE_SECRET_KEY is required: 32 random bytes, base64 encoded. " +
		"Generate one with: waxgrove genkey")

// Load reads configuration and fails closed on anything missing or malformed.
func Load() (*Config, error) {
	c := &Config{
		Addr:         env("WAXGROVE_ADDR", "127.0.0.1:8080"),
		DatabaseURL:  env("WAXGROVE_DB", "waxgrove.db"),
		SecretKeyB64: os.Getenv("WAXGROVE_SECRET_KEY"),
		BaseURL:      env("WAXGROVE_BASE_URL", "http://127.0.0.1:8080"),
		Environment:  env("WAXGROVE_ENV", "development"),
		InviteOnly:   envBool("WAXGROVE_INVITE_ONLY", true), // §6: friends app, not a public service
	}

	if c.SecretKeyB64 == "" {
		return nil, ErrMissingKey
	}
	if c.Environment != "development" && c.Environment != "production" {
		return nil, fmt.Errorf("WAXGROVE_ENV must be development or production, got %q", c.Environment)
	}
	if !c.InviteOnly && c.Environment == "production" {
		// Open registration on a friends instance is almost certainly a mistake.
		return nil, errors.New("refusing to run production with WAXGROVE_INVITE_ONLY=false")
	}
	if c.Environment == "production" && strings.HasPrefix(c.BaseURL, "http://") {
		return nil, errors.New("WAXGROVE_BASE_URL must be https in production (OAuth redirect safety)")
	}
	return c, nil
}

// Redacted renders the config for logging with no secret material in it.
func (c *Config) Redacted() string {
	return fmt.Sprintf("addr=%s db=%s base=%s env=%s invite_only=%t secret_key=[set,%d chars]",
		c.Addr, c.DatabaseURL, c.BaseURL, c.Environment, c.InviteOnly, len(c.SecretKeyB64))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
