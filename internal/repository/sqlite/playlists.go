package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// PlaylistRepo owns playlists and their append-only content history.
//
// BR-3 is enforced here by omission: this type has no method that touches
// ratings, tags or comments. Annotations live elsewhere and never write a
// revision.
type PlaylistRepo struct{ s *Store }

func (s *Store) Playlists() *PlaylistRepo { return &PlaylistRepo{s: s} }

// Create makes a playlist and writes revision 1.
func (r *PlaylistRepo) Create(ctx context.Context, ownerID, title, description string) (*domain.Playlist, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO playlists (id, owner_id, title, description, current_rev, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`,
		id, nullStr(ownerID), title, nullStr(description), now, now); err != nil {
		return nil, fmt.Errorf("insert playlist: %w", err)
	}
	if err := writeRevision(ctx, tx, id, 1, ownerID, domain.OpCreate, map[string]any{"title": title}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Get loads a playlist and its ordered tracks.
func (r *PlaylistRepo) Get(ctx context.Context, id string) (*domain.Playlist, error) {
	var p domain.Playlist
	var owner, desc sql.NullString
	var created, updated string

	err := r.s.Reader().QueryRowContext(ctx, `
		SELECT id, owner_id, title, description, current_rev, created_at, updated_at
		  FROM playlists WHERE id = ?`, id).
		Scan(&p.ID, &owner, &p.Title, &desc, &p.CurrentRev, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.OwnerID, p.Description = owner.String, desc.String
	p.CreatedAt, _ = time.Parse(time.RFC3339, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updated)

	rows, err := r.s.Reader().QueryContext(ctx,
		`SELECT position, record_id, added_in_rev FROM playlist_tracks
		  WHERE playlist_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type slot struct {
		pos, rev int
		recordID string
	}
	var slots []slot
	for rows.Next() {
		var s slot
		if err := rows.Scan(&s.pos, &s.recordID, &s.rev); err != nil {
			return nil, err
		}
		slots = append(slots, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// One batch rather than a query per track. A 100-track playlist was costing
	// 201 queries to render, and this is the path every playlist view takes.
	//
	// The cursor above is fully drained before this runs, which is what
	// releases its pooled connection — see scanIDs in records.go for why that
	// ordering is load-bearing rather than stylistic.
	ids := make([]string, len(slots))
	for i, s := range slots {
		ids[i] = s.recordID
	}
	recs, err := r.s.Records().getMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*domain.Record, len(recs))
	for i := range recs {
		byID[recs[i].ID] = &recs[i]
	}
	for _, s := range slots {
		rec, ok := byID[s.recordID]
		if !ok {
			// A track pointing at a record that no longer exists would mean the
			// foreign key was bypassed. Surface it rather than silently
			// shortening the playlist.
			return nil, fmt.Errorf("playlist %s position %d references missing record %s",
				p.ID, s.pos, s.recordID)
		}
		p.Tracks = append(p.Tracks, domain.Track{Position: s.pos, Record: *rec, AddedInRev: s.rev})
	}
	return &p, nil
}

// ListOwned returns a user's playlists, newest first.
func (r *PlaylistRepo) ListOwned(ctx context.Context, ownerID string) ([]domain.Playlist, error) {
	rows, err := r.s.Reader().QueryContext(ctx,
		`SELECT id FROM playlists WHERE owner_id = ? ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]domain.Playlist, 0, len(ids))
	for _, id := range ids {
		p, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

// AddRecords appends records and writes exactly ONE revision, however many
// tracks were added. A crate commit of twenty songs is one authored event, not
// twenty — which is what keeps blame legible (§3.3).
func (r *PlaylistRepo) AddRecords(ctx context.Context, playlistID, actorID string, recordIDs []string) (*domain.Playlist, error) {
	if len(recordIDs) == 0 {
		return r.Get(ctx, playlistID)
	}

	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rev, nextPos int
	if err := tx.QueryRowContext(ctx,
		`SELECT current_rev FROM playlists WHERE id = ?`, playlistID).Scan(&rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM playlist_tracks WHERE playlist_id = ?`,
		playlistID).Scan(&nextPos); err != nil {
		return nil, err
	}
	rev++

	for _, rid := range recordIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO playlist_tracks (playlist_id, position, record_id, added_in_rev)
			 VALUES (?, ?, ?, ?)`, playlistID, nextPos, rid, rev); err != nil {
			return nil, fmt.Errorf("insert track: %w", err)
		}
		nextPos++
	}
	if err := writeRevision(ctx, tx, playlistID, rev, actorID, domain.OpAdd,
		map[string]any{"count": len(recordIDs)}); err != nil {
		return nil, err
	}
	if err := bumpPlaylist(ctx, tx, playlistID, rev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, playlistID)
}

// ErrReorderMismatch means the submitted ordering is not a rearrangement of
// what the playlist currently holds.
var ErrReorderMismatch = errors.New("sqlite: reorder must be a permutation of the current tracks")

// Reorder replaces the whole ordering with the given record sequence.
//
// The sequence must be a permutation of what is already there. Without that
// check, Reorder would happily accept a list with tracks added or dropped and
// then write "reordered it" into the append-only history — a change recorded as
// something it is not, which is worse than a rejected request (F17, BR-3).
func (r *PlaylistRepo) Reorder(ctx context.Context, playlistID, actorID string, recordIDs []string) (*domain.Playlist, error) {
	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rev int
	if err := tx.QueryRowContext(ctx,
		`SELECT current_rev FROM playlists WHERE id = ?`, playlistID).Scan(&rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rev++

	if err := assertPermutation(ctx, tx, playlistID, recordIDs); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM playlist_tracks WHERE playlist_id = ?`, playlistID); err != nil {
		return nil, err
	}
	for i, rid := range recordIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO playlist_tracks (playlist_id, position, record_id, added_in_rev)
			 VALUES (?, ?, ?, ?)`, playlistID, i, rid, rev); err != nil {
			return nil, err
		}
	}
	if err := writeRevision(ctx, tx, playlistID, rev, actorID, domain.OpReorder,
		map[string]any{"count": len(recordIDs)}); err != nil {
		return nil, err
	}
	if err := bumpPlaylist(ctx, tx, playlistID, rev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, playlistID)
}

// assertPermutation checks the submitted ordering against what is stored.
//
// Multiset, not set: the same record may legitimately appear at two positions
// in a playlist, so counting matters and a plain set comparison would let a
// duplicate be silently dropped.
func assertPermutation(ctx context.Context, tx *sql.Tx, playlistID string, want []string) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT record_id FROM playlist_tracks WHERE playlist_id = ?`, playlistID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	have := make(map[string]int)
	n := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		have[id]++
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(want) != n {
		return ErrReorderMismatch
	}
	for _, id := range want {
		if have[id] == 0 {
			return ErrReorderMismatch
		}
		have[id]--
	}
	return nil
}

// RemoveAt drops one position and closes the gap.
func (r *PlaylistRepo) RemoveAt(ctx context.Context, playlistID, actorID string, position int) (*domain.Playlist, error) {
	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rev int
	if err := tx.QueryRowContext(ctx,
		`SELECT current_rev FROM playlists WHERE id = ?`, playlistID).Scan(&rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rev++

	res, err := tx.ExecContext(ctx,
		`DELETE FROM playlist_tracks WHERE playlist_id = ? AND position = ?`, playlistID, position)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	// Positions are a dense sequence, so close the hole.
	if _, err := tx.ExecContext(ctx,
		`UPDATE playlist_tracks SET position = position - 1
		  WHERE playlist_id = ? AND position > ?`, playlistID, position); err != nil {
		return nil, err
	}
	if err := writeRevision(ctx, tx, playlistID, rev, actorID, domain.OpRemove,
		map[string]any{"position": position}); err != nil {
		return nil, err
	}
	if err := bumpPlaylist(ctx, tx, playlistID, rev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, playlistID)
}

// Rename changes the title and records it as a content revision.
func (r *PlaylistRepo) Rename(ctx context.Context, playlistID, actorID, title string) (*domain.Playlist, error) {
	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var rev int
	if err := tx.QueryRowContext(ctx,
		`SELECT current_rev FROM playlists WHERE id = ?`, playlistID).Scan(&rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rev++

	if _, err := tx.ExecContext(ctx,
		`UPDATE playlists SET title = ? WHERE id = ?`, title, playlistID); err != nil {
		return nil, err
	}
	if err := writeRevision(ctx, tx, playlistID, rev, actorID, domain.OpRename,
		map[string]any{"title": title}); err != nil {
		return nil, err
	}
	if err := bumpPlaylist(ctx, tx, playlistID, rev); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, playlistID)
}

// History returns the append-only content revisions, newest first. ActorID is
// empty where the author has been anonymised (BR-4).
func (r *PlaylistRepo) History(ctx context.Context, playlistID string) ([]domain.Revision, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT id, playlist_id, rev, actor_id, op, detail, created_at
		  FROM playlist_revisions WHERE playlist_id = ? ORDER BY rev DESC`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Revision
	for rows.Next() {
		var v domain.Revision
		var actor, detail sql.NullString
		var created string
		if err := rows.Scan(&v.ID, &v.PlaylistID, &v.Rev, &actor, &v.Op, &detail, &created); err != nil {
			return nil, err
		}
		v.ActorID, v.Detail = actor.String, detail.String
		v.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, v)
	}
	return out, rows.Err()
}

// Delete removes a playlist. Revisions, tracks and annotations cascade.
func (r *PlaylistRepo) Delete(ctx context.Context, playlistID string) error {
	res, err := r.s.Writer().ExecContext(ctx, `DELETE FROM playlists WHERE id = ?`, playlistID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func writeRevision(ctx context.Context, tx *sql.Tx, playlistID string, rev int,
	actorID string, op domain.RevisionOp, detail map[string]any) error {
	id, err := newID()
	if err != nil {
		return err
	}
	var payload any
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		payload = string(b)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO playlist_revisions (id, playlist_id, rev, actor_id, op, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, playlistID, rev, nullStr(actorID), string(op), payload,
		time.Now().UTC().Format(time.RFC3339))
	return err
}

func bumpPlaylist(ctx context.Context, tx *sql.Tx, playlistID string, rev int) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE playlists SET current_rev = ?, updated_at = ? WHERE id = ?`,
		rev, time.Now().UTC().Format(time.RFC3339), playlistID)
	return err
}
