package sip

import (
	"sync"
	"time"
)

// authFailureTracker counts REGISTER authentication failures per source
// host and locks repeat offenders out for a window — the anti-brute-force
// backstop of issue #38. Keyed by source IP (not device ID) so device-ID
// spraying from one host is contained too. Safe for concurrent use.
type authFailureTracker struct {
	mu      sync.Mutex
	entries map[string]*authFailEntry

	max     int           // failures before lockout; 0 = disabled
	window  time.Duration // failures older than this decay away
	lockout time.Duration // lockout duration once max is reached
	timeNow func() time.Time
}

type authFailEntry struct {
	fails       int
	lastFailure time.Time
	lockedUntil time.Time
}

func newAuthFailureTracker(limit int, window, lockout time.Duration) *authFailureTracker {
	return &authFailureTracker{
		entries: make(map[string]*authFailEntry),
		max:     limit,
		window:  window,
		lockout: lockout,
		timeNow: time.Now,
	}
}

// locked reports whether key is currently locked out. Expired locks clear
// lazily.
func (t *authFailureTracker) locked(key string) bool {
	if t.max <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		return false
	}
	now := t.timeNow()
	if now.Before(e.lockedUntil) {
		return true
	}
	if !e.lockedUntil.IsZero() && now.After(e.lockedUntil) {
		// Lockout served: reset the failure budget.
		delete(t.entries, key)
	}
	return false
}

// recordFailure adds one failure; at max failures within the window the
// key locks out for the configured duration.
func (t *authFailureTracker) recordFailure(key string) {
	if t.max <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.timeNow()
	e, ok := t.entries[key]
	if !ok || now.Sub(e.lastFailure) > t.window {
		e = &authFailEntry{}
		t.entries[key] = e
	}
	e.fails++
	e.lastFailure = now
	if e.fails >= t.max {
		e.lockedUntil = now.Add(t.lockout)
	}
}

// recordSuccess clears the failure budget for key (a device that
// eventually authenticates is clearly not under attack from that host —
// or the attacker gave up).
func (t *authFailureTracker) recordSuccess(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}
