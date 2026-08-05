// Package memlimit teaches the Go runtime what the container told the kernel.
//
// Go's collector sizes the heap against GOGC, which knows nothing about a
// cgroup limit. Left alone in a memory-limited container it will happily grow
// the heap until the kernel kills the process — measured here at 253 MiB
// against a 256 MiB limit, flat, sitting one allocation from an OOM kill.
//
// GOMEMLIMIT fixes that, but only if it is set, and asking an operator to set
// it in sync with the container limit is asking for the two to drift. So the
// binary reads the limit the container is already running under and derives it.
// One knob, set in one place, and it is the one that actually enforces.
//
// This is deliberately not a dependency: it is a file read and a multiply.
package memlimit

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Headroom is the fraction of the cgroup limit the heap is allowed to reach.
//
// The gap is not slack, it is the part of the container's memory that is not
// Go heap: stacks, the runtime's own bookkeeping, and page cache for the SQLite
// file. Setting GOMEMLIMIT to the full cgroup limit would tell the collector it
// may use memory that is already spoken for.
const Headroom = 0.75

// cgroup v2 first; v1 for older hosts. "max" means unlimited in v2, and the v1
// sentinel is an enormous number rather than a word.
var paths = []string{
	"/sys/fs/cgroup/memory.max",
	"/sys/fs/cgroup/memory/memory.limit_in_bytes",
}

// ErrNoLimit means the process is not memory-limited, which is the normal case
// outside a container and not a failure.
var ErrNoLimit = errors.New("memlimit: no cgroup memory limit in effect")

// Apply sets GOMEMLIMIT from the cgroup limit and reports what it did.
//
// An explicit GOMEMLIMIT in the environment always wins: an operator who set it
// deliberately should not have it silently overridden.
func Apply() (string, error) {
	if v := os.Getenv("GOMEMLIMIT"); v != "" {
		return fmt.Sprintf("GOMEMLIMIT=%s from the environment, left alone", v), nil
	}

	limit, err := detect()
	if err != nil {
		return "", err
	}
	target := int64(float64(limit) * Headroom)
	debug.SetMemoryLimit(target)
	return fmt.Sprintf("GOMEMLIMIT set to %d MiB from a %d MiB cgroup limit",
		target>>20, limit>>20), nil
}

// detect reads the effective cgroup memory limit in bytes.
func detect() (int64, error) {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		n, err := parseLimit(string(b))
		if err != nil {
			continue
		}
		return n, nil
	}
	return 0, ErrNoLimit
}

// parseLimit reads one cgroup limit value.
//
// Anything absurdly large is "unlimited" in disguise: cgroup v1 reports no
// limit as a number near the maximum int64 rather than as a word, and treating
// that as a real limit would set a GOMEMLIMIT of several exabytes.
func parseLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0, ErrNoLimit
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	if n <= 0 || n > 1<<52 { // 4 PiB — nothing real, so it means unlimited
		return 0, ErrNoLimit
	}
	return n, nil
}
