package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnzastrow/waxgrove/internal/domain"
)

func seedRecords(t *testing.T, store *Store, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		_, err := store.Records().Upsert(ctx, domain.Candidate{
			Title:  fmt.Sprintf("Song %d", i),
			Artist: fmt.Sprintf("Artist %d", i%10),
			ISRC:   fmt.Sprintf("ZZ%011d", i),
		}, domain.TierCurated)
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

// Concurrent searches must not deadlock the connection pool.
//
// The failure this guards against is not a slowdown: a query that holds an open
// cursor and then issues another query needs two connections at once, so with
// enough concurrent callers every connection in the pool ends up held by a
// cursor waiting for a connection. Nothing completes, ever — the process serves
// static files fine while every database request hangs and the health check
// goes red. It was found by load-testing, not by the unit tests, because it
// only appears above the pool size.
func TestConcurrentSearchDoesNotDeadlock(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedRecords(t, store, 60)

	// Comfortably more than the read pool, which is where the old code hung.
	const callers = 32
	done := make(chan error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			recs, err := store.Records().Search(ctx, fmt.Sprintf("Artist %d", i%10), 50)
			if err != nil {
				done <- fmt.Errorf("search %d: %w", i, err)
				return
			}
			if len(recs) == 0 {
				done <- fmt.Errorf("search %d returned nothing", i)
				return
			}
			done <- nil
		}(i)
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent searches deadlocked the connection pool")
	}
	close(done)
	for err := range done {
		if err != nil {
			t.Error(err)
		}
	}
}

// Same shape, through the resolution ladder's fuzzy step.
func TestConcurrentFuzzyMatchDoesNotDeadlock(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedRecords(t, store, 40)

	const callers = 32
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := store.Records().FuzzyMatch(ctx, domain.Candidate{
				Title: fmt.Sprintf("Song %d", i), Artist: fmt.Sprintf("Artist %d", i%10),
			}); err != nil {
				errs <- err
			}
		}(i)
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent fuzzy matches deadlocked the connection pool")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Health must stay answerable while searches are in flight — it is what an
// orchestrator uses to decide the container is alive.
func TestPingSucceedsUnderSearchLoad(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedRecords(t, store, 40)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = store.Records().Search(ctx, fmt.Sprintf("Artist %d", i%10), 50)
				cancel()
			}
		}(i)
	}
	t.Cleanup(func() { close(stop); wg.Wait() })

	// Give the load a moment to saturate the pool, then check health.
	time.Sleep(300 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping failed while searches were in flight: %v", err)
	}
}

// Search results must come back in the ranking order the query produced —
// getMany reorders by id internally, so this guards the reassembly.
func TestSearchPreservesOrderAndISRCs(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	const mbid = "1b7c9f5e-0000-4000-8000-00000000dede"

	// The many-to-one case (BR-1): one recording, two ISRCs, arriving
	// separately as they would from two different services. The MBID is what
	// converges them — without one, a new ISRC is a new record, correctly,
	// because nothing has said otherwise.
	if _, err := store.Records().Upsert(ctx, domain.Candidate{
		MBID: mbid, Title: "Dreams", Artist: "Fleetwood Mac", ISRC: "USWB10101368",
	}, domain.TierCurated); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := store.Records().Upsert(ctx, domain.Candidate{
		MBID: mbid, Title: "Dreams", Artist: "Fleetwood Mac", ISRC: "USEE10001573",
	}, domain.TierCurated); err != nil {
		t.Fatalf("second isrc: %v", err)
	}

	got, err := store.Records().Search(ctx, "Dreams", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1 (ISRCs are a set on one record)", len(got))
	}
	if len(got[0].ISRCs) != 2 {
		t.Errorf("got %d ISRCs, want 2: %v", len(got[0].ISRCs), got[0].ISRCs)
	}
	if got[0].Title != "Dreams" {
		t.Errorf("title = %q", got[0].Title)
	}
}
