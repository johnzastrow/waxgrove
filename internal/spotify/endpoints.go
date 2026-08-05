//go:build !waxgrovedev

package spotify

// endpoints returns where Spotify lives.
//
// Hardcoded, and deliberately not configurable. There is exactly one Spotify,
// so these are not a deployment variable — and a configurable *authorisation*
// endpoint is a credential-harvesting vector: anyone who could set it could
// point users' sign-in at a page they control. Refusing it at runtime would
// still leave the code path in the binary; this way there is nothing to refuse.
//
// Tests override the URLs through WithBaseURLs, which is the right seam for
// them. The dev build tag (endpoints_dev.go) exists only so the whole binary
// can be driven against a stub during development, and is never used for a
// release.
func endpoints() (api, accounts string) {
	return "https://api.spotify.com/v1", "https://accounts.spotify.com"
}
