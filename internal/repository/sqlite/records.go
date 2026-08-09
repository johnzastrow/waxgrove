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
// SearchOptions is a search across everything, a search within named fields,
// or both at once.
//
// Any is the free-text box; the rest narrow it. They compose — "anything
// mentioning moon, by Drake, from 1972" is one query, not three searches the
// user has to intersect in their head.
type SearchOptions struct {
	Any    string
	Title  string
	Artist string
	Album  string
	Year   int
	Limit  int
}

// Empty reports whether there is anything to search for.
func (o SearchOptions) Empty() bool {
	return strings.TrimSpace(o.Any) == "" && strings.TrimSpace(o.Title) == "" &&
		strings.TrimSpace(o.Artist) == "" && strings.TrimSpace(o.Album) == "" && o.Year == 0
}

// Search runs a free-text query, kept for callers that only have one.
func (r *RecordRepo) Search(ctx context.Context, query string, limit int) ([]domain.Record, error) {
	return r.SearchBy(ctx, SearchOptions{Any: query, Limit: limit})
}

// SearchBy searches the catalogue, optionally within named fields.
//
// FTS5 already indexes title, artist and album as separate columns, so scoping
// a term to one of them is a column filter rather than a second query. Year is
// an ordinary column and is filtered in SQL alongside.
func (r *RecordRepo) SearchBy(ctx context.Context, opts SearchOptions) ([]domain.Record, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if opts.Empty() {
		return nil, nil
	}

	// A year on its own is a perfectly reasonable search, and FTS has nothing
	// to match on — so the FTS clause is only applied when there are terms.
	match := ftsQueryFor(opts)
	args := []any{}
	sql := `SELECT r.id FROM records r`
	where := ` WHERE r.tier = 'curated'`
	order := ` ORDER BY r.norm_artist, r.norm_title`

	if match != "" {
		sql = `SELECT r.id FROM records_fts f JOIN records r ON r.rowid = f.rowid`
		where += ` AND records_fts MATCH ?`
		args = append(args, match)
		order = ` ORDER BY rank`
	}
	if opts.Year != 0 {
		where += ` AND r.year = ?`
		args = append(args, opts.Year)
	}
	args = append(args, limit)

	rows, err := r.s.Reader().QueryContext(ctx, sql+where+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	return r.getMany(ctx, ids)
}

// ListOptions selects part of the catalogue.
type ListOptions struct {
	// AddedBy narrows to records this user deliberately contributed. Empty
	// means the whole shared catalogue.
	AddedBy string
	Limit   int
	Offset  int
	// Sort is "artist" (default) or "recent".
	Sort string
}

// List browses the local catalogue (F4).
//
// Search answers "where is this song"; this answers "what have we got". They
// are different questions, and a search box cannot answer the second — you
// cannot search for something you have forgotten you added.
//
// Ambient records stay out, exactly as they do in search: they exist to make
// resolution instant and are not part of what the group chose (D11, F24).
func (r *RecordRepo) List(ctx context.Context, opts ListOptions) ([]domain.Record, int, error) {
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 50
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	where := "r.tier = 'curated'"
	args := []any{}
	if opts.AddedBy != "" {
		// A record can be contributed by several people; EXISTS avoids the
		// duplicate rows a join would produce.
		where += ` AND EXISTS (SELECT 1 FROM record_provenance p
		                        WHERE p.record_id = r.id AND p.user_id = ?)`
		args = append(args, opts.AddedBy)
	}

	var total int
	if err := r.s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM records r WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := "r.norm_artist, r.norm_title"
	if opts.Sort == "recent" {
		order = "r.created_at DESC"
	}

	rows, err := r.s.Reader().QueryContext(ctx,
		`SELECT r.id FROM records r WHERE `+where+
			` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(args, opts.Limit, opts.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, 0, err
	}
	recs, err := r.getMany(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	return recs, total, nil
}

// ftsQuery turns free user input into a safe FTS5 prefix query. FTS5 has its
// own expression syntax, so quoting each term prevents a stray operator from
// changing the meaning of the query or erroring out.
func ftsQuery(s string) string {
	return terms(s, "")
}

// ftsQueryFor builds one FTS expression from the free-text box and the scoped
// fields together, so they intersect rather than being run separately.
func ftsQueryFor(o SearchOptions) string {
	parts := make([]string, 0, 4)
	for _, p := range []struct{ col, val string }{
		{"", o.Any},
		{"title", o.Title},
		{"artist_credit", o.Artist},
		{"album", o.Album},
	} {
		if t := terms(p.val, p.col); t != "" {
			parts = append(parts, t)
		}
	}
	// Space-separated is AND in FTS5, which is what "by Drake, from Rumours"
	// means to a person.
	return strings.Join(parts, " ")
}

// terms turns user input into a safe FTS5 prefix expression, optionally scoped
// to one indexed column.
//
// Quotes are stripped rather than escaped: FTS5's grammar treats them as
// phrase delimiters, and a stray one from a song title would otherwise turn a
// search into a syntax error.
func terms(s, column string) string {
	fields := strings.Fields(s)
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		q := `"` + f + `"*`
		if column != "" {
			q = column + ":" + q
		}
		quoted = append(quoted, q)
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
