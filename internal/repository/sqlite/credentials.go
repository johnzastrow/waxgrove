package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/johnzastrow/waxgrove/internal/crypto"
)

// nowRFC3339 is the timestamp format every table in this schema uses: UTC,
// RFC 3339, sortable as text — which is what makes ORDER BY on a TEXT column
// correct rather than coincidental.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// CredentialRepo stores per-user provider credentials.
//
// Everything secret in here is AES-256-GCM ciphertext with the key from the
// environment (§6). These are the crown jewels: a Client Secret plus a refresh
// token is standing write access to somebody's real music library, so the
// plaintext exists only in memory, for the duration of one call, and never
// appears in a log line or an API response.
type CredentialRepo struct {
	s      *Store
	sealer *crypto.Sealer
}

// Credentials returns the repository. It needs the sealer, so unlike the other
// repositories it is not reachable from the Store alone — there is no way to
// accidentally read these without the means to decrypt them.
func (s *Store) Credentials(sealer *crypto.Sealer) *CredentialRepo {
	return &CredentialRepo{s: s, sealer: sealer}
}

// Services Waxgrove can connect to.
const (
	ServiceSpotify = "spotify"
	ServiceApple   = "apple"
)

var (
	// ErrNoCredentials means this user has not connected this service.
	ErrNoCredentials = errors.New("sqlite: no credentials for that user and service")
	// ErrNoApp means credentials exist but the user's own app is not set up,
	// so there is nothing to authorise against (D6).
	ErrNoApp = errors.New("sqlite: no client id and secret stored for that user")
)

// Credential is one user's connection to one service, decrypted.
type Credential struct {
	UserID     string
	Service    string
	Storefront string

	ClientID     string
	ClientSecret string

	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Connected reports whether there is a usable authorisation, as opposed to just
// a registered app awaiting one.
func (c *Credential) Connected() bool { return c.RefreshToken != "" }

// SaveApp stores the user's own Client ID and Secret (D6).
//
// It deliberately does not touch the tokens: re-entering a Client Secret to fix
// a typo should not silently disconnect a working authorisation. Changing the
// Client ID does invalidate the tokens, and that is handled explicitly.
func (r *CredentialRepo) SaveApp(ctx context.Context, userID, service, clientID, clientSecret string) error {
	secretEnc, err := r.sealer.Seal([]byte(clientSecret))
	if err != nil {
		return err
	}
	now := nowRFC3339()

	// A different Client ID means the existing tokens were issued by a
	// different app and cannot be refreshed — clearing them is the honest
	// outcome, and the UI then asks the user to reconnect.
	_, err = r.s.Writer().ExecContext(ctx, `
		INSERT INTO user_provider_credentials
		    (user_id, service, client_id, client_secret_enc, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, service) DO UPDATE SET
		    client_secret_enc = excluded.client_secret_enc,
		    access_token_enc  = CASE WHEN user_provider_credentials.client_id IS ?
		                             THEN user_provider_credentials.access_token_enc END,
		    refresh_token_enc = CASE WHEN user_provider_credentials.client_id IS ?
		                             THEN user_provider_credentials.refresh_token_enc END,
		    expires_at        = CASE WHEN user_provider_credentials.client_id IS ?
		                             THEN user_provider_credentials.expires_at END,
		    client_id         = excluded.client_id,
		    updated_at        = excluded.updated_at`,
		userID, service, clientID, secretEnc, now, now,
		clientID, clientID, clientID)
	return err
}

// SaveTokens records an authorisation.
func (r *CredentialRepo) SaveTokens(ctx context.Context, userID, service string,
	accessToken, refreshToken string, expiresAt time.Time, scopes, storefront string) error {

	accessEnc, err := r.sealer.Seal([]byte(accessToken))
	if err != nil {
		return err
	}
	refreshEnc, err := r.sealer.Seal([]byte(refreshToken))
	if err != nil {
		return err
	}

	res, err := r.s.Writer().ExecContext(ctx, `
		UPDATE user_provider_credentials
		   SET access_token_enc = ?, refresh_token_enc = ?, expires_at = ?,
		       scopes = ?, storefront = COALESCE(NULLIF(?, ''), storefront),
		       updated_at = ?
		 WHERE user_id = ? AND service = ?`,
		accessEnc, refreshEnc, expiresAt.UTC().Format(time.RFC3339), scopes,
		storefront, nowRFC3339(), userID, service)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Tokens with no app row would be unrefreshable the moment they expire.
		return ErrNoApp
	}
	return nil
}

// Get returns one user's credentials, decrypted.
func (r *CredentialRepo) Get(ctx context.Context, userID, service string) (*Credential, error) {
	var (
		c                  Credential
		storefront, scopes sql.NullString
		secretEnc          []byte
		accessEnc          []byte
		refreshEnc         []byte
		clientID           sql.NullString
		expires            sql.NullString
		created, updated   string
	)
	err := r.s.Reader().QueryRowContext(ctx, `
		SELECT user_id, service, storefront, client_id, client_secret_enc,
		       access_token_enc, refresh_token_enc, expires_at, scopes,
		       created_at, updated_at
		  FROM user_provider_credentials
		 WHERE user_id = ? AND service = ?`, userID, service).
		Scan(&c.UserID, &c.Service, &storefront, &clientID, &secretEnc,
			&accessEnc, &refreshEnc, &expires, &scopes, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoCredentials
	}
	if err != nil {
		return nil, err
	}

	c.Storefront, c.ClientID, c.Scopes = storefront.String, clientID.String, scopes.String
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if expires.Valid {
		c.ExpiresAt, _ = time.Parse(time.RFC3339, expires.String)
	}

	if c.ClientSecret, err = r.open(secretEnc); err != nil {
		return nil, err
	}
	if c.AccessToken, err = r.open(accessEnc); err != nil {
		return nil, err
	}
	if c.RefreshToken, err = r.open(refreshEnc); err != nil {
		return nil, err
	}
	return &c, nil
}

// open decrypts one field. A decryption failure is returned rather than
// swallowed: it means the key changed, and silently presenting an empty
// credential would send the user round a reconnect loop with no explanation.
func (r *CredentialRepo) open(enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", nil
	}
	plain, err := r.sealer.Open(enc)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Delete removes a connection entirely.
//
// Disconnecting must leave nothing behind: the tokens are the sensitive part,
// and a user who disconnects has withdrawn consent for us to hold them.
func (r *CredentialRepo) Delete(ctx context.Context, userID, service string) error {
	_, err := r.s.Writer().ExecContext(ctx,
		`DELETE FROM user_provider_credentials WHERE user_id = ? AND service = ?`,
		userID, service)
	return err
}

// ListServices reports which services a user has any row for, so the UI can
// show connection state without ever decrypting anything.
func (r *CredentialRepo) ListServices(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := r.s.Reader().QueryContext(ctx, `
		SELECT service, refresh_token_enc IS NOT NULL
		  FROM user_provider_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]bool)
	for rows.Next() {
		var service string
		var connected bool
		if err := rows.Scan(&service, &connected); err != nil {
			return nil, err
		}
		out[service] = connected
	}
	return out, rows.Err()
}
