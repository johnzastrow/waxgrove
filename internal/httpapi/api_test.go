package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/resolve"
)

type client struct {
	t       *testing.T
	h       http.Handler
	cookies []*http.Cookie
}

func newAPI(t *testing.T) *client {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Remote deliberately nil: N6 requires the API to work with no connector.
	api := &API{Store: store, Resolver: resolve.New(store.Records(), nil), Env: "test"}
	return &client{t: t, h: New(store, "test").WithAPI(api).Routes()}
}

func (c *client) do(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	if cs := rec.Result().Cookies(); len(cs) > 0 {
		c.cookies = cs
	}
	return rec
}

func (c *client) mustJSON(rec *httptest.ResponseRecorder) map[string]any {
	c.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		c.t.Fatalf("bad JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return m
}

const goodPassword = "correct-horse-battery-staple"

// The whole M1 loop, with no streaming connector attached anywhere.
func TestEndToEndPlaylistFlow(t *testing.T) {
	c := newAPI(t)

	// First user bootstraps as admin — nobody exists to invite them.
	rec := c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if c.mustJSON(rec)["role"] != "admin" {
		t.Error("first user did not become admin")
	}

	// Create a playlist.
	rec = c.do("POST", "/api/playlists", map[string]any{"title": "Porch, July"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create playlist: %d %s", rec.Code, rec.Body)
	}
	pid := c.mustJSON(rec)["id"].(string)

	// Add three identity-bearing tracks plus one that cannot resolve.
	rec = c.do("POST", "/api/playlists/"+pid+"/tracks", map[string]any{
		"candidates": []map[string]any{
			{"Title": "Pink Moon", "Artist": "Nick Drake", "ISRC": "GBAYE0601498"},
			{"Title": "Northern Sky", "Artist": "Nick Drake", "ISRC": "GBAYE0601499"},
			{"Title": "Dreams", "Artist": "Fleetwood Mac", "ISRC": "USWB10101368"},
			{"Title": "jonny", "Artist": "sparklehorse??", "Raw": "jonny — sparklehorse??"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("add tracks: %d %s", rec.Code, rec.Body)
	}
	body := c.mustJSON(rec)
	if got := body["added"].(float64); got != 3 {
		t.Errorf("added %v tracks, want 3", got)
	}
	// The unresolvable one is REPORTED, never silently dropped (BR-5).
	un := body["unresolved"].([]any)
	if len(un) != 1 {
		t.Fatalf("unresolved = %d, want 1 — an unmatched track was dropped", len(un))
	}

	// One revision for the whole add, plus the create (§3.3).
	rec = c.do("GET", "/api/playlists/"+pid+"/history", nil)
	revs := c.mustJSON(rec)["revisions"].([]any)
	if len(revs) != 2 {
		t.Errorf("history has %d revisions, want 2 (create + one add)", len(revs))
	}

	// Export to JSPF.
	rec = c.do("GET", "/api/playlists/"+pid+"/export.jspf", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d", rec.Code)
	}
	exported := rec.Body.String()
	if !strings.Contains(exported, "GBAYE0601498") {
		t.Error("export lost the ISRC")
	}

	// Re-import it: everything must resolve against the now-warm catalog.
	req := httptest.NewRequest("POST", "/api/playlists/import", strings.NewReader(exported))
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec2 := httptest.NewRecorder()
	c.h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec2.Code, rec2.Body)
	}
	imp := c.mustJSON(rec2)
	if got := imp["imported"].(float64); got != 3 {
		t.Errorf("re-import resolved %v of 3 tracks", got)
	}
}

// Every non-public route must reject an anonymous caller.
func TestUnauthenticatedIsRejected(t *testing.T) {
	c := newAPI(t)
	for _, r := range []struct{ m, p string }{
		{"GET", "/api/me"}, {"GET", "/api/playlists"}, {"POST", "/api/playlists"},
		{"GET", "/api/records?q=x"}, {"POST", "/api/invites"},
		{"POST", "/api/playlists/abc/tracks"}, {"GET", "/api/playlists/abc/export.jspf"},
	} {
		rec := c.do(r.m, r.p, map[string]any{})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", r.m, r.p, rec.Code)
		}
	}
}

// Registration is invite-only after the first account (§6).
func TestSecondUserNeedsAnInvite(t *testing.T) {
	c := newAPI(t)
	c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword})

	fresh := &client{t: t, h: c.h}
	rec := fresh.do("POST", "/api/register", map[string]any{
		"email": "ben@example.test", "display_name": "Ben", "password": goodPassword})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("open registration allowed: %d %s", rec.Code, rec.Body)
	}

	// With a valid invite it works.
	inv := c.mustJSON(c.do("POST", "/api/invites", nil))["code"].(string)
	rec = fresh.do("POST", "/api/register", map[string]any{
		"email": "ben@example.test", "display_name": "Ben",
		"password": goodPassword, "invite_code": inv})
	if rec.Code != http.StatusOK {
		t.Fatalf("invited registration failed: %d %s", rec.Code, rec.Body)
	}
	// And the invite is single-use.
	third := &client{t: t, h: c.h}
	rec = third.do("POST", "/api/register", map[string]any{
		"email": "cal@example.test", "display_name": "Cal",
		"password": goodPassword, "invite_code": inv})
	if rec.Code != http.StatusForbidden {
		t.Errorf("invite was reusable: %d", rec.Code)
	}
}

// A non-owner gets 404, not 403 — the response must not confirm the playlist
// exists (§6).
func TestNonOwnerCannotModifyAndGets404(t *testing.T) {
	ana := newAPI(t)
	ana.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword})
	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "Private"}))["id"].(string)

	inv := ana.mustJSON(ana.do("POST", "/api/invites", nil))["code"].(string)
	ben := &client{t: t, h: ana.h}
	ben.do("POST", "/api/register", map[string]any{
		"email": "ben@example.test", "display_name": "Ben",
		"password": goodPassword, "invite_code": inv})

	if rec := ben.do("DELETE", "/api/playlists/"+pid, nil); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner delete = %d, want 404", rec.Code)
	}
	// But sharing by reference means he can still READ it (D8).
	if rec := ben.do("GET", "/api/playlists/"+pid, nil); rec.Code != http.StatusOK {
		t.Errorf("non-owner read = %d, want 200 (playlists are shared by reference)", rec.Code)
	}
}

// Weak passwords are refused at the boundary, not deep in the stack.
func TestWeakPasswordRejected(t *testing.T) {
	c := newAPI(t)
	rec := c.do("POST", "/api/register", map[string]any{
		"email": "a@example.test", "display_name": "A", "password": "short"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password accepted: %d", rec.Code)
	}
}

// Login must not reveal whether an address is registered.
func TestLoginIsUniformOnFailure(t *testing.T) {
	c := newAPI(t)
	c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword})

	unknown := c.do("POST", "/api/login", map[string]any{
		"email": "nobody@example.test", "password": goodPassword})
	wrongPw := c.do("POST", "/api/login", map[string]any{
		"email": "ana@example.test", "password": "wrong-password-here"})

	if unknown.Code != wrongPw.Code || unknown.Body.String() != wrongPw.Body.String() {
		t.Errorf("responses differ: unknown=%d %s / wrong=%d %s",
			unknown.Code, unknown.Body, wrongPw.Code, wrongPw.Body)
	}
}

// The session cookie must not be readable from JavaScript.
func TestSessionCookieIsHardened(t *testing.T) {
	c := newAPI(t)
	rec := c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword})
	for _, ck := range rec.Result().Cookies() {
		if ck.Name != sessionCookie {
			continue
		}
		if !ck.HttpOnly {
			t.Error("session cookie is not HttpOnly")
		}
		if ck.SameSite != http.SameSiteLaxMode {
			t.Error("session cookie has no SameSite protection")
		}
		return
	}
	t.Fatal("no session cookie issued")
}

// Remote search absent is a normal state, not an error (N6).
func TestRemoteSearchDegradesWhenUnconfigured(t *testing.T) {
	c := newAPI(t)
	c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword})
	rec := c.do("GET", "/api/records/remote?q=drake", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("unconfigured remote search = %d, want 200", rec.Code)
	}
}
