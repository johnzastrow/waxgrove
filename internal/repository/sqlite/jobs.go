package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// JobRepo persists long provider operations.
//
// A cold export can exceed a minute — a dozen MusicBrainz lookups at one per
// second, plus Spotify resolution per track — so it cannot live inside a
// request/response (F22, §7). Persisting progress is also what makes a job
// resumable across a restart, which on a self-hosted box that reboots for
// updates is the difference between "carry on" and "start again".
type JobRepo struct{ s *Store }

func (s *Store) Jobs() *JobRepo { return &JobRepo{s: s} }

// ErrJobNotFound is returned for an unknown job id.
var ErrJobNotFound = errors.New("sqlite: job not found")

// NewJob creates a queued job.
func (r *JobRepo) NewJob(ctx context.Context, j domain.Job) (*domain.Job, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := nowRFC3339()
	j.ID, j.State, j.CreatedAt, j.UpdatedAt = id, domain.JobQueued, now, now

	_, err = r.s.Writer().ExecContext(ctx, `
		INSERT INTO jobs (id, kind, state, user_id, playlist_id, service,
		                  storefront, source_ref, done, total, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		j.ID, j.Kind, j.State, nullStr(j.UserID), nullStr(j.PlaylistID),
		nullStr(j.Service), nullStr(j.Storefront), nullStr(j.SourceRef),
		j.Total, now, now)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// SetState moves a job and records why, if it failed.
//
// The error text reaching this table is written for the user, not copied from
// a provider: §6 keeps internal detail in the logs, and a job surface is very
// much user-facing.
func (r *JobRepo) SetState(ctx context.Context, id string, state domain.JobState, userMessage string) error {
	res, err := r.s.Writer().ExecContext(ctx,
		`UPDATE jobs SET state = ?, error = ?, updated_at = ? WHERE id = ?`,
		state, nullStr(userMessage), nowRFC3339(), id)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrJobNotFound)
}

// Progress records how far along a job is.
func (r *JobRepo) Progress(ctx context.Context, id string, done, total int) error {
	res, err := r.s.Writer().ExecContext(ctx,
		`UPDATE jobs SET done = ?, total = ?, updated_at = ? WHERE id = ?`,
		done, total, nowRFC3339(), id)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrJobNotFound)
}

// AddItem records the outcome for one track.
//
// Every track gets a row, including the ones that failed. Partial success is
// the normal outcome of an export — regional licensing, exclusives, delistings
// — and F15 requires showing exactly which tracks did not make it and why,
// rather than delivering a quietly shorter playlist.
func (r *JobRepo) AddItem(ctx context.Context, jobID string, position int,
	recordID, status, detail string) error {

	id, err := newID()
	if err != nil {
		return err
	}
	_, err = r.s.Writer().ExecContext(ctx, `
		INSERT INTO job_items (id, job_id, position, record_id, status, detail)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, jobID, position, nullStr(recordID), status, nullStr(detail))
	return err
}

// Get returns a job and its items.
func (r *JobRepo) Get(ctx context.Context, id string) (*domain.Job, error) {
	j, err := r.scanJob(r.s.Reader().QueryRowContext(ctx, jobColumns+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	items, err := r.items(ctx, id)
	if err != nil {
		return nil, err
	}
	j.Items = items
	return j, nil
}

const jobColumns = `
	SELECT id, kind, state, user_id, playlist_id, service, storefront,
	       source_ref, done, total, error, created_at, updated_at
	  FROM jobs`

func (r *JobRepo) scanJob(row *sql.Row) (*domain.Job, error) {
	var j domain.Job
	var user, playlist, service, storefront, sourceRef, errText sql.NullString
	err := row.Scan(&j.ID, &j.Kind, &j.State, &user, &playlist, &service,
		&storefront, &sourceRef, &j.Done, &j.Total, &errText, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, err
	}
	j.UserID, j.PlaylistID = user.String, playlist.String
	j.Service, j.Storefront, j.Error = service.String, storefront.String, errText.String
	j.SourceRef = sourceRef.String
	return &j, nil
}

func (r *JobRepo) items(ctx context.Context, jobID string) ([]domain.JobItem, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT position, record_id, status, detail
		  FROM job_items WHERE job_id = ? ORDER BY position`, jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.JobItem
	for rows.Next() {
		var it domain.JobItem
		var recordID, detail sql.NullString
		if err := rows.Scan(&it.Position, &recordID, &it.Status, &detail); err != nil {
			return nil, err
		}
		it.RecordID, it.Detail = recordID.String, detail.String
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListForUser returns a user's recent jobs, newest first, without their items.
func (r *JobRepo) ListForUser(ctx context.Context, userID string, limit int) ([]domain.Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.s.Reader().QueryContext(ctx,
		jobColumns+` WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Job
	for rows.Next() {
		var j domain.Job
		var user, playlist, service, storefront, sourceRef, errText sql.NullString
		if err := rows.Scan(&j.ID, &j.Kind, &j.State, &user, &playlist, &service,
			&storefront, &sourceRef, &j.Done, &j.Total, &errText,
			&j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.UserID, j.PlaylistID = user.String, playlist.String
		j.Service, j.Storefront, j.Error = service.String, storefront.String, errText.String
		j.SourceRef = sourceRef.String
		out = append(out, j)
	}
	return out, rows.Err()
}

// ReclaimRunning moves jobs that were running when the process died back to
// queued.
//
// Called once at startup. Without it a job interrupted by a restart shows as
// "running" forever, with nothing running it — which is worse than showing as
// failed, because the user waits instead of retrying.
func (r *JobRepo) ReclaimRunning(ctx context.Context) (int, error) {
	res, err := r.s.Writer().ExecContext(ctx, `
		UPDATE jobs SET state = ?, updated_at = ? WHERE state = ?`,
		domain.JobQueued, nowRFC3339(), domain.JobRunning)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// NextQueued claims one queued job for this worker.
//
// The claim is a conditional update, so two workers racing for the same job
// produce one winner and one miss rather than two runs of the same export.
func (r *JobRepo) NextQueued(ctx context.Context) (*domain.Job, error) {
	var id string
	err := r.s.Writer().QueryRowContext(ctx,
		`SELECT id FROM jobs WHERE state = ? ORDER BY created_at LIMIT 1`,
		domain.JobQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, err
	}

	res, err := r.s.Writer().ExecContext(ctx,
		`UPDATE jobs SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		domain.JobRunning, nowRFC3339(), id, domain.JobQueued)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrJobNotFound // somebody else claimed it
	}
	return r.Get(ctx, id)
}

// Cancel asks a job to stop. A job already finished is left alone.
func (r *JobRepo) Cancel(ctx context.Context, id, userID string) error {
	res, err := r.s.Writer().ExecContext(ctx, `
		UPDATE jobs SET state = ?, updated_at = ?
		 WHERE id = ? AND user_id = ? AND state IN (?, ?)`,
		domain.JobCancelled, nowRFC3339(), id, userID,
		domain.JobQueued, domain.JobRunning)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrJobNotFound)
}

// Cancelled reports whether a running job has been asked to stop, so a worker
// can abandon a long loop between tracks rather than at the end of it.
func (r *JobRepo) Cancelled(ctx context.Context, id string) bool {
	var state string
	if err := r.s.Reader().QueryRowContext(ctx,
		`SELECT state FROM jobs WHERE id = ?`, id).Scan(&state); err != nil {
		return false
	}
	return domain.JobState(state) == domain.JobCancelled
}

// PurgeOlderThan deletes finished jobs, so the table does not grow forever on
// an instance nobody prunes. Items go with them by cascade.
func (r *JobRepo) PurgeOlderThan(ctx context.Context, age time.Duration) (int, error) {
	cutoff := time.Now().Add(-age).UTC().Format(time.RFC3339)
	res, err := r.s.Writer().ExecContext(ctx, `
		DELETE FROM jobs
		 WHERE updated_at < ? AND state IN (?, ?, ?)`,
		cutoff, domain.JobDone, domain.JobFailed, domain.JobCancelled)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AttachPlaylist records which playlist a job produced, so the job surface can
// link straight to the result rather than telling the user to go and find it.
func (r *JobRepo) AttachPlaylist(ctx context.Context, jobID, playlistID string) error {
	res, err := r.s.Writer().ExecContext(ctx,
		`UPDATE jobs SET playlist_id = ?, updated_at = ? WHERE id = ?`,
		playlistID, nowRFC3339(), jobID)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrJobNotFound)
}

func mustAffect(res sql.Result, notFound error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	return nil
}
