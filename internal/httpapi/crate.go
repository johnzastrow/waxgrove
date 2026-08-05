package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
)

// mountCrate registers the crate and annotation routes (F16, F12, F18).
func (a *API) mountCrate(mux *http.ServeMux) {
	mux.Handle("GET /api/crate", a.authed(a.crateList))
	mux.Handle("POST /api/crate", a.authed(a.crateAdd))
	mux.Handle("POST /api/crate/paste", a.authed(a.cratePaste))
	mux.Handle("POST /api/crate/{id}/resolve", a.authed(a.crateResolve))
	mux.Handle("DELETE /api/crate/{id}", a.authed(a.crateRemove))
	mux.Handle("DELETE /api/crate", a.authed(a.crateClear))
	mux.Handle("POST /api/crate/commit", a.authed(a.crateCommit))

	mux.Handle("GET /api/playlists/{id}/annotations", a.authed(a.annotations))
	mux.Handle("PUT /api/playlists/{id}/rating", a.authed(a.rate))
	mux.Handle("DELETE /api/playlists/{id}/rating", a.authed(a.unrate))
	mux.Handle("POST /api/playlists/{id}/tags", a.authed(a.addTag))
	mux.Handle("DELETE /api/tags/{id}", a.authed(a.removeTag))
	mux.Handle("POST /api/playlists/{id}/comments", a.authed(a.addComment))
	mux.Handle("DELETE /api/comments/{id}", a.authed(a.deleteComment))

	mux.Handle("GET /api/shared", a.authed(a.sharedPlaylists))

	mux.Handle("GET /api/me/export", a.authed(a.exportMyData))
	mux.Handle("DELETE /api/me", a.authed(a.eraseMe))
}

// ------------------------------------------------------------------- crate --

func (a *API) crateList(w http.ResponseWriter, r *http.Request, u *domain.User) {
	items, err := a.Store.Crates().List(r.Context(), u.ID)
	if err != nil {
		internal(w, "crate list", err)
		return
	}
	writeJSON(w, http.StatusOK, crateView(items))
}

// crateAdd stages candidates, running each down the resolution ladder first.
//
// Resolution happens on the way in so the crate can show what needs a decision
// without re-resolving on every read — and so a low-confidence match is
// surfaced now rather than after a playlist exists (F12).
func (a *API) crateAdd(w http.ResponseWriter, r *http.Request, u *domain.User) {
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
	if len(in.Candidates) > MaxCrateAdd {
		problem(w, http.StatusBadRequest, "too many at once — nothing is unlimited")
		return
	}
	a.stage(w, r, u, in.Candidates)
}

// MaxCrateAdd bounds one request. A paste of a thousand lines is a job, not a
// request; §6 says nothing is unlimited.
const MaxCrateAdd = 200

// cratePaste turns free text into staged candidates.
//
// This is the "a pasted list of Artist — Title lines" source from §3.3, and it
// is the one that produces the most ambiguity — which is exactly why it lands
// in the crate rather than straight into a playlist.
func (a *API) cratePaste(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	candidates := ParsePastedList(in.Text)
	if len(candidates) == 0 {
		problem(w, http.StatusBadRequest,
			"nothing there looked like a song — one per line, ideally \"Artist — Title\"")
		return
	}
	if len(candidates) > MaxCrateAdd {
		candidates = candidates[:MaxCrateAdd]
	}
	a.stage(w, r, u, candidates)
}

func (a *API) stage(w http.ResponseWriter, r *http.Request, u *domain.User, cands []domain.Candidate) {
	for _, c := range cands {
		// Deliberately not aborting the batch on one failure: a pasted list is
		// mostly good lines with a couple of odd ones, and losing the good ones
		// to a bad one is the wrong trade.
		m, err := a.Resolver.Resolve(r.Context(), c, domain.TierCurated)
		if err != nil {
			m = domain.Match{}
		}
		if _, err := a.Store.Crates().Add(r.Context(), u.ID, c, m); err != nil {
			internal(w, "crate add", err)
			return
		}
	}
	items, err := a.Store.Crates().List(r.Context(), u.ID)
	if err != nil {
		internal(w, "crate list", err)
		return
	}
	writeJSON(w, http.StatusOK, crateView(items))
}

// crateResolve settles one item against the record the user chose (F12).
func (a *API) crateResolve(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		RecordID  string            `json:"record_id"`
		Candidate *domain.Candidate `json:"candidate"`
	}
	if !decode(w, r, &in) {
		return
	}

	recordID := in.RecordID
	if recordID == "" && in.Candidate != nil {
		// The user picked an alternative that is not in the catalogue yet, so
		// it is created now — a deliberate choice, hence curated (D11).
		rec, err := a.Store.Records().Upsert(r.Context(), *in.Candidate, domain.TierCurated)
		if err != nil {
			internal(w, "crate resolve upsert", err)
			return
		}
		recordID = rec.ID
	}
	if recordID == "" {
		problem(w, http.StatusBadRequest, "either record_id or candidate is required")
		return
	}

	item, err := a.Store.Crates().Resolve(r.Context(), u.ID, r.PathValue("id"), recordID)
	if errors.Is(err, sqlite.ErrCrateItemNotFound) {
		problem(w, http.StatusNotFound, "that item is no longer in your crate")
		return
	}
	if err != nil {
		internal(w, "crate resolve", err)
		return
	}
	writeJSON(w, http.StatusOK, crateItemView(*item))
}

func (a *API) crateRemove(w http.ResponseWriter, r *http.Request, u *domain.User) {
	err := a.Store.Crates().Remove(r.Context(), u.ID, r.PathValue("id"))
	if errors.Is(err, sqlite.ErrCrateItemNotFound) {
		problem(w, http.StatusNotFound, "that item is no longer in your crate")
		return
	}
	if err != nil {
		internal(w, "crate remove", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) crateClear(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if err := a.Store.Crates().Clear(r.Context(), u.ID); err != nil {
		internal(w, "crate clear", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// crateCommit turns the resolved part of the crate into a playlist.
func (a *API) crateCommit(w http.ResponseWriter, r *http.Request, u *domain.User) {
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

	playlist, left, err := a.Store.Crates().Commit(r.Context(), u.ID,
		strings.TrimSpace(in.Title), in.Description)
	if errors.Is(err, sqlite.ErrNothingToCommit) {
		problem(w, http.StatusPreconditionFailed,
			"nothing in your crate is resolved yet — settle the matches first")
		return
	}
	if err != nil {
		internal(w, "crate commit", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"playlist": playlistView(playlist),
		// What stayed behind, so the UI can say so rather than leave the user
		// to notice their crate is not empty.
		"left_in_crate": left,
	})
}

// ------------------------------------------------------------- annotations --

func (a *API) annotations(w http.ResponseWriter, r *http.Request, u *domain.User) {
	id := r.PathValue("id")
	// Any member may annotate a playlist shared with them (D8), but it has to
	// exist — otherwise this is a way to probe for ids.
	if _, err := a.Store.Playlists().Get(r.Context(), id); err != nil {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	writeJSON(w, http.StatusOK, a.annotationPayload(w, r, u, id))
}

func (a *API) annotationPayload(w http.ResponseWriter, r *http.Request, u *domain.User, id string) map[string]any {
	ratings, err := a.Store.Annotations().Ratings(r.Context(), id, u.ID)
	if err != nil {
		internal(w, "ratings", err)
		return nil
	}
	tags, err := a.Store.Annotations().Tags(r.Context(), id, u.ID)
	if err != nil {
		internal(w, "tags", err)
		return nil
	}
	comments, err := a.Store.Annotations().Comments(r.Context(), id, u.ID)
	if err != nil {
		internal(w, "comments", err)
		return nil
	}
	return map[string]any{
		"rating": map[string]any{
			"average": ratings.Average, "count": ratings.Count, "mine": ratings.Mine,
		},
		"tags":     tagViews(tags),
		"comments": commentViews(comments),
	}
}

func (a *API) rate(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Value int `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if _, err := a.Store.Playlists().Get(r.Context(), id); err != nil {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	err := a.Store.Annotations().Rate(r.Context(), id, u.ID, in.Value)
	if errors.Is(err, sqlite.ErrBadRating) {
		problem(w, http.StatusBadRequest, "a rating is 1 to 5")
		return
	}
	if err != nil {
		internal(w, "rate", err)
		return
	}
	writeJSON(w, http.StatusOK, a.annotationPayload(w, r, u, id))
}

func (a *API) unrate(w http.ResponseWriter, r *http.Request, u *domain.User) {
	if err := a.Store.Annotations().Unrate(r.Context(), r.PathValue("id"), u.ID); err != nil {
		internal(w, "unrate", err)
		return
	}
	writeJSON(w, http.StatusOK, a.annotationPayload(w, r, u, r.PathValue("id")))
}

func (a *API) addTag(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if _, err := a.Store.Playlists().Get(r.Context(), id); err != nil {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	_, err := a.Store.Annotations().AddTag(r.Context(), id, u.ID, in.Name, in.Visibility)
	if errors.Is(err, sqlite.ErrEmptyText) {
		problem(w, http.StatusBadRequest, "a tag needs a name")
		return
	}
	if err != nil {
		internal(w, "add tag", err)
		return
	}
	writeJSON(w, http.StatusOK, a.annotationPayload(w, r, u, id))
}

func (a *API) removeTag(w http.ResponseWriter, r *http.Request, u *domain.User) {
	err := a.Store.Annotations().RemoveTag(r.Context(), r.PathValue("id"), u.ID)
	if errors.Is(err, sqlite.ErrNotYours) {
		// 404 rather than 403: the response should not confirm that somebody
		// else's tag exists (§6).
		problem(w, http.StatusNotFound, "no such tag of yours")
		return
	}
	if err != nil {
		internal(w, "remove tag", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) addComment(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if _, err := a.Store.Playlists().Get(r.Context(), id); err != nil {
		problem(w, http.StatusNotFound, "playlist not found")
		return
	}
	_, err := a.Store.Annotations().AddComment(r.Context(), id, u.ID, in.Body)
	if errors.Is(err, sqlite.ErrEmptyText) {
		problem(w, http.StatusBadRequest, "a comment needs something in it")
		return
	}
	if err != nil {
		internal(w, "add comment", err)
		return
	}
	writeJSON(w, http.StatusCreated, a.annotationPayload(w, r, u, id))
}

func (a *API) deleteComment(w http.ResponseWriter, r *http.Request, u *domain.User) {
	err := a.Store.Annotations().DeleteComment(r.Context(), r.PathValue("id"), u.ID)
	if errors.Is(err, sqlite.ErrNotYours) {
		problem(w, http.StatusNotFound, "no such comment of yours")
		return
	}
	if err != nil {
		internal(w, "delete comment", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// -------------------------------------------------------------- discovery --

// sharedPlaylists lists everyone else's playlists (F9).
//
// Playlists are shared by reference across the instance (D8), so "shared" is
// simply everything not yours. There is no per-playlist sharing toggle because
// there is no private playlist — an instance is a group of friends, and a
// visibility model nobody asked for would be a feature to maintain forever.
func (a *API) sharedPlaylists(w http.ResponseWriter, r *http.Request, u *domain.User) {
	pls, err := a.Store.Playlists().ListShared(r.Context(), u.ID, intParam(r, "limit", 50))
	if err != nil {
		internal(w, "shared playlists", err)
		return
	}
	out := make([]map[string]any, 0, len(pls))
	for i := range pls {
		v := playlistSummary(&pls[i])
		if rt, err := a.Store.Annotations().Ratings(r.Context(), pls[i].ID, u.ID); err == nil {
			v["rating"] = map[string]any{
				"average": rt.Average, "count": rt.Count, "mine": rt.Mine,
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": out})
}

// ------------------------------------------------------------------- views --

func crateView(items []domain.CrateItem) map[string]any {
	out := make([]map[string]any, 0, len(items))
	needs := 0
	for _, it := range items {
		if it.NeedsDecision() {
			needs++
		}
		out = append(out, crateItemView(it))
	}
	return map[string]any{
		"items": out, "total": len(items), "needs_decision": needs,
	}
}

func crateItemView(it domain.CrateItem) map[string]any {
	v := map[string]any{
		"id": it.ID, "position": it.Position, "status": it.Status,
		"candidate": it.Candidate, "method": it.Method, "confidence": it.Confidence,
		"source_ref": it.SourceRef,
	}
	if it.Record != nil {
		v["record"] = recordView(*it.Record)
	}
	return v
}

func tagViews(tags []domain.Tag) []map[string]any {
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, map[string]any{
			"id": t.ID, "name": t.Name, "visibility": t.Visibility, "mine": t.Mine,
		})
	}
	return out
}

func commentViews(cs []domain.Comment) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{
			"id": c.ID, "body": c.Body, "author": c.Author,
			"mine": c.Mine, "created_at": c.CreatedAt,
		})
	}
	return out
}

// playlistSummary omits the tracks. A discovery list of fifty playlists does
// not need every track of every one of them serialised into it.
func playlistSummary(p *domain.Playlist) map[string]any {
	return map[string]any{
		"id": p.ID, "title": p.Title, "description": p.Description,
		"owner_id": p.OwnerID, "owner": p.OwnerName, "revision": p.CurrentRev,
		"track_count": len(p.Tracks),
	}
}
