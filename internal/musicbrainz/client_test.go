package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func stub(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test@example.test", "test", WithHTTPClient(srv.Client()), WithBurst(100))
	return c, srv
}

// MusicBrainz blocks requests without a meaningful User-Agent, so this is not
// cosmetic — getting it wrong means every call fails in production.
func TestSendsUserAgent(t *testing.T) {
	var got string
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"recordings":[]}`))
	})
	_ = c.get(context.Background(), srv.URL, &struct{}{})
	if got == "" || got == "Go-http-client/1.1" {
		t.Fatalf("User-Agent = %q, want a Waxgrove identifier", got)
	}
}

// One request must return a whole album with every ISRC — the property the
// album-shaped fetching in §3.6 depends on.
func TestReleaseTracksParsesWholeAlbum(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
		  "title":"Rumours","date":"1977-02-04",
		  "media":[{"tracks":[
		    {"position":1,"recording":{"id":"aaa","title":"Second Hand News","length":175000,
		      "isrcs":["USWB10101367"],"artist-credit":[{"name":"Fleetwood Mac"}]}},
		    {"position":2,"recording":{"id":"bbb","title":"Dreams","length":254000,
		      "isrcs":["USWB10101368","USWB19900178"],"artist-credit":[{"name":"Fleetwood Mac"}]}}
		  ]}]}`))
	})
	old := apiBaseForTest
	apiBaseForTest = srv.URL
	defer func() { apiBaseForTest = old }()

	cands, err := c.releaseTracksAt(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d tracks, want 2", len(cands))
	}
	if cands[1].Title != "Dreams" || cands[1].ISRC != "USWB10101368" {
		t.Errorf("track 2 = %+v", cands[1])
	}
	if cands[0].Album != "Rumours" || cands[0].Year != 1977 {
		t.Errorf("album/year not applied: %+v", cands[0])
	}
	if cands[0].Artist != "Fleetwood Mac" {
		t.Errorf("artist credit not joined: %q", cands[0].Artist)
	}
}

// The limiter is the only thing standing between this app and a ban.
func TestRateLimiterThrottles(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	c2 := New("test@example.test", "test", WithHTTPClient(srv.Client()), WithBurst(1))

	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := c2.get(ctx, srv.URL, &struct{}{}); err != nil {
			t.Fatalf("get %d: %v", i, err)
		}
	}
	// burst=1 at 1/sec means three calls take at least ~2s.
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Errorf("3 requests took %v — limiter is not throttling", elapsed)
	}
	_ = c
}

// A cancelled context must abort the wait rather than blocking on the limiter.
func TestRateLimiterRespectsContext(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	slow := New("t@e.test", "t", WithHTTPClient(srv.Client()), WithBurst(1))
	_ = slow.get(context.Background(), srv.URL, &struct{}{}) // drain the burst

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := slow.get(ctx, srv.URL, &struct{}{}); err == nil {
		t.Fatal("expected context deadline, got nil")
	}
	_ = c
}

func TestThrottleResponseIsAnError(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := c.get(context.Background(), srv.URL, &struct{}{}); err == nil {
		t.Fatal("503 was not surfaced as an error")
	}
}
