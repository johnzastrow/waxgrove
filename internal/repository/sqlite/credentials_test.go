package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnzastrow/waxgrove/internal/crypto"
)

func credStore(t *testing.T) (*Store, *CredentialRepo, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cred.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	sealer, err := crypto.NewSealer(key)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}

	u, err := store.Users().Register(context.Background(),
		"owner@example.test", "Owner", "correct-horse-battery-staple", "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return store, store.Credentials(sealer), u.ID, path
}

func TestSaveAndGetApp(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	ctx := context.Background()

	if err := creds.SaveApp(ctx, uid, ServiceSpotify, "my-client-id", "my-secret"); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	got, err := creds.Get(ctx, uid, ServiceSpotify)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ClientID != "my-client-id" || got.ClientSecret != "my-secret" {
		t.Errorf("got %q / %q", got.ClientID, got.ClientSecret)
	}
	if got.Connected() {
		t.Error("an app with no authorisation should not report as connected")
	}
}

// The whole point of this table. If the plaintext is on disk, everything else
// here is decoration.
func TestSecretsAreNotOnDiskInPlaintext(t *testing.T) {
	store, creds, uid, path := credStore(t)
	ctx := context.Background()

	const secret = "SUPER-SECRET-CLIENT-SECRET-9f2a"
	const refresh = "SUPER-SECRET-REFRESH-TOKEN-4b71"
	const access = "SUPER-SECRET-ACCESS-TOKEN-cc03"

	if err := creds.SaveApp(ctx, uid, ServiceSpotify, "cid", secret); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if err := creds.SaveTokens(ctx, uid, ServiceSpotify, access, refresh,
		time.Now().Add(time.Hour), "playlist-read-private", "GB"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	// Force everything out of the WAL and into the file we are about to read.
	if _, err := store.Writer().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(path + suffix)
		if err != nil {
			continue // -wal and -shm may not exist after a checkpoint
		}
		for _, plain := range []string{secret, refresh, access} {
			if bytes.Contains(raw, []byte(plain)) {
				t.Fatalf("%q appears in plaintext in %s", plain, path+suffix)
			}
		}
	}
}

func TestSaveTokensRoundTrips(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	ctx := context.Background()
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	if err := creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "sec"); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if err := creds.SaveTokens(ctx, uid, ServiceSpotify, "AT", "RT", expiry, "s1 s2", "US"); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	got, err := creds.Get(ctx, uid, ServiceSpotify)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "AT" || got.RefreshToken != "RT" {
		t.Errorf("tokens = %q / %q", got.AccessToken, got.RefreshToken)
	}
	if !got.ExpiresAt.Equal(expiry) {
		t.Errorf("expiry = %v, want %v", got.ExpiresAt, expiry)
	}
	if got.Scopes != "s1 s2" || got.Storefront != "US" {
		t.Errorf("scopes %q storefront %q", got.Scopes, got.Storefront)
	}
	if !got.Connected() {
		t.Error("an authorised credential should report as connected")
	}
}

// Tokens without an app row could never be refreshed, so storing them would
// create a connection that silently dies in an hour.
func TestSaveTokensRequiresAnApp(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	err := creds.SaveTokens(context.Background(), uid, ServiceSpotify,
		"AT", "RT", time.Now().Add(time.Hour), "", "")
	if !errors.Is(err, ErrNoApp) {
		t.Fatalf("got %v, want ErrNoApp", err)
	}
}

// Fixing a typo in the Client Secret must not log the user out of a working
// connection.
func TestReSavingTheSameAppKeepsTheAuthorisation(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	ctx := context.Background()

	_ = creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "old-secret")
	_ = creds.SaveTokens(ctx, uid, ServiceSpotify, "AT", "RT", time.Now().Add(time.Hour), "sc", "GB")

	if err := creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "corrected-secret"); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	got, err := creds.Get(ctx, uid, ServiceSpotify)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ClientSecret != "corrected-secret" {
		t.Errorf("secret = %q, want the corrected one", got.ClientSecret)
	}
	if got.RefreshToken != "RT" {
		t.Errorf("refresh token = %q, want the authorisation kept", got.RefreshToken)
	}
}

// A different Client ID means the stored tokens were issued by a different app
// and can never be refreshed. Keeping them would present a connection that
// fails at the next expiry with no explanation.
func TestChangingTheClientIDClearsTheAuthorisation(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	ctx := context.Background()

	_ = creds.SaveApp(ctx, uid, ServiceSpotify, "old-app", "sec")
	_ = creds.SaveTokens(ctx, uid, ServiceSpotify, "AT", "RT", time.Now().Add(time.Hour), "sc", "GB")

	if err := creds.SaveApp(ctx, uid, ServiceSpotify, "new-app", "sec"); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	got, err := creds.Get(ctx, uid, ServiceSpotify)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshToken != "" || got.AccessToken != "" {
		t.Errorf("tokens survived an app change: %q / %q", got.AccessToken, got.RefreshToken)
	}
	if got.Connected() {
		t.Error("still reporting connected after the app changed")
	}
}

func TestGetMissingIsDistinguishable(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	_, err := creds.Get(context.Background(), uid, ServiceSpotify)
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("got %v, want ErrNoCredentials", err)
	}
}

// Disconnecting must leave nothing behind — the user has withdrawn consent for
// us to hold their tokens.
func TestDeleteRemovesEverything(t *testing.T) {
	store, creds, uid, _ := credStore(t)
	ctx := context.Background()

	_ = creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "sec")
	_ = creds.SaveTokens(ctx, uid, ServiceSpotify, "AT", "RT", time.Now().Add(time.Hour), "sc", "GB")

	if err := creds.Delete(ctx, uid, ServiceSpotify); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := creds.Get(ctx, uid, ServiceSpotify); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("Get after Delete = %v", err)
	}
	var n int
	if err := store.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_provider_credentials WHERE user_id = ?`, uid).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d credential rows survived the delete", n)
	}
}

// Erasing a user must take their provider tokens with them (GDPR Art. 17,
// BR-4). A refresh token outliving its owner is the worst kind of leftover.
func TestErasingAUserRemovesTheirCredentials(t *testing.T) {
	store, creds, uid, _ := credStore(t)
	ctx := context.Background()

	_ = creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "sec")
	_ = creds.SaveTokens(ctx, uid, ServiceSpotify, "AT", "RT", time.Now().Add(time.Hour), "sc", "GB")

	if err := store.Users().Erase(ctx, uid); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	var n int
	if err := store.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_provider_credentials WHERE user_id = ?`, uid).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d credential rows survived the erasure", n)
	}
}

func TestListServicesReportsConnectionState(t *testing.T) {
	_, creds, uid, _ := credStore(t)
	ctx := context.Background()

	_ = creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "sec")
	got, err := creds.ListServices(ctx, uid)
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if connected, ok := got[ServiceSpotify]; !ok || connected {
		t.Errorf("got %v, want spotify present but not connected", got)
	}

	_ = creds.SaveTokens(ctx, uid, ServiceSpotify, "AT", "RT", time.Now().Add(time.Hour), "sc", "GB")
	got, _ = creds.ListServices(ctx, uid)
	if !got[ServiceSpotify] {
		t.Errorf("got %v, want spotify connected", got)
	}
}

// A key change must be loud. Returning an empty credential would send the user
// round a reconnect loop that cannot succeed.
func TestWrongKeyIsAnErrorNotAnEmptyCredential(t *testing.T) {
	store, creds, uid, _ := credStore(t)
	ctx := context.Background()
	_ = creds.SaveApp(ctx, uid, ServiceSpotify, "cid", "sec")

	otherKey, _ := crypto.GenerateKey()
	otherSealer, _ := crypto.NewSealer(otherKey)
	wrong := store.Credentials(otherSealer)

	if got, err := wrong.Get(ctx, uid, ServiceSpotify); err == nil {
		t.Fatalf("decrypted with the wrong key and returned %+v", got)
	}
}
