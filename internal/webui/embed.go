// Package webui serves the compiled PWA out of the binary.
//
// The build output in dist/ is committed. That is a deliberate trade: it costs
// a little repo churn and buys `go build ./...`, `go test ./...` and `go
// install` working in a checkout with no Node installed at all (N2 — one
// artifact, nothing external required to boot). The Docker build regenerates
// dist/ from app/ so a release never ships whatever happened to be committed;
// CI checks the two agree.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// all: is required — without it, embed skips files beginning with "_", which
// is exactly what Vite names some of its chunks.
//
//go:embed all:dist
var embedded embed.FS

// Assets is the built PWA rooted at dist/.
func Assets() (fs.FS, error) { return fs.Sub(embedded, "dist") }

// Built reports whether a real build is embedded rather than the placeholder
// that keeps `go build` working before anyone has run the frontend build.
func Built() bool {
	_, err := fs.Stat(embedded, "dist/assets")
	return err == nil
}

// Handler serves the PWA with an SPA fallback.
//
// A request for a path that is not a file is answered with index.html so
// client-side routes survive a refresh or a shared link. Two paths are
// deliberately excluded from that fallback: anything that looks like an API
// call, and the service worker itself — answering either with HTML would turn
// a clean 404 into a confusing parse error.
func Handler() (http.Handler, error) {
	assets, err := Assets()
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	files := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := path.Clean("/" + r.URL.Path)

		// Never let the SPA fallback shadow the API surface.
		if strings.HasPrefix(upath, "/api/") || upath == "/health" {
			http.NotFound(w, r)
			return
		}

		if name := strings.TrimPrefix(upath, "/"); name != "" {
			if st, serr := fs.Stat(assets, name); serr == nil && !st.IsDir() {
				setCaching(w, name)
				setContentType(w, name)
				files.ServeHTTP(w, r)
				return
			} else if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			// A missing file with an extension is a genuine 404, not a route:
			// serving index.html for /assets/missing.js would mask the bug.
			if ext := path.Ext(name); ext != "" && ext != ".html" {
				http.NotFound(w, r)
				return
			}
		}

		// The shell must never be cached, or a deploy leaves clients pinned to
		// asset URLs that no longer exist.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}), nil
}

// setCaching marks Vite's content-hashed assets immutable and leaves
// everything else revalidating. The hash is in the filename, so a changed file
// is a changed URL.
func setCaching(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	// sw.js and the manifest must be re-checked, or updates never arrive.
	w.Header().Set("Cache-Control", "no-cache")
}

// setContentType fills the gaps in Go's MIME table.
//
// .webmanifest is not in it, so the manifest would otherwise go out as
// text/plain and browsers would reject it — the app stays usable but silently
// stops being installable, which is the kind of bug nobody notices for months.
func setContentType(w http.ResponseWriter, name string) {
	switch path.Ext(name) {
	case ".webmanifest":
		w.Header().Set("Content-Type", "application/manifest+json")
	}
}
