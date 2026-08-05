package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ProviderRefRepo caches how a canonical record maps onto a provider.
//
// This is the single biggest saving in the whole export path. Provider quota is
// scarce under Development Mode, and a song resolved once by anyone on the
// instance never costs a lookup again — which is the shared catalogue's whole
// argument (§3.0) applied to provider ids.
//
// Keyed by storefront, because an id that plays in one country may be absent in
// another (§3.6). A single-country friend group never notices; a mixed one
// would get silently wrong results without it.
type ProviderRefRepo struct{ s *Store }

func (s *Store) ProviderRefs() *ProviderRefRepo { return &ProviderRefRepo{s: s} }

// Resolution outcomes, matching the schema's CHECK constraint.
const (
	RefOK      = "ok"      // resolved, and the external id is usable
	RefAbsent  = "absent"  // the provider does not have it at all
	RefRegion  = "region"  // it exists, but not in this storefront
	RefUnknown = "unknown" // not yet attempted
)

// ProviderRef is one cached mapping.
type ProviderRef struct {
	RecordID   string
	Service    string
	Storefront string
	ExternalID string
	Status     string
	CheckedAt  time.Time
}

// Stale reports whether a negative result is old enough to be worth retrying.
//
// Positive results never go stale: a Spotify URI does not stop being that
// recording. Negative ones do — catalogues gain songs, and a permanent "absent"
// would mean a track that arrives later is never found again.
func (r ProviderRef) Stale(negativeTTL time.Duration) bool {
	if r.Status == RefOK {
		return false
	}
	return time.Since(r.CheckedAt) > negativeTTL
}

// NegativeTTL is how long an unavailable result is trusted before being
// retried. Long enough to save real quota across a session; short enough that a
// newly licensed track appears within a fortnight.
const NegativeTTL = 14 * 24 * time.Hour

// ErrNoRef means nothing has been cached for that record and storefront.
var ErrNoRef = errors.New("sqlite: no provider ref cached")

// Get returns a cached mapping, treating a stale negative as a miss.
func (r *ProviderRefRepo) Get(ctx context.Context, recordID, service, storefront string) (ProviderRef, error) {
	var (
		ref        ProviderRef
		externalID sql.NullString
		checked    string
	)
	err := r.s.Reader().QueryRowContext(ctx, `
		SELECT record_id, service, storefront, external_id, status, checked_at
		  FROM provider_refs
		 WHERE record_id = ? AND service = ? AND storefront = ?`,
		recordID, service, storefront).
		Scan(&ref.RecordID, &ref.Service, &ref.Storefront, &externalID, &ref.Status, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderRef{}, ErrNoRef
	}
	if err != nil {
		return ProviderRef{}, err
	}
	ref.ExternalID = externalID.String
	ref.CheckedAt, _ = time.Parse(time.RFC3339, checked)

	if ref.Stale(NegativeTTL) {
		return ProviderRef{}, ErrNoRef
	}
	return ref, nil
}

// Put records a resolution outcome, positive or negative.
//
// Negative results are cached deliberately: without them, every export of a
// playlist containing an unavailable track pays the full lookup cost again, and
// under a scarce quota that is the difference between an export completing and
// running dry.
func (r *ProviderRefRepo) Put(ctx context.Context, recordID, service, storefront, externalID, status string) error {
	_, err := r.s.Writer().ExecContext(ctx, `
		INSERT INTO provider_refs
		    (record_id, service, storefront, external_id, status, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (record_id, service, storefront) DO UPDATE SET
		    external_id = excluded.external_id,
		    status      = excluded.status,
		    checked_at  = excluded.checked_at`,
		recordID, service, storefront, nullStr(externalID), status, nowRFC3339())
	return err
}

// Count reports how many mappings are cached for a service, for the settings
// screen — it is the most legible measure of how much quota the instance has
// already saved itself.
func (r *ProviderRefRepo) Count(ctx context.Context, service string) (int, error) {
	var n int
	err := r.s.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM provider_refs WHERE service = ? AND status = ?`,
		service, RefOK).Scan(&n)
	return n, err
}
