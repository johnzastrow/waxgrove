package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnzastrow/waxgrove/internal/auth"
	"github.com/johnzastrow/waxgrove/internal/connector"
	"github.com/johnzastrow/waxgrove/internal/crypto"
	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/jobs"
	"github.com/johnzastrow/waxgrove/internal/jspf"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/resolve"
	"github.com/johnzastrow/waxgrove/internal/version"
)

// API holds the dependencies the handlers need.
type API struct {
	Store    *sqlite.Store
	Resolver *resolve.Resolver
	Remote   RemoteSearch // nil when no metadata source is configured (N6)
	Env      string
	Secure   bool // set Secure on cookies; false only for local http development

	// Spotify and Jobs are nil on an instance with no connector. N6 makes that
	// a supported configuration rather than a broken one, so the routes are
	// simply not registered instead of registering and failing.
	Spotify *connector.Spotify
	Jobs    *jobs.Runner
	Sealer  *crypto.Sealer

	// pending holds in-flight OAuth attempts. Built lazily by Mount so a
	// zero-value API is still usable in tests.
	pending *authStore
}

// RemoteSearch is the slice of MusicBrainz that F5 needs.
type RemoteSearch interface {
	Search(ctx context.Context, query string, limit int) ([]domain.Candidate, error)
}

type ctxKey int

const userKey ctxKey = 1

const sessionCookie = "waxgrove_session"

// Mount registers the API routes onto a mux.
func (a *API) Mount(mux *http.ServeMux) {
	// Public
	mux.HandleFunc("GET /api/instance", a.instance)
	mux.HandleFunc("GET /api/version", a.versionInfo)
	mux.HandleFunc("POST /api/register", a.register)
	mux.HandleFunc("POST /api/login", a.login)

	// Authenticated
	mux.Handle("POST /api/logout", a.authed(a.logout))
	mux.Handle("GET /api/me", a.authed(a.me))
	mux.Handle("POST /api/invites", a.authed(a.createInvite))
	mux.Handle("POST /api/me/password", a.authed(a.changePassword))

	mux.Handle("GET /api/records", a.authed(a.searchRecords))
	mux.Handle("GET /api/records/remote", a.authed(a.searchRemote))

	mux.Handle("GET /api/playlists", a.authed(a.listPlaylists))
	mux.Handle("POST /api/playlists", a.authed(a.createPlaylist))
	mux.Handle("GET /api/playlists/{id}", a.authed(a.getPlaylist))
	mux.Handle("PATCH /api/playlists/{id}", a.authed(a.renamePlaylist))
	mux.Handle("DELETE /api/playlists/{id}", a.authed(a.deletePlaylist))
	mux.Handle("POST /api/playlists/{id}/tracks", a.authed(a.addTracks))
	mux.Handle("PUT /api/playlists/{id}/tracks", a.authed(a.reorderTracks))
	mux.Handle("DELETE /api/playlists/{id}/tracks/{pos}", a.authed(a.removeTrack))
	mux.Handle("GET /api/playlists/{id}/history", a.authed(a.history))
	mux.Handle("GET /api/playlists/{id}/export.jspf", a.authed(a.exportJSPF))
	mux.Handle("POST /api/playlists/import", a.authed(a.importJSPF))

	// The crate, annotations, discovery and the privacy pair.
	a.mountCrate(mux)

	// Provider routes, only when a connector is wired (N6).
	a.pending = newAuthStore()
	a.mountConnect(mux)
}

// ------------------------------------------------------------- middleware --

// authed requires a valid session. §6: authorization is checked on every
// request, not just at login.
func (a *API) authed(next func(http.ResponseWriter, *http.Request, *domain.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			problem(w, http.StatusUnauthorized, "authentication required")
			return
		}
		user, err := a.Store.Users().UserForSession(r.Context(), c.Value)
		if err != nil {
			// Clear the stale cookie so the client stops sending it.
			a.clearSession(w)
			problem(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, user)), user)
	})
}

func (a *API) setSession(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,                 // not readable from JavaScript (§6)
		Secure:   a.Secure,             // HTTPS only in production
		SameSite: http.SameSiteLaxMode, // CSRF defence for state-changing routes
	})
}

func (a *API) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// ------------------------------------------------------------------- auth --

type registerReq struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	InviteCode  string `json:"invite_code"`
}

// instance describes the sign-in surface before anyone has authenticated.
//
// It exists for one reason: the registration form cannot otherwise know whether
// to ask for an invite code. The very first account does not need one, and a
// form that demands one anyway locks the operator out of their own instance —
// which is exactly what shipped before this.
func (a *API) instance(w http.ResponseWriter, r *http.Request) {
	claimed, err := a.Store.Users().HasAnyUser(r.Context())
	if err != nil {
		internal(w, "instance state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// An unclaimed instance takes its first account with no code. After
		// that, invite-only (§6).
		"invite_required": claimed,
		"first_account":   !claimed,
	})
}

// versionInfo reports which build this is.
//
// Public, and readable before signing in: "which version am I looking at" is
// the first question when something behaves unexpectedly, and needing an
// account to answer it makes it useless during a failed deploy.
func (a *API) versionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, version.Current())
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var in registerReq
	if !decode(w, r, &in) {
		return
	}
	user, err := a.Store.Users().Register(r.Context(),
		in.Email, in.DisplayName, in.Password, in.InviteCode)
	switch {
	case errors.Is(err, auth.ErrWeak):
		problem(w, http.StatusBadRequest,
			"password must be at least "+strconv.Itoa(auth.MinPasswordLen)+" characters")
		return
	case errors.Is(err, sqlite.ErrInviteBad):
		problem(w, http.StatusForbidden, "invite code is invalid, used, or expired")
		return
	case errors.Is(err, sqlite.ErrEmailTaken):
		// Registration is invite-only, so the inviter already knows who they
		// invited; this is not a meaningful enumeration vector.
		problem(w, http.StatusConflict, "that email is already registered")
		return
	case err != nil:
		internal(w, "register", err)
		return
	}
	a.issueSession(w, r, user)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	user, err := a.Store.Users().Authenticate(r.Context(), in.Email, in.Password)
	if err != nil {
		// Identical response for unknown account and wrong password.
		problem(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	a.issueSession(w, r, user)
}

func (a *API) issueSession(w http.ResponseWriter, r *http.Request, user *domain.User) {
	token, expires, err := a.Store.Users().StartSession(r.Context(), user.ID)
	if err != nil {
		internal(w, "session", err)
		return
	}
	a.setSession(w, token, expires)
	writeJSON(w, http.StatusOK, userView(user))
}

func (a *API) logout(w http.ResponseWriter, r *http.Request, _ *domain.User) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.Store.Users().EndSession(r.Context(), c.Value)
	}
	a.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, _ *http.Request, u *domain.User) {
	writeJSON(w, http.StatusOK, userView(u))
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if u.Role != domain.RoleAdmin {
		problem(w, http.StatusForbidden, "only an admin can create invites")
		return
	}
	code, err := a.Store.Users().CreateInvite(r.Context(), u.ID)
	if err != nil {
		internal(w, "invite", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":       code,
		"expires_in": int(sqlite.InviteTTL.Seconds()),
	})
}

// changePassword replaces the caller's password.
//
// It ends every session, including this one, so the client must sign in again.
// That is the point rather than a side effect: if the reason for the change is
// that somebody else knows the old password, leaving their session alive would
// make the change cosmetic.
func (a *API) changePassword(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := a.Store.Users().ChangePassword(r.Context(), u.ID, in.Current, in.New)
	switch {
	case errors.Is(err, auth.ErrWeak):
		problem(w, http.StatusBadRequest,
			"the new password must be at least "+strconv.Itoa(auth.MinPasswordLen)+" characters")
		return
	case errors.Is(err, sqlite.ErrCredentials):
		problem(w, http.StatusForbidden, "that is not your current password")
		return
	case errors.Is(err, sqlite.ErrNoPassword):
		problem(w, http.StatusConflict, "this account signs in another way")
		return
	case err != nil:
		internal(w, "change password", err)
		return
	}
	// Every session is gone, so the cookie this request arrived with is dead.
	a.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- records --

// searchRecords searches the catalogue, or browses it when given no query.
//
// "Where is this song" and "what have we got" are different questions, and a
// search box only answers the first — you cannot search for something you have
// forgotten you added. An empty query therefore lists rather than returning
// nothing, which is what it used to do.
func (a *API) searchRecords(w http.ResponseWriter, r *http.Request, u *domain.User) {
	opts := sqlite.SearchOptions{
		Any:    strings.TrimSpace(r.URL.Query().Get("q")),
		Title:  strings.TrimSpace(r.URL.Query().Get("title")),
		Artist: strings.TrimSpace(r.URL.Query().Get("artist")),
		Album:  strings.TrimSpace(r.URL.Query().Get("album")),
		Year:   intParam(r, "year", 0),
		Limit:  intParam(r, "limit", 50),
	}
	// Nothing asked for means "show me what is here", not "show me nothing".
	if opts.Empty() {
		a.browseRecords(w, r, u)
		return
	}
	recs, err := a.Store.Records().SearchBy(r.Context(), opts)
	if err != nil {
		internal(w, "search", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": recordViews(recs)})
}

func (a *API) browseRecords(w http.ResponseWriter, r *http.Request, u *domain.User) {
	opts := sqlite.ListOptions{
		Limit:  intParam(r, "limit", 50),
		Offset: intParam(r, "offset", 0),
		Sort:   r.URL.Query().Get("sort"),
	}
	// The catalogue is shared, so "mine" means the records this user
	// deliberately contributed rather than a private collection (§3.0).
	if r.URL.Query().Get("mine") == "true" {
		opts.AddedBy = u.ID
	}

	recs, total, err := a.Store.Records().List(r.Context(), opts)
	if err != nil {
		internal(w, "browse records", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"records": recordViews(recs),
		"total":   total,
		"offset":  opts.Offset,
		"browse":  true,
	})
}

func (a *API) searchRemote(w http.ResponseWriter, r *http.Request, _ *domain.User) {
	if a.Remote == nil {
		// N6: remote search is an enhancement, and its absence is a normal
		// state rather than an error.
		writeJSON(w, http.StatusOK, map[string]any{
			"candidates": []any{},
			"note":       "no metadata source configured",
		})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		problem(w, http.StatusBadRequest, "q is required")
		return
	}
	cands, err := a.Remote.Search(r.Context(), q, intParam(r, "limit", 25))
	if err != nil {
		// The client abandoned this search — almost always because the user
		// typed another character and the debounce aborted it. That is the
		// normal case, not a fault, and logging it as one buries the real
		// failures in noise.
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		// A metadata source being down must not read as a Waxgrove failure.
		slog.Warn("remote search failed", "err", err)
		problem(w, http.StatusBadGateway, "the metadata source is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": cands})
}

// -------------------------------------------------------------- playlists --

func (a *API) listPlaylists(w http.ResponseWriter, r *http.Request, u *domain.User) {
	pls, err := a.Store.Playlists().ListOwned(r.Context(), u.ID)
	if err != nil {
		internal(w, "list playlists", err)
		return
	}
	out := make([]any, 0, len(pls))
	for i := range pls {
		out = append(out, playlistView(&pls[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": out})
}

func (a *API) createPlaylist(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		problem(w, http.StatusBadRequest, "title is required")
		return
	}
	p, err := a.Store.Playlists().Create(r.Context(), u.ID, in.Title, in.Description)
	if err != nil {
		internal(w, "create playlist", err)
		return
	}
	writeJSON(w, http.StatusCreated, playlistView(p))
}

func (a *API) getPlaylist(w http.ResponseWriter, r *http.Request, _ *domain.User) {
	p, err := a.Store.Playlists().Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sqlite.ErrNotFound) {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	if err != nil {
		internal(w, "get playlist", err)
		return
	}
	v := playlistView(p)
	// Where it came from, if anywhere. Attribution is the point of a fork
	// (F20) — without it a fork is indistinguishable from something you made.
	if srcID, title, owner, ferr := a.Store.Playlists().ForkedFrom(r.Context(), p.ID); ferr == nil && srcID != "" {
		v["forked_from"] = map[string]any{"id": srcID, "title": title, "owner": owner}
	}
	// D8: playlists are shared by reference, so any member may read one.
	writeJSON(w, http.StatusOK, v)
}

// ownedOr404 enforces write access. A non-owner gets 404 rather than 403 so the
// response does not confirm the playlist exists (§6).
func (a *API) ownedOr404(w http.ResponseWriter, r *http.Request, u *domain.User) (*domain.Playlist, bool) {
	p, err := a.Store.Playlists().Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sqlite.ErrNotFound) {
		problem(w, http.StatusNotFound, "playlist not found")
		return nil, false
	}
	if err != nil {
		internal(w, "get playlist", err)
		return nil, false
	}
	if p.OwnerID != u.ID {
		problem(w, http.StatusNotFound, "playlist not found")
		return nil, false
	}
	return p, true
}

func (a *API) deletePlaylist(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if _, ok := a.ownedOr404(w, r, u); !ok {
		return
	}
	if err := a.Store.Playlists().Delete(r.Context(), r.PathValue("id")); err != nil {
		internal(w, "delete playlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// addTracks resolves each candidate then adds the resolved ones in a single
// revision (§3.3). Unresolved candidates are reported back, never dropped.
func (a *API) addTracks(w http.ResponseWriter, r *http.Request, u *domain.User) {
	p, ok := a.ownedOr404(w, r, u)
	if !ok {
		return
	}
	var in struct {
		Candidates []domain.Candidate `json:"candidates"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Candidates) == 0 {
		problem(w, http.StatusBadRequest, "candidates is required")
		return
	}

	// Resolution happens BEFORE the write transaction — §7.2 forbids holding a
	// write lock across network I/O.
	var ids []string
	unresolved := make([]map[string]any, 0)
	for _, c := range in.Candidates {
		m, err := a.Resolver.Resolve(r.Context(), c, domain.TierCurated)
		if err != nil {
			internal(w, "resolve", err)
			return
		}
		if m.Resolved() {
			ids = append(ids, m.Record.ID)
			_ = a.Store.Records().RecordProvenance(r.Context(), m.Record.ID, u.ID)
			continue
		}
		unresolved = append(unresolved, map[string]any{
			"candidate":    c,
			"method":       m.Method,
			"confidence":   m.Confidence,
			"alternatives": m.Alternatives,
		})
	}

	updated, err := a.Store.Playlists().AddRecords(r.Context(), p.ID, u.ID, ids)
	if err != nil {
		internal(w, "add tracks", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"playlist":   playlistView(updated),
		"added":      len(ids),
		"unresolved": unresolved, // never silently dropped (BR-5)
	})
}

// renamePlaylist changes the title. A rename is a content change, so it writes
// a revision like any other (BR-3) — annotations are what do not.
func (a *API) renamePlaylist(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if _, ok := a.ownedOr404(w, r, u); !ok {
		return
	}
	var in struct {
		Title string `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		problem(w, http.StatusBadRequest, "title is required")
		return
	}
	updated, err := a.Store.Playlists().Rename(r.Context(), r.PathValue("id"), u.ID, title)
	if errors.Is(err, sqlite.ErrNotFound) {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	if err != nil {
		internal(w, "rename playlist", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistView(updated))
}

// reorderTracks replaces the ordering. The body must list every record the
// playlist currently holds, exactly once each — the store enforces that, so a
// list with something added or dropped is rejected rather than recorded as a
// reorder it is not.
func (a *API) reorderTracks(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if _, ok := a.ownedOr404(w, r, u); !ok {
		return
	}
	var in struct {
		RecordIDs []string `json:"record_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	updated, err := a.Store.Playlists().Reorder(r.Context(), r.PathValue("id"), u.ID, in.RecordIDs)
	switch {
	case errors.Is(err, sqlite.ErrReorderMismatch):
		problem(w, http.StatusConflict,
			"the submitted order must contain exactly the tracks the playlist holds; "+
				"reload it and try again")
		return
	case errors.Is(err, sqlite.ErrNotFound):
		problem(w, http.StatusNotFound, "playlist not found")
		return
	case err != nil:
		internal(w, "reorder tracks", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistView(updated))
}

func (a *API) removeTrack(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if _, ok := a.ownedOr404(w, r, u); !ok {
		return
	}
	pos, err := strconv.Atoi(r.PathValue("pos"))
	if err != nil || pos < 0 {
		problem(w, http.StatusBadRequest, "position must be a non-negative integer")
		return
	}
	updated, err := a.Store.Playlists().RemoveAt(r.Context(), r.PathValue("id"), u.ID, pos)
	if errors.Is(err, sqlite.ErrNotFound) {
		problem(w, http.StatusNotFound, "no track at that position")
		return
	}
	if err != nil {
		internal(w, "remove track", err)
		return
	}
	writeJSON(w, http.StatusOK, playlistView(updated))
}

// history returns the append-only content log. Annotations never appear here —
// only changes to what the playlist *is* produce a revision (BR-3).
func (a *API) history(w http.ResponseWriter, r *http.Request, _ *domain.User) {
	revs, err := a.Store.Playlists().History(r.Context(), r.PathValue("id"))
	if err != nil {
		internal(w, "history", err)
		return
	}
	// Resolve actors to display names once each. A history is a human-readable
	// log, so returning opaque IDs would push this lookup onto every client.
	names := make(map[string]string, 4)
	out := make([]map[string]any, 0, len(revs))
	for _, v := range revs {
		actor, ok := names[v.ActorID]
		if !ok {
			// BR-4: the author was erased; the history itself survives.
			actor = "a departed member"
			if v.ActorID != "" {
				if u, err := a.Store.Users().Get(r.Context(), v.ActorID); err == nil {
					actor = u.DisplayName
				}
			}
			names[v.ActorID] = actor
		}
		out = append(out, map[string]any{
			"rev": v.Rev, "op": v.Op, "actor": actor,
			"detail": v.Detail, "created_at": v.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out})
}

// ------------------------------------------------------------------- JSPF --

func (a *API) exportJSPF(w http.ResponseWriter, r *http.Request, _ *domain.User) {
	p, err := a.Store.Playlists().Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sqlite.ErrNotFound) {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	if err != nil {
		internal(w, "export", err)
		return
	}
	owner := ""
	if p.OwnerID != "" {
		if u, err := a.Store.Users().Get(r.Context(), p.OwnerID); err == nil {
			owner = u.DisplayName
		}
	}
	w.Header().Set("Content-Type", "application/jspf+json")
	w.Header().Set("Content-Disposition", `attachment; filename="playlist.jspf"`)
	if err := jspf.Write(w, jspf.Export(p, owner)); err != nil {
		slog.Error("jspf write", "err", err)
	}
}

func (a *API) importJSPF(w http.ResponseWriter, r *http.Request, u *domain.User) {
	title, cands, err := jspf.Parse(http.MaxBytesReader(w, r.Body, jspf.MaxSize))
	if err != nil {
		problem(w, http.StatusBadRequest, "not a valid JSPF playlist")
		return
	}
	if title == "" {
		title = "Imported playlist"
	}
	p, err := a.Store.Playlists().Create(r.Context(), u.ID, title, "")
	if err != nil {
		internal(w, "import create", err)
		return
	}

	var ids []string
	unresolved := make([]map[string]any, 0)
	for _, c := range cands {
		m, rerr := a.Resolver.Resolve(r.Context(), c, domain.TierCurated)
		if rerr != nil {
			internal(w, "import resolve", rerr)
			return
		}
		if m.Resolved() {
			ids = append(ids, m.Record.ID)
			continue
		}
		unresolved = append(unresolved, map[string]any{"candidate": c, "confidence": m.Confidence})
	}
	updated, err := a.Store.Playlists().AddRecords(r.Context(), p.ID, u.ID, ids)
	if err != nil {
		internal(w, "import add", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"playlist":   playlistView(updated),
		"imported":   len(ids),
		"unresolved": unresolved,
	})
}

// ------------------------------------------------------------------ views --
// Explicit view types keep internal columns out of responses by construction.

func userView(u *domain.User) map[string]any {
	return map[string]any{
		"id": u.ID, "email": u.Email, "display_name": u.DisplayName, "role": u.Role,
	}
}

func recordView(r domain.Record) map[string]any {
	return map[string]any{
		"id": r.ID, "mbid": r.MBID, "title": r.Title, "artist": r.ArtistCredit,
		"album": r.Album, "duration_ms": r.DurationMS, "year": r.Year, "isrcs": r.ISRCs,
	}
}

func recordViews(recs []domain.Record) []map[string]any {
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		out = append(out, recordView(r))
	}
	return out
}

func playlistView(p *domain.Playlist) map[string]any {
	tracks := make([]map[string]any, 0, len(p.Tracks))
	for _, t := range p.Tracks {
		tracks = append(tracks, map[string]any{
			"position": t.Position, "record": recordView(t.Record), "added_in_rev": t.AddedInRev,
		})
	}
	return map[string]any{
		"id": p.ID, "title": p.Title, "description": p.Description,
		"owner_id": p.OwnerID, "revision": p.CurrentRev, "tracks": tracks,
	}
}

// ------------------------------------------------------------------ utils --

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		problem(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}

func intParam(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// problem returns a generic, safe message. §6: never leak internal detail.
func problem(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

// internal logs the real cause and tells the client nothing about it.
func internal(w http.ResponseWriter, op string, err error) {
	slog.Error("request failed", "op", op, "err", err)
	problem(w, http.StatusInternalServerError, "something went wrong")
}
