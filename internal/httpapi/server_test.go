package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubStore struct{ err error }

func (s stubStore) Ping(context.Context) error { return s.err }

func TestHealthReportsHealthy(t *testing.T) {
	srv := New(stubStore{}, "development")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "healthy" {
		t.Errorf("status = %q, want healthy", body.Status)
	}
}

// A failing database must surface as 503, and the client must not receive the
// underlying error text (§6: generic messages out, detail stays in the logs).
func TestHealthHidesDatabaseErrorDetail(t *testing.T) {
	srv := New(stubStore{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")}, "production")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); contains(body, "connection refused") || contains(body, "10.0.0.5") {
		t.Errorf("internal error detail leaked to client: %s", body)
	}
}

func TestSecurityHeadersApplied(t *testing.T) {
	srv := New(stubStore{}, "production")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy missing")
	}
	// The PWA is built so none of these are needed. If one ever appears here it
	// means the frontend regressed into inline script, inline style, or a CDN.
	for _, forbidden := range []string{"unsafe-inline", "unsafe-eval", "https://", "data:"} {
		if contains(csp, forbidden) {
			t.Errorf("CSP has been loosened with %q: %s", forbidden, csp)
		}
	}
	for _, required := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'none'"} {
		if !contains(csp, required) {
			t.Errorf("CSP missing %q: %s", required, csp)
		}
	}
}

// The PWA is mounted at "/" and must not shadow anything registered before it.
// net/http's mux gives "/" the lowest precedence, so this is really a guard
// against someone later mounting the web UI in a way that does.
func TestWebUIDoesNotShadowAPIRoutes(t *testing.T) {
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // unmistakable if it wins
	})
	srv := New(stubStore{}, "development").WithWebUI(web)

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200: the web UI swallowed an API route", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything-else", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("GET /anything-else = %d, want the web UI to handle it", rec.Code)
	}
}

// Without WithWebUI the server must still be a complete API. Tests and any
// API-only deployment depend on that.
func TestWebUIIsOptional(t *testing.T) {
	srv := New(stubStore{}, "development")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET / with no web UI = %d, want 404", rec.Code)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
