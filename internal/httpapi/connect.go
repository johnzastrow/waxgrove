package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/jobs"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/spotify"
)

// pendingAuth holds one in-flight OAuth attempt.
//
// It lives in memory rather than the database on purpose: the PKCE verifier is
// single-use, expires in minutes, and is worthless after the callback. Writing
// it to disk would mean a secret at rest with no lifetime benefit. Losing these
// on restart costs the user one click.
type pendingAuth struct {
	userID  string
	pkce    spotify.PKCE
	created time.Time
}

// authStore is the set of authorisations awaiting their callback.
type authStore struct {
	mu      sync.Mutex
	byState map[string]pendingAuth
}

func newAuthStore() *authStore {
	return &authStore{byState: make(map[string]pendingAuth)}
}

// pendingTTL bounds how long a half-finished connection is remembered. Long
// enough to read a consent screen; short enough that an abandoned attempt is
// not a lingering secret.
const pendingTTL = 15 * time.Minute

func (a *authStore) put(userID string, p spotify.PKCE) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked()
	a.byState[p.State] = pendingAuth{userID: userID, pkce: p, created: time.Now()}
}

// take consumes an attempt. Single-use: a replayed callback finds nothing,
// which is the property that makes the state parameter worth having.
func (a *authStore) take(state string) (pendingAuth, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked()
	p, ok := a.byState[state]
	delete(a.byState, state)
	return p, ok
}

func (a *authStore) sweepLocked() {
	for k, v := range a.byState {
		if time.Since(v.created) > pendingTTL {
			delete(a.byState, k)
		}
	}
}

// mountConnect registers the provider routes.
func (a *API) mountConnect(mux *http.ServeMux) {
	if a.Spotify == nil {
		// N6: with no connector configured these routes simply do not exist,
		// rather than existing and failing.
		return
	}
	mux.Handle("GET /api/connect/spotify", a.authed(a.spotifyStatus))
	mux.Handle("PUT /api/connect/spotify/app", a.authed(a.spotifySaveApp))
	mux.Handle("POST /api/connect/spotify/begin", a.authed(a.spotifyBegin))
	mux.Handle("GET /api/connect/spotify/callback", a.authed(a.spotifyCallback))
	mux.Handle("DELETE /api/connect/spotify", a.authed(a.spotifyDisconnect))

	mux.Handle("POST /api/import/spotify", a.authed(a.spotifyImport))
	mux.Handle("POST /api/playlists/{id}/export/spotify", a.authed(a.spotifyExport))

	mux.Handle("GET /api/jobs", a.authed(a.listJobs))
	mux.Handle("GET /api/jobs/{id}", a.authed(a.getJob))
	mux.Handle("POST /api/jobs/{id}/cancel", a.authed(a.cancelJob))
}

func (a *API) spotifyStatus(w http.ResponseWriter, r *http.Request, u *domain.User) {
	st, err := a.Spotify.Status(r.Context(), u.ID)
	if err != nil {
		internal(w, "spotify status", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// spotifySaveApp stores the user's own Client ID and Secret (D6).
//
// The secret is write-only from the API's point of view: it goes in, and no
// endpoint ever returns it. A user who has forgotten theirs re-reads it from
// their own Spotify dashboard, not from us.
func (a *API) spotifySaveApp(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.ClientID = strings.TrimSpace(in.ClientID)
	in.ClientSecret = strings.TrimSpace(in.ClientSecret)
	if in.ClientID == "" || in.ClientSecret == "" {
		problem(w, http.StatusBadRequest, "both the Client ID and Client Secret are required")
		return
	}
	if err := a.Store.Credentials(a.Sealer).SaveApp(r.Context(), u.ID,
		sqlite.ServiceSpotify, in.ClientID, in.ClientSecret); err != nil {
		internal(w, "save spotify app", err)
		return
	}
	st, err := a.Spotify.Status(r.Context(), u.ID)
	if err != nil {
		internal(w, "spotify status", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) spotifyBegin(w http.ResponseWriter, r *http.Request, u *domain.User) {
	url, pkce, err := a.Spotify.Begin(r.Context(), u.ID)
	if errors.Is(err, sqlite.ErrNoApp) {
		problem(w, http.StatusPreconditionRequired,
			"add your Spotify Client ID and Secret first")
		return
	}
	if err != nil {
		internal(w, "spotify begin", err)
		return
	}
	a.pending.put(u.ID, pkce)
	writeJSON(w, http.StatusOK, map[string]any{"authorize_url": url})
}

// spotifyCallback finishes the flow and redirects back into the app.
//
// It is a redirect rather than JSON because the browser arrives here from
// Spotify, not from the PWA's fetch — the user is looking at this URL.
func (a *API) spotifyCallback(w http.ResponseWriter, r *http.Request, u *domain.User) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		// The user pressed Cancel on Spotify's consent screen. Not an error.
		redirect(w, r, "/settings?spotify=denied")
		return
	}

	pending, ok := a.pending.take(q.Get("state"))
	if !ok {
		// An unknown state means a forged, replayed, or long-abandoned callback.
		redirect(w, r, "/settings?spotify=expired")
		return
	}
	if pending.userID != u.ID {
		// The session that finished the flow is not the one that started it.
		redirect(w, r, "/settings?spotify=expired")
		return
	}

	if err := a.Spotify.Complete(r.Context(), u.ID, pending.pkce, q.Get("code")); err != nil {
		slog.Warn("spotify callback failed", "err", err)
		redirect(w, r, "/settings?spotify=failed")
		return
	}
	redirect(w, r, "/settings?spotify=connected")
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (a *API) spotifyDisconnect(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if err := a.Spotify.Disconnect(r.Context(), u.ID); err != nil {
		internal(w, "spotify disconnect", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------ import/export --

func (a *API) spotifyImport(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Link string `json:"link"`
	}
	if !decode(w, r, &in) {
		return
	}
	// Validated here so an obviously wrong paste fails immediately rather than
	// as a job that runs and fails a second later.
	if _, err := spotify.ParsePlaylistRef(in.Link); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	job, err := a.Jobs.QueueImport(r.Context(), jobs.ImportRequest{UserID: u.ID, Ref: in.Link})
	if err != nil {
		internal(w, "queue import", err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobView(job))
}

func (a *API) spotifyExport(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if _, ok := a.ownedOr404(w, r, u); !ok {
		return
	}
	job, err := a.Jobs.QueueExport(r.Context(), jobs.ExportRequest{
		UserID: u.ID, PlaylistID: r.PathValue("id"),
	})
	if err != nil {
		internal(w, "queue export", err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobView(job))
}

// -------------------------------------------------------------------- jobs --

func (a *API) listJobs(w http.ResponseWriter, r *http.Request, u *domain.User) {
	js, err := a.Store.Jobs().ListForUser(r.Context(), u.ID, intParam(r, "limit", 20))
	if err != nil {
		internal(w, "list jobs", err)
		return
	}
	out := make([]map[string]any, 0, len(js))
	for i := range js {
		out = append(out, jobView(&js[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request, u *domain.User) {
	job, err := a.Store.Jobs().Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sqlite.ErrJobNotFound) {
		problem(w, http.StatusNotFound, "no such job")
		return
	}
	if err != nil {
		internal(w, "get job", err)
		return
	}
	// A job is one user's business. 404 rather than 403 so the response does
	// not confirm it exists (§6).
	if job.UserID != u.ID {
		problem(w, http.StatusNotFound, "no such job")
		return
	}
	writeJSON(w, http.StatusOK, jobViewWithItems(job))
}

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request, u *domain.User) {
	err := a.Store.Jobs().Cancel(r.Context(), r.PathValue("id"), u.ID)
	if errors.Is(err, sqlite.ErrJobNotFound) {
		problem(w, http.StatusNotFound, "no such job, or it has already finished")
		return
	}
	if err != nil {
		internal(w, "cancel job", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------- views --

func jobView(j *domain.Job) map[string]any {
	return map[string]any{
		"id": j.ID, "kind": j.Kind, "state": j.State,
		"service": j.Service, "playlist_id": j.PlaylistID,
		"done": j.Done, "total": j.Total, "error": j.Error,
		"created_at": j.CreatedAt, "updated_at": j.UpdatedAt,
		"terminal": j.Terminal(),
	}
}

// jobViewWithItems includes the per-track outcomes.
//
// Only the ones that need attention: a hundred "ok" rows tell the user nothing,
// and the point of this list is the tracks that did not make it (F15).
func jobViewWithItems(j *domain.Job) map[string]any {
	v := jobView(j)
	problems := make([]map[string]any, 0)
	ok := 0
	for _, it := range j.Items {
		if it.Status == domain.JobItemOK {
			ok++
			continue
		}
		problems = append(problems, map[string]any{
			"position": it.Position, "status": it.Status, "detail": it.Detail,
		})
	}
	v["succeeded"] = ok
	v["problems"] = problems
	return v
}
