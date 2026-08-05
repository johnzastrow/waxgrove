package httpapi

import (
	"net/http"
	"testing"
)

// seedPlaylist returns a playlist id holding three resolvable tracks, in order.
func seedPlaylist(t *testing.T, c *client) (string, []string) {
	t.Helper()
	c.do("POST", "/api/register", map[string]any{
		"email": "owner@example.test", "display_name": "Owner", "password": goodPassword,
	})
	pid := c.mustJSON(c.do("POST", "/api/playlists",
		map[string]any{"title": "Original title"}))["id"].(string)

	rec := c.do("POST", "/api/playlists/"+pid+"/tracks", map[string]any{
		"candidates": []map[string]any{
			{"title": "One", "artist": "A", "isrc": "AA00000000001"},
			{"title": "Two", "artist": "B", "isrc": "AA00000000002"},
			{"title": "Three", "artist": "C", "isrc": "AA00000000003"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("seed tracks: %d %s", rec.Code, rec.Body)
	}
	return pid, trackIDs(t, c, pid)
}

func trackIDs(t *testing.T, c *client, pid string) []string {
	t.Helper()
	pl := c.mustJSON(c.do("GET", "/api/playlists/"+pid, nil))
	tracks, _ := pl["tracks"].([]any)
	out := make([]string, 0, len(tracks))
	for _, tr := range tracks {
		m := tr.(map[string]any)
		out = append(out, m["record"].(map[string]any)["id"].(string))
	}
	return out
}

func titlesInOrder(t *testing.T, c *client, pid string) []string {
	t.Helper()
	pl := c.mustJSON(c.do("GET", "/api/playlists/"+pid, nil))
	tracks, _ := pl["tracks"].([]any)
	out := make([]string, 0, len(tracks))
	for _, tr := range tracks {
		m := tr.(map[string]any)
		out = append(out, m["record"].(map[string]any)["title"].(string))
	}
	return out
}

func TestReorderRewritesPositions(t *testing.T) {
	c := newAPI(t)
	pid, ids := seedPlaylist(t, c)
	if len(ids) != 3 {
		t.Fatalf("seeded %d tracks, want 3", len(ids))
	}

	reversed := []string{ids[2], ids[1], ids[0]}
	rec := c.do("PUT", "/api/playlists/"+pid+"/tracks",
		map[string]any{"record_ids": reversed})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body)
	}

	got := titlesInOrder(t, c, pid)
	want := []string{"Three", "Two", "One"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d = %q, want %q (full order %v)", i, got[i], want[i], got)
		}
	}
}

// The invariant that makes the history trustworthy: a "reorder" that quietly
// dropped a track would be recorded as a reorder, which is a lie in an
// append-only log (F17, BR-3).
func TestReorderRejectsDroppedTrack(t *testing.T) {
	c := newAPI(t)
	pid, ids := seedPlaylist(t, c)

	rec := c.do("PUT", "/api/playlists/"+pid+"/tracks",
		map[string]any{"record_ids": []string{ids[0], ids[1]}}) // one short
	if rec.Code != http.StatusConflict {
		t.Fatalf("dropping a track = %d, want 409: %s", rec.Code, rec.Body)
	}
	if n := len(titlesInOrder(t, c, pid)); n != 3 {
		t.Errorf("playlist has %d tracks after a rejected reorder, want 3", n)
	}
}

func TestReorderRejectsForeignTrack(t *testing.T) {
	c := newAPI(t)
	pid, ids := seedPlaylist(t, c)

	rec := c.do("PUT", "/api/playlists/"+pid+"/tracks",
		map[string]any{"record_ids": []string{ids[0], ids[1], "some-other-record"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("substituting a track = %d, want 409: %s", rec.Code, rec.Body)
	}
	if got := titlesInOrder(t, c, pid); len(got) != 3 || got[2] != "Three" {
		t.Errorf("playlist changed after a rejected reorder: %v", got)
	}
}

func TestReorderRecordsARevision(t *testing.T) {
	c := newAPI(t)
	pid, ids := seedPlaylist(t, c)
	before := c.mustJSON(c.do("GET", "/api/playlists/"+pid, nil))["revision"].(float64)

	c.do("PUT", "/api/playlists/"+pid+"/tracks",
		map[string]any{"record_ids": []string{ids[1], ids[0], ids[2]}})

	after := c.mustJSON(c.do("GET", "/api/playlists/"+pid, nil))["revision"].(float64)
	if after != before+1 {
		t.Errorf("revision %v -> %v, want exactly one new revision", before, after)
	}

	revs := c.mustJSON(c.do("GET", "/api/playlists/"+pid+"/history", nil))["revisions"].([]any)
	newest := revs[0].(map[string]any)
	if newest["op"] != "reorder" {
		t.Errorf("newest op = %v, want reorder", newest["op"])
	}
	if newest["actor"] != "Owner" {
		t.Errorf("actor = %v, want the display name", newest["actor"])
	}
}

func TestRenameChangesTitleAndLogsIt(t *testing.T) {
	c := newAPI(t)
	pid, _ := seedPlaylist(t, c)

	rec := c.do("PATCH", "/api/playlists/"+pid, map[string]any{"title": "A better name"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if got := c.mustJSON(rec)["title"]; got != "A better name" {
		t.Errorf("title = %v, want the new one", got)
	}

	revs := c.mustJSON(c.do("GET", "/api/playlists/"+pid+"/history", nil))["revisions"].([]any)
	if op := revs[0].(map[string]any)["op"]; op != "rename" {
		t.Errorf("newest op = %v, want rename", op)
	}
}

func TestRenameRejectsBlankTitle(t *testing.T) {
	c := newAPI(t)
	pid, _ := seedPlaylist(t, c)

	for _, title := range []string{"", "   ", "\t\n"} {
		rec := c.do("PATCH", "/api/playlists/"+pid, map[string]any{"title": title})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("title %q = %d, want 400", title, rec.Code)
		}
	}
	if got := c.mustJSON(c.do("GET", "/api/playlists/"+pid, nil))["title"]; got != "Original title" {
		t.Errorf("title changed to %v despite rejection", got)
	}
}

// Both writes are owner-only, and a non-owner must get 404 rather than 403 so
// the response does not confirm the playlist exists (§6).
func TestReorderAndRenameAreOwnerOnly(t *testing.T) {
	owner := newAPI(t)
	pid, ids := seedPlaylist(t, owner)

	invite := owner.mustJSON(owner.do("POST", "/api/invites", nil))["code"].(string)
	other := &client{t: owner.t, h: owner.h}
	other.do("POST", "/api/register", map[string]any{
		"email": "other@example.test", "display_name": "Other",
		"password": goodPassword, "invite_code": invite,
	})

	// A member may read it — playlists are shared by reference (D8).
	if rec := other.do("GET", "/api/playlists/"+pid, nil); rec.Code != http.StatusOK {
		t.Fatalf("member cannot read a shared playlist: %d", rec.Code)
	}

	if rec := other.do("PATCH", "/api/playlists/"+pid,
		map[string]any{"title": "hijacked"}); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner rename = %d, want 404", rec.Code)
	}
	if rec := other.do("PUT", "/api/playlists/"+pid+"/tracks",
		map[string]any{"record_ids": []string{ids[2], ids[1], ids[0]}}); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner reorder = %d, want 404", rec.Code)
	}

	if got := owner.mustJSON(owner.do("GET", "/api/playlists/"+pid, nil))["title"]; got != "Original title" {
		t.Errorf("a non-owner changed the title to %v", got)
	}
}
