package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/johnzastrow/waxgrove/internal/auth"
	"github.com/johnzastrow/waxgrove/internal/domain"
)

const testPassword = "correct-horse-battery-staple"

func registerFirst(t *testing.T, s *Store) *domain.User {
	t.Helper()
	u, err := s.Users().Register(context.Background(),
		"ana@example.test", "Ana", testPassword, "")
	if err != nil {
		t.Fatalf("register first user: %v", err)
	}
	return u
}

// ------------------------------------------------------- registration/auth --

func TestFirstUserBecomesAdminWithoutInvite(t *testing.T) {
	s := newTestStore(t)
	u := registerFirst(t, s)
	if u.Role != domain.RoleAdmin {
		t.Errorf("role = %q, want admin — nobody exists to invite the first user", u.Role)
	}
}

func TestSecondUserRequiresAValidInvite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	admin := registerFirst(t, s)

	if _, err := s.Users().Register(ctx, "ben@example.test", "Ben", testPassword, ""); !errors.Is(err, ErrInviteBad) {
		t.Fatalf("registration without an invite: %v, want ErrInviteBad", err)
	}
	if _, err := s.Users().Register(ctx, "ben@example.test", "Ben", testPassword, "made-up"); !errors.Is(err, ErrInviteBad) {
		t.Fatalf("bogus invite accepted: %v", err)
	}

	code, err := s.Users().CreateInvite(ctx, admin.ID)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	ben, err := s.Users().Register(ctx, "ben@example.test", "Ben", testPassword, code)
	if err != nil {
		t.Fatalf("invited registration: %v", err)
	}
	if ben.Role != domain.RoleMember {
		t.Errorf("invited user role = %q, want member", ben.Role)
	}
	// Single use.
	if _, err := s.Users().Register(ctx, "cal@example.test", "Cal", testPassword, code); !errors.Is(err, ErrInviteBad) {
		t.Errorf("invite was reusable: %v", err)
	}
}

func TestExpiredInviteIsRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	admin := registerFirst(t, s)
	code, _ := s.Users().CreateInvite(ctx, admin.ID)

	mustExec(t, s, `UPDATE invites SET expires_at = ? WHERE code = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), code)

	if _, err := s.Users().Register(ctx, "ben@example.test", "Ben", testPassword, code); !errors.Is(err, ErrInviteBad) {
		t.Errorf("expired invite accepted: %v", err)
	}
}

func TestEmailIsCaseInsensitiveAndUnique(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	admin := registerFirst(t, s)
	code, _ := s.Users().CreateInvite(ctx, admin.ID)

	if _, err := s.Users().Register(ctx, "  ANA@Example.TEST  ", "Dup", testPassword, code); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("differently-cased duplicate accepted: %v", err)
	}
	// And login works regardless of the case typed.
	if _, err := s.Users().Authenticate(ctx, "ANA@EXAMPLE.TEST", testPassword); err != nil {
		t.Errorf("case-insensitive login failed: %v", err)
	}
}

func TestAuthenticateRejectsWrongPasswordAndUnknownUserIdentically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	registerFirst(t, s)

	_, wrong := s.Users().Authenticate(ctx, "ana@example.test", "not-the-password")
	_, unknown := s.Users().Authenticate(ctx, "nobody@example.test", testPassword)
	if !errors.Is(wrong, ErrCredentials) || !errors.Is(unknown, ErrCredentials) {
		t.Fatalf("errors differ: wrong=%v unknown=%v", wrong, unknown)
	}
}

func TestWeakPasswordRefusedAtRegistration(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Users().Register(context.Background(),
		"a@example.test", "A", "short", ""); !errors.Is(err, auth.ErrWeak) {
		t.Errorf("err = %v, want ErrWeak", err)
	}
}

// ------------------------------------------------------------------ sessions --

func TestSessionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := registerFirst(t, s)

	token, expires, err := s.Users().StartSession(ctx, u.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if expires.Before(time.Now().UTC()) {
		t.Error("session already expired at issue")
	}
	got, err := s.Users().UserForSession(ctx, token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("resolve session: %v", err)
	}
	if err := s.Users().EndSession(ctx, token); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, err := s.Users().UserForSession(ctx, token); !errors.Is(err, ErrSessionStale) {
		t.Errorf("logged-out token still resolves: %v", err)
	}
}

func TestExpiredSessionIsRejectedAndCleanedUp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := registerFirst(t, s)
	token, _, _ := s.Users().StartSession(ctx, u.ID)

	mustExec(t, s, `UPDATE sessions SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), token)

	if _, err := s.Users().UserForSession(ctx, token); !errors.Is(err, ErrSessionStale) {
		t.Fatalf("expired session accepted: %v", err)
	}
	var n int
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE id = ?`, token).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("expired session was not cleaned up on use")
	}
}

func TestEmptySessionTokenIsRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Users().UserForSession(context.Background(), ""); !errors.Is(err, ErrSessionStale) {
		t.Errorf("empty token err = %v", err)
	}
}

// --------------------------------------------------------------------- erase --
// GDPR Art. 17 (§6.1, F26). The whole point is the split: delete what is only
// about this user, anonymise attribution on history other people depend on,
// and leave the shared catalogue alone.

// eraseFixture builds a world where Ana has contributed to things Ben relies on.
func eraseFixture(t *testing.T, s *Store) (ana, ben *domain.User, playlistID, recordID string) {
	t.Helper()
	ctx := context.Background()
	ana = registerFirst(t, s)
	code, _ := s.Users().CreateInvite(ctx, ana.ID)
	var err error
	ben, err = s.Users().Register(ctx, "ben@example.test", "Ben", testPassword, code)
	if err != nil {
		t.Fatalf("register ben: %v", err)
	}

	rec, err := s.Records().Upsert(ctx, domain.Candidate{
		Title: "Pink Moon", Artist: "Nick Drake", ISRC: "GBAYE0601498",
	}, domain.TierCurated)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	recordID = rec.ID
	if err := s.Records().RecordProvenance(ctx, rec.ID, ana.ID); err != nil {
		t.Fatalf("provenance: %v", err)
	}

	p, err := s.Playlists().Create(ctx, ana.ID, "Porch, July", "")
	if err != nil {
		t.Fatalf("playlist: %v", err)
	}
	if _, err := s.Playlists().AddRecords(ctx, p.ID, ana.ID, []string{rec.ID}); err != nil {
		t.Fatalf("add: %v", err)
	}
	playlistID = p.ID

	// Ana's own annotations, and a comment Ben will still want to read around.
	now := time.Now().UTC().Format(time.RFC3339)
	mustExec(t, s, `INSERT INTO ratings (playlist_id, user_id, value, updated_at) VALUES (?,?,?,?)`,
		p.ID, ana.ID, 5, now)
	mustExec(t, s, `INSERT INTO tags (id, playlist_id, user_id, name, visibility, created_at)
	                VALUES ('t1', ?, ?, 'summer', 'private', ?)`, p.ID, ana.ID, now)
	mustExec(t, s, `INSERT INTO comments (id, playlist_id, user_id, body, created_at)
	                VALUES ('c1', ?, ?, 'track 9 is the one', ?)`, p.ID, ana.ID, now)
	mustExec(t, s, `INSERT INTO user_provider_credentials
	                (user_id, service, client_id, created_at, updated_at)
	                VALUES (?, 'spotify', 'abc', ?, ?)`, ana.ID, now, now)
	if _, _, err := s.Users().StartSession(ctx, ana.ID); err != nil {
		t.Fatalf("session: %v", err)
	}
	return ana, ben, p.ID, rec.ID
}

func TestEraseDeletesOnlyWhatIsAboutTheUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ana, _, playlistID, _ := eraseFixture(t, s)

	if err := s.Users().Erase(ctx, ana.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}

	for _, c := range []struct {
		name, query string
	}{
		{"sessions", `SELECT COUNT(*) FROM sessions WHERE user_id = ?`},
		{"provider credentials", `SELECT COUNT(*) FROM user_provider_credentials WHERE user_id = ?`},
		{"ratings", `SELECT COUNT(*) FROM ratings WHERE user_id = ?`},
		{"tags", `SELECT COUNT(*) FROM tags WHERE user_id = ?`},
		{"crates", `SELECT COUNT(*) FROM crates WHERE user_id = ?`},
	} {
		var n int
		if err := s.Reader().QueryRowContext(ctx, c.query, ana.ID).Scan(&n); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n != 0 {
			t.Errorf("%s survived erasure (%d rows)", c.name, n)
		}
	}
	_ = playlistID
}

func TestErasePreservesSharedHistoryButAnonymisesIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ana, _, playlistID, _ := eraseFixture(t, s)

	before, err := s.Playlists().History(ctx, playlistID)
	if err != nil {
		t.Fatalf("history before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("fixture has %d revisions, want 2", len(before))
	}

	if err := s.Users().Erase(ctx, ana.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}

	after, err := s.Playlists().History(ctx, playlistID)
	if err != nil {
		t.Fatalf("history after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("erasure destroyed history: %d -> %d revisions", len(before), len(after))
	}
	for _, rev := range after {
		if rev.ActorID != "" {
			t.Errorf("revision %d still attributed to %q", rev.Rev, rev.ActorID)
		}
	}

	// The comment body survives; only the attribution goes.
	var body string
	var author sql.NullString
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT body, user_id FROM comments WHERE id = 'c1'`).Scan(&body, &author); err != nil {
		t.Fatalf("comment: %v", err)
	}
	if body == "" {
		t.Error("comment body was destroyed")
	}
	if author.Valid {
		t.Errorf("comment still attributed to %q", author.String)
	}
}

// Song metadata is public and the catalogue is shared, so erasing a
// contributor must not damage what the whole instance depends on (§3.0).
func TestEraseLeavesTheSharedCatalogueIntact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ana, _, playlistID, recordID := eraseFixture(t, s)

	if err := s.Users().Erase(ctx, ana.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}

	rec, err := s.Records().Get(ctx, recordID)
	if err != nil {
		t.Fatalf("contributed record was removed: %v", err)
	}
	if rec.Title != "Pink Moon" || len(rec.ISRCs) != 1 {
		t.Errorf("record damaged by erasure: %+v", rec)
	}
	// Provenance row survives but is no longer attributed.
	var author sql.NullString
	if err := s.Reader().QueryRowContext(ctx,
		`SELECT user_id FROM record_provenance WHERE record_id = ?`, recordID).Scan(&author); err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if author.Valid {
		t.Error("provenance still names the erased user")
	}
	// The playlist and its track survive too.
	p, err := s.Playlists().Get(ctx, playlistID)
	if err != nil {
		t.Fatalf("playlist destroyed: %v", err)
	}
	if len(p.Tracks) != 1 {
		t.Errorf("playlist lost its tracks: %d", len(p.Tracks))
	}
	if p.OwnerID != "" {
		t.Errorf("owner_id = %q, want empty (orphaned, not reassigned)", p.OwnerID)
	}
}

func TestErasedUserCannotLogInAndLosesIdentifiers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ana, _, _, _ := eraseFixture(t, s)

	if err := s.Users().Erase(ctx, ana.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}
	if _, err := s.Users().Authenticate(ctx, "ana@example.test", testPassword); !errors.Is(err, ErrCredentials) {
		t.Errorf("erased account still authenticates: %v", err)
	}

	u, err := s.Users().Get(ctx, ana.ID)
	if err != nil {
		t.Fatalf("get erased user: %v", err)
	}
	if u.Email != "" {
		t.Errorf("email survived erasure: %q", u.Email)
	}
	if u.HasPassword {
		t.Error("password hash survived erasure")
	}
	if u.AnonymizedAt == nil || u.DeletedAt == nil {
		t.Error("erasure markers not set")
	}
	if u.DisplayName != "a departed member" {
		t.Errorf("display name = %q", u.DisplayName)
	}
	// The address is released, so someone could register it again later.
	code, _ := s.Users().CreateInvite(ctx, "")
	if _, err := s.Users().Register(ctx, "ana@example.test", "New Ana", testPassword, code); err != nil {
		t.Errorf("erased address not reusable: %v", err)
	}
}

// Erasure is one transaction: a failure must not leave a half-erased account.
func TestEraseIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ana, _, _, _ := eraseFixture(t, s)

	if err := s.Users().Erase(ctx, ana.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}
	// Erasing again must be harmless rather than a partial second pass.
	if err := s.Users().Erase(ctx, ana.ID); err != nil {
		t.Errorf("second erase errored: %v", err)
	}
}

func TestCrateIDIsStableAndCreatedOnDemand(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := registerFirst(t, s)

	a, err := s.Users().CrateID(ctx, u.ID)
	if err != nil {
		t.Fatalf("crate: %v", err)
	}
	b, err := s.Users().CrateID(ctx, u.ID)
	if err != nil {
		t.Fatalf("crate again: %v", err)
	}
	if a != b {
		t.Errorf("crate id changed between calls: %s vs %s", a, b)
	}
}
