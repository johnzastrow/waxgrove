package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
	"github.com/johnzastrow/waxgrove/internal/normalize"
)

var ErrNotFound = errors.New("sqlite: not found")

// RecordRepo owns the canonical catalog.
type RecordRepo struct{ s *Store }

func (s *Store) Records() *RecordRepo { return &RecordRepo{s: s} }

// FindByISRC resolves an ISRC to its record via set membership (BR-1).
// This is step 1 of the §3.2 ladder and the reason Spotify's and Apple's
// different ISRCs for one recording converge on a single record.
func (r *RecordRepo) FindByISRC(ctx context.Context, isrc string) (*domain.Record, error) {
	var id string
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT record_id FROM record_isrcs WHERE isrc = ?`, isrc).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// FindByMBID resolves a MusicBrainz recording ID — step 2 of the ladder.
func (r *RecordRepo) FindByMBID(ctx context.Context, mbid string) (*domain.Record, error) {
	var id string
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT id FROM records WHERE mbid = ?`, mbid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Get loads one record with its full ISRC set.
func (r *RecordRepo) Get(ctx context.Context, id string) (*domain.Record, error) {
	var rec domain.Record
	var mbid, album sql.NullString
	var dur, year sql.NullInt64
	var created, updated string

	err := r.s.Reader().QueryRowContext(ctx, `
		SELECT id, mbid, title, artist_credit, album, duration_ms, year,
		       norm_title, norm_artist, tier, created_at, updated_at
		  FROM records WHERE id = ?`, id).
		Scan(&rec.ID, &mbid, &rec.Title, &rec.ArtistCredit, &album, &dur, &year,
			&rec.NormTitle, &rec.NormArtist, &rec.Tier, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rec.MBID, rec.Album = mbid.String, album.String
	rec.DurationMS, rec.Year = int(dur.Int64), int(year.Int64)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, created)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updated)

	rows, err := r.s.Reader().QueryContext(ctx,
		`SELECT isrc FROM record_isrcs WHERE record_id = ? ORDER BY isrc`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var isrc string
		if err := rows.Scan(&isrc); err != nil {
			return nil, err
		}
		rec.ISRCs = append(rec.ISRCs, isrc)
	}
	return &rec, rows.Err()
}

// Upsert finds or creates the record a candidate refers to, following BR-1's
// identity rules, and merges any new ISRCs into the existing set.
//
// The transaction here is short and touches no network — §7.2 requires all
// resolution to have happened before this is called.
func (r *RecordRepo) Upsert(ctx context.Context, c domain.Candidate, tier domain.Tier) (*domain.Record, error) {
	// Identity lookups first, cheapest to most expensive.
	if c.ISRC != "" {
		if rec, err := r.FindByISRC(ctx, c.ISRC); err == nil {
			return r.enrich(ctx, rec, c, tier)
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if c.MBID != "" {
		if rec, err := r.FindByMBID(ctx, c.MBID); err == nil {
			return r.enrich(ctx, rec, c, tier)
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}

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
		INSERT INTO records (id, mbid, title, artist_credit, album, duration_ms, year,
		                     norm_title, norm_artist, tier, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, nullStr(c.MBID), c.Title, c.Artist, nullStr(c.Album),
		nullInt(c.DurationMS), nullInt(c.Year),
		normalize.Key(c.Title), normalize.Key(c.Artist), string(tier), now, now); err != nil {
		return nil, fmt.Errorf("insert record: %w", err)
	}
	if c.ISRC != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO record_isrcs (record_id, isrc) VALUES (?, ?)`, id, c.ISRC); err != nil {
			return nil, fmt.Errorf("insert isrc: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// enrich folds a newly-seen ISRC or MBID into an existing record, and promotes
// an ambient record to curated when it is deliberately used (F24).
func (r *RecordRepo) enrich(ctx context.Context, rec *domain.Record, c domain.Candidate, tier domain.Tier) (*domain.Record, error) {
	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// A second service supplying a different ISRC for the same recording adds
	// to the set rather than creating a rival record (BR-1).
	if c.ISRC != "" && !contains(rec.ISRCs, c.ISRC) {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO record_isrcs (record_id, isrc) VALUES (?, ?)`,
			rec.ID, c.ISRC); err != nil {
			return nil, err
		}
	}
	// Learning the MBID upgrades an ISRC-only record to full identity.
	if c.MBID != "" && rec.MBID == "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE records SET mbid = ?, updated_at = ? WHERE id = ?`,
			c.MBID, time.Now().UTC().Format(time.RFC3339), rec.ID); err != nil {
			return nil, err
		}
	}
	if tier == domain.TierCurated && rec.Tier == domain.TierAmbient {
		if _, err := tx.ExecContext(ctx,
			`UPDATE records SET tier = 'curated', updated_at = ? WHERE id = ?`,
			time.Now().UTC().Format(time.RFC3339), rec.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, rec.ID)
}

// Search runs the local fuzzy search behind F4. Curated records only — ambient
// records exist to speed resolution, not to fill the Grove with songs nobody
// chose (BR-6).
func (r *RecordRepo) Search(ctx context.Context, query string, limit int) ([]domain.Record, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}

	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT r.id
		  FROM records_fts f
		  JOIN records r ON r.rowid = f.rowid
		 WHERE records_fts MATCH ?
		   AND r.tier = 'curated'
		 ORDER BY rank
		 LIMIT ?`, ftsQuery(q), limit)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	return r.getMany(ctx, ids)
}

// ftsQuery turns free user input into a safe FTS5 prefix query. FTS5 has its
// own expression syntax, so quoting each term prevents a stray operator from
// changing the meaning of the query or erroring out.
func ftsQuery(s string) string {
	fields := strings.Fields(s)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		quoted = append(quoted, `"`+f+`"*`)
	}
	if len(quoted) == 0 {
		return `""`
	}
	return strings.Join(quoted, " ")
}

// FuzzyMatch is step 4 of the §3.2 ladder: normalised comparison with a
// duration window, used only when the identity lookups miss.
func (r *RecordRepo) FuzzyMatch(ctx context.Context, c domain.Candidate) ([]domain.Record, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT id FROM records
		 WHERE norm_artist = ? AND norm_title = ?
		 LIMIT 25`, normalize.Key(c.Artist), normalize.Key(c.Title))
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	return r.getMany(ctx, ids)
}

// scanIDs drains an id cursor completely and closes it.
//
// Draining before doing anything else is not a style preference, it is what
// keeps the pool from deadlocking: an open cursor holds its connection, so a
// per-row query issued inside the loop needs a second one. With N concurrent
// searches against a pool of N, every connection ends up held by a cursor
// waiting for a connection, and nothing ever completes. That is a permanent
// hang, not a slowdown — the health check goes red and stays red.
func scanIDs(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
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
	return ids, rows.Close()
}

// getMany loads records by id, preserving the order it was given — callers
// have already ranked them and that ranking is the answer.
//
// Two queries total rather than the 1+2N a per-id loop would cost. That matters
// less on local SQLite than the deadlock above, but a 50-result search issuing
// 101 queries is worth not doing.
func (r *RecordRepo) getMany(ctx context.Context, ids []string) ([]domain.Record, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ph := placeholders(len(ids))

	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT id, mbid, title, artist_credit, album, duration_ms, year,
		       norm_title, norm_artist, tier, created_at, updated_at
		  FROM records WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*domain.Record, len(ids))
	if err := func() error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var rec domain.Record
			var mbid, album sql.NullString
			var dur, year sql.NullInt64
			var created, updated string
			if err := rows.Scan(&rec.ID, &mbid, &rec.Title, &rec.ArtistCredit, &album,
				&dur, &year, &rec.NormTitle, &rec.NormArtist, &rec.Tier,
				&created, &updated); err != nil {
				return err
			}
			rec.MBID, rec.Album = mbid.String, album.String
			rec.DurationMS, rec.Year = int(dur.Int64), int(year.Int64)
			rec.CreatedAt, _ = time.Parse(time.RFC3339, created)
			rec.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
			byID[rec.ID] = &rec
		}
		return rows.Err()
	}(); err != nil {
		return nil, err
	}

	// ISRCs for the whole set in one pass. A record has a set of them (BR-1),
	// so this is a join, not a lookup.
	isrcRows, err := r.s.Reader().QueryContext(ctx,
		`SELECT record_id, isrc FROM record_isrcs
		  WHERE record_id IN (`+ph+`) ORDER BY isrc`, args...)
	if err != nil {
		return nil, err
	}
	if err := func() error {
		defer func() { _ = isrcRows.Close() }()
		for isrcRows.Next() {
			var rid, isrc string
			if err := isrcRows.Scan(&rid, &isrc); err != nil {
				return err
			}
			if rec := byID[rid]; rec != nil {
				rec.ISRCs = append(rec.ISRCs, isrc)
			}
		}
		return isrcRows.Err()
	}(); err != nil {
		return nil, err
	}

	out := make([]domain.Record, 0, len(ids))
	for _, id := range ids {
		if rec := byID[id]; rec != nil {
			out = append(out, *rec)
		}
	}
	return out, nil
}

// placeholders builds "?, ?, ?" for an IN clause. The count comes from a slice
// length we control, never from user input, and every value is still bound —
// this constructs the shape of the query, not any part of its data.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// RecordProvenance notes that a user deliberately contributed a record.
func (r *RecordRepo) RecordProvenance(ctx context.Context, recordID, userID string) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = r.s.Writer().ExecContext(ctx,
		`INSERT INTO record_provenance (id, record_id, user_id, added_at) VALUES (?, ?, ?, ?)`,
		id, recordID, nullStr(userID), time.Now().UTC().Format(time.RFC3339))
	return err
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
