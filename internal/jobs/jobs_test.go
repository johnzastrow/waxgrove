package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnzastrow/waxgrove/internal/connector"
	"github.com/johnzastrow/waxgrove/internal/crypto"
	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/resolve"
	"github.com/johnzastrow/waxgrove/internal/spotify"
)

// fakeSpotify stands in for the provider so the whole job path — queue, claim,
// resolve, write, record — runs for real against something that behaves like
// Spotify, including its failure modes.
type fakeSpotify struct {
	*httptest.Server
	mu sync.Mutex

	tracks       []map[string]any
	createdName  string
	addedURIs    []string
	searchHits   map[string]string // ISRC or text -> uri; a miss means unavailable
	failCreate   bool
	playlistGone bool

	// Export state, so a second export can be seen to reuse the first playlist.
	created      int      // how many playlists have been created
	replacedURIs []string // what the last PUT wrote
	snapshot     string   // moves on every modification, as Spotify's does
	targetGone   bool     // the exported playlist was deleted on Spotify
}

func newFakeSpotify(t *testing.T) *fakeSpotify {
	t.Helper()
	f := &fakeSpotify{searchHits: map[string]string{}}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "AT", "refresh_token": "RT", "expires_in": 3600,
		})
	})
	mux.HandleFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "spotify-user", "country": "GB"})
	})
	mux.HandleFunc("GET /playlists/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		gone := f.playlistGone
		targetGone := f.targetGone && r.PathValue("id") == "new-provider-playlist"
		snap := f.snapshot
		f.mu.Unlock()
		if gone || targetGone {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Not found."}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": r.PathValue("id"), "name": "From Spotify",
			"owner": map[string]any{"id": "spotify-user"}, "snapshot_id": snap,
		})
	})
	mux.HandleFunc("PUT /playlists/{id}/tracks", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URIs []string `json:"uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.replacedURIs = body.URIs
		f.snapshot = f.snapshot + "+" // any write moves the snapshot on
		snap := f.snapshot
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"snapshot_id": snap})
	})
	mux.HandleFunc("GET /playlists/{id}/tracks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		items := make([]any, 0, len(f.tracks))
		for _, tr := range f.tracks {
			items = append(items, map[string]any{"track": tr})
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"next": "", "items": items})
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		f.mu.Lock()
		defer f.mu.Unlock()
		for key, uri := range f.searchHits {
			if strings.Contains(q, key) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"tracks": map[string]any{"items": []any{map[string]any{"uri": uri}}},
				})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tracks": map[string]any{"items": []any{}},
		})
	})
	mux.HandleFunc("POST /users/{id}/playlists", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failCreate {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.createdName = body.Name
		f.created++
		f.snapshot = "snap1"
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "new-provider-playlist"})
	})
	mux.HandleFunc("POST /playlists/{id}/tracks", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URIs []string `json:"uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.addedURIs = append(f.addedURIs, body.URIs...)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"snapshot_id":"s"}`))
	})

	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSpotify) added() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.addedURIs...)
}

func (f *fakeSpotify) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

func (f *fakeSpotify) replaced() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.replacedURIs...)
}

// editedElsewhere simulates the user reordering the playlist in Spotify.
func (f *fakeSpotify) editedElsewhere() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = "edited-by-hand"
}

type harness struct {
	store  *sqlite.Store
	runner *Runner
	userID string
	fake   *fakeSpotify
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key, _ := crypto.GenerateKey()
	sealer, _ := crypto.NewSealer(key)

	u, err := store.Users().Register(ctx, "ana@example.test", "Ana",
		"correct-horse-battery-staple", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	fake := newFakeSpotify(t)
	client := spotify.New(spotify.WithBaseURLs(fake.URL, fake.URL))
	creds := store.Credentials(sealer)
	if err := creds.SaveApp(ctx, u.ID, sqlite.ServiceSpotify, "cid", "sec"); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if err := creds.SaveTokens(ctx, u.ID, sqlite.ServiceSpotify, "AT", "RT",
		time.Now().Add(time.Hour), spotify.ScopeString(), "GB"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	conn := connector.NewSpotify(client, creds, store.ProviderRefs(), "https://wg.test")
	// Remote nil: the resolution ladder must work with no metadata source (N6).
	runner := NewRunner(store, conn, resolve.New(store.Records(), nil))

	// Queueing wakes a background drain, so the test has to be able to stop it.
	// Registered after the store's cleanup so it runs first (cleanups are LIFO)
	// — otherwise a drain outlives the database and logs a confusing
	// "database is closed" against whichever test happens to be running next.
	runCtx, cancel := context.WithCancel(context.Background())
	runner.base = runCtx
	t.Cleanup(func() {
		cancel()
		for range 300 {
			runner.mu.Lock()
			busy := runner.running
			runner.mu.Unlock()
			if !busy {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("a job was still running when the test finished")
	})

	return &harness{store: store, runner: runner, userID: u.ID, fake: fake}
}

// runToCompletion waits for a job to finish.
//
// It cannot simply call drain and read the result: queueing already woke a
// background drain, and a second drain returns immediately when one is in
// flight. Polling for the terminal state is what the runner actually
// guarantees, so that is what this asserts on.
func (h *harness) runToCompletion(t *testing.T, jobID string) *domain.Job {
	t.Helper()
	ctx := context.Background()
	h.runner.drain(ctx) // in case nothing else is running, start now

	deadline := time.Now().Add(20 * time.Second)
	for {
		job, err := h.store.Jobs().Get(ctx, jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.Terminal() {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job is still %s after 20s", job.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func track(id, name, artist, isrc string) map[string]any {
	return map[string]any{
		"id": id, "name": name,
		"artists":      []any{map[string]any{"name": artist}},
		"external_ids": map[string]any{"isrc": isrc},
	}
}

// ------------------------------------------------------------------ import --

func TestImportCreatesAPlaylistFromProviderTracks(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.fake.tracks = []map[string]any{
		track("1", "Pink Moon", "Nick Drake", "GBAYE0601498"),
		track("2", "Dreams", "Fleetwood Mac", "USWB10101368"),
	}

	job, err := h.runner.QueueImport(ctx, ImportRequest{
		UserID: h.userID,
		Ref:    "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
	})
	if err != nil {
		t.Fatalf("QueueImport: %v", err)
	}

	done := h.runToCompletion(t, job.ID)
	if done.State != domain.JobDone {
		t.Fatalf("state = %s, error = %q", done.State, done.Error)
	}
	if done.Done != 2 || done.Total != 2 {
		t.Errorf("progress = %d/%d, want 2/2", done.Done, done.Total)
	}
	if done.PlaylistID == "" {
		t.Fatal("the job did not record which playlist it produced")
	}

	pl, err := h.store.Playlists().Get(ctx, done.PlaylistID)
	if err != nil {
		t.Fatalf("get playlist: %v", err)
	}
	if pl.Title != "From Spotify" {
		t.Errorf("title = %q, want the provider's name", pl.Title)
	}
	if len(pl.Tracks) != 2 {
		t.Fatalf("playlist has %d tracks, want 2", len(pl.Tracks))
	}
	// Provider imports carry an ISRC, so they resolve at step 1 of the ladder —
	// exact and automatic, no disambiguation (§3.2).
	if got := pl.Tracks[0].Record.ISRCs; len(got) != 1 || got[0] != "GBAYE0601498" {
		t.Errorf("ISRCs = %v, want the provider's", got)
	}
}

// The whole playlist must not be lost because one track cannot be identified.
func TestImportRecordsUnresolvedTracksWithoutFailing(t *testing.T) {
	h := newHarness(t)
	h.fake.tracks = []map[string]any{
		track("1", "Pink Moon", "Nick Drake", "GBAYE0601498"),
		// No ISRC and no metadata source, so nothing can identify this.
		{"id": "2", "name": "", "artists": []any{}},
	}

	job, _ := h.runner.QueueImport(context.Background(), ImportRequest{
		UserID: h.userID, Ref: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M",
	})
	done := h.runToCompletion(t, job.ID)

	if done.State != domain.JobDone {
		t.Fatalf("state = %s, error = %q — one bad track should not fail an import",
			done.State, done.Error)
	}
	full, _ := h.store.Jobs().Get(context.Background(), job.ID)
	var unresolved int
	for _, it := range full.Items {
		if it.Status == domain.JobItemUnresolved {
			unresolved++
		}
	}
	if unresolved != 1 {
		t.Errorf("recorded %d unresolved items, want 1 — nothing may be silently dropped (BR-5)",
			unresolved)
	}
}

func TestImportOfAMissingPlaylistFailsReadably(t *testing.T) {
	h := newHarness(t)
	h.fake.playlistGone = true

	job, _ := h.runner.QueueImport(context.Background(), ImportRequest{
		UserID: h.userID, Ref: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M",
	})
	done := h.runToCompletion(t, job.ID)

	if done.State != domain.JobFailed {
		t.Fatalf("state = %s, want failed", done.State)
	}
	if !strings.Contains(done.Error, "not there") {
		t.Errorf("error = %q, want something a user can act on", done.Error)
	}
	// A failed fetch must not leave an empty playlist behind.
	pls, _ := h.store.Playlists().ListOwned(context.Background(), h.userID)
	if len(pls) != 0 {
		t.Errorf("%d playlists were left behind by a failed import", len(pls))
	}
}

// ------------------------------------------------------------------ export --

// seedPlaylist puts records in the catalogue and a playlist around them.
func (h *harness) seedPlaylist(t *testing.T, isrcs ...string) *domain.Playlist {
	t.Helper()
	ctx := context.Background()
	pl, err := h.store.Playlists().Create(ctx, h.userID, "Sunday morning, slow", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var ids []string
	for i, isrc := range isrcs {
		rec, err := h.store.Records().Upsert(ctx, domain.Candidate{
			Title: "Track " + isrc, Artist: "Artist", ISRC: isrc,
		}, domain.TierCurated)
		if err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
		ids = append(ids, rec.ID)
	}
	if _, err := h.store.Playlists().AddRecords(ctx, pl.ID, h.userID, ids); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, _ := h.store.Playlists().Get(ctx, pl.ID)
	return got
}

func TestExportWritesThePlaylistAndRecordsTheSync(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001", "AA00000000002")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"
	h.fake.searchHits["AA00000000002"] = "spotify:track:two"

	job, err := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	if err != nil {
		t.Fatalf("QueueExport: %v", err)
	}
	done := h.runToCompletion(t, job.ID)
	if done.State != domain.JobDone {
		t.Fatalf("state = %s, error = %q", done.State, done.Error)
	}

	if got := h.fake.added(); len(got) != 2 {
		t.Errorf("added %v, want both tracks", got)
	}
	if h.fake.createdName != "Sunday morning, slow" {
		t.Errorf("created %q, want the playlist's own title", h.fake.createdName)
	}

	// Sync state is what makes "how far behind is the copy" answerable (F21).
	sync, err := h.store.Syncs().Get(ctx, pl.ID, sqlite.ServiceSpotify, "GB")
	if err != nil {
		t.Fatalf("no sync recorded: %v", err)
	}
	if sync.ProviderSnapshot == "" {
		t.Error("no provider snapshot recorded; divergence cannot be detected without one")
	}
	if sync.ProviderPlaylistID != "new-provider-playlist" {
		t.Errorf("provider id = %q", sync.ProviderPlaylistID)
	}
	if sync.LastSyncedRev != pl.CurrentRev {
		t.Errorf("synced rev %d, want %d", sync.LastSyncedRev, pl.CurrentRev)
	}
	if n := sync.Behind(pl.CurrentRev); n != 0 {
		t.Errorf("reports %d revisions behind immediately after a sync", n)
	}
	if n := sync.Behind(pl.CurrentRev + 3); n != 3 {
		t.Errorf("Behind = %d, want 3", n)
	}
}

// Partial success is the normal outcome of an export, not a failure: regional
// licensing and delistings guarantee it. F15 requires reporting exactly which
// tracks did not make it rather than delivering a quietly shorter playlist.
func TestExportReportsUnavailableTracksAndStillSucceeds(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001", "AA00000000002", "AA00000000003")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"
	// The other two exist nowhere on this fake Spotify.

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	done := h.runToCompletion(t, job.ID)

	if done.State != domain.JobDone {
		t.Fatalf("state = %s (%q); a partial export is a success, not a failure",
			done.State, done.Error)
	}
	if got := h.fake.added(); len(got) != 1 {
		t.Errorf("added %v, want only the available track", got)
	}

	full, _ := h.store.Jobs().Get(ctx, job.ID)
	var unavailable int
	for _, it := range full.Items {
		if it.Status == domain.JobItemUnavailable {
			unavailable++
			// The item names the track; the status says why, and the market
			// lives on the job. Repeating the reason in both reads as a
			// stutter in the job surface.
			if !strings.Contains(it.Detail, "Track AA") {
				t.Errorf("detail %q does not name the track", it.Detail)
			}
		}
	}
	if unavailable != 2 {
		t.Errorf("recorded %d unavailable tracks, want 2", unavailable)
	}
	// Which market it resolved against is what makes "unavailable" actionable.
	if full.Storefront != "GB" {
		t.Errorf("job storefront = %q, want the market it resolved against", full.Storefront)
	}
}

// An export where nothing resolves has produced nothing, and saying "done"
// would be a lie.
func TestExportFailsWhenNothingIsAvailable(t *testing.T) {
	h := newHarness(t)
	pl := h.seedPlaylist(t, "AA00000000001")

	job, _ := h.runner.QueueExport(context.Background(), ExportRequest{
		UserID: h.userID, PlaylistID: pl.ID,
	})
	done := h.runToCompletion(t, job.ID)

	if done.State != domain.JobFailed {
		t.Fatalf("state = %s, want failed", done.State)
	}
	if !strings.Contains(done.Error, "GB") {
		t.Errorf("error = %q, want it to name the market", done.Error)
	}
}

// The cache is the main defence against a scarce provider quota (§3.0): a song
// resolved once by anyone never costs a lookup again.
func TestExportCachesResolutionsAndReusesThem(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	if done := h.runToCompletion(t, job.ID); done.State != domain.JobDone {
		t.Fatalf("first export: %s %q", done.State, done.Error)
	}

	ref, err := h.store.ProviderRefs().Get(ctx, pl.Tracks[0].Record.ID, sqlite.ServiceSpotify, "GB")
	if err != nil {
		t.Fatalf("nothing was cached: %v", err)
	}
	if ref.ExternalID != "spotify:track:one" || ref.Status != sqlite.RefOK {
		t.Errorf("cached %+v", ref)
	}

	// Second export: the fake now knows nothing, so a cache miss would produce
	// an unavailable track and the export would fail.
	h.fake.mu.Lock()
	h.fake.searchHits = map[string]string{}
	h.fake.mu.Unlock()

	job2, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	if done := h.runToCompletion(t, job2.ID); done.State != domain.JobDone {
		t.Fatalf("second export did not use the cache: %s %q", done.State, done.Error)
	}
}

// A negative result is cached too, so a playlist with an unavailable track does
// not pay the full lookup cost on every export.
func TestUnavailableResultsAreCached(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001", "AA00000000002")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	h.runToCompletion(t, job.ID)

	missing := pl.Tracks[1].Record.ID
	ref, err := h.store.ProviderRefs().Get(ctx, missing, sqlite.ServiceSpotify, "GB")
	if err != nil {
		t.Fatalf("the negative result was not cached: %v", err)
	}
	if ref.Status != sqlite.RefAbsent {
		t.Errorf("status = %q, want absent", ref.Status)
	}
}

func TestExportOfAnEmptyPlaylistFails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl, _ := h.store.Playlists().Create(ctx, h.userID, "Empty", "")

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	done := h.runToCompletion(t, job.ID)
	if done.State != domain.JobFailed {
		t.Errorf("state = %s, want failed", done.State)
	}
}

// ------------------------------------------------------------------- state --

// A job interrupted by a restart must not sit as "running" with nothing running
// it — the user waits instead of retrying.
func TestRunningJobsAreRequeuedAfterARestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	job, _ := h.store.Jobs().NewJob(ctx, domain.Job{
		Kind: domain.JobExport, UserID: h.userID,
	})
	if err := h.store.Jobs().SetState(ctx, job.ID, domain.JobRunning, ""); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	n, err := h.store.Jobs().ReclaimRunning(ctx)
	if err != nil {
		t.Fatalf("ReclaimRunning: %v", err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d jobs, want 1", n)
	}
	got, _ := h.store.Jobs().Get(ctx, job.ID)
	if got.State != domain.JobQueued {
		t.Errorf("state = %s, want queued", got.State)
	}
}

// Work must not inherit the context of whoever queued it.
//
// This was a real bug: Wake took the caller's context, so a job started from an
// API request was cancelled the instant that request's response was written. It
// looked fine only because the 2-second poller picked the job up afterwards
// with the runner's own context — a race that would have shown up as
// mysteriously slow imports rather than as a failure.
func TestQueuedWorkOutlivesTheRequestThatQueuedIt(t *testing.T) {
	h := newHarness(t)
	h.fake.tracks = []map[string]any{track("1", "Pink Moon", "Nick Drake", "GBAYE0601498")}

	// A context that is already dead, standing in for a completed request.
	reqCtx, cancel := context.WithCancel(context.Background())
	job, err := h.runner.QueueImport(reqCtx, ImportRequest{
		UserID: h.userID, Ref: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M",
	})
	if err != nil {
		t.Fatalf("QueueImport: %v", err)
	}
	cancel()

	done := h.runToCompletion(t, job.ID)
	if done.State != domain.JobDone {
		t.Fatalf("state = %s (%q): the job died with the request that queued it",
			done.State, done.Error)
	}
}

// Two workers racing for the same job must produce one winner, not two runs of
// the same export.
func TestClaimingAJobIsExclusive(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.store.Jobs().NewJob(ctx, domain.Job{
		Kind: domain.JobExport, UserID: h.userID,
	}); err != nil {
		t.Fatalf("NewJob: %v", err)
	}

	first, err := h.store.Jobs().NextQueued(ctx)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := h.store.Jobs().NextQueued(ctx); err == nil {
		t.Error("a second worker claimed the same job")
	}
	if first.State != domain.JobRunning {
		t.Errorf("claimed job is %s, want running", first.State)
	}
}

func TestCancellingStopsAJobAndIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	if err := h.store.Jobs().Cancel(ctx, job.ID, h.userID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	h.runner.drain(ctx)
	got, _ := h.store.Jobs().Get(ctx, job.ID)
	if got.State != domain.JobCancelled {
		t.Errorf("state = %s, want cancelled", got.State)
	}
	if len(h.fake.added()) != 0 {
		t.Error("a cancelled export still wrote to Spotify")
	}
}

// Another user must not be able to cancel a job that is not theirs.
func TestCancelIsScopedToTheOwner(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	job, _ := h.store.Jobs().NewJob(ctx, domain.Job{
		Kind: domain.JobExport, UserID: h.userID,
	})
	if err := h.store.Jobs().Cancel(ctx, job.ID, "somebody-else"); err == nil {
		t.Error("a stranger cancelled another user's job")
	}
}

func TestPurgeRemovesOnlyFinishedJobs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	old, _ := h.store.Jobs().NewJob(ctx, domain.Job{Kind: domain.JobExport, UserID: h.userID})
	_ = h.store.Jobs().SetState(ctx, old.ID, domain.JobDone, "")
	live, _ := h.store.Jobs().NewJob(ctx, domain.Job{Kind: domain.JobExport, UserID: h.userID})

	// Everything just written is newer than the cutoff, so nothing goes yet.
	if n, err := h.store.Jobs().PurgeOlderThan(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("purged %d (err %v), want 0", n, err)
	}
	// A cutoff in the future catches the finished one and spares the queued one.
	n, err := h.store.Jobs().PurgeOlderThan(ctx, -time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d, want only the finished job", n)
	}
	if _, err := h.store.Jobs().Get(ctx, live.ID); err != nil {
		t.Errorf("the queued job was purged: %v", err)
	}
}

// -------------------------------------------------------------- re-syncing --

// The defect this replaced: every export created a new Spotify playlist, so
// exporting twice left the user with two.
func TestSecondExportUpdatesTheSamePlaylist(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001", "AA00000000002")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"
	h.fake.searchHits["AA00000000002"] = "spotify:track:two"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	if done := h.runToCompletion(t, job.ID); done.State != domain.JobDone {
		t.Fatalf("first export: %s %q", done.State, done.Error)
	}
	if n := h.fake.createdCount(); n != 1 {
		t.Fatalf("created %d playlists on the first export, want 1", n)
	}

	job2, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	if done := h.runToCompletion(t, job2.ID); done.State != domain.JobDone {
		t.Fatalf("second export: %s %q", done.State, done.Error)
	}

	if n := h.fake.createdCount(); n != 1 {
		t.Errorf("created %d playlists after two exports — the second should have "+
			"updated the first", n)
	}
	if got := h.fake.replaced(); len(got) != 2 {
		t.Errorf("replaced with %v, want both tracks written to the existing playlist", got)
	}
}

// D10: a copy edited on the provider side must not be silently overwritten.
func TestReSyncRefusesWhenTheCopyWasEditedElsewhere(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	h.runToCompletion(t, job.ID)

	h.fake.editedElsewhere()

	job2, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	done := h.runToCompletion(t, job2.ID)
	if done.State != domain.JobFailed {
		t.Fatalf("state = %s, want failed — the copy was edited on Spotify", done.State)
	}
	if !strings.Contains(done.Error, "edited") {
		t.Errorf("error = %q, want it to say the copy was edited", done.Error)
	}

	// And it is recorded, so the UI can offer the choice rather than just
	// failing again next time.
	sync, err := h.store.Syncs().Get(ctx, pl.ID, sqlite.ServiceSpotify, "GB")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !sync.Diverged {
		t.Error("divergence was not recorded")
	}
}

// ...and the user can decide to overwrite anyway.
func TestForcedReSyncOverwritesADivergedCopy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	h.runToCompletion(t, job.ID)
	h.fake.editedElsewhere()

	job2, _ := h.runner.QueueExport(ctx, ExportRequest{
		UserID: h.userID, PlaylistID: pl.ID, Force: true,
	})
	done := h.runToCompletion(t, job2.ID)
	if done.State != domain.JobDone {
		t.Fatalf("forced re-sync: %s %q", done.State, done.Error)
	}
	if n := h.fake.createdCount(); n != 1 {
		t.Errorf("a forced re-sync created %d playlists, want it to reuse the one", n)
	}
	// Divergence is cleared: the copy now matches again.
	sync, _ := h.store.Syncs().Get(ctx, pl.ID, sqlite.ServiceSpotify, "GB")
	if sync.Diverged {
		t.Error("still marked diverged after a successful forced re-sync")
	}
}

// A copy deleted on Spotify must not trap the user with an error they cannot
// act on — Waxgrove makes a fresh one.
func TestReSyncRecreatesAPlaylistDeletedOnSpotify(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	h.runToCompletion(t, job.ID)

	h.fake.mu.Lock()
	h.fake.targetGone = true
	h.fake.mu.Unlock()

	job2, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	done := h.runToCompletion(t, job2.ID)
	if done.State != domain.JobDone {
		t.Fatalf("state = %s %q, want a fresh playlist to have been made", done.State, done.Error)
	}
	if n := h.fake.createdCount(); n != 2 {
		t.Errorf("created %d playlists, want a replacement for the deleted one", n)
	}
}

// Sync state answers "how far behind is the copy", not "is it synced" (F21).
func TestSyncReportsHowFarBehindTheCopyIs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pl := h.seedPlaylist(t, "AA00000000001")
	h.fake.searchHits["AA00000000001"] = "spotify:track:one"

	job, _ := h.runner.QueueExport(ctx, ExportRequest{UserID: h.userID, PlaylistID: pl.ID})
	h.runToCompletion(t, job.ID)

	// Two more content changes on the Waxgrove side.
	rec, _ := h.store.Records().Upsert(ctx, domain.Candidate{
		Title: "Another", Artist: "Someone", ISRC: "AA00000000009",
	}, domain.TierCurated)
	updated, _ := h.store.Playlists().AddRecords(ctx, pl.ID, h.userID, []string{rec.ID})
	updated, _ = h.store.Playlists().Rename(ctx, pl.ID, h.userID, "Renamed")

	sync, err := h.store.Syncs().Get(ctx, pl.ID, sqlite.ServiceSpotify, "GB")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := sync.Behind(updated.CurrentRev); got != 2 {
		t.Errorf("behind = %d, want 2 revisions", got)
	}
}
