package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultAPI      = "https://api.spotify.com/v1"
	defaultAccounts = "https://accounts.spotify.com"

	// maxBody caps what a provider response may cost us in memory. A playlist
	// page is a few hundred KB; anything near this is a bug or an attack, and
	// on a 256 MiB instance an unbounded read is an OOM.
	maxBody = 8 << 20
)

// Client talks to Spotify. Safe for concurrent use.
//
// It holds no credentials: under BYO (D6) those belong to the user, so every
// call takes them. What it does hold is the rate limiting, because that is the
// one thing that must be shared across callers to be correct.
type Client struct {
	hc          *http.Client
	apiURL      string
	accountsURL string

	// Quota is pooled per developer account, and under BYO each user has their
	// own — so one global limiter would throttle users who are not competing
	// for anything. Keyed by Client ID instead.
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

type Option func(*Client)

// WithHTTPClient replaces the transport, for tests and for operators behind a
// proxy.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// WithBaseURLs points the client at a test server. Both must be set together:
// pointing only one at a stub leaves the other reaching the real Spotify from
// a test, which is exactly the failure this exists to prevent.
func WithBaseURLs(api, accounts string) Option {
	return func(c *Client) { c.apiURL, c.accountsURL = api, accounts }
}

func New(opts ...Option) *Client {
	c := &Client{
		hc:          &http.Client{Timeout: 30 * time.Second},
		apiURL:      defaultAPI,
		accountsURL: defaultAccounts,
		limiters:    make(map[string]*rate.Limiter),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// limiterFor returns the token bucket for one app's quota.
//
// Spotify publishes no fixed rate, so this is a politeness bound rather than a
// documented one: steady 10/s with a burst of 20, which keeps a bulk export
// moving while staying far from anything that would look abusive.
func (c *Client) limiterFor(key string) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.limiters[key]
	if !ok {
		l = rate.NewLimiter(10, 20)
		c.limiters[key] = l
	}
	return l
}

// ErrRateLimited is returned when Spotify pushes back and the wait would exceed
// what a caller should block for. Jobs handle this by pausing and resuming;
// see internal/jobs.
type ErrRateLimited struct{ RetryAfter time.Duration }

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("spotify: rate limited, retry after %s", e.RetryAfter)
}

// ErrStatus is any other non-success response.
type ErrStatus struct {
	Code    int
	Message string
}

func (e *ErrStatus) Error() string {
	return fmt.Sprintf("spotify: request failed (%d): %s", e.Code, e.Message)
}

// NotFound reports whether the resource is simply not there — a deleted
// playlist, or one the user cannot see.
func (e *ErrStatus) NotFound() bool { return e.Code == http.StatusNotFound }

// doJSON performs a request under the rate limit and decodes the response.
//
// 429 is surfaced rather than silently slept through: a cold export can take
// minutes, and a caller that cannot see it is being throttled has no way to
// report honest progress (F22).
func (c *Client) doJSON(ctx context.Context, req *http.Request, quota string, out any) error {
	if err := c.limiterFor(quota).Wait(ctx); err != nil {
		return err
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &ErrRateLimited{RetryAfter: retryAfter(resp)}
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrNeedsReconnect
	case resp.StatusCode >= 400:
		// The token-exchange endpoint reports its failures in a 400 body, so
		// let those through to be read as structured errors rather than
		// flattened into a status.
		if out != nil && resp.StatusCode == http.StatusBadRequest {
			if json.Unmarshal(body, out) == nil {
				return nil
			}
		}
		return &ErrStatus{Code: resp.StatusCode, Message: spotifyMessage(body)}
	}

	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("spotify: malformed response: %w", err)
	}
	return nil
}

// retryAfter reads the header, with a floor so a missing or zero value cannot
// turn into a hot retry loop.
func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 5 * time.Second
}

// spotifyMessage extracts the human-readable half of an error body without
// echoing the whole thing, which can carry request context we should not log.
func spotifyMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return http.StatusText(http.StatusInternalServerError)
}

// get issues an authenticated GET.
func (c *Client) get(ctx context.Context, app App, tok Token, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	return c.doJSON(ctx, req, quotaKey(app), out)
}

// postJSON issues an authenticated POST with a JSON body.
func (c *Client) postJSON(ctx context.Context, app App, tok Token, url string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(ctx, req, quotaKey(app), out)
}

// IsAuthError reports whether an error means the user must reconnect.
func IsAuthError(err error) bool {
	return errors.Is(err, ErrNeedsReconnect) || errors.Is(err, ErrAuthDenied)
}

// RateLimit extracts the retry delay from an error, if it is one.
func RateLimit(err error) (time.Duration, bool) {
	var rl *ErrRateLimited
	if errors.As(err, &rl) {
		return rl.RetryAfter, true
	}
	return 0, false
}
