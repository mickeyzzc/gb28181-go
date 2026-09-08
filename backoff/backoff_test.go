package backoff

import (
	"testing"
	"time"
)

func TestBackoffDoublesThenCaps(t *testing.T) {
	b := New(1*time.Second, 8*time.Second)

	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second, // capped
		8 * time.Second, // …and stays there
	}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("wait %d = %v, want %v", i, got, w)
		}
	}
}

func TestBackoffResetReturnsToBase(t *testing.T) {
	b := New(1*time.Second, 30*time.Second)

	if got := b.Next(); got != 1*time.Second {
		t.Fatalf("first wait = %v, want 1s", got)
	}
	if got := b.Next(); got != 2*time.Second {
		t.Fatalf("second wait = %v, want 2s", got)
	}

	b.Reset()

	if got := b.Next(); got != 1*time.Second {
		t.Fatalf("wait after reset = %v, want 1s (back to base)", got)
	}
}

func TestBackoffDegenerateArgumentsNormalized(t *testing.T) {
	// Non-positive base falls back to the default base.
	b := New(0, time.Minute)
	if got := b.Next(); got != DefaultBase {
		t.Fatalf("wait with zero base = %v, want default %v", got, DefaultBase)
	}

	// max below base is clamped to base — the sequence is constant.
	b2 := New(5*time.Second, 2*time.Second)
	if got := b2.Next(); got != 5*time.Second {
		t.Fatalf("first wait = %v, want 5s", got)
	}
	if got := b2.Next(); got != 5*time.Second {
		t.Fatalf("second wait = %v, want 5s (max clamped to base)", got)
	}
}
