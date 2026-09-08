// Package backoff provides the exponential backoff used by retry loops —
// the cascade REGISTER retry today (issue #44).
//
// The sequence is deterministic (no jitter): the populations using it are
// single-digit (a handful of cascade uppers), so there is no thundering
// herd to decorrelate, and deterministic waits keep retry timing testable.
// Hosts wanting jitter wrap Next.
package backoff

import "time"

// DefaultBase is the fallback first wait when New is called with a
// non-positive base.
const DefaultBase = time.Second

// Backoff yields exponentially growing waits. It is not safe for
// concurrent use — one Backoff per retry loop.
type Backoff struct {
	base time.Duration
	max  time.Duration
	next time.Duration
}

// New creates a Backoff whose first Next returns base, each subsequent
// Next doubles (capped at max), and Reset returns to base. Degenerate
// arguments are normalized: base ≤ 0 → DefaultBase, max < base → base.
func New(base, ceiling time.Duration) *Backoff {
	if base <= 0 {
		base = DefaultBase
	}
	if ceiling < base {
		ceiling = base
	}
	return &Backoff{base: base, max: ceiling, next: base}
}

// Next returns the current wait and schedules the next one (double the
// current, capped at max).
func (b *Backoff) Next() time.Duration {
	wait := b.next
	if doubled := b.next * 2; doubled > b.max {
		b.next = b.max
	} else {
		b.next = doubled
	}
	return wait
}

// Reset returns the sequence to its base wait.
func (b *Backoff) Reset() {
	b.next = b.base
}
