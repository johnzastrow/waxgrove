package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func handler(t *testing.T) http.Handler {
	t.Helper()
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The embed must contain a real app, not just the placeholder — a release that
// shipped an unbuilt frontend would still start and still serve pages, which is
// exactly the failure worth catching in CI rather than in a browser.
func TestBuiltAppIsEmbedded(t *testing.T) {
	if !Built() {
		t.Fatal("no built frontend embedded: run `make web`")
	}
	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	for _, want := range []string{"index.html", "manifest.webmanifest", "sw.js"} {
		if _, err := fs.Stat(assets, want); err != nil {
			t.Errorf("missing %s from the build: %v", want, err)
		}
	}
}

func TestServesIndex(t *testing.T) {
	rec := get(t, handler(t), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), `id="root"`) {
		t.Error("index.html does not look like the app shell")
	}
}

// A refresh on a client-side route must not 404, or every shared link breaks.
func TestSPAFallback(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/playlists", "/playlists/abc-123", "/settings", "/nope"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (SPA fallback)", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("GET %s did not return the app shell", path)
		}
	}
}

// The fallback must never answer an API path. Serving HTML there would turn a
// clean 404 into a JSON parse error in the client, which is far harder to read.
func TestAPIPathsAreNotSwallowed(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/api/records", "/api/playlists/x", "/health"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("GET %s returned the app shell", path)
		}
	}
}

// A missing asset is a bug, and must look like one.
func TestMissingAssetIs404(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/assets/nope.js", "/icons/missing.png", "/sw-not-real.js"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestRealAssetsAreServed(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	// Find one hashed asset from the actual build rather than hardcoding a
	// filename that changes on every rebuild.
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		t.Fatalf("read assets dir: %v", err)
	}
	var name string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			name = e.Name()
			break
		}
	}
	if name == "" {
		t.Fatal("the build produced no JavaScript")
	}

	rec := get(t, handler(t), "/assets/"+name)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/%s = %d, want 200", name, rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable for a content-hashed asset", cc)
	}
}

// The service worker and the shell must both revalidate, or a deploy strands
// clients on asset URLs that no longer exist.
func TestShellAndWorkerAreNotCachedHard(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/", "/sw.js", "/manifest.webmanifest"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, cc)
		}
	}
}

// Browsers reject a manifest served as text/plain, and Go's MIME table has no
// entry for .webmanifest — so the app would quietly stop being installable.
func TestManifestContentType(t *testing.T) {
	rec := get(t, handler(t), "/manifest.webmanifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /manifest.webmanifest = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}
}

// The service worker must be served as JavaScript or the browser refuses to
// register it.
func TestServiceWorkerContentType(t *testing.T) {
	rec := get(t, handler(t), "/sw.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sw.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want a JavaScript type", ct)
	}
}

// Path traversal must not escape the embedded filesystem.
func TestTraversalIsContained(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/../embed.go", "/assets/../../embed.go", "/%2e%2e/embed.go"} {
		rec := get(t, h, path)
		if strings.Contains(rec.Body.String(), "package webui") {
			t.Errorf("GET %s escaped the embedded FS", path)
		}
	}
}
