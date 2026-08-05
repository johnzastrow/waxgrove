//go:build waxgrovedev

package spotify

import (
	"log/slog"
	"os"
)

// endpoints allows the whole binary to be pointed at a stub Spotify, so the
// connect wizard, import and export can be driven end to end in a browser.
//
// This file is behind a build tag on purpose. Pointing the authorisation
// endpoint somewhere else is how you harvest a user's Spotify credentials, so
// rather than shipping the capability and refusing it at runtime, a release
// binary does not contain it at all:
//
//	go build -tags waxgrovedev ./cmd/waxgrove   # development only
//	make build                                  # what ships
func endpoints() (api, accounts string) {
	api, accounts = "https://api.spotify.com/v1", "https://accounts.spotify.com"
	if v := os.Getenv("WAXGROVE_DEV_SPOTIFY_API_URL"); v != "" {
		api = v
	}
	if v := os.Getenv("WAXGROVE_DEV_SPOTIFY_ACCOUNTS_URL"); v != "" {
		accounts = v
	}
	if api != "https://api.spotify.com/v1" || accounts != "https://accounts.spotify.com" {
		slog.Warn("DEVELOPMENT BUILD: talking to a stub Spotify",
			"api", api, "accounts", accounts)
	}
	return api, accounts
}
