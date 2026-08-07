package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func signedIn(t *testing.T) *client {
	t.Helper()
	c := newAPI(t)
	c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana", "password": goodPassword,
	})
	return c
}

// second returns another member on the same instance, sharing its mux.
func second(t *testing.T, c *client) *client {
	t.Helper()
	invite := c.mustJSON(c.do("POST", "/api/invites", nil))["code"].(string)
	other := &client{t: t, h: c.h}
	other.do("POST", "/api/register", map[string]any{
		"email": "ben@example.test", "display_name": "Ben",
		"password": goodPassword, "invite_code": invite,
	})
	return other
}

func crate(t *testing.T, c *client) map[string]any {
	t.Helper()
	return c.mustJSON(c.do("GET", "/api/crate", nil))
}

// -------------------------------------------------------------- the crate --

func TestCrateStagesAndCommits(t *testing.T) {
	c := signedIn(t)

	rec := c.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{
			{"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498"},
			{"title": "Dreams", "artist": "Fleetwood Mac", "isrc": "USWB10101368"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("stage: %d %s", rec.Code, rec.Body)
	}
	body := c.mustJSON(rec)
	if got := body["total"].(float64); got != 2 {
		t.Fatalf("staged %v items, want 2", got)
	}
	if got := body["needs_decision"].(float64); got != 0 {
		t.Errorf("%v items need a decision; both carry an ISRC and should resolve", got)
	}

	rec = c.do("POST", "/api/crate/commit", map[string]any{"title": "Sunday morning"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body)
	}
	out := c.mustJSON(rec)
	pl := out["playlist"].(map[string]any)
	if len(pl["tracks"].([]any)) != 2 {
		t.Errorf("committed playlist has %d tracks, want 2", len(pl["tracks"].([]any)))
	}
	// One revision for the whole commit, not one per song (§3.3).
	if rev := pl["revision"].(float64); rev != 2 {
		t.Errorf("revision = %v, want 2 (create, then one add)", rev)
	}
	// The crate empties of what was committed.
	if got := crate(t, c)["total"].(float64); got != 0 {
		t.Errorf("%v items left in the crate after committing everything", got)
	}
}

// The crate's central promise: an item that cannot be identified stays put
// rather than vanishing (BR-5).
func TestUnresolvedItemsSurviveACommit(t *testing.T) {
	c := signedIn(t)

	c.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{
			{"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498"},
			{"title": "whatever that one was", "artist": "??"},
		},
	})
	body := crate(t, c)
	if got := body["needs_decision"].(float64); got != 1 {
		t.Fatalf("%v items need a decision, want 1", got)
	}

	out := c.mustJSON(c.do("POST", "/api/crate/commit", map[string]any{"title": "Partial"}))
	if got := out["left_in_crate"].(float64); got != 1 {
		t.Errorf("left_in_crate = %v, want 1", got)
	}
	after := crate(t, c)
	if got := after["total"].(float64); got != 1 {
		t.Fatalf("crate has %v items after commit, want the unresolved one to remain", got)
	}
	item := after["items"].([]any)[0].(map[string]any)
	if item["status"] == "resolved" {
		t.Error("the unresolved item was quietly marked resolved")
	}
	// The original text is preserved so the user can still see what they asked for.
	cand := item["candidate"].(map[string]any)
	if cand["title"] != "whatever that one was" {
		t.Errorf("the candidate text was lost: %v", cand)
	}
}

// Committing with nothing resolved must refuse rather than make an empty
// playlist the user then has to delete.
func TestCommitRefusesWhenNothingIsResolved(t *testing.T) {
	c := signedIn(t)
	c.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{{"title": "mystery", "artist": "??"}},
	})
	rec := c.do("POST", "/api/crate/commit", map[string]any{"title": "Nope"})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("commit = %d, want 412: %s", rec.Code, rec.Body)
	}
	if pls := c.mustJSON(c.do("GET", "/api/playlists", nil))["playlists"].([]any); len(pls) != 0 {
		t.Errorf("%d playlists were created by a refused commit", len(pls))
	}
}

// F12: the user picks, and the choice is recorded as a choice rather than as a
// score, so a later audit can tell decisions from guesses.
func TestResolvingAnItemRecordsThatAHumanChose(t *testing.T) {
	c := signedIn(t)
	c.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{{"title": "mystery", "artist": "??"}},
	})
	item := crate(t, c)["items"].([]any)[0].(map[string]any)

	rec := c.do("POST", "/api/crate/"+item["id"].(string)+"/resolve", map[string]any{
		"candidate": map[string]any{
			"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	got := c.mustJSON(rec)
	if got["status"] != "resolved" {
		t.Errorf("status = %v, want resolved", got["status"])
	}
	if got["method"] != "chosen" {
		t.Errorf("method = %v, want chosen — a human decided this", got["method"])
	}
	if got["record"] == nil {
		t.Error("no record attached after resolving")
	}
}

// A crate belongs to one user. Another member must not be able to reach into it.
func TestCratesAreNotSharedBetweenUsers(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)

	ana.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{
			{"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498"},
		},
	})
	if got := crate(t, ben)["total"].(float64); got != 0 {
		t.Errorf("Ben sees %v items from Ana's crate", got)
	}

	item := crate(t, ana)["items"].([]any)[0].(map[string]any)
	if rec := ben.do("DELETE", "/api/crate/"+item["id"].(string), nil); rec.Code != http.StatusNotFound {
		t.Errorf("Ben deleting Ana's crate item = %d, want 404", rec.Code)
	}
	if got := crate(t, ana)["total"].(float64); got != 1 {
		t.Error("Ana's item was removed by somebody else")
	}
}

func TestPasteStagesLines(t *testing.T) {
	c := signedIn(t)
	rec := c.do("POST", "/api/crate/paste", map[string]any{
		"text": "Nick Drake — Pink Moon\n2. Fleetwood Mac - Dreams\n\n   \nQueen | Under Pressure",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("paste: %d %s", rec.Code, rec.Body)
	}
	if got := c.mustJSON(rec)["total"].(float64); got != 3 {
		t.Errorf("staged %v, want 3 (blank lines skipped)", got)
	}
}

func TestPasteRejectsNothing(t *testing.T) {
	c := signedIn(t)
	if rec := c.do("POST", "/api/crate/paste", map[string]any{"text": "  \n\n "}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty paste = %d, want 400", rec.Code)
	}
}

// ------------------------------------------------------------ annotations --

func TestRatingIsPerUserWithAnAggregate(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)
	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "Rate me"}))["id"].(string)

	ana.do("PUT", "/api/playlists/"+pid+"/rating", map[string]any{"value": 5})
	body := ben.mustJSON(ben.do("PUT", "/api/playlists/"+pid+"/rating", map[string]any{"value": 3}))

	rating := body["rating"].(map[string]any)
	if rating["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", rating["count"])
	}
	if avg := rating["average"].(float64); avg < 3.9 || avg > 4.1 {
		t.Errorf("average = %v, want 4", avg)
	}
	// Ben's own rating, not Ana's — one user's opinion never overwrites another's.
	if rating["mine"].(float64) != 3 {
		t.Errorf("mine = %v, want Ben's own 3", rating["mine"])
	}
	anaSees := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid+"/annotations", nil))
	if anaSees["rating"].(map[string]any)["mine"].(float64) != 5 {
		t.Error("Ana's own rating changed when Ben rated it")
	}
}

func TestRatingRejectsOutOfRange(t *testing.T) {
	c := signedIn(t)
	pid := c.mustJSON(c.do("POST", "/api/playlists", map[string]any{"title": "P"}))["id"].(string)
	for _, v := range []int{0, 6, -1, 99} {
		if rec := c.do("PUT", "/api/playlists/"+pid+"/rating",
			map[string]any{"value": v}); rec.Code != http.StatusBadRequest {
			t.Errorf("rating %d = %d, want 400", v, rec.Code)
		}
	}
}

// The whole meaning of a private tag. If this leaks, the feature is a lie.
func TestPrivateTagsAreInvisibleToOthers(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)
	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "Tag me"}))["id"].(string)

	ana.do("POST", "/api/playlists/"+pid+"/tags",
		map[string]any{"name": "Secret Shame", "visibility": "private"})
	ana.do("POST", "/api/playlists/"+pid+"/tags",
		map[string]any{"name": "Late Night", "visibility": "shared"})

	seen := ben.mustJSON(ben.do("GET", "/api/playlists/"+pid+"/annotations", nil))
	raw := ben.do("GET", "/api/playlists/"+pid+"/annotations", nil).Body.String()
	if strings.Contains(strings.ToLower(raw), "secret shame") {
		t.Fatalf("Ana's private tag leaked to Ben: %s", raw)
	}
	tags := seen["tags"].([]any)
	if len(tags) != 1 {
		t.Fatalf("Ben sees %d tags, want only the shared one", len(tags))
	}
	if tags[0].(map[string]any)["name"] != "late night" {
		t.Errorf("tag = %v", tags[0])
	}
	// Ana still sees both of hers.
	if got := len(ana.mustJSON(ana.do("GET", "/api/playlists/"+pid+"/annotations", nil))["tags"].([]any)); got != 2 {
		t.Errorf("Ana sees %d of her own tags, want 2", got)
	}
}

func TestTagsAreNormalisedAndIdempotent(t *testing.T) {
	c := signedIn(t)
	pid := c.mustJSON(c.do("POST", "/api/playlists", map[string]any{"title": "P"}))["id"].(string)

	for _, name := range []string{"Late Night", "late  night", "LATE NIGHT "} {
		c.do("POST", "/api/playlists/"+pid+"/tags",
			map[string]any{"name": name, "visibility": "shared"})
	}
	tags := c.mustJSON(c.do("GET", "/api/playlists/"+pid+"/annotations", nil))["tags"].([]any)
	if len(tags) != 1 {
		t.Errorf("got %d tags, want 1 — these are the same tag typed three ways", len(tags))
	}
}

func TestCannotRemoveSomebodyElsesTag(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)
	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "P"}))["id"].(string)

	ana.do("POST", "/api/playlists/"+pid+"/tags",
		map[string]any{"name": "mellow", "visibility": "shared"})
	tags := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid+"/annotations", nil))["tags"].([]any)
	tagID := tags[0].(map[string]any)["id"].(string)

	if rec := ben.do("DELETE", "/api/tags/"+tagID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("Ben removing Ana's tag = %d, want 404", rec.Code)
	}
}

func TestCommentsAndDeletion(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)
	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "Discuss"}))["id"].(string)

	ben.do("POST", "/api/playlists/"+pid+"/comments", map[string]any{"body": "track 3 is the one"})
	body := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid+"/annotations", nil))
	comments := body["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	first := comments[0].(map[string]any)
	if first["author"] != "Ben" {
		t.Errorf("author = %v, want the display name", first["author"])
	}
	if first["mine"] != false {
		t.Error("Ben's comment is marked as Ana's")
	}

	// Ana cannot delete Ben's comment.
	if rec := ana.do("DELETE", "/api/comments/"+first["id"].(string), nil); rec.Code != http.StatusNotFound {
		t.Errorf("Ana deleting Ben's comment = %d, want 404", rec.Code)
	}
	// Ben can.
	if rec := ben.do("DELETE", "/api/comments/"+first["id"].(string), nil); rec.Code != http.StatusNoContent {
		t.Errorf("Ben deleting his own comment = %d, want 204", rec.Code)
	}
	after := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid+"/annotations", nil))
	if got := len(after["comments"].([]any)); got != 0 {
		t.Errorf("%d comments after deletion, want 0", got)
	}
}

func TestEmptyCommentsAndTagsAreRejected(t *testing.T) {
	c := signedIn(t)
	pid := c.mustJSON(c.do("POST", "/api/playlists", map[string]any{"title": "P"}))["id"].(string)
	if rec := c.do("POST", "/api/playlists/"+pid+"/comments",
		map[string]any{"body": "   "}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty comment = %d, want 400", rec.Code)
	}
	if rec := c.do("POST", "/api/playlists/"+pid+"/tags",
		map[string]any{"name": " ", "visibility": "shared"}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty tag = %d, want 400", rec.Code)
	}
}

// BR-3, the rule the whole annotation model rests on: annotating a playlist
// must not touch its content history. A rating that bumped the revision would
// make every exported copy look out of date.
func TestAnnotationsNeverProduceARevision(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)
	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "Untouched"}))["id"].(string)

	before := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid, nil))["revision"].(float64)
	beforeHistory := len(ana.mustJSON(
		ana.do("GET", "/api/playlists/"+pid+"/history", nil))["revisions"].([]any))

	ben.do("PUT", "/api/playlists/"+pid+"/rating", map[string]any{"value": 4})
	ben.do("POST", "/api/playlists/"+pid+"/tags", map[string]any{"name": "great", "visibility": "shared"})
	ben.do("POST", "/api/playlists/"+pid+"/comments", map[string]any{"body": "lovely"})

	after := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid, nil))["revision"].(float64)
	afterHistory := len(ana.mustJSON(
		ana.do("GET", "/api/playlists/"+pid+"/history", nil))["revisions"].([]any))

	if after != before {
		t.Errorf("revision moved %v -> %v because of annotations (BR-3)", before, after)
	}
	if afterHistory != beforeHistory {
		t.Errorf("history grew from %d to %d entries because of annotations (BR-3)",
			beforeHistory, afterHistory)
	}
}

// -------------------------------------------------------------- discovery --

func TestSharedListsOtherPeoplesPlaylists(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)

	ana.do("POST", "/api/playlists", map[string]any{"title": "Ana's mix"})
	ben.do("POST", "/api/playlists", map[string]any{"title": "Ben's mix"})

	seen := ben.mustJSON(ben.do("GET", "/api/shared", nil))["playlists"].([]any)
	if len(seen) != 1 {
		t.Fatalf("Ben sees %d shared playlists, want just Ana's", len(seen))
	}
	p := seen[0].(map[string]any)
	if p["title"] != "Ana's mix" {
		t.Errorf("title = %v", p["title"])
	}
	if p["owner"] != "Ana" {
		t.Errorf("owner = %v, want the display name", p["owner"])
	}
	// A member may open a playlist shared with them (D8).
	if rec := ben.do("GET", "/api/playlists/"+p["id"].(string), nil); rec.Code != http.StatusOK {
		t.Errorf("Ben opening Ana's playlist = %d, want 200", rec.Code)
	}
}

// ---------------------------------------------------------------- privacy --

func TestExportContainsMyDataAndNoSecrets(t *testing.T) {
	c := signedIn(t)
	c.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{
			{"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498"},
		},
	})
	c.do("POST", "/api/crate/commit", map[string]any{"title": "Mine"})

	rec := c.do("GET", "/api/me/export", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
	body := c.mustJSON(rec)
	if body["account"] == nil || body["playlists"] == nil || body["crate"] == nil {
		t.Fatalf("export is missing sections: %v", body)
	}
	if len(body["playlists"].([]any)) != 1 {
		t.Errorf("export has %d playlists, want 1", len(body["playlists"].([]any)))
	}
	// An export travels. Nothing that grants access may be in it (§6).
	raw := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"password_hash", "refresh_token", "client_secret", "access_token"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the export contains %q", forbidden)
		}
	}
}

func TestErasureNeedsTypedConfirmation(t *testing.T) {
	c := signedIn(t)
	if rec := c.do("DELETE", "/api/me", map[string]any{"confirm": "yes"}); rec.Code != http.StatusBadRequest {
		t.Errorf("erase with a wrong confirmation = %d, want 400", rec.Code)
	}
	// Still signed in and intact.
	if rec := c.do("GET", "/api/me", nil); rec.Code != http.StatusOK {
		t.Errorf("the account was affected by a refused erasure: %d", rec.Code)
	}
}

// The GDPR three-way split, end to end: what is only about the user goes, their
// attribution is anonymised, and the shared catalogue is untouched (§6.1, BR-4).
func TestErasureRemovesTheUserButNotTheCatalogue(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)

	ana.do("POST", "/api/crate", map[string]any{
		"candidates": []map[string]any{
			{"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498"},
		},
	})
	ana.do("POST", "/api/crate/commit", map[string]any{"title": "Ana's mix"})

	rec := ana.do("DELETE", "/api/me", map[string]any{"confirm": "ana@example.test"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("erase: %d %s", rec.Code, rec.Body)
	}

	// The song Ana contributed stays: it is not personal data, and deleting it
	// would break everyone else's playlists.
	found := ben.mustJSON(ben.do("GET", "/api/records?q=Pink", nil))["records"].([]any)
	if len(found) != 1 {
		t.Errorf("the shared catalogue lost a song when a user was erased: %v", found)
	}
	// Ana's session is gone.
	if rec := ana.do("GET", "/api/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("an erased user's session still works: %d", rec.Code)
	}
}

// ----------------------------------------------------------------- forking --

// F20: a fork is a real playlist of your own, with a pointer back to where it
// came from — without which it is indistinguishable from something you made.
func TestForkCopiesAPlaylistWithProvenance(t *testing.T) {
	ana := signedIn(t)
	ben := second(t, ana)

	pid := ana.mustJSON(ana.do("POST", "/api/playlists",
		map[string]any{"title": "Ana's mix"}))["id"].(string)
	ana.do("POST", "/api/playlists/"+pid+"/tracks", map[string]any{
		"candidates": []map[string]any{
			{"title": "Pink Moon", "artist": "Nick Drake", "isrc": "GBAYE0601498"},
			{"title": "Dreams", "artist": "Fleetwood Mac", "isrc": "USWB10101368"},
		},
	})

	rec := ben.do("POST", "/api/playlists/"+pid+"/fork", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("fork: %d %s", rec.Code, rec.Body)
	}
	fork := ben.mustJSON(rec)
	forkID := fork["id"].(string)

	if len(fork["tracks"].([]any)) != 2 {
		t.Errorf("the fork has %d tracks, want 2", len(fork["tracks"].([]any)))
	}
	// One revision for the whole fork: copying a playlist is one act.
	if rev := fork["revision"].(float64); rev != 2 {
		t.Errorf("revision = %v, want 2 (create, then one add)", rev)
	}

	full := ben.mustJSON(ben.do("GET", "/api/playlists/"+forkID, nil))
	src, ok := full["forked_from"].(map[string]any)
	if !ok {
		t.Fatalf("the fork does not say where it came from: %v", full)
	}
	if src["id"] != pid || src["owner"] != "Ana" {
		t.Errorf("provenance = %v, want Ana's playlist", src)
	}

	// It is Ben's now: he can change it, and Ana's is untouched.
	if rec := ben.do("PATCH", "/api/playlists/"+forkID,
		map[string]any{"title": "Ben's take"}); rec.Code != http.StatusOK {
		t.Errorf("Ben renaming his own fork = %d, want 200", rec.Code)
	}
	if got := ana.mustJSON(ana.do("GET", "/api/playlists/"+pid, nil))["title"]; got != "Ana's mix" {
		t.Errorf("Ana's playlist changed when Ben forked and renamed it: %v", got)
	}
}

// A plain playlist must not claim provenance it does not have.
func TestAPlainPlaylistHasNoForkProvenance(t *testing.T) {
	c := signedIn(t)
	pid := c.mustJSON(c.do("POST", "/api/playlists", map[string]any{"title": "Mine"}))["id"].(string)
	if got := c.mustJSON(c.do("GET", "/api/playlists/"+pid, nil))["forked_from"]; got != nil {
		t.Errorf("forked_from = %v, want absent", got)
	}
}

// ------------------------------------------------------- changing password --

func TestChangePasswordAndSignInWithTheNewOne(t *testing.T) {
	c := signedIn(t)
	const newPassword = "a-much-better-passphrase"

	rec := c.do("POST", "/api/me/password", map[string]any{
		"current_password": goodPassword, "new_password": newPassword,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body)
	}

	// The change ends every session, including this one.
	if got := c.do("GET", "/api/me", nil); got.Code != http.StatusUnauthorized {
		t.Errorf("still signed in after changing the password: %d", got.Code)
	}
	// The old password is dead.
	if got := c.do("POST", "/api/login", map[string]any{
		"email": "ana@example.test", "password": goodPassword,
	}); got.Code != http.StatusUnauthorized {
		t.Errorf("the old password still works: %d", got.Code)
	}
	// The new one works.
	if got := c.do("POST", "/api/login", map[string]any{
		"email": "ana@example.test", "password": newPassword,
	}); got.Code != http.StatusOK {
		t.Fatalf("cannot sign in with the new password: %d %s", got.Code, got.Body)
	}
}

// A valid session is not enough. A password change is exactly what somebody
// does with a borrowed laptop to make their access permanent.
func TestChangePasswordRequiresTheCurrentOne(t *testing.T) {
	c := signedIn(t)
	rec := c.do("POST", "/api/me/password", map[string]any{
		"current_password": "not-the-right-one", "new_password": "a-much-better-passphrase",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("= %d, want 403", rec.Code)
	}
	// Nothing changed, and the session survives a failed attempt.
	if got := c.do("GET", "/api/me", nil); got.Code != http.StatusOK {
		t.Errorf("a failed attempt ended the session: %d", got.Code)
	}
	if got := c.do("POST", "/api/login", map[string]any{
		"email": "ana@example.test", "password": goodPassword,
	}); got.Code != http.StatusOK {
		t.Error("the existing password stopped working after a failed change")
	}
}

func TestChangePasswordEnforcesTheLengthFloor(t *testing.T) {
	c := signedIn(t)
	rec := c.do("POST", "/api/me/password", map[string]any{
		"current_password": goodPassword, "new_password": "short",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("= %d, want 400", rec.Code)
	}
	if got := c.do("GET", "/api/me", nil); got.Code != http.StatusOK {
		t.Error("a rejected change ended the session")
	}
}

// Changing a password must log out the other browser too — otherwise, if the
// reason for changing it is that somebody else has it, the change is cosmetic.
func TestChangePasswordEndsEveryOtherSession(t *testing.T) {
	c := signedIn(t)

	// A second sign-in for the same account, standing in for another device.
	otherDevice := &client{t: t, h: c.h}
	if rec := otherDevice.do("POST", "/api/login", map[string]any{
		"email": "ana@example.test", "password": goodPassword,
	}); rec.Code != http.StatusOK {
		t.Fatalf("second sign-in: %d", rec.Code)
	}
	if rec := otherDevice.do("GET", "/api/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("second session not usable: %d", rec.Code)
	}

	if rec := c.do("POST", "/api/me/password", map[string]any{
		"current_password": goodPassword, "new_password": "a-much-better-passphrase",
	}); rec.Code != http.StatusNoContent {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}

	if rec := otherDevice.do("GET", "/api/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("the other device is still signed in: %d", rec.Code)
	}
}

// ------------------------------------------------------- first account UX --

// The registration form cannot otherwise know whether to ask for an invite
// code, and a form that demands one on an unclaimed instance locks the operator
// out of the thing they just installed. That shipped once; this stops it again.
func TestInstanceReportsWhetherAnInviteIsNeeded(t *testing.T) {
	c := newAPI(t)

	// Unclaimed: no code needed, and the endpoint is readable without auth.
	rec := c.do("GET", "/api/instance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("instance = %d, want 200 without signing in", rec.Code)
	}
	before := c.mustJSON(rec)
	if before["invite_required"] != false || before["first_account"] != true {
		t.Fatalf("unclaimed instance reports %v, want no invite required", before)
	}

	// The first account really does register with an empty code.
	if got := c.do("POST", "/api/register", map[string]any{
		"email": "ana@example.test", "display_name": "Ana",
		"password": goodPassword, "invite_code": "",
	}); got.Code != http.StatusOK {
		t.Fatalf("first registration with no code = %d: %s", got.Code, got.Body)
	}

	// Claimed: a code is now required, and the form is told so.
	after := c.mustJSON(c.do("GET", "/api/instance", nil))
	if after["invite_required"] != true || after["first_account"] != false {
		t.Errorf("claimed instance reports %v, want an invite required", after)
	}

	// And the server enforces it, not just the form.
	if got := c.do("POST", "/api/register", map[string]any{
		"email": "ben@example.test", "display_name": "Ben",
		"password": goodPassword, "invite_code": "",
	}); got.Code != http.StatusForbidden {
		t.Errorf("second registration with no code = %d, want 403", got.Code)
	}
}
