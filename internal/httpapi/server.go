// Package httpapi wires the HTTP surface. stdlib net/http only — N1 rules out
// a heavy framework.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger is the subset of the store the health check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Server struct {
	store Pinger
	env   string
}

func New(store Pinger, env string) *Server {
	return &Server{store: store, env: env}
}

// Routes builds the mux. Security headers are applied to everything.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
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
		h.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
