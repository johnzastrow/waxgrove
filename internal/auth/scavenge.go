package auth

import (
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Argon2id is memory-hard on purpose: every password hash allocates
// argonMemory (64 MiB) and immediately drops it. That is the point of the
// algorithm and is not negotiable.
//
// The problem is what happens next. Go's collector runs when the heap grows
// toward its goal, and a Waxgrove instance between logins allocates almost
// nothing — so that 64 MiB stays uncollected, indefinitely. Measured on the
// container: 67 MiB resident at boot (the init() placeholder hash), 132 MiB
// after a single registration, and flat there for as long as you watch it.
// None of it is live; all of it counts against a small host.
//
// GOMEMLIMIT does not solve this. It is a ceiling, so at 132 MiB against a 180
// MiB limit the runtime is correct to do nothing, and setting it low enough to
// force collection would make it thrash during the login burst it exists to
// survive.
//
// So: collect once the hashing stops. A burst of logins pays nothing — the
// timer is simply pushed back — and the memory is handed to the OS shortly
// after things go quiet. debug.FreeOSMemory is a stop-the-world GC plus a
// scavenge, which is exactly the wrong thing to do under load and exactly the
// right thing to do when there is none.

// defaultQuietPeriod is how long hashing must be idle before releasing. Long
// enough that a burst of logins never pays for a collection; short enough that
// an instance nobody is using is not sitting on 128 MiB of garbage.
const defaultQuietPeriod = 20 * time.Second

// The scavenger goroutine outlives every caller, so anything it reads has to be
// safe to write from elsewhere. These are atomic rather than plain globals for
// exactly that reason — init() starts the goroutine, so a test that assigns to
// a plain var races with it.
var (
	scavengeOnce sync.Once
	hashed       = make(chan struct{}, 1)

	quietNanos atomic.Int64           // overridden in tests
	onScavenge atomic.Pointer[func()] // fired after each release; nil in production
)

// quietPeriod treats an unset value as the default rather than initialising it
// in an init(). auth.go's init() hashes, which starts the goroutine below, and
// package init functions run in file order — so an init() here would be too
// late and the goroutine would briefly see a zero delay.
func quietPeriod() time.Duration {
	if n := quietNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return defaultQuietPeriod
}

// noteHash records that a hash just happened and (re)arms the release.
func noteHash() {
	scavengeOnce.Do(func() { go scavenge() })
	// Non-blocking: the timer only needs to know that activity occurred, not
	// how much. Dropping a signal when one is already pending is correct.
	select {
	case hashed <- struct{}{}:
	default:
	}
}

func scavenge() {
	t := time.NewTimer(quietPeriod())
	defer t.Stop()

	for {
		select {
		case <-hashed:
			// More hashing: push the release out rather than paying for it now.
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			t.Reset(quietPeriod())

		case <-t.C:
			debug.FreeOSMemory()
			if fn := onScavenge.Load(); fn != nil {
				(*fn)()
			}
			// Nothing to release until something hashes again, so wait for it
			// rather than spinning on a timer forever.
			<-hashed
			t.Reset(quietPeriod())
		}
	}
}
