// Package connector turns a provider account into Waxgrove operations:
// import a playlist in, export one back out.
//
// It sits above internal/spotify (which knows the API) and below
// internal/httpapi (which knows about requests), and it owns the two things
// neither of them should: keeping a user's access token fresh, and the
// cheapest-first ladder that maps a canonical record to a provider id.
//
// Nothing here runs inside a request. Every operation is driven by a job
// (internal/jobs), because a cold export can exceed a minute and because §7.2
// forbids holding a write transaction across network I/O.
package connector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/spotify"
)

// Spotify is the connector for one instance. Safe for concurrent use.
type Spotify struct {
	client  *spotify.Client
	creds   *sqlite.CredentialRepo
	refs    *sqlite.ProviderRefRepo
	baseURL string
}

// NewSpotify wires the connector. baseURL is the instance's public URL, which
// determines the OAuth redirect the user must register in their own Spotify app.
func NewSpotify(client *spotify.Client, creds *sqlite.CredentialRepo,
	refs *sqlite.ProviderRefRepo, baseURL string) *Spotify {
	return &Spotify{client: client, creds: creds, refs: refs, baseURL: baseURL}
}

// RedirectURI is what the user pastes into their Spotify app settings.
//
// It must match byte for byte on both sides, so it is derived in exactly one
// place and shown to the user rather than described.
func (s *Spotify) RedirectURI() string {
	return strings.TrimRight(s.baseURL, "/") + "/api/connect/spotify/callback"
}

// ErrNotConnected means the user has no usable authorisation.
var ErrNotConnected = errors.New("connector: this account is not connected to Spotify")

// app builds the user's own application from stored credentials (D6).
func (s *Spotify) app(c *sqlite.Credential) spotify.App {
	return spotify.App{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURI:  s.RedirectURI(),
	}
}

// session returns a usable token for a user, refreshing it if needed and
// persisting the result.
//
// Refreshing here rather than at the call site means one place understands
// token lifetime, and a long export cannot half-fail because one batch used a
// token that expired between requests.
func (s *Spotify) session(ctx context.Context, userID string) (spotify.App, spotify.Token, error) {
	c, err := s.creds.Get(ctx, userID, sqlite.ServiceSpotify)
	if errors.Is(err, sqlite.ErrNoCredentials) {
		return spotify.App{}, spotify.Token{}, ErrNotConnected
	}
	if err != nil {
		return spotify.App{}, spotify.Token{}, err
	}
	if !c.Connected() {
		return spotify.App{}, spotify.Token{}, ErrNotConnected
	}

	app := s.app(c)
	tok := spotify.Token{
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		Expiry:       c.ExpiresAt,
		Scopes:       c.Scopes,
	}
	if !tok.Expired() {
		return app, tok, nil
	}

	fresh, err := s.client.Refresh(ctx, app, tok.RefreshToken)
	if err != nil {
		return spotify.App{}, spotify.Token{}, err
	}
	if err := s.creds.SaveTokens(ctx, userID, sqlite.ServiceSpotify,
		fresh.AccessToken, fresh.RefreshToken, fresh.Expiry, fresh.Scopes, c.Storefront); err != nil {
		return spotify.App{}, spotify.Token{}, err
	}
	return app, fresh, nil
}

// ------------------------------------------------------------------ connect --

// Begin starts an authorisation and returns the URL to send the user to,
// together with the PKCE material the callback will need.
func (s *Spotify) Begin(ctx context.Context, userID string) (string, spotify.PKCE, error) {
	c, err := s.creds.Get(ctx, userID, sqlite.ServiceSpotify)
	if errors.Is(err, sqlite.ErrNoCredentials) {
		return "", spotify.PKCE{}, sqlite.ErrNoApp
	}
	if err != nil {
		return "", spotify.PKCE{}, err
	}
	if c.ClientID == "" {
		return "", spotify.PKCE{}, sqlite.ErrNoApp
	}
	p, err := spotify.NewPKCE()
	if err != nil {
		return "", spotify.PKCE{}, err
	}
	return s.client.AuthorizeURL(s.app(c), p), p, nil
}

// Complete exchanges the callback's code for tokens and stores them.
//
// It also records the user's market, because availability is regional and a
// provider id resolved without one may not play for them (§3.6).
func (s *Spotify) Complete(ctx context.Context, userID string, p spotify.PKCE, code string) error {
	c, err := s.creds.Get(ctx, userID, sqlite.ServiceSpotify)
	if err != nil {
		return err
	}
	app := s.app(c)

	tok, err := s.client.Exchange(ctx, app, p, code)
	if err != nil {
		return err
	}
	_, market, err := s.client.Me(ctx, app, tok)
	if err != nil {
		// A market we could not read is not worth failing a connection over;
		// resolution falls back to unmarketed lookups.
		market = c.Storefront
	}
	return s.creds.SaveTokens(ctx, userID, sqlite.ServiceSpotify,
		tok.AccessToken, tok.RefreshToken, tok.Expiry, tok.Scopes, market)
}

// Status describes a connection for the UI, without decrypting anything it
// does not need.
type Status struct {
	AppConfigured bool   `json:"app_configured"`
	Connected     bool   `json:"connected"`
	Storefront    string `json:"storefront,omitempty"`
	RedirectURI   string `json:"redirect_uri"`
	ClientID      string `json:"client_id,omitempty"`
}

// Status reports where the user is in the connect flow.
//
// The Client ID is returned because the user pasted it and needs to check it
// against their Spotify dashboard. The Secret is never returned, by anything.
func (s *Spotify) Status(ctx context.Context, userID string) (Status, error) {
	st := Status{RedirectURI: s.RedirectURI()}
	c, err := s.creds.Get(ctx, userID, sqlite.ServiceSpotify)
	if errors.Is(err, sqlite.ErrNoCredentials) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.AppConfigured = c.ClientID != ""
	st.Connected = c.Connected()
	st.Storefront = c.Storefront
	st.ClientID = c.ClientID
	return st, nil
}

// Disconnect removes the connection entirely.
func (s *Spotify) Disconnect(ctx context.Context, userID string) error {
	return s.creds.Delete(ctx, userID, sqlite.ServiceSpotify)
}

// ------------------------------------------------------------------- import --

// FetchPlaylist reads a provider playlist into candidates.
//
// Every track carries an ISRC, so these enter the resolution ladder at step 1 —
// exact and automatic (§3.2). That is why a provider import produces almost
// entirely high-confidence records where a pasted text list does not.
func (s *Spotify) FetchPlaylist(ctx context.Context, userID, ref string) (string, []domain.Candidate, error) {
	id, err := spotify.ParsePlaylistRef(ref)
	if err != nil {
		return "", nil, err
	}
	app, tok, err := s.session(ctx, userID)
	if err != nil {
		return "", nil, err
	}

	// One call, with the tracks inline. Spotify refuses the separate tracks
	// endpoint for apps in Development Mode, so asking the playlist endpoint
	// for its items is the route that actually works.
	meta, tracks, err := s.client.PlaylistWithTracks(ctx, app, tok, id)

	name := meta.Name
	if name == "" {
		name = "Imported from Spotify"
	}

	// A partial read is reported, never silently accepted: importing the first
	// hundred of two hundred tracks and calling it done is the quiet data loss
	// F15 exists to prevent.
	var truncated *spotify.ErrTruncated
	if errors.As(err, &truncated) {
		return name, tracks, fmt.Errorf(
			"only the first %d of %d tracks could be read — Spotify does not let "+
				"apps in Development Mode page through a playlist. Split it into "+
				"playlists of 100 or fewer and import them separately: %w",
			truncated.Got, truncated.Total, ErrPartialRead)
	}
	if err != nil {
		return "", nil, describe(err)
	}
	return name, tracks, nil
}

// ErrPartialRead means only part of a playlist was readable.
var ErrPartialRead = errors.New("connector: the playlist could not be read in full")

// ------------------------------------------------------------------- export --

// Resolution is how one record mapped onto Spotify.
type Resolution struct {
	RecordID string
	URI      string
	Status   string // domain.JobItemOK, JobItemUnavailable, ...
	Detail   string
}

// Resolve maps a record to a Spotify track URI, cheapest first (§5).
//
// The order is the point: a cached ref costs nothing, an ISRC lookup is an
// identity match, and text search is a guess constrained by Development Mode's
// 10-result cap. A record that reaches the end is genuinely unavailable in that
// market, which is a result to report rather than an error to raise (F15).
func (s *Spotify) Resolve(ctx context.Context, app spotify.App, tok spotify.Token,
	rec domain.Record, market string) Resolution {

	res := Resolution{RecordID: rec.ID}

	// 1. Cached, keyed by storefront because availability is regional (§3.6).
	if ref, err := s.refs.Get(ctx, rec.ID, sqlite.ServiceSpotify, market); err == nil {
		switch ref.Status {
		case sqlite.RefOK:
			res.URI, res.Status = ref.ExternalID, domain.JobItemOK
			res.Detail = "already known"
			return res
		case sqlite.RefAbsent, sqlite.RefRegion:
			res.Status, res.Detail = domain.JobItemUnavailable, "known to be unavailable here"
			return res
		}
	}

	// 2. ISRC — an identity match, and a record may carry several (BR-1), so
	//    every one is worth trying before falling back to a guess.
	for _, isrc := range rec.ISRCs {
		uri, err := s.client.FindByISRC(ctx, app, tok, isrc, market)
		if err == nil {
			s.cache(ctx, rec.ID, market, uri, sqlite.RefOK)
			res.URI, res.Status, res.Detail = uri, domain.JobItemOK, "matched by ISRC"
			return res
		}
		if !errors.Is(err, spotify.ErrNoMatch) {
			res.Status, res.Detail = domain.JobItemFailed, userError(err)
			return res
		}
	}

	// 3. Text, last resort.
	uri, err := s.client.FindByText(ctx, app, tok, rec.ArtistCredit, rec.Title, market)
	switch {
	case err == nil:
		s.cache(ctx, rec.ID, market, uri, sqlite.RefOK)
		res.URI, res.Status = uri, domain.JobItemOK
		res.Detail = "matched by name — worth checking"
		return res
	case errors.Is(err, spotify.ErrNoMatch):
		// Cached as absent so the next export of the same song costs nothing.
		s.cache(ctx, rec.ID, market, "", sqlite.RefAbsent)
		res.Status = domain.JobItemUnavailable
		res.Detail = "not on Spotify in " + marketName(market)
		return res
	default:
		res.Status, res.Detail = domain.JobItemFailed, userError(err)
		return res
	}
}

func (s *Spotify) cache(ctx context.Context, recordID, market, uri, status string) {
	// Best-effort: a cache write failing must not fail an export that
	// otherwise succeeded.
	_ = s.refs.Put(ctx, recordID, sqlite.ServiceSpotify, market, uri, status)
}

// ErrDiverged means the provider copy was edited since Waxgrove last wrote it.
//
// Re-syncing would silently discard whatever the user did over there, which D10
// forbids: the provider copy is a projection, but somebody's work is still
// somebody's work. The caller asks; it does not decide.
var ErrDiverged = errors.New("connector: the playlist on Spotify has been edited since it was last sent")

// Written is the outcome of an export.
type Written struct {
	ProviderPlaylistID string
	Snapshot           string
	Added              int
	Replaced           bool // updated an existing playlist rather than creating one
}

// CreateAndFill writes a playlist to Spotify.
//
// Tracks are added in batches, and the count actually added is returned even on
// failure so an interrupted export resumes from what landed rather than
// duplicating it.
func (s *Spotify) CreateAndFill(ctx context.Context, app spotify.App, tok spotify.Token,
	name, description string, uris []string) (Written, error) {

	spotifyUserID, _, err := s.client.Me(ctx, app, tok)
	if err != nil {
		return Written{}, describe(err)
	}
	// Private by default. A playlist appearing publicly on someone's profile
	// because an export defaulted that way is not a mistake to make once.
	playlistID, err := s.client.CreatePlaylist(ctx, app, tok, spotifyUserID, name, description, false)
	if err != nil {
		return Written{}, describe(err)
	}
	added, err := s.client.AddTracks(ctx, app, tok, playlistID, uris)
	out := Written{ProviderPlaylistID: playlistID, Added: added}
	if err != nil {
		return out, describe(err)
	}
	if pl, err := s.client.GetPlaylist(ctx, app, tok, playlistID); err == nil {
		out.Snapshot = pl.SnapshotID
	}
	return out, nil
}

// Update rewrites a playlist Waxgrove has already sent.
//
// This is what stops a second export producing a second playlist. It refuses if
// the copy has moved since we last wrote it, unless the caller has explicitly
// decided to overwrite — the user is the only one who can weigh their own edits
// against a re-sync (D10).
func (s *Spotify) Update(ctx context.Context, app spotify.App, tok spotify.Token,
	sync sqlite.Sync, uris []string, force bool) (Written, error) {

	current, err := s.client.GetPlaylist(ctx, app, tok, sync.ProviderPlaylistID)
	if err != nil {
		var st *spotify.ErrStatus
		if errors.As(err, &st) && st.NotFound() {
			// Deleted on the provider side. Nothing to update, and refusing
			// would trap the user — so the caller creates a fresh one.
			return Written{}, ErrGone
		}
		return Written{}, describe(err)
	}

	// A snapshot we never recorded cannot be compared, so it is not evidence of
	// divergence — treating it as such would block every playlist synced before
	// this check existed.
	if !force && sync.ProviderSnapshot != "" && current.SnapshotID != sync.ProviderSnapshot {
		return Written{}, ErrDiverged
	}

	snapshot, err := s.client.ReplaceTracks(ctx, app, tok, sync.ProviderPlaylistID, uris)
	out := Written{
		ProviderPlaylistID: sync.ProviderPlaylistID,
		Snapshot:           snapshot,
		Added:              len(uris),
		Replaced:           true,
	}
	if err != nil {
		return out, describe(err)
	}
	return out, nil
}

// ErrGone means the provider copy no longer exists.
var ErrGone = errors.New("connector: that playlist is no longer on Spotify")

// Session exposes a refreshed session to the job runner, which needs one for
// the whole of an export rather than per call.
func (s *Spotify) Session(ctx context.Context, userID string) (spotify.App, spotify.Token, string, error) {
	app, tok, err := s.session(ctx, userID)
	if err != nil {
		return app, tok, "", err
	}
	c, err := s.creds.Get(ctx, userID, sqlite.ServiceSpotify)
	if err != nil {
		return app, tok, "", err
	}
	return app, tok, c.Storefront, nil
}

// ------------------------------------------------------------------- errors --

// describe turns a provider failure into something a user can act on.
//
// §6 keeps internal detail in the logs, but a job surface is user-facing and
// "something went wrong" on a five-minute export is useless.
func describe(err error) error {
	switch {
	case err == nil:
		return nil
	case spotify.IsAuthError(err):
		return fmt.Errorf("%w: Spotify needs you to reconnect", ErrNotConnected)
	}
	var st *spotify.ErrStatus
	if errors.As(err, &st) {
		// The full error, with the request in it, goes to the log. The user
		// gets something they can act on (§6).
		slog.Warn("spotify refused a request", "err", st.Error())

		if st.NotFound() {
			return errors.New("that playlist is not there, or your account cannot see it")
		}
		if st.Code == http.StatusForbidden {
			// A 403 on a Development Mode app is nearly always one of two
			// things, and the user fixes them differently — so say both rather
			// than "Forbidden", which is actionable by nobody.
			return errors.New(
				"Spotify refused this. Two things cause it: your Spotify account " +
					"is not listed under User Management in your own app's settings " +
					"in the Spotify developer dashboard, or the playlist belongs to " +
					"Spotify itself (Discover Weekly, Release Radar, any editorial " +
					"or algorithmic playlist), which apps in Development Mode cannot " +
					"read. Try one of your own playlists")
		}
	}
	if d, ok := spotify.RateLimit(err); ok {
		return fmt.Errorf("Spotify is rate limiting this account; it will retry in %s", d.Round(time.Second))
	}
	return err
}

func userError(err error) string {
	if d, ok := spotify.RateLimit(err); ok {
		return fmt.Sprintf("rate limited, retrying in %s", d.Round(time.Second))
	}
	if spotify.IsAuthError(err) {
		return "the Spotify connection expired"
	}
	return "Spotify could not be reached"
}

func marketName(market string) string {
	if market == "" {
		return "your region"
	}
	return market
}
