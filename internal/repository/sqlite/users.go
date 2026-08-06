package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/johnzastrow/waxgrove/internal/auth"
	"github.com/johnzastrow/waxgrove/internal/domain"
)

var (
	ErrEmailTaken   = errors.New("sqlite: email already registered")
	ErrInviteBad    = errors.New("sqlite: invite code is invalid, used, or expired")
	ErrCredentials  = errors.New("sqlite: invalid credentials")
	ErrSessionStale = errors.New("sqlite: session expired")
)

// UserRepo owns accounts, invites and sessions.
type UserRepo struct{ s *Store }

func (s *Store) Users() *UserRepo { return &UserRepo{s: s} }

// SessionTTL is how long a login lasts.
const SessionTTL = 30 * 24 * time.Hour

// InviteTTL is how long an unredeemed invite stays valid.
const InviteTTL = 14 * 24 * time.Hour

// Register creates a local account. Registration is invite-only — this is a
// friends app, not a public service (§6) — except for the very first user,
// who becomes the admin because there is nobody to invite them.
func (r *UserRepo) Register(ctx context.Context, email, displayName, password, inviteCode string) (*domain.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || displayName == "" {
		return nil, errors.New("sqlite: email and display name are required")
	}
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		return nil, err // includes ErrWeak
	}

	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, ErrEmailTaken
	}

	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, err
	}

	role, invitedBy := domain.RoleMember, ""
	if total == 0 {
		// Bootstrap: the first account owns the instance.
		role = domain.RoleAdmin
	} else {
		var createdBy sql.NullString
		var expires string
		var usedBy sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT created_by, used_by, expires_at FROM invites WHERE code = ?`, inviteCode).
			Scan(&createdBy, &usedBy, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteBad
		}
		if err != nil {
			return nil, err
		}
		if usedBy.Valid {
			return nil, ErrInviteBad
		}
		exp, perr := time.Parse(time.RFC3339, expires)
		if perr != nil || time.Now().UTC().After(exp) {
			return nil, ErrInviteBad
		}
		invitedBy = createdBy.String
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name, password_hash, role, invited_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, email, displayName, hash, role, nullStr(invitedBy), now); err != nil {
		return nil, err
	}
	if inviteCode != "" && total > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE invites SET used_by = ? WHERE code = ?`, id, inviteCode); err != nil {
			return nil, err
		}
	}
	// Every user gets a working crate up front (§3.3).
	crateID, err := newID()
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO crates (id, user_id, created_at) VALUES (?, ?, ?)`, crateID, id, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Authenticate verifies a password and returns the user.
//
// The same error is returned whether the account is unknown or the password is
// wrong, so the response cannot be used to enumerate registered addresses.
func (r *UserRepo) Authenticate(ctx context.Context, email, password string) (*domain.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	var id, hash string
	var deleted sql.NullString
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT id, COALESCE(password_hash, ''), deleted_at FROM users WHERE email = ?`, email).
		Scan(&id, &hash, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		// Spend comparable time on an unknown account so timing does not leak
		// whether the address exists.
		auth.EqualiseTiming(ctx, password)
		return nil, ErrCredentials
	}
	if err != nil {
		return nil, err
	}
	if deleted.Valid || hash == "" {
		auth.EqualiseTiming(ctx, password)
		return nil, ErrCredentials
	}
	if err := auth.VerifyPassword(ctx, password, hash); err != nil {
		return nil, ErrCredentials
	}
	return r.Get(ctx, id)
}

// ChangePassword replaces a password, verifying the current one first.
//
// The current password is required even though the caller already holds a
// valid session: a session is "this browser was logged in at some point", and a
// password change is exactly the operation an attacker performs on a borrowed
// laptop to make their access permanent. Re-proving the password is what turns
// a stolen session from permanent into temporary.
//
// Every other session is ended. If the reason for changing a password is that
// somebody else has one, leaving their session alive defeats the whole point —
// so the change logs everyone out, including the caller, who signs back in.
func (r *UserRepo) ChangePassword(ctx context.Context, userID, current, next string) error {
	var hash string
	var deleted sql.NullString
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT COALESCE(password_hash, ''), deleted_at FROM users WHERE id = ?`, userID).
		Scan(&hash, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted.Valid {
		return ErrCredentials
	}
	if err != nil {
		return err
	}
	if hash == "" {
		// An OIDC-only account has no password to change (D5).
		return ErrNoPassword
	}
	if err := auth.VerifyPassword(ctx, current, hash); err != nil {
		return ErrCredentials
	}

	// Hashed before the transaction: Argon2 takes real time, and §7.2 keeps
	// slow work out of a write lock.
	newHash, err := auth.HashPassword(ctx, next)
	if err != nil {
		return err // includes ErrWeak
	}

	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, newHash, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrNoPassword means the account signs in another way and has none to change.
var ErrNoPassword = errors.New("sqlite: this account has no password set")

// Get loads a user by ID.
func (r *UserRepo) Get(ctx context.Context, id string) (*domain.User, error) {
	var u domain.User
	var email, oidc sql.NullString
	var pw sql.NullString
	var created string
	var deleted, anon sql.NullString

	err := r.s.Reader().QueryRowContext(ctx, `
		SELECT id, email, display_name, password_hash, oidc_subject, role,
		       created_at, deleted_at, anonymized_at
		  FROM users WHERE id = ?`, id).
		Scan(&u.ID, &email, &u.DisplayName, &pw, &oidc, &u.Role, &created, &deleted, &anon)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Email, u.OIDCSubject = email.String, oidc.String
	u.HasPassword = pw.Valid && pw.String != ""
	u.CreatedAt, _ = time.Parse(time.RFC3339, created)
	if deleted.Valid {
		t, _ := time.Parse(time.RFC3339, deleted.String)
		u.DeletedAt = &t
	}
	if anon.Valid {
		t, _ := time.Parse(time.RFC3339, anon.String)
		u.AnonymizedAt = &t
	}
	return &u, nil
}

// CreateInvite mints a single-use registration code.
func (r *UserRepo) CreateInvite(ctx context.Context, createdBy string) (string, error) {
	code, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = r.s.Writer().ExecContext(ctx,
		`INSERT INTO invites (code, created_by, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		code, nullStr(createdBy), now.Add(InviteTTL).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return code, nil
}

// StartSession issues an opaque session token.
func (r *UserRepo) StartSession(ctx context.Context, userID string) (string, time.Time, error) {
	token, err := auth.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().UTC().Add(SessionTTL)
	_, err = r.s.Writer().ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		token, userID, expires.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// UserForSession resolves a session token, rejecting expired ones.
func (r *UserRepo) UserForSession(ctx context.Context, token string) (*domain.User, error) {
	if token == "" {
		return nil, ErrSessionStale
	}
	var userID, expires string
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM sessions WHERE id = ?`, token).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionStale
	}
	if err != nil {
		return nil, err
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	if perr != nil || time.Now().UTC().After(exp) {
		_ = r.EndSession(ctx, token)
		return nil, ErrSessionStale
	}
	return r.Get(ctx, userID)
}

// EndSession logs a session out.
func (r *UserRepo) EndSession(ctx context.Context, token string) error {
	_, err := r.s.Writer().ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, token)
	return err
}

// CrateID returns the user's working crate, creating one if missing.
func (r *UserRepo) CrateID(ctx context.Context, userID string) (string, error) {
	var id string
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT id FROM crates WHERE user_id = ? ORDER BY created_at LIMIT 1`, userID).Scan(&id)
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
	if _, err := r.s.Writer().ExecContext(ctx,
		`INSERT INTO crates (id, user_id, created_at) VALUES (?, ?, ?)`,
		id, userID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	return id, nil
}

// Erase implements GDPR Art. 17 (§6.1, F26).
//
// The split is the whole point: data that is only about this user is deleted,
// while attribution on shared history is anonymised in place so playlist
// history other people depend on survives.
func (r *UserRepo) Erase(ctx context.Context, userID string) error {
	tx, err := r.s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Deleted outright — only ever about this user.
	for _, q := range []string{
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM user_provider_credentials WHERE user_id = ?`,
		`DELETE FROM ratings WHERE user_id = ?`,
		`DELETE FROM tags WHERE user_id = ?`,
		`DELETE FROM crates WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}

	// Anonymised in place — the content is not personal data and other people
	// depend on it. ON DELETE SET NULL would do this too, but being explicit
	// keeps the intent legible and independent of pragma state.
	for _, q := range []string{
		`UPDATE playlist_revisions SET actor_id = NULL WHERE actor_id = ?`,
		`UPDATE comments        SET user_id  = NULL WHERE user_id  = ?`,
		`UPDATE record_provenance SET user_id = NULL WHERE user_id = ?`,
		`UPDATE invites SET created_by = NULL WHERE created_by = ?`,
		`UPDATE invites SET used_by    = NULL WHERE used_by    = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}

	// Playlists they own are orphaned rather than destroyed; reassignment is a
	// policy decision for the operator, not something to do silently here.
	if _, err := tx.ExecContext(ctx,
		`UPDATE playlists SET owner_id = NULL WHERE owner_id = ?`, userID); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		UPDATE users
		   SET email = NULL, password_hash = NULL, oidc_subject = NULL,
		       display_name = 'a departed member',
		       deleted_at = ?, anonymized_at = ?
		 WHERE id = ?`, now, now, userID); err != nil {
		return err
	}
	return tx.Commit()
}
