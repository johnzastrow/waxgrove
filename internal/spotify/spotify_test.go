package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// stub is a fake Spotify. Every test drives the real client against it, so the
// request shaping — query grammar, pagination, batching — is exercised rather
// than assumed.
type stub struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	handler  func(w http.ResponseWriter, r *http.Request)
}

func newStub(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) (*stub, *Client) {
	t.Helper()
	s := &stub{handler: h}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, r)
		s.bodies = append(s.bodies, string(body))
		s.mu.Unlock()
		s.handler(w, r)
	}))
	t.Cleanup(s.Close)
	c := New(WithBaseURLs(s.URL, s.URL))
	return s, c
}

func (s *stub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *stub) body(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[i]
}

func (s *stub) req(i int) *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[i]
}

var testApp = App{ClientID: "cid", ClientSecret: "secret", RedirectURI: "https://wg.test/cb"}
var testTok = Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}

// A user will paste whatever their Spotify app gave them. All of these are
// things the Share menu actually produces.
func TestParsePlaylistRef(t *testing.T) {
	const want = "37i9dQZF1DXcBWIGoYBM5M"
	good := []string{
		"https://open.spotify.com/playlist/" + want,
		"https://open.spotify.com/playlist/" + want + "?si=abc123&pt=x",
		"http://open.spotify.com/playlist/" + want,
		"spotify:playlist:" + want,
		want,
		"  " + want + "  ",
	}
	for _, in := range good {
		got, err := ParsePlaylistRef(in)
		if err != nil {
			t.Errorf("ParsePlaylistRef(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParsePlaylistRef(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{
		"", "   ", "not a link",
		"https://open.spotify.com/album/37i9dQZF1DXcBWIGoYBM5M", // an album, not a playlist
		"https://open.spotify.com/track/37i9dQZF1DXcBWIGoYBM5M",
		"https://music.apple.com/us/playlist/foo/pl.abc",
		"short",
	}
	for _, in := range bad {
		if got, err := ParsePlaylistRef(in); err == nil {
			t.Errorf("ParsePlaylistRef(%q) = %q, want an error", in, got)
		}
	}
}

func TestAuthorizeURLCarriesPKCEAndMinimalScopes(t *testing.T) {
	_, c := newStub(t, func(http.ResponseWriter, *http.Request) {})
	p, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	raw := c.AuthorizeURL(testApp, p)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()

	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("challenge method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") != p.Challenge {
		t.Error("the challenge in the URL is not the one we generated")
	}
	// The verifier must never leave Waxgrove; that is the whole point of PKCE.
	if strings.Contains(raw, p.Verifier) {
		t.Error("the PKCE verifier leaked into the authorize URL")
	}
	if q.Get("state") == "" {
		t.Error("no state parameter: the callback would be forgeable")
	}
	for _, unwanted := range []string{"user-read-email", "user-library-modify", "streaming"} {
		if strings.Contains(q.Get("scope"), unwanted) {
			t.Errorf("asking for %q, which no feature needs", unwanted)
		}
	}
}

func TestNewPKCEIsUnpredictable(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		p, err := NewPKCE()
		if err != nil {
			t.Fatalf("NewPKCE: %v", err)
		}
		if seen[p.Verifier] || seen[p.State] {
			t.Fatal("NewPKCE repeated a value")
		}
		seen[p.Verifier], seen[p.State] = true, true
		if len(p.Verifier) < 43 { // RFC 7636 floor
			t.Errorf("verifier is %d chars, below the RFC 7636 minimum of 43", len(p.Verifier))
		}
	}
}

func TestExchangeReturnsTokens(t *testing.T) {
	s, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "AT", "refresh_token": "RT",
			"expires_in": 3600, "scope": ScopeString(), "token_type": "Bearer",
		})
	})
	p, _ := NewPKCE()
	tok, err := c.Exchange(context.Background(), testApp, p, "code123")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "AT" || tok.RefreshToken != "RT" {
		t.Errorf("got %+v", tok)
	}
	if tok.Expired() {
		t.Error("a token valid for an hour reports as expired")
	}
	// The verifier must be sent, or PKCE is theatre.
	if !strings.Contains(s.body(0), "code_verifier="+p.Verifier) {
		t.Errorf("the code_verifier was not sent: %s", s.body(0))
	}
}

// A revoked grant must be distinguishable from a transient failure, because
// the response to it is "ask the user to reconnect", not "retry".
func TestExchangeInvalidGrantAsksForReconnect(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "invalid_grant", "error_description": "code expired",
		})
	})
	p, _ := NewPKCE()
	_, err := c.Exchange(context.Background(), testApp, p, "stale")
	if !errors.Is(err, ErrNeedsReconnect) {
		t.Fatalf("got %v, want ErrNeedsReconnect", err)
	}
}

// Spotify does not always return a new refresh token. Overwriting the stored
// one with an empty string would disconnect the user at the next expiry.
func TestRefreshKeepsTheOldRefreshTokenWhenNoneComesBack(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "AT2", "expires_in": 3600,
		})
	})
	tok, err := c.Refresh(context.Background(), testApp, "ORIGINAL")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.RefreshToken != "ORIGINAL" {
		t.Errorf("refresh token = %q, want the original to be kept", tok.RefreshToken)
	}
}

func TestTokenExpiryHasSkew(t *testing.T) {
	// Valid for another 30s: still "expired", because a long export must not
	// have a token die mid-batch.
	soon := Token{AccessToken: "x", Expiry: time.Now().Add(30 * time.Second)}
	if !soon.Expired() {
		t.Error("a token expiring in 30s should be refreshed early")
	}
	fine := Token{AccessToken: "x", Expiry: time.Now().Add(10 * time.Minute)}
	if fine.Expired() {
		t.Error("a token with 10 minutes left was treated as expired")
	}
	if !(Token{}).Expired() {
		t.Error("an empty token must count as expired")
	}
}

func TestPlaylistTracksPaginatesAndSkipsUnplayable(t *testing.T) {
	var serverURL string
	page := 0
	s, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"next": serverURL + "/page2",
				"items": []any{
					map[string]any{"track": map[string]any{
						"id": "1", "name": "Pink Moon", "duration_ms": 128000,
						"artists":      []any{map[string]any{"name": "Nick Drake"}},
						"album":        map[string]any{"name": "Pink Moon", "release_date": "1972-02-25"},
						"external_ids": map[string]any{"isrc": "gbaye0601498"},
					}},
					// A removed track arrives as null; one of these must not
					// fail the whole import.
					map[string]any{"track": nil},
					// A local file exists only on that user's machine.
					map[string]any{"track": map[string]any{
						"id": "2", "name": "demo.mp3", "is_local": true,
					}},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"next": "",
			"items": []any{
				map[string]any{"track": map[string]any{
					"id": "3", "name": "Dreams",
					"artists":      []any{map[string]any{"name": "Fleetwood Mac"}},
					"external_ids": map[string]any{"isrc": "USWB10101368"},
				}},
			},
		})
	})
	serverURL = s.URL

	got, err := c.PlaylistTracks(context.Background(), testApp, testTok, "pl1")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (null and local skipped): %+v", len(got), got)
	}
	if got[0].ISRC != "GBAYE0601498" {
		t.Errorf("ISRC = %q, want it upper-cased for set membership", got[0].ISRC)
	}
	if got[0].Year != 1972 {
		t.Errorf("year = %d, want 1972 from the release date", got[0].Year)
	}
	if got[0].SourceRef != "spotify:1" {
		t.Errorf("SourceRef = %q, want the provider id preserved", got[0].SourceRef)
	}
	if got[1].Title != "Dreams" {
		t.Errorf("second page was not followed: %+v", got[1])
	}
	if s.count() != 2 {
		t.Errorf("made %d requests, want 2 pages", s.count())
	}
}

// Multiple artists are one credit string, matching how the catalogue stores it.
func TestMultipleArtistsBecomeOneCredit(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"next": "",
			"items": []any{map[string]any{"track": map[string]any{
				"id": "1", "name": "Under Pressure",
				"artists": []any{
					map[string]any{"name": "Queen"},
					map[string]any{"name": "David Bowie"},
				},
			}}},
		})
	})
	got, err := c.PlaylistTracks(context.Background(), testApp, testTok, "pl")
	if err != nil {
		t.Fatalf("PlaylistTracks: %v", err)
	}
	if got[0].Artist != "Queen, David Bowie" {
		t.Errorf("artist = %q", got[0].Artist)
	}
}

func TestFindByISRCUsesTheISRCQueryForm(t *testing.T) {
	s, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tracks": map[string]any{"items": []any{
				map[string]any{"uri": "spotify:track:abc"},
			}},
		})
	})
	uri, err := c.FindByISRC(context.Background(), testApp, testTok, "GBAYE0601498", "GB")
	if err != nil {
		t.Fatalf("FindByISRC: %v", err)
	}
	if uri != "spotify:track:abc" {
		t.Errorf("uri = %q", uri)
	}
	q := s.req(0).URL.Query()
	if q.Get("q") != "isrc:GBAYE0601498" {
		t.Errorf("query = %q, want the isrc: form", q.Get("q"))
	}
	// Availability is regional; resolving without a market yields ids that may
	// not play for this user (§3.6).
	if q.Get("market") != "GB" {
		t.Errorf("market = %q, want it passed through", q.Get("market"))
	}
}

func TestNoMatchIsAnExpectedOutcome(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tracks": map[string]any{"items": []any{}},
		})
	})
	if _, err := c.FindByISRC(context.Background(), testApp, testTok, "ZZ", "US"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("got %v, want ErrNoMatch", err)
	}
	if _, err := c.FindByText(context.Background(), testApp, testTok, "a", "b", "US"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("got %v, want ErrNoMatch", err)
	}
}

// A title with quotes or a colon must search, not corrupt the query grammar.
func TestTextSearchEscapesQuerySyntax(t *testing.T) {
	s, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tracks": map[string]any{"items": []any{map[string]any{"uri": "spotify:track:x"}}},
		})
	})
	if _, err := c.FindByText(context.Background(), testApp, testTok,
		`Guns N" Roses`, `Welcome: to the "Jungle"`, "US"); err != nil {
		t.Fatalf("FindByText: %v", err)
	}
	q := s.req(0).URL.Query().Get("q")
	// Exactly the four quotes this code adds, none from the user's text.
	if n := strings.Count(q, `"`); n != 4 {
		t.Errorf("query has %d quotes, want 4 (user quotes should be stripped): %s", n, q)
	}
	if strings.Contains(q, "Welcome:") {
		t.Errorf("a colon from the title survived into the query grammar: %s", q)
	}
}

func TestAddTracksBatchesAtOneHundred(t *testing.T) {
	s, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"snapshot_id":"s"}`))
	})
	uris := make([]string, 250)
	for i := range uris {
		uris[i] = "spotify:track:" + string(rune('a'+i%26))
	}

	added, err := c.AddTracks(context.Background(), testApp, testTok, "pl", uris)
	if err != nil {
		t.Fatalf("AddTracks: %v", err)
	}
	if added != 250 {
		t.Errorf("added %d, want 250", added)
	}
	if s.count() != 3 {
		t.Fatalf("made %d requests, want 3 batches of <=100", s.count())
	}
	var first struct {
		URIs []string `json:"uris"`
	}
	if err := json.Unmarshal([]byte(s.body(0)), &first); err != nil {
		t.Fatalf("first batch body: %v", err)
	}
	if len(first.URIs) != AddTracksBatchSize {
		t.Errorf("first batch had %d uris, want %d", len(first.URIs), AddTracksBatchSize)
	}
}

// A batch failing partway through must report what actually landed. Reporting
// zero would make a resume duplicate everything already added.
func TestAddTracksReportsPartialProgress(t *testing.T) {
	n := 0
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Header().Set("Retry-After", "7")
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	uris := make([]string, 250)
	for i := range uris {
		uris[i] = "spotify:track:x"
	}
	added, err := c.AddTracks(context.Background(), testApp, testTok, "pl", uris)
	if err == nil {
		t.Fatal("want an error from the throttled batch")
	}
	if added != 200 {
		t.Errorf("reported %d added, want the 200 that actually landed", added)
	}
	if _, ok := RateLimit(err); !ok {
		t.Errorf("error %v is not recognisable as a rate limit", err)
	}
}

func TestRateLimitSurfacesRetryAfter(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.GetPlaylist(context.Background(), testApp, testTok, "pl")
	d, ok := RateLimit(err)
	if !ok {
		t.Fatalf("got %v, want a rate-limit error", err)
	}
	if d != 12*time.Second {
		t.Errorf("retry after %v, want 12s from the header", d)
	}
}

// A missing or zero Retry-After must not become a hot retry loop.
func TestRateLimitHasAFloor(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.GetPlaylist(context.Background(), testApp, testTok, "pl")
	d, ok := RateLimit(err)
	if !ok {
		t.Fatalf("got %v", err)
	}
	if d < time.Second {
		t.Errorf("retry after %v, too short to be a real back-off", d)
	}
}

func TestUnauthorizedMeansReconnect(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.GetPlaylist(context.Background(), testApp, testTok, "pl")
	if !IsAuthError(err) {
		t.Errorf("got %v, want an auth error the UI can act on", err)
	}
}

func TestMissingPlaylistIsDistinguishable(t *testing.T) {
	_, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"status":404,"message":"Not found."}}`))
	})
	_, err := c.GetPlaylist(context.Background(), testApp, testTok, "gone")
	var st *ErrStatus
	if !errors.As(err, &st) || !st.NotFound() {
		t.Fatalf("got %v, want a recognisable 404", err)
	}
}

// Every authenticated call must carry the bearer token; one that forgets would
// fail confusingly rather than obviously.
func TestCallsAreAuthenticated(t *testing.T) {
	s, c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","country":"US"}`))
	})
	if _, _, err := c.Me(context.Background(), testApp, testTok); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got := s.req(0).Header.Get("Authorization"); got != "Bearer at" {
		t.Errorf("Authorization = %q", got)
	}
}
