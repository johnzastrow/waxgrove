// Package jobs runs provider work in the background, with progress the user
// can see and a state that survives a restart.
//
// Provider operations are jobs rather than requests for three reasons, all from
// §7: a cold export can exceed a minute; §7.2 forbids network I/O inside a
// write transaction; and a self-hosted box restarts for updates, so progress
// that lives only in a goroutine is progress the user loses.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/johnzastrow/waxgrove/internal/connector"
	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/repository/sqlite"
	"github.com/johnzastrow/waxgrove/internal/resolve"
	"github.com/johnzastrow/waxgrove/internal/spotify"
)

// Runner executes queued jobs.
type Runner struct {
	store    *sqlite.Store
	spotify  *connector.Spotify
	resolver *resolve.Resolver

	// One job at a time. This is a friends instance on a small box, and two
	// concurrent exports would double the memory and compete for the same
	// provider quota to no one's benefit.
	poll time.Duration

	mu      sync.Mutex
	running bool
	// base is the runner's own lifetime, set by Start. Work must never inherit
	// the context of whoever queued it: an API request's context is cancelled
	// the moment its response is written, which would kill an import a few
	// milliseconds after starting it.
	base context.Context
}

func NewRunner(store *sqlite.Store, sp *connector.Spotify, r *resolve.Resolver) *Runner {
	return &Runner{
		store: store, spotify: sp, resolver: r,
		poll: 2 * time.Second,
		// Until Start runs, work has no lifetime of its own to borrow.
		base: context.Background(),
	}
}

// Start runs until the context is cancelled.
//
// It first reclaims anything left "running" by a process that died, because a
// job nothing is working on but which reports as running is worse than one that
// reports as failed: the user waits instead of retrying.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	r.base = ctx
	r.mu.Unlock()

	if n, err := r.store.Jobs().ReclaimRunning(ctx); err != nil {
		slog.Error("could not reclaim interrupted jobs", "err", err)
	} else if n > 0 {
		slog.Info("requeued jobs interrupted by a restart", "count", n)
	}

	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

// Wake asks the runner to look for work now rather than at the next tick, so
// pressing a button feels immediate.
//
// It deliberately takes no context. The work belongs to the runner's lifetime,
// not to the request that queued it — inheriting an HTTP request's context
// would cancel the job as soon as the response was written.
func (r *Runner) Wake() {
	r.mu.Lock()
	ctx := r.base
	r.mu.Unlock()
	go r.drain(ctx)
}

func (r *Runner) drain(ctx context.Context) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	for {
		job, err := r.store.Jobs().NextQueued(ctx)
		if errors.Is(err, sqlite.ErrJobNotFound) {
			return
		}
		if err != nil {
			slog.Error("could not claim a job", "err", err)
			return
		}
		r.run(ctx, job)
		if ctx.Err() != nil {
			return
		}
	}
}

func (r *Runner) run(ctx context.Context, job *domain.Job) {
	var err error
	switch job.Kind {
	case domain.JobImport:
		err = r.runImport(ctx, job)
	case domain.JobExport:
		err = r.runExport(ctx, job)
	default:
		err = fmt.Errorf("unknown job kind %q", job.Kind)
	}

	jobs := r.store.Jobs()
	switch {
	case jobs.Cancelled(ctx, job.ID):
		// The user asked it to stop; that is not a failure.
		return
	case err != nil:
		slog.Warn("job failed", "id", job.ID, "kind", job.Kind, "err", err)
		if serr := jobs.SetState(ctx, job.ID, domain.JobFailed, err.Error()); serr != nil {
			slog.Error("could not record a job failure", "id", job.ID, "err", serr)
		}
	default:
		if serr := jobs.SetState(ctx, job.ID, domain.JobDone, ""); serr != nil {
			slog.Error("could not record a job completion", "id", job.ID, "err", serr)
		}
	}
}

// ------------------------------------------------------------------ import --

// ImportRequest is what the API hands over to start an import.
type ImportRequest struct {
	UserID string
	Ref    string // whatever the user pasted
	Title  string // optional override
}

// QueueImport creates the job. The playlist itself is created by the runner, so
// a failed fetch does not leave an empty playlist behind.
func (r *Runner) QueueImport(ctx context.Context, req ImportRequest) (*domain.Job, error) {
	job, err := r.store.Jobs().NewJob(ctx, domain.Job{
		Kind:      domain.JobImport,
		UserID:    req.UserID,
		Service:   sqlite.ServiceSpotify,
		SourceRef: req.Ref,
	})
	if err != nil {
		return nil, err
	}
	r.Wake()
	return job, nil
}

func (r *Runner) runImport(ctx context.Context, job *domain.Job) error {
	jobs := r.store.Jobs()
	ref := job.SourceRef
	if ref == "" {
		return errors.New("no playlist link was given")
	}

	name, candidates, err := r.spotify.FetchPlaylist(ctx, job.UserID, ref)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return errors.New("that playlist has no tracks Waxgrove can import")
	}
	if err := jobs.Progress(ctx, job.ID, 0, len(candidates)); err != nil {
		return err
	}

	playlist, err := r.store.Playlists().Create(ctx, job.UserID, name,
		"Imported from Spotify")
	if err != nil {
		return err
	}

	// Resolution happens outside any write transaction (§7.2), one track at a
	// time, so progress is honest and a cancel lands between tracks rather than
	// at the end.
	var recordIDs []string
	for i, c := range candidates {
		if jobs.Cancelled(ctx, job.ID) {
			return nil
		}
		m, err := r.resolver.Resolve(ctx, c, domain.TierCurated)
		switch {
		case err != nil:
			_ = jobs.AddItem(ctx, job.ID, i, "", domain.JobItemFailed, "could not be looked up")
		case m.Resolved():
			recordIDs = append(recordIDs, m.Record.ID)
			_ = r.store.Records().RecordProvenance(ctx, m.Record.ID, job.UserID)
			_ = jobs.AddItem(ctx, job.ID, i, m.Record.ID, domain.JobItemOK, string(m.Method))
		default:
			// Never silently dropped (BR-5): the user is told which tracks did
			// not make it, with enough to go and find them.
			_ = jobs.AddItem(ctx, job.ID, i, "", domain.JobItemUnresolved,
				describeCandidate(c))
		}
		_ = jobs.Progress(ctx, job.ID, i+1, len(candidates))
	}

	if _, err := r.store.Playlists().AddRecords(ctx, playlist.ID, job.UserID, recordIDs); err != nil {
		return err
	}
	return r.attachPlaylist(ctx, job.ID, playlist.ID)
}

func describeCandidate(c domain.Candidate) string {
	s := c.Title
	if c.Artist != "" {
		s += " — " + c.Artist
	}
	if s == "" {
		s = c.Raw
	}
	return "could not identify: " + s
}

// ------------------------------------------------------------------ export --

// ExportRequest starts an export.
type ExportRequest struct {
	UserID     string
	PlaylistID string
	// Force overwrites a provider copy that has been edited elsewhere. Only
	// ever set because a user was asked and said yes (D10).
	Force bool
}

func (r *Runner) QueueExport(ctx context.Context, req ExportRequest) (*domain.Job, error) {
	sourceRef := ""
	if req.Force {
		sourceRef = forceMarker
	}
	job, err := r.store.Jobs().NewJob(ctx, domain.Job{
		Kind:       domain.JobExport,
		UserID:     req.UserID,
		PlaylistID: req.PlaylistID,
		Service:    sqlite.ServiceSpotify,
		SourceRef:  sourceRef,
	})
	if err != nil {
		return nil, err
	}
	r.Wake()
	return job, nil
}

func (r *Runner) runExport(ctx context.Context, job *domain.Job) error {
	jobs := r.store.Jobs()

	playlist, err := r.store.Playlists().Get(ctx, job.PlaylistID)
	if err != nil {
		return errors.New("that playlist no longer exists")
	}
	if len(playlist.Tracks) == 0 {
		return errors.New("there is nothing in that playlist to export")
	}

	app, tok, market, err := r.spotify.Session(ctx, job.UserID)
	if err != nil {
		return err
	}
	if err := jobs.SetStorefront(ctx, job.ID, market); err != nil {
		return err
	}
	if err := jobs.Progress(ctx, job.ID, 0, len(playlist.Tracks)); err != nil {
		return err
	}

	// Resolve everything before writing anything. A playlist that appears in
	// Spotify and then fills in slowly looks broken; one that appears complete
	// does not.
	uris := make([]string, 0, len(playlist.Tracks))
	unavailable := 0
	for i, t := range playlist.Tracks {
		if jobs.Cancelled(ctx, job.ID) {
			return nil
		}
		res := r.spotify.Resolve(ctx, app, tok, t.Record, market)
		// The detail names the track; the status carries why. Putting the
		// reason in both reads as a stutter in the job surface.
		_ = jobs.AddItem(ctx, job.ID, i, t.Record.ID, res.Status, trackLabel(t.Record))
		if res.Status == domain.JobItemOK && res.URI != "" {
			uris = append(uris, res.URI)
		} else {
			unavailable++
		}
		_ = jobs.Progress(ctx, job.ID, i+1, len(playlist.Tracks))
	}

	if len(uris) == 0 {
		return fmt.Errorf("none of these %d tracks are on Spotify in %s",
			len(playlist.Tracks), marketLabel(market))
	}

	written, err := r.writePlaylist(ctx, job, app, tok, market, playlist, uris)
	if err != nil {
		// The playlist may exist with some tracks in it — record what we know
		// so a retry does not start from a false assumption.
		if written.ProviderPlaylistID != "" {
			_ = r.store.Syncs().Record(ctx, playlist.ID, sqlite.ServiceSpotify,
				market, written.ProviderPlaylistID, written.Snapshot, playlist.CurrentRev)
		}
		if errors.Is(err, connector.ErrDiverged) {
			// Not a failure of ours. The user has to decide whether their edits
			// on Spotify or this playlist wins.
			_ = r.store.Syncs().MarkDiverged(ctx, playlist.ID, sqlite.ServiceSpotify, market)
			return err
		}
		return fmt.Errorf("added %d of %d tracks, then: %w", written.Added, len(uris), err)
	}

	if err := r.store.Syncs().Record(ctx, playlist.ID, sqlite.ServiceSpotify,
		market, written.ProviderPlaylistID, written.Snapshot, playlist.CurrentRev); err != nil {
		return err
	}
	added := written.Added
	if unavailable > 0 {
		// Not a failure. Partial success is the normal outcome of an export,
		// and F15 requires saying so plainly rather than delivering a quietly
		// shorter playlist.
		slog.Info("export finished with unavailable tracks",
			"job", job.ID, "added", added, "unavailable", unavailable)
	}
	return nil
}

// forceMarker rides in the job's source_ref for an export, which is otherwise
// unused by that kind. It has to persist, so that a force decision survives the
// job being requeued after a restart.
const forceMarker = "force"

// writePlaylist updates the copy Waxgrove already made, or makes one.
//
// Re-using the existing playlist is what stops a second export producing a
// second playlist on the user's account — the defect this replaced.
func (r *Runner) writePlaylist(ctx context.Context, job *domain.Job,
	app spotify.App, tok spotify.Token, market string,
	playlist *domain.Playlist, uris []string) (connector.Written, error) {

	sync, err := r.store.Syncs().Get(ctx, playlist.ID, sqlite.ServiceSpotify, market)
	if err == nil && sync.ProviderPlaylistID != "" {
		w, uerr := r.spotify.Update(ctx, app, tok, sync, uris, job.SourceRef == forceMarker)
		if !errors.Is(uerr, connector.ErrGone) {
			return w, uerr
		}
		// It was deleted on Spotify. Fall through and make a new one rather
		// than trapping the user with an error they cannot act on.
		slog.Info("the provider copy is gone; creating a fresh playlist",
			"playlist", playlist.ID)
	}
	return r.spotify.CreateAndFill(ctx, app, tok,
		playlist.Title, exportDescription(playlist), uris)
}

func exportDescription(p *domain.Playlist) string {
	if p.Description != "" {
		return p.Description
	}
	return "Exported from Waxgrove"
}

func trackLabel(rec domain.Record) string {
	if rec.ArtistCredit == "" {
		return rec.Title
	}
	return rec.Title + " — " + rec.ArtistCredit
}

func marketLabel(market string) string {
	if market == "" {
		return "your region"
	}
	return market
}

// attachPlaylist records which playlist an import produced, so the job surface
// can link straight to it.
func (r *Runner) attachPlaylist(ctx context.Context, jobID, playlistID string) error {
	return r.store.Jobs().AttachPlaylist(ctx, jobID, playlistID)
}
