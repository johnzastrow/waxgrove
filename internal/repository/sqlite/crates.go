package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// CrateRepo is the persistent per-user staging area (F16, §3.3).
//
// The crate is what makes "build a playlist from anything" true. Candidates
// arrive from catalogue search, a metadata source, a pasted list, a JSPF file,
// a provider import — accumulate over days if need be — and commit as one
// playlist, which is one authored event rather than twenty.
//
// Two properties do the work:
//
//   - **Nothing is dropped.** An item that could not be resolved stays as raw
//     text with its confidence recorded, so the user decides later rather than
//     discovering afterwards that a song quietly vanished (BR-5, §3.2).
//   - **Disambiguation happens before commit** (F12). By the time a playlist
//     exists, its bad matches have already been settled — the alternative is
//     fixing a playlist that is already shared.
type CrateRepo struct{ s *Store }

func (s *Store) Crates() *CrateRepo { return &CrateRepo{s: s} }

// Crate item statuses.
const (
	CrateResolved   = "resolved"
	CrateAmbiguous  = "ambiguous"
	CrateUnresolved = "unresolved"
)

// ErrCrateItemNotFound is returned for an unknown item.
var ErrCrateItemNotFound = errors.New("sqlite: no such crate item")

// forUser returns the user's crate id, creating it on first use.
//
// A crate is implicit: a user should never have to make one before staging a
// song, and "you have no crate" is not a state worth expressing.
func (r *CrateRepo) forUser(ctx context.Context, userID string) (string, error) {
	var id string
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT id FROM crates WHERE user_id = ?`, userID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	id, err = newID()
	if err != nil {
		return "", err
	}
	// ON CONFLICT covers two requests racing to create the same user's crate.
	if _, err := r.s.Writer().ExecContext(ctx,
		`INSERT INTO crates (id, user_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO NOTHING`,
		id, userID, nowRFC3339()); err != nil {
		return "", err
	}
	if err := r.s.Reader().QueryRowContext(ctx,
		`SELECT id FROM crates WHERE user_id = ?`, userID).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// Add stages one candidate and whatever the resolver made of it.
//
// The match is stored rather than re-derived on read: how something matched is
// part of the record's history, and a later re-resolution should be visible as
// a change rather than silently replacing what the user saw (§3.2).
func (r *CrateRepo) Add(ctx context.Context, userID string, c domain.Candidate, m domain.Match) (*domain.CrateItem, error) {
	crateID, err := r.forUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}

	status := CrateUnresolved
	recordID := ""
	switch {
	case m.Resolved():
		status, recordID = CrateResolved, m.Record.ID
	case m.Record != nil || len(m.Alternatives) > 0:
		// Something was found, but not confidently enough to apply (§3.2).
		status = CrateAmbiguous
		if m.Record != nil {
			recordID = m.Record.ID
		}
	}

	// The original candidate is kept whatever happened, so an item can be
	// re-resolved later against a warmer catalogue, and so an unresolved one
	// still shows the user what they asked for.
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}

	var position int
	if err := r.s.Reader().QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM crate_items WHERE crate_id = ?`,
		crateID).Scan(&position); err != nil {
		return nil, err
	}

	if _, err := r.s.Writer().ExecContext(ctx, `
		INSERT INTO crate_items
		    (id, crate_id, position, record_id, raw_candidate, source_ref,
		     resolution_method, confidence, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, crateID, position, nullStr(recordID), string(raw), nullStr(c.SourceRef),
		nullStr(string(m.Method)), m.Confidence, status, nowRFC3339()); err != nil {
		return nil, err
	}
	return r.item(ctx, id)
}

// List returns everything staged, oldest first.
func (r *CrateRepo) List(ctx context.Context, userID string) ([]domain.CrateItem, error) {
	crateID, err := r.forUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := r.s.Reader().QueryContext(ctx, crateColumns+
		` WHERE crate_id = ? ORDER BY position`, crateID)
	if err != nil {
		return nil, err
	}
	ids, items, err := scanCrateRows(rows)
	if err != nil {
		return nil, err
	}
	return r.attachRecords(ctx, ids, items)
}

const crateColumns = `
	SELECT id, position, record_id, raw_candidate, source_ref,
	       resolution_method, confidence, status
	  FROM crate_items`

func scanCrateRows(rows *sql.Rows) ([]string, []domain.CrateItem, error) {
	defer func() { _ = rows.Close() }()
	var (
		recordIDs []string
		items     []domain.CrateItem
	)
	for rows.Next() {
		var (
			it                       domain.CrateItem
			recordID, raw, sourceRef sql.NullString
			method                   sql.NullString
			confidence               sql.NullFloat64
		)
		if err := rows.Scan(&it.ID, &it.Position, &recordID, &raw, &sourceRef,
			&method, &confidence, &it.Status); err != nil {
			return nil, nil, err
		}
		it.RecordID = recordID.String
		it.SourceRef = sourceRef.String
		it.Method = domain.MatchMethod(method.String)
		it.Confidence = confidence.Float64
		if raw.Valid {
			_ = json.Unmarshal([]byte(raw.String), &it.Candidate)
		}
		if it.RecordID != "" {
			recordIDs = append(recordIDs, it.RecordID)
		}
		items = append(items, it)
	}
	return recordIDs, items, rows.Err()
}

// attachRecords fills in the catalogue side in one batch rather than per item.
func (r *CrateRepo) attachRecords(ctx context.Context, ids []string, items []domain.CrateItem) ([]domain.CrateItem, error) {
	if len(ids) == 0 {
		return items, nil
	}
	recs, err := r.s.Records().getMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*domain.Record, len(recs))
	for i := range recs {
		byID[recs[i].ID] = &recs[i]
	}
	for i := range items {
		if rec := byID[items[i].RecordID]; rec != nil {
			items[i].Record = rec
		}
	}
	return items, nil
}

func (r *CrateRepo) item(ctx context.Context, id string) (*domain.CrateItem, error) {
	rows, err := r.s.Reader().QueryContext(ctx, crateColumns+` WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	ids, items, err := scanCrateRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrCrateItemNotFound
	}
	items, err = r.attachRecords(ctx, ids, items)
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

// Resolve settles an ambiguous or unresolved item against a chosen record.
//
// This is what F12 writes to: the user picked from the alternatives, so the
// item becomes resolved and the method records that a human decided, not a
// score — which keeps a later audit honest about what was guessed and what
// was chosen.
func (r *CrateRepo) Resolve(ctx context.Context, userID, itemID, recordID string) (*domain.CrateItem, error) {
	crateID, err := r.forUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	res, err := r.s.Writer().ExecContext(ctx, `
		UPDATE crate_items
		   SET record_id = ?, status = ?, resolution_method = ?, confidence = 1.0
		 WHERE id = ? AND crate_id = ?`,
		recordID, CrateResolved, domain.MatchChosen, itemID, crateID)
	if err != nil {
		return nil, err
	}
	if err := mustAffect(res, ErrCrateItemNotFound); err != nil {
		return nil, err
	}
	return r.item(ctx, itemID)
}

// Remove drops one item. Scoped to the owner's crate, so an id from elsewhere
// cannot delete somebody else's staging.
func (r *CrateRepo) Remove(ctx context.Context, userID, itemID string) error {
	crateID, err := r.forUser(ctx, userID)
	if err != nil {
		return err
	}
	res, err := r.s.Writer().ExecContext(ctx,
		`DELETE FROM crate_items WHERE id = ? AND crate_id = ?`, itemID, crateID)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrCrateItemNotFound)
}

// Clear empties the crate.
func (r *CrateRepo) Clear(ctx context.Context, userID string) error {
	crateID, err := r.forUser(ctx, userID)
	if err != nil {
		return err
	}
	_, err = r.s.Writer().ExecContext(ctx,
		`DELETE FROM crate_items WHERE crate_id = ?`, crateID)
	return err
}

// Commit turns the resolved part of a crate into a playlist.
//
// Only resolved items go. Anything still ambiguous or unresolved **stays in the
// crate** rather than being dropped or guessed at — the user asked to commit
// what was ready, not to abandon the rest (§3.3, BR-5). The count of what
// stayed is returned so the caller can say so plainly.
func (r *CrateRepo) Commit(ctx context.Context, userID, title, description string) (*domain.Playlist, int, error) {
	items, err := r.List(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	var (
		recordIDs []string
		committed []string
		left      int
	)

	for _, it := range items {
		if it.Status == CrateResolved && it.RecordID != "" {
			recordIDs = append(recordIDs, it.RecordID)
			committed = append(committed, it.ID)
			continue
		}
		left++
	}
	if len(recordIDs) == 0 {
		return nil, left, ErrNothingToCommit
	}

	playlist, err := r.s.Playlists().Create(ctx, userID, title, description)
	if err != nil {
		return nil, 0, err
	}
	// One revision for the whole commit: twenty songs staged over a week is
	// still one authored event (§3.3).
	updated, err := r.s.Playlists().AddRecords(ctx, playlist.ID, userID, recordIDs)
	if err != nil {
		return nil, 0, err
	}

	// Only the committed items leave. Whatever still needs a decision stays
	// staged, which is the point of the crate.
	for _, id := range committed {
		if _, err := r.s.Writer().ExecContext(ctx,
			`DELETE FROM crate_items WHERE id = ?`, id); err != nil {
			return nil, 0, err
		}
	}
	// Provenance is per record, not per crate item: this user deliberately put
	// these songs into the shared catalogue (§3.0, F24).
	for _, recordID := range recordIDs {
		_ = r.s.Records().RecordProvenance(ctx, recordID, userID)
	}
	return updated, left, nil
}

// ErrNothingToCommit means every staged item still needs a decision.
var ErrNothingToCommit = errors.New("sqlite: nothing in the crate is resolved yet")

// Count reports how much is staged, for a badge on the nav.
func (r *CrateRepo) Count(ctx context.Context, userID string) (total, needsAttention int, err error) {
	crateID, err := r.forUser(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	err = r.s.Reader().QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN status != ? THEN 1 ELSE 0 END), 0)
		  FROM crate_items WHERE crate_id = ?`,
		CrateResolved, crateID).Scan(&total, &needsAttention)
	return total, needsAttention, err
}
