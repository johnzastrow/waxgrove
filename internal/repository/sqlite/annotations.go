package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

// AnnotationRepo holds what people say *about* a playlist: ratings, tags and
// comments (F18, §3.4).
//
// The governing rule is BR-3: **annotations never produce a revision.** A
// playlist's content history records what the playlist *is*; annotations record
// what people think of it. Mixing them would mean a rating bumping a revision
// number, which would in turn make an exported copy look out of date — and
// would make blame useless, since most entries would be somebody liking it.
//
// The second rule is D8: annotations are per-user and never alter the owner's
// playlist. Anyone may rate, tag and comment on a playlist shared with them;
// nobody but the owner can change what is in it.
type AnnotationRepo struct{ s *Store }

func (s *Store) Annotations() *AnnotationRepo { return &AnnotationRepo{s: s} }

// Tag visibility.
const (
	TagPrivate = "private"
	TagShared  = "shared"
)

var (
	// ErrBadRating means the value is outside 1..5.
	ErrBadRating = errors.New("sqlite: a rating must be between 1 and 5")
	// ErrEmptyText means a tag or comment had no content.
	ErrEmptyText = errors.New("sqlite: that cannot be empty")
	// ErrNotYours means the annotation belongs to somebody else.
	ErrNotYours = errors.New("sqlite: that annotation is not yours")
)

// ------------------------------------------------------------------ ratings --

// Rate records this user's rating, replacing any previous one.
//
// Per-user with a derived aggregate (D8): there is no single "the rating", and
// storing one would mean somebody's opinion overwriting somebody else's.
func (r *AnnotationRepo) Rate(ctx context.Context, playlistID, userID string, value int) error {
	if value < 1 || value > 5 {
		return ErrBadRating
	}
	_, err := r.s.Writer().ExecContext(ctx, `
		INSERT INTO ratings (playlist_id, user_id, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (playlist_id, user_id) DO UPDATE SET
		    value = excluded.value, updated_at = excluded.updated_at`,
		playlistID, userID, value, nowRFC3339())
	return err
}

// Unrate withdraws this user's rating.
func (r *AnnotationRepo) Unrate(ctx context.Context, playlistID, userID string) error {
	_, err := r.s.Writer().ExecContext(ctx,
		`DELETE FROM ratings WHERE playlist_id = ? AND user_id = ?`, playlistID, userID)
	return err
}

// Ratings is the aggregate plus this user's own.
type Ratings struct {
	Average float64
	Count   int
	Mine    int // 0 when this user has not rated it
}

// Ratings returns the aggregate and the caller's own rating in one read.
func (r *AnnotationRepo) Ratings(ctx context.Context, playlistID, userID string) (Ratings, error) {
	var out Ratings
	var avg sql.NullFloat64
	if err := r.s.Reader().QueryRowContext(ctx, `
		SELECT AVG(value), COUNT(*) FROM ratings WHERE playlist_id = ?`,
		playlistID).Scan(&avg, &out.Count); err != nil {
		return out, err
	}
	out.Average = avg.Float64

	var mine sql.NullInt64
	if err := r.s.Reader().QueryRowContext(ctx,
		`SELECT value FROM ratings WHERE playlist_id = ? AND user_id = ?`,
		playlistID, userID).Scan(&mine); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out.Mine = int(mine.Int64)
	return out, nil
}

// --------------------------------------------------------------------- tags --

// AddTag attaches a tag. Private tags are visible only to their author, and
// that is enforced here rather than in the UI (§6).
func (r *AnnotationRepo) AddTag(ctx context.Context, playlistID, userID, name, visibility string) (*domain.Tag, error) {
	name = normaliseTag(name)
	if name == "" {
		return nil, ErrEmptyText
	}
	if visibility != TagPrivate && visibility != TagShared {
		visibility = TagPrivate // the safer default if a caller gets it wrong
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	// Re-adding a tag you already have is a no-op, not an error: the user's
	// intent ("this playlist is mellow") is already satisfied.
	if _, err := r.s.Writer().ExecContext(ctx, `
		INSERT INTO tags (id, playlist_id, user_id, name, visibility, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (playlist_id, user_id, name, visibility) DO NOTHING`,
		id, playlistID, userID, name, visibility, nowRFC3339()); err != nil {
		return nil, err
	}
	var t domain.Tag
	if err := r.s.Reader().QueryRowContext(ctx, `
		SELECT id, name, visibility FROM tags
		 WHERE playlist_id = ? AND user_id = ? AND name = ? AND visibility = ?`,
		playlistID, userID, name, visibility).Scan(&t.ID, &t.Name, &t.Visibility); err != nil {
		return nil, err
	}
	t.Mine = true
	return &t, nil
}

// normaliseTag folds case and collapses whitespace so "Late Night" and
// "late  night" are the same tag rather than two that look identical.
func normaliseTag(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// RemoveTag deletes one of the caller's own tags.
func (r *AnnotationRepo) RemoveTag(ctx context.Context, tagID, userID string) error {
	res, err := r.s.Writer().ExecContext(ctx,
		`DELETE FROM tags WHERE id = ? AND user_id = ?`, tagID, userID)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrNotYours)
}

// Tags returns the shared tags plus the caller's own private ones.
//
// Somebody else's private tag must never appear here — that is the entire
// meaning of "private", and it is a server-side filter, not a UI one.
func (r *AnnotationRepo) Tags(ctx context.Context, playlistID, userID string) ([]domain.Tag, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT id, name, visibility, user_id = ? AS mine
		  FROM tags
		 WHERE playlist_id = ?
		   AND (visibility = ? OR user_id = ?)
		 ORDER BY visibility, name`,
		userID, playlistID, TagShared, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Tag
	for rows.Next() {
		var t domain.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Visibility, &t.Mine); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ----------------------------------------------------------------- comments --

// AddComment posts a comment.
func (r *AnnotationRepo) AddComment(ctx context.Context, playlistID, userID, body string) (*domain.Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, ErrEmptyText
	}
	if len(body) > MaxCommentLen {
		body = body[:MaxCommentLen]
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := nowRFC3339()
	if _, err := r.s.Writer().ExecContext(ctx, `
		INSERT INTO comments (id, playlist_id, user_id, body, created_at)
		VALUES (?, ?, ?, ?, ?)`, id, playlistID, userID, body, now); err != nil {
		return nil, err
	}
	c := r.commentFrom(id, body, now, userID)
	return &c, nil
}

// MaxCommentLen bounds a comment. Nothing is unlimited (§6), and a comment
// long enough to matter is long enough to be a playlist description.
const MaxCommentLen = 2000

func (r *AnnotationRepo) commentFrom(id, body, created, userID string) domain.Comment {
	return domain.Comment{ID: id, Body: body, CreatedAt: created, AuthorID: userID, Mine: true}
}

// DeleteComment soft-deletes the caller's own comment.
//
// Soft, because a hard delete would silently reshape a conversation other
// people replied to; the body is cleared so nothing of the content survives.
func (r *AnnotationRepo) DeleteComment(ctx context.Context, commentID, userID string) error {
	res, err := r.s.Writer().ExecContext(ctx, `
		UPDATE comments SET deleted_at = ?, body = ''
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		nowRFC3339(), commentID, userID)
	if err != nil {
		return err
	}
	return mustAffect(res, ErrNotYours)
}

// Comments returns a playlist's conversation, oldest first.
//
// Deleted ones are omitted entirely. An author who has been erased shows as a
// departed member rather than a blank: the comment is content, and it survives
// the account (BR-4).
func (r *AnnotationRepo) Comments(ctx context.Context, playlistID, userID string) ([]domain.Comment, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT c.id, c.body, c.created_at,
		       COALESCE(c.user_id, ''), COALESCE(u.display_name, '')
		  FROM comments c
		  LEFT JOIN users u ON u.id = c.user_id
		 WHERE c.playlist_id = ? AND c.deleted_at IS NULL
		 ORDER BY c.created_at`, playlistID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Comment
	for rows.Next() {
		var c domain.Comment
		if err := rows.Scan(&c.ID, &c.Body, &c.CreatedAt, &c.AuthorID, &c.Author); err != nil {
			return nil, err
		}
		if c.Author == "" {
			c.Author = "a departed member"
		}
		c.Mine = c.AuthorID != "" && c.AuthorID == userID
		out = append(out, c)
	}
	return out, rows.Err()
}
