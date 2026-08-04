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
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy missing")
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
