package atomicutil

import (
	"sync/atomic"
	"testing"
)

func TestUpdateMax(t *testing.T) {
	var field atomic.Int64

	// Zero → positive updates.
	UpdateMax(&field, 100)
	if got := field.Load(); got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}

	// Larger value replaces.
	UpdateMax(&field, 200)
	if got := field.Load(); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}

	// Smaller value is a no-op.
	UpdateMax(&field, 50)
	if got := field.Load(); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}

	// Equal value is a no-op.
	UpdateMax(&field, 200)
	if got := field.Load(); got != 200 {
		t.Fatalf("expected 200, got %d", got)
	}

	// Negative values: start negative, update to less-negative.
	field.Store(-500)
	UpdateMax(&field, -100)
	if got := field.Load(); got != -100 {
		t.Fatalf("expected -100, got %d", got)
	}
}
