package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The bound is a memory ceiling with a security consequence: /api/login is
// unauthenticated and each hash costs 64 MiB, so an unbounded number in flight
// is an OOM anyone can trigger. Measured on a 256 MiB container before this
// existed: 8 and 16 concurrent logins were killed outright.
func TestHashingConcurrencyIsBounded(t *testing.T) {
	var inFlight, peak atomic.Int64

	// Watch the slot channel's occupancy while a crowd of hashes runs.
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := acquireHashSlot(context.Background()); err != nil {
				return
			}
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			// Long enough that the goroutines genuinely overlap; without a
			// bound, all 24 would be counted at once.
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
			releaseHashSlot()
		}()
	}
	wg.Wait()

	if got := peak.Load(); got > MaxConcurrentHashes {
		t.Errorf("peak concurrent hashes = %d, want at most %d", got, MaxConcurrentHashes)
	}
	if got := peak.Load(); got < 2 && MaxConcurrentHashes >= 2 {
		t.Errorf("peak = %d: the slots are not actually being used in parallel", got)
	}
	if n := inFlight.Load(); n != 0 {
		t.Errorf("%d slots still held after everything finished — a leak", n)
	}
}

// A caller that has given up must stop queueing rather than hold a slot the
// requests behind it need.
func TestHashingRespectsCancellation(t *testing.T) {
	// Fill every slot.
	for range MaxConcurrentHashes {
		if err := acquireHashSlot(context.Background()); err != nil {
			t.Fatalf("acquire: %v", err)
		}
	}
	t.Cleanup(func() {
		for range MaxConcurrentHashes {
			releaseHashSlot()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := HashPassword(ctx, "correct-horse-battery-staple"); err == nil {
		t.Fatal("HashPassword succeeded with every slot held; it did not wait at all")
	} else if !errorsIsDeadline(err) {
		t.Fatalf("want a context error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waited %v before giving up; cancellation is not being honoured", elapsed)
	}
}

// A weak password must be rejected before it costs a slot — otherwise the
// cheapest possible garbage input still consumes the scarce resource.
func TestWeakPasswordCostsNoSlot(t *testing.T) {
	for range MaxConcurrentHashes {
		if err := acquireHashSlot(context.Background()); err != nil {
			t.Fatalf("acquire: %v", err)
		}
	}
	t.Cleanup(func() {
		for range MaxConcurrentHashes {
			releaseHashSlot()
		}
	})

	done := make(chan error, 1)
	go func() {
		_, err := HashPassword(context.Background(), "short")
		done <- err
	}()

	select {
	case err := <-done:
		if err != ErrWeak {
			t.Errorf("got %v, want ErrWeak", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a too-short password blocked on a hashing slot instead of being rejected outright")
	}
}

func errorsIsDeadline(err error) bool {
	return err == context.DeadlineExceeded || err == context.Canceled
}
