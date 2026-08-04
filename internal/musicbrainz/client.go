// Package musicbrainz talks to MusicBrainz and the ListenBrainz MBID Mapper.
//
// Two rules from docs/requirements.md §3.6 shape everything here:
//
//   - MusicBrainz permits roughly one request per second, enforced as a burst
//     bucket. Every call goes through a limiter; nothing else in the codebase
//     may call these hosts directly.
//   - Requests are album-shaped, not track-shaped. One release lookup returns
//     the entire tracklist with every ISRC, which is what makes a cold import
//     cost about one request per album rather than one per track.
//
// The MBID Mapper does the fuzzy matching (§3.1) — people with the full
// MusicBrainz corpus solved it better than a local matcher could.
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"golang.org/x/time/rate"
)

const (
	apiBase    = "https://musicbrainz.org/ws/2"
	mapperBase = "https://labs.api.listenbrainz.org"
)

// Client is a rate-limited MusicBrainz client. Safe for concurrent use.
type Client struct {
	http    *http.Client
	limiter *rate.Limiter
	// userAgent is required by MusicBrainz; requests without a meaningful one
	// are blocked, and rightly so.
	userAgent string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient swaps the transport, primarily so tests can point at a stub.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithBurst adjusts the limiter burst. The default respects the documented
// 1 req/sec average with a small burst allowance.
func WithBurst(b int) Option {
	return func(c *Client) { c.limiter = rate.NewLimiter(rate.Limit(1), b) }
}

// New builds a client. The version string goes in the User-Agent so MusicBrainz
// can identify and contact the operator if an instance misbehaves.
func New(contact, version string, opts ...Option) *Client {
	c := &Client{
		http:      &http.Client{Timeout: 20 * time.Second},
		limiter:   rate.NewLimiter(rate.Limit(1), 3),
		userAgent: fmt.Sprintf("Waxgrove/%s ( %s )", version, contact),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) get(ctx context.Context, rawURL string, out any) error {
	// Blocks until the bucket allows it, or the context is cancelled. This is
	// the only place the rate limit is enforced, deliberately.
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable,
		resp.StatusCode == http.StatusTooManyRequests:
		// Being throttled is a signal to back off, not to retry immediately.
		return fmt.Errorf("musicbrainz: rate limited (%d)", resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 400:
		return fmt.Errorf("musicbrainz: http %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrNotFound means MusicBrainz has no such entity.
var ErrNotFound = fmt.Errorf("musicbrainz: not found")

// ---------------------------------------------------------------- lookups --

type recordingResponse struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Length       int      `json:"length"`
	ISRCs        []string `json:"isrcs"`
	ArtistCredit []struct {
		Name string `json:"name"`
	} `json:"artist-credit"`
	Releases []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Date  string `json:"date"`
	} `json:"releases"`
}

func (r recordingResponse) toCandidate() domain.Candidate {
	c := domain.Candidate{
		Title:      r.Title,
		DurationMS: r.Length,
		MBID:       r.ID,
		SourceRef:  "musicbrainz",
	}
	names := make([]string, 0, len(r.ArtistCredit))
	for _, a := range r.ArtistCredit {
		names = append(names, a.Name)
	}
	c.Artist = strings.Join(names, ", ")
	if len(r.ISRCs) > 0 {
		c.ISRC = r.ISRCs[0]
	}
	if len(r.Releases) > 0 {
		c.Album = r.Releases[0].Title
		if len(r.Releases[0].Date) >= 4 {
			fmt.Sscanf(r.Releases[0].Date[:4], "%d", &c.Year)
		}
	}
	return c
}

// LookupISRC resolves an ISRC to its recording — step 1 of the §3.2 ladder
// when the local catalog misses.
func (c *Client) LookupISRC(ctx context.Context, isrc string) (domain.Candidate, error) {
	var out struct {
		Recordings []recordingResponse `json:"recordings"`
	}
	u := fmt.Sprintf("%s/isrc/%s?inc=artist-credits+releases&fmt=json", apiBase, url.PathEscape(isrc))
	if err := c.get(ctx, u, &out); err != nil {
		return domain.Candidate{}, err
	}
	if len(out.Recordings) == 0 {
		return domain.Candidate{}, ErrNotFound
	}
	cand := out.Recordings[0].toCandidate()
	cand.ISRC = isrc // the queried code is authoritative for this lookup
	return cand, nil
}

// LookupRecording fetches one recording by MBID, with its full ISRC set.
func (c *Client) LookupRecording(ctx context.Context, mbid string) (domain.Candidate, error) {
	var out recordingResponse
	u := fmt.Sprintf("%s/recording/%s?inc=artist-credits+releases+isrcs&fmt=json", apiBase, url.PathEscape(mbid))
	if err := c.get(ctx, u, &out); err != nil {
		return domain.Candidate{}, err
	}
	return out.toCandidate(), nil
}

// Search runs a free-text recording search, backing F5.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]domain.Candidate, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var out struct {
		Recordings []recordingResponse `json:"recordings"`
	}
	u := fmt.Sprintf("%s/recording/?query=%s&limit=%d&fmt=json", apiBase, url.QueryEscape(query), limit)
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}
	cands := make([]domain.Candidate, 0, len(out.Recordings))
	for _, r := range out.Recordings {
		cands = append(cands, r.toCandidate())
	}
	return cands, nil
}

// apiBaseForTest lets tests point the release parser at a stub server.
var apiBaseForTest = apiBase

// ReleaseTracks fetches an entire release in ONE request, with every ISRC.
//
// This is what makes album-shaped fetching cheap (§3.6) and feeds D11's
// ambient tier: the surplus tracks are cached so later resolutions are free.
func (c *Client) ReleaseTracks(ctx context.Context, releaseMBID string) ([]domain.Candidate, error) {
	u := fmt.Sprintf("%s/release/%s?inc=recordings+artist-credits+isrcs&fmt=json",
		apiBaseForTest, url.PathEscape(releaseMBID))
	return c.releaseTracksAt(ctx, u)
}

// releaseTracksAt is the parsing half, split out so it can be tested directly.
func (c *Client) releaseTracksAt(ctx context.Context, u string) ([]domain.Candidate, error) {
	var out struct {
		Title string `json:"title"`
		Date  string `json:"date"`
		Media []struct {
			Tracks []struct {
				Position  int               `json:"position"`
				Recording recordingResponse `json:"recording"`
			} `json:"tracks"`
		} `json:"media"`
	}
	if err := c.get(ctx, u, &out); err != nil {
		return nil, err
	}

	var year int
	if len(out.Date) >= 4 {
		fmt.Sscanf(out.Date[:4], "%d", &year)
	}
	var cands []domain.Candidate
	for _, m := range out.Media {
		for _, t := range m.Tracks {
			c := t.Recording.toCandidate()
			c.Album, c.Year = out.Title, year
			cands = append(cands, c)
		}
	}
	return cands, nil
}

// MapRecording asks the ListenBrainz MBID Mapper to resolve free text.
//
// §3.1: this is the fuzzy matcher, already built by people with the whole
// corpus to train against. Calling it first removes the largest chunk of
// matching risk; the local fuzzy pass is only a fallback.
func (c *Client) MapRecording(ctx context.Context, artist, title string) (domain.Candidate, error) {
	var out []struct {
		RecordingMBID string `json:"recording_mbid"`
		RecordingName string `json:"recording_name"`
		ArtistName    string `json:"artist_credit_name"`
	}
	u := fmt.Sprintf("%s/mbid-mapping/json?artist_credit_name=%s&recording_name=%s",
		mapperBase, url.QueryEscape(artist), url.QueryEscape(title))
	if err := c.get(ctx, u, &out); err != nil {
		return domain.Candidate{}, err
	}
	if len(out) == 0 || out[0].RecordingMBID == "" {
		return domain.Candidate{}, ErrNotFound
	}
	return domain.Candidate{
		Title:     out[0].RecordingName,
		Artist:    out[0].ArtistName,
		MBID:      out[0].RecordingMBID,
		SourceRef: "mbid-mapper",
	}, nil
}
