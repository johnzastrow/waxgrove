// Package httpapi wires the HTTP surface. stdlib net/http only — N1 rules out
// a heavy framework.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Pinger is the subset of the store the health check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Server struct {
	store Pinger
	env   string
	api   *API         // nil in tests that only exercise /health
	web   http.Handler // nil when the PWA is not being served
}

func New(store Pinger, env string) *Server {
	return &Server{store: store, env: env}
}

// WithAPI attaches the application routes.
func (s *Server) WithAPI(a *API) *Server { s.api = a; return s }

// WithWebUI attaches the embedded PWA at the root. Optional: the API is a
// complete, usable surface on its own, and tests that only exercise it should
// not have to carry the frontend.
func (s *Server) WithWebUI(h http.Handler) *Server { s.web = h; return s }

// Routes builds the mux. Security headers are applied to everything.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	if s.api != nil {
		s.api.Mount(mux)
	}
	if s.web != nil {
		// "/" is the lowest-precedence pattern in net/http's mux, so every
		// route registered above still wins. The PWA only sees what is left.
		mux.Handle("/", s.web)
	}
	return securityHeaders(mux)
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Version is stamped at build time via -ldflags.
var Version = "dev"

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	status := "healthy"
	code := http.StatusOK
	if err := s.store.Ping(ctx); err != nil {
		// §6: generic message to the client, detail stays in the logs.
		status = "unhealthy"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, healthResponse{Status: status, Version: Version})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// securityHeaders applies the §6 baseline to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The PWA is built to satisfy this as written: no inline scripts or
		// styles, no data: URIs (vite.config.ts sets assetsInlineLimit to 0),
		// fonts bundled rather than pulled from a CDN, and every request it
		// makes is same-origin. Anything that needs this loosened is a change
		// to the frontend, not to this header.
		h.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"object-src 'none'",
			"frame-ancestors 'none'",
			"base-uri 'none'",
			"form-action 'self'",
		}, "; "))
		next.ServeHTTP(w, r)
	})
}
