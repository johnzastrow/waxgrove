// Package spotify is a Spotify Web API client scoped to what Waxgrove needs:
// read a playlist, find a track, write a playlist back.
//
// Two things shape this package more than anything else.
//
// **BYO credentials (D6).** Spotify's Development Mode quota is pooled per
// developer account and caps an app at 5 users, so an operator-owned app would
// put a hard ceiling on instance size. Each user brings their own Client ID and
// Secret instead, which means nothing here may hold a single app's credentials
// — every call carries the user's.
//
// **The February 2026 migration removed GET /users/{id}/playlists.** Waxgrove
// therefore cannot list a user's playlists, and import has to start from a link
// the user pastes. That is not a UX choice; see docs/streaming-integration.md §4.
package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Scopes are the minimum that supports import and export (F6/F7).
//
// Deliberately not `playlist-read-collaborative` or anything user-profile
// shaped: an authorisation screen that asks for more than the feature needs is
// how a self-hosted app loses trust it cannot re-earn.
var Scopes = []string{
	"playlist-read-private",   // read a private playlist the user links
	"playlist-modify-public",  // export
	"playlist-modify-private", // export
	// The user's country, and nothing else we use. Availability is regional
	// (§3.6), so without this every provider id is resolved with no market and
	// may name a track that will not play for them. Left out originally, which
	// is why the first connections recorded an empty storefront.
	"user-read-private",
}

// ScopeString renders Scopes for an authorize URL.
func ScopeString() string { return strings.Join(Scopes, " ") }

// App is one user's own Spotify application (D6).
type App struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// Token is what an authorisation produces.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	Scopes       string
}

// Expired reports whether the access token needs refreshing.
//
// The skew is deliberate: a token that expires mid-flight during a long export
// would fail a batch that has already partly applied, so it is renewed early
// rather than optimistically used.
func (t Token) Expired() bool {
	return t.AccessToken == "" || time.Now().After(t.Expiry.Add(-60*time.Second))
}

// PKCE is one authorisation attempt's proof.
//
// The verifier never leaves Waxgrove; only its hash does. That is the whole
// point of PKCE — an intercepted authorisation code is useless without it.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

// NewPKCE generates a verifier, its S256 challenge, and a state value.
func NewPKCE() (PKCE, error) {
	verifier, err := randomURLSafe(64)
	if err != nil {
		return PKCE{}, err
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		State:     state,
	}, nil
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthorizeURL builds the URL the user's browser is sent to.
//
// The user authenticates on Spotify's own page; Waxgrove never sees their
// Spotify password and must never offer to.
func (c *Client) AuthorizeURL(app App, p PKCE) string {
	q := url.Values{
		"client_id":             {app.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {app.RedirectURI},
		"state":                 {p.State},
		"scope":                 {ScopeString()},
		"code_challenge_method": {"S256"},
		"code_challenge":        {p.Challenge},
	}
	return c.accountsURL + "/authorize?" + q.Encode()
}

var (
	// ErrAuthDenied means the user said no, or the grant is not usable. It is a
	// normal outcome, not a fault.
	ErrAuthDenied = errors.New("spotify: authorisation was denied or has expired")
	// ErrNeedsReconnect means the refresh token is no longer valid — the user
	// revoked access, or changed their app's credentials.
	ErrNeedsReconnect = errors.New("spotify: this connection must be re-authorised")
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// Exchange trades an authorisation code for tokens.
func (c *Client) Exchange(ctx context.Context, app App, p PKCE, code string) (Token, error) {
	return c.token(ctx, app, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {app.RedirectURI},
		"code_verifier": {p.Verifier},
	})
}

// Refresh renews an access token.
//
// Spotify may or may not return a new refresh token; when it does not, the old
// one stays valid. Overwriting it with an empty string would silently
// disconnect the user at the next expiry.
func (c *Client) Refresh(ctx context.Context, app App, refreshToken string) (Token, error) {
	tok, err := c.token(ctx, app, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		return Token{}, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

func (c *Client) token(ctx context.Context, app App, form url.Values) (Token, error) {
	if app.ClientID == "" {
		return Token{}, errors.New("spotify: no client id configured for this user")
	}
	form.Set("client_id", app.ClientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.accountsURL+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// A confidential client authenticates with its secret. PKCE is still sent
	// as well: it protects the code in transit regardless of client type.
	if app.ClientSecret != "" {
		req.SetBasicAuth(app.ClientID, app.ClientSecret)
	}

	var out tokenResponse
	if err := c.doJSON(ctx, req, quotaKey(app), &out); err != nil {
		return Token{}, err
	}
	if out.Error != "" {
		// invalid_grant covers "the user revoked us" and "that code is spent".
		if out.Error == "invalid_grant" {
			return Token{}, fmt.Errorf("%w: %s", ErrNeedsReconnect, out.ErrorDesc)
		}
		return Token{}, fmt.Errorf("%w: %s", ErrAuthDenied, out.Error)
	}
	if out.AccessToken == "" {
		return Token{}, ErrAuthDenied
	}
	return Token{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
		Scopes:       out.Scope,
	}, nil
}

// quotaKey identifies whose quota a call spends. Under BYO that is the user's
// own app, so limiters are per Client ID rather than per instance.
func quotaKey(app App) string {
	if app.ClientID == "" {
		return "anonymous"
	}
	return app.ClientID
}
