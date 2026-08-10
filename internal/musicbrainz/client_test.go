package musicbrainz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func stub(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test@example.test", "test", WithHTTPClient(srv.Client()), WithBurst(100),
		WithBaseURLs(srv.URL, srv.URL))
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

// --- response parsing -------------------------------------------------------
// These lock down the shapes the resolution ladder depends on. A silent parse
// change would degrade matching without failing anything else.

func TestRecordingResponseMapsToCandidate(t *testing.T) {
	r := recordingResponse{
		ID: "248cc9d1", Title: "Dreams", Length: 254000,
		ISRCs: []string{"USWB10101368", "USWB19900178"},
	}
	r.ArtistCredit = append(r.ArtistCredit, struct {
		Name string `json:"name"`
	}{Name: "Fleetwood Mac"})
	r.Releases = append(r.Releases, struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Date  string `json:"date"`
	}{Title: "Rumours", Date: "1977-02-04"})

	c := r.toCandidate()
	if c.MBID != "248cc9d1" || c.Title != "Dreams" {
		t.Errorf("identity lost: %+v", c)
	}
	if c.Artist != "Fleetwood Mac" {
		t.Errorf("artist = %q", c.Artist)
	}
	if c.ISRC != "USWB10101368" {
		t.Errorf("ISRC = %q, want the first of the set", c.ISRC)
	}
	if c.Album != "Rumours" || c.Year != 1977 {
		t.Errorf("release info = %q/%d", c.Album, c.Year)
	}
	if c.DurationMS != 254000 {
		t.Errorf("duration = %d", c.DurationMS)
	}
}

// Collaborations produce several artist-credit entries that must be joined,
// not silently truncated to the first.
func TestMultipleArtistCreditsAreJoined(t *testing.T) {
	var r recordingResponse
	for _, n := range []string{"Nick Drake", "Robert Kirby"} {
		r.ArtistCredit = append(r.ArtistCredit, struct {
			Name string `json:"name"`
		}{Name: n})
	}
	if got := r.toCandidate().Artist; got != "Nick Drake, Robert Kirby" {
		t.Errorf("artist = %q", got)
	}
}

func TestCandidateSurvivesSparseResponse(t *testing.T) {
	c := recordingResponse{ID: "x", Title: "t"}.toCandidate()
	if c.MBID != "x" || c.Title != "t" {
		t.Errorf("sparse response mangled: %+v", c)
	}
	if c.ISRC != "" || c.Album != "" || c.Year != 0 {
		t.Errorf("sparse response invented data: %+v", c)
	}
}

func TestLookupISRCKeepsTheQueriedCode(t *testing.T) {
	// MusicBrainz returns the whole ISRC set; the code we asked about is the
	// authoritative one for this lookup.
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"recordings":[{"id":"abc","title":"Dreams",
		  "isrcs":["USWB19900178","USWB10101368"],
		  "artist-credit":[{"name":"Fleetwood Mac"}]}]}`))
	})
	var out struct {
		Recordings []recordingResponse `json:"recordings"`
	}
	if err := c.get(context.Background(), srv.URL, &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(out.Recordings) != 1 || len(out.Recordings[0].ISRCs) != 2 {
		t.Fatalf("parse: %+v", out)
	}
}

func TestNotFoundIsDistinguishable(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if err := c.get(context.Background(), srv.URL, &struct{}{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 err = %v, want ErrNotFound", err)
	}
}

func TestServerErrorIsSurfaced(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := c.get(context.Background(), srv.URL, &struct{}{}); err == nil {
		t.Error("500 was swallowed")
	}
}

func TestMalformedJSONIsAnError(t *testing.T) {
	c, srv := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	})
	if err := c.get(context.Background(), srv.URL, &struct{}{}); err == nil {
		t.Error("malformed JSON was accepted")
	}
}

// --- network-facing methods, against a stub ---------------------------------

func TestLookupISRCEndToEnd(t *testing.T) {
	var gotPath string
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`{"recordings":[{"id":"248cc9d1","title":"Dreams",
		  "length":254000,"isrcs":["USWB19900178"],
		  "artist-credit":[{"name":"Fleetwood Mac"}]}]}`))
	})
	got, err := c.LookupISRC(context.Background(), "USWB10101368")
	if err != nil {
		t.Fatalf("LookupISRC: %v", err)
	}
	// The queried code wins over whatever the response happened to list first —
	// otherwise the caller's ISRC would be silently swapped for another.
	if got.ISRC != "USWB10101368" {
		t.Errorf("ISRC = %q, want the queried code", got.ISRC)
	}
	if got.MBID != "248cc9d1" || got.Artist != "Fleetwood Mac" {
		t.Errorf("candidate = %+v", got)
	}
	if !strings.Contains(gotPath, "USWB10101368") {
		t.Errorf("request path did not carry the ISRC: %s", gotPath)
	}
}

func TestLookupISRCNotFound(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"recordings":[]}`))
	})
	if _, err := c.LookupISRC(context.Background(), "XX00000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLookupRecordingReturnsFullISRCSet(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"248cc9d1","title":"Dreams","length":254000,
		  "isrcs":["USWB10101368","USWB19900178"],
		  "artist-credit":[{"name":"Fleetwood Mac"}]}`))
	})
	got, err := c.LookupRecording(context.Background(), "248cc9d1")
	if err != nil {
		t.Fatalf("LookupRecording: %v", err)
	}
	if got.MBID != "248cc9d1" || got.DurationMS != 254000 {
		t.Errorf("candidate = %+v", got)
	}
}

func TestSearchParsesResults(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "5" {
			t.Errorf("limit not passed through: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"recordings":[
		  {"id":"a","title":"Road","artist-credit":[{"name":"Nick Drake"}]},
		  {"id":"b","title":"Pink Moon","artist-credit":[{"name":"Nick Drake"}]}]}`))
	})
	got, err := c.Search(context.Background(), "nick drake", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 || got[1].Title != "Pink Moon" {
		t.Errorf("results = %+v", got)
	}
}

func TestSearchClampsLimit(t *testing.T) {
	var seen string
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(`{"recordings":[]}`))
	})
	// An absurd limit must not be forwarded to a third party.
	if _, err := c.Search(context.Background(), "x", 100000); err != nil {
		t.Fatal(err)
	}
	if seen == "100000" {
		t.Errorf("limit forwarded unclamped: %s", seen)
	}
}

func TestReleaseTracksFetchesWholeAlbum(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Pink Moon","date":"1972",
		  "media":[{"tracks":[
		    {"position":1,"recording":{"id":"a","title":"Pink Moon","isrcs":["GB1"],
		      "artist-credit":[{"name":"Nick Drake"}]}},
		    {"position":2,"recording":{"id":"b","title":"Road","isrcs":["GB2"],
		      "artist-credit":[{"name":"Nick Drake"}]}}]}]}`))
	})
	got, err := c.ReleaseTracks(context.Background(), "rel-1")
	if err != nil {
		t.Fatalf("ReleaseTracks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2", len(got))
	}
	for _, g := range got {
		if g.Album != "Pink Moon" || g.Year != 1972 {
			t.Errorf("release info not applied: %+v", g)
		}
	}
}

func TestMapRecordingResolvesFreeText(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"recording_mbid":"abc-123","recording_name":"The Wind",
		  "artist_credit_name":"Cat Stevens"}]`))
	})
	got, err := c.MapRecording(context.Background(), "Cat Stevens", "The Wind")
	if err != nil {
		t.Fatalf("MapRecording: %v", err)
	}
	if got.MBID != "abc-123" || got.SourceRef != "mbid-mapper" {
		t.Errorf("candidate = %+v", got)
	}
}

func TestMapRecordingEmptyIsNotFound(t *testing.T) {
	c, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.MapRecording(context.Background(), "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// A field-scoped search must stay scoped at MusicBrainz. Flattening it into
// free text returns anything mentioning the word anywhere, which is the wrong
// answer to "by this artist" and reads as broken scoping.
func TestSearchFieldsBuildAScopedQuery(t *testing.T) {
	cases := []struct {
		name string
		in   SearchFields
		want string
	}{
		{"artist only", SearchFields{Artist: "Depeche Mode"}, `artist:(Depeche Mode)`},
		{"title only", SearchFields{Title: "Enjoy the Silence"}, `recording:(Enjoy the Silence)`},
		{"album only", SearchFields{Album: "Violator"}, `release:(Violator)`},
		{"free text only", SearchFields{Any: "depeche"}, `depeche`},
		{
			"combined",
			SearchFields{Artist: "Depeche Mode", Album: "Violator"},
			`artist:(Depeche Mode) AND release:(Violator)`,
		},
		{"year", SearchFields{Year: 1990}, `firstreleasedate:1990*`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Query(); got != c.want {
				t.Errorf("Query() = %q, want %q", got, c.want)
			}
		})
	}
}

// A title with a colon or brackets must search, not fail to parse.
func TestSearchFieldsEscapeQuerySyntax(t *testing.T) {
	got := SearchFields{Title: `Fade Out (Remix): Part 1`}.Query()
	want := `recording:(Fade Out \(Remix\)\: Part 1)`
	if got != want {
		t.Errorf("Query() = %s, want %s", got, want)
	}
}

func TestSearchFieldsEmpty(t *testing.T) {
	if !(SearchFields{}).Empty() {
		t.Error("a blank SearchFields is not reported empty")
	}
	if (SearchFields{Year: 1990}).Empty() {
		t.Error("a year alone should count as something to search for")
	}
	if (SearchFields{Artist: "  "}).Empty() != true {
		t.Error("whitespace should not count as a search")
	}
}

// The scoped query is what actually goes over the wire.
func TestSearchBySendsTheScopedQuery(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"recordings":[]}`))
	}))
	defer srv.Close()

	c := New("probe@example.test", "test", WithBaseURLs(srv.URL, srv.URL), WithBurst(5))
	if _, err := c.SearchBy(context.Background(), SearchFields{Artist: "Depeche Mode"}, 10); err != nil {
		t.Fatalf("SearchBy: %v", err)
	}
	if got != `artist:(Depeche Mode)` {
		t.Errorf("sent query = %q, want it scoped to the artist", got)
	}
}
