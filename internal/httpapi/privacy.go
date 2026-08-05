package httpapi

import (
	"net/http"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// exportMyData returns everything of the caller's, in machine-readable form
// (F25, GDPR Art. 15 and 20).
//
// "Everything of theirs" is the account, the playlists they own, their
// annotations and their crate — not the shared catalogue, which is not
// personal data and belongs to the instance.
//
// Secrets are deliberately absent. A data export is a file that ends up in
// downloads folders and email attachments; a refresh token in it would be a
// standing grant to somebody's music library sitting in plain text (§6).
func (a *API) exportMyData(w http.ResponseWriter, r *http.Request, u *domain.User) {
	ctx := r.Context()

	playlists, err := a.Store.Playlists().ListOwned(ctx, u.ID)
	if err != nil {
		internal(w, "export playlists", err)
		return
	}
	pls := make([]map[string]any, 0, len(playlists))
	for i := range playlists {
		v := playlistView(&playlists[i])
		// Annotations travel with the playlist they are about, which is how a
		// person would expect to read their own export.
		if tags, err := a.Store.Annotations().Tags(ctx, playlists[i].ID, u.ID); err == nil {
			v["my_tags"] = tagViews(onlyMine(tags))
		}
		pls = append(pls, v)
	}

	crate, err := a.Store.Crates().List(ctx, u.ID)
	if err != nil {
		internal(w, "export crate", err)
		return
	}

	jobs, err := a.Store.Jobs().ListForUser(ctx, u.ID, 100)
	if err != nil {
		internal(w, "export jobs", err)
		return
	}
	jobViews := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		jobViews = append(jobViews, jobView(&jobs[i]))
	}

	w.Header().Set("Content-Disposition", `attachment; filename="waxgrove-export.json"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"exported_at": nowRFC3339(),
		"account":     userView(u),
		"playlists":   pls,
		"crate":       crateView(crate),
		"jobs":        jobViews,
		"note": "Provider credentials and session tokens are deliberately " +
			"excluded: they are secrets, and an export is a file that travels.",
	})
}

func onlyMine(tags []domain.Tag) []domain.Tag {
	out := make([]domain.Tag, 0, len(tags))
	for _, t := range tags {
		if t.Mine {
			out = append(out, t)
		}
	}
	return out
}

// eraseMe deletes the caller's account (F26, GDPR Art. 17).
//
// The three-way split is in the store: what is only about this user is deleted,
// their attribution is anonymised, and the shared catalogue is left alone. That
// last part is why erasure does not simply cascade — a song somebody added is
// not personal data, and deleting it would silently break other people's
// playlists (§6.1, BR-4).
func (a *API) eraseMe(w http.ResponseWriter, r *http.Request, u *domain.User) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	// Typed confirmation, because this is not undoable and a mis-click should
	// not be able to reach it.
	if in.Confirm != u.Email {
		problem(w, http.StatusBadRequest,
			"type your email address to confirm — this cannot be undone")
		return
	}
	if err := a.Store.Users().Erase(r.Context(), u.ID); err != nil {
		internal(w, "erase account", err)
		return
	}
	a.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}
