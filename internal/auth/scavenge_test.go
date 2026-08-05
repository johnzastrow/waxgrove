package auth

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// withFastQuiet shrinks the quiet period so a test does not wait 20 seconds.
// The scavenger goroutine is already running (init() hashes), so this has to be
// an atomic store rather than a plain assignment.
func withFastQuiet(t *testing.T, d time.Duration) {
	t.Helper()
	prev := quietNanos.Swap(int64(d))
	t.Cleanup(func() { quietNanos.Store(prev) })
}

// watchScavenge reports each release on a channel.
func watchScavenge(t *testing.T) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{}, 4)
	fn := func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	onScavenge.Store(&fn)
	t.Cleanup(func() { onScavenge.Store(nil) })
	return ch
}

// The whole point: after hashing stops, the 64 MiB Argon2 leaves behind must
// actually be handed back, not merely become collectable.
func TestScavengeReleasesAfterQuiet(t *testing.T) {
	withFastQuiet(t, 150*time.Millisecond)
	fired := watchScavenge(t)

	var before, after runtime.MemStats
	if _, err := HashPassword(t.Context(), "correct-horse-battery-staple"); err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	runtime.ReadMemStats(&before)

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("no release happened after the quiet period elapsed")
	}

	runtime.ReadMemStats(&after)
	// HeapReleased only grows, so this is a floor, not an exact figure: other
	// tests in this package hash too. Asserting it moved at all is the honest
	// claim — asserting a specific number would be asserting the allocator's
	// behaviour rather than ours.
	if after.HeapReleased <= before.HeapReleased && after.HeapIdle >= before.HeapIdle {
		t.Errorf("nothing was released: HeapReleased %d -> %d, HeapIdle %d -> %d",
			before.HeapReleased, after.HeapReleased, before.HeapIdle, after.HeapIdle)
	}
}

// A burst of logins must not pay for a collection mid-burst — the release is
// pushed back each time, so it lands once, after the burst.
func TestScavengeIsDebounced(t *testing.T) {
	withFastQuiet(t, 250*time.Millisecond)
	fired := watchScavenge(t)

	// Keep hashing for longer than the quiet period. Nothing should fire while
	// the work is still arriving.
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(700 * time.Millisecond)
		for time.Now().Before(deadline) {
			noteHash()
			time.Sleep(40 * time.Millisecond)
		}
	}()

	select {
	case <-fired:
		t.Fatal("released while hashing was still arriving; the debounce is not working")
	case <-time.After(600 * time.Millisecond):
		// Correct: still busy.
	}
	<-done

	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("never released after the burst ended")
	}
}

// noteHash is called from both hashing paths and must be safe to call
// concurrently — every login does.
func TestNoteHashIsConcurrencySafe(t *testing.T) {
	withFastQuiet(t, time.Hour) // do not let a release interleave with this
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); noteHash() }()
	}
	wg.Wait()
}

// The timing placeholder is built in init() and costs a full Argon2 hash, so it
// is one of the two sources of resident memory at boot. Guard that it stays
// well-formed: a malformed one would verify instantly and silently defeat the
// enumeration defence it exists for.
func TestDummyHashIsRealWork(t *testing.T) {
	if dummyHash == "" {
		t.Fatal("dummyHash is empty")
	}
	if err := VerifyPassword(t.Context(), "waxgrove-timing-equalisation-placeholder", dummyHash); err != nil {
		t.Fatalf("the placeholder does not verify against itself: %v", err)
	}
	start := time.Now()
	EqualiseTiming(t.Context(), "something-else-entirely")
	if elapsed := time.Since(start); elapsed < 5*time.Millisecond {
		t.Errorf("EqualiseTiming took %v — too fast to be a real Argon2 verification", elapsed)
	}
}
