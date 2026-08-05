package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SyncRepo tracks the copies of a playlist that live on providers.
//
// Sync is tracked one-way (D10): the provider copy is a projection, never a
// source of truth, and Waxgrove's revision history stays authoritative. What
// this table buys is the ability to say *how far behind* that copy is, because
// "synced" is not a boolean (F21) — a playlist synced at revision 4 and since
// edited twice is a different situation from one that is up to date, and the
// user should be able to see which they have.
type SyncRepo struct{ s *Store }

func (s *Store) Syncs() *SyncRepo { return &SyncRepo{s: s} }

// Sync is one playlist's relationship with one service in one storefront.
type Sync struct {
	PlaylistID         string
	Service            string
	Storefront         string
	ProviderPlaylistID string
	LastSyncedRev      int
	LastSyncedAt       time.Time
	// Diverged means the copy was edited on the provider side. A re-sync must
	// ask rather than overwrite — the same "never silently mismatch" principle
	// that governs matching (§3.2, D10).
	Diverged bool
}

// Behind reports how many revisions the provider copy is behind.
func (s Sync) Behind(currentRev int) int {
	if currentRev <= s.LastSyncedRev {
		return 0
	}
	return currentRev - s.LastSyncedRev
}

// ErrNoSync means this playlist has never been sent to that service.
var ErrNoSync = errors.New("sqlite: no sync recorded")

// Record notes a successful export.
func (r *SyncRepo) Record(ctx context.Context, playlistID, service, storefront,
	providerPlaylistID string, rev int) error {

	_, err := r.s.Writer().ExecContext(ctx, `
		INSERT INTO playlist_syncs
		    (playlist_id, service, storefront, provider_playlist_id,
		     last_synced_rev, last_synced_at, diverged)
		VALUES (?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT (playlist_id, service, storefront) DO UPDATE SET
		    provider_playlist_id = excluded.provider_playlist_id,
		    last_synced_rev      = excluded.last_synced_rev,
		    last_synced_at       = excluded.last_synced_at,
		    diverged             = 0`,
		playlistID, service, storefront, nullStr(providerPlaylistID),
		rev, nowRFC3339())
	return err
}

// MarkDiverged records that the provider copy was edited elsewhere.
func (r *SyncRepo) MarkDiverged(ctx context.Context, playlistID, service, storefront string) error {
	_, err := r.s.Writer().ExecContext(ctx, `
		UPDATE playlist_syncs SET diverged = 1
		 WHERE playlist_id = ? AND service = ? AND storefront = ?`,
		playlistID, service, storefront)
	return err
}

// Get returns one sync relationship.
func (r *SyncRepo) Get(ctx context.Context, playlistID, service, storefront string) (Sync, error) {
	var (
		s          Sync
		providerID sql.NullString
		syncedAt   sql.NullString
		rev        sql.NullInt64
		diverged   int
	)
	err := r.s.Reader().QueryRowContext(ctx, `
		SELECT playlist_id, service, storefront, provider_playlist_id,
		       last_synced_rev, last_synced_at, diverged
		  FROM playlist_syncs
		 WHERE playlist_id = ? AND service = ? AND storefront = ?`,
		playlistID, service, storefront).
		Scan(&s.PlaylistID, &s.Service, &s.Storefront, &providerID,
			&rev, &syncedAt, &diverged)
	if errors.Is(err, sql.ErrNoRows) {
		return Sync{}, ErrNoSync
	}
	if err != nil {
		return Sync{}, err
	}
	s.ProviderPlaylistID = providerID.String
	s.LastSyncedRev = int(rev.Int64)
	s.Diverged = diverged == 1
	if syncedAt.Valid {
		s.LastSyncedAt, _ = time.Parse(time.RFC3339, syncedAt.String)
	}
	return s, nil
}

// ListForPlaylist returns every service this playlist has been sent to.
func (r *SyncRepo) ListForPlaylist(ctx context.Context, playlistID string) ([]Sync, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT playlist_id, service, storefront, provider_playlist_id,
		       last_synced_rev, last_synced_at, diverged
		  FROM playlist_syncs WHERE playlist_id = ?`, playlistID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Sync
	for rows.Next() {
		var (
			s          Sync
			providerID sql.NullString
			syncedAt   sql.NullString
			rev        sql.NullInt64
			diverged   int
		)
		if err := rows.Scan(&s.PlaylistID, &s.Service, &s.Storefront, &providerID,
			&rev, &syncedAt, &diverged); err != nil {
			return nil, err
		}
		s.ProviderPlaylistID = providerID.String
		s.LastSyncedRev = int(rev.Int64)
		s.Diverged = diverged == 1
		if syncedAt.Valid {
			s.LastSyncedAt, _ = time.Parse(time.RFC3339, syncedAt.String)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
