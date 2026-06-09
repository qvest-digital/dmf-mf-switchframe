package backoff

import (
	"context"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	var b Backoff
	// Calling Next initializes defaults: 1s initial, 30s max, no jitter.
	d := b.Next()
	if d != time.Second {
		t.Fatalf("expected default initial 1s, got %v", d)
	}
	// Verify max is set by advancing through the progression.
	var last time.Duration
	for i := 0; i < 20; i++ {
		last = b.Next()
	}
	if last != 30*time.Second {
		t.Fatalf("expected default max 30s, got %v", last)
	}
}

func TestExponentialProgressionNoJitter(t *testing.T) {
	b := Backoff{
		Initial:   time.Second,
		Max:       30 * time.Second,
		JitterPct: 0,
	}
	expected := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
	}
	for i, want := range expected {
		got := b.Next()
		if got != want {
			t.Fatalf("step %d: got %v, want %v", i, got, want)
		}
	}
}

func TestCapAtMax(t *testing.T) {
	b := Backoff{
		Initial:   time.Second,
		Max:       5 * time.Second,
		JitterPct: 0,
	}
	// 1, 2, 4, 5, 5, 5 ...
	expected := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		5 * time.Second,
		5 * time.Second,
		5 * time.Second,
	}
	for i, want := range expected {
		got := b.Next()
		if got != want {
			t.Fatalf("step %d: got %v, want %v", i, got, want)
		}
	}
}

func TestReset(t *testing.T) {
	b := Backoff{
		Initial:   time.Second,
		Max:       30 * time.Second,
		JitterPct: 0,
	}
	b.Next() // 1s
	b.Next() // 2s
	b.Next() // 4s
	b.Reset()
	got := b.Next()
	if got != time.Second {
		t.Fatalf("after reset: got %v, want 1s", got)
	}
}

func TestJitterRange(t *testing.T) {
	b := Backoff{
		Initial:   10 * time.Second,
		Max:       10 * time.Second, // keep it at 10s so every call returns ~10s
		JitterPct: 0.25,
	}

	minSeen := time.Duration(1<<63 - 1)
	maxSeen := time.Duration(0)

	for i := 0; i < 100; i++ {
		d := b.Next()
		if d < minSeen {
			minSeen = d
		}
		if d > maxSeen {
			maxSeen = d
		}
		// Each value must be within [7.5s, 12.5s].
		if d < 7500*time.Millisecond || d > 12500*time.Millisecond {
			t.Fatalf("iteration %d: %v out of [7.5s, 12.5s]", i, d)
		}
	}

	// With 100 samples, we should see some spread.
	spread := maxSeen - minSeen
	if spread < 500*time.Millisecond {
		t.Fatalf("jitter spread too small: %v (min=%v, max=%v)", spread, minSeen, maxSeen)
	}
}

func TestJitterPctClampedAboveOne(t *testing.T) {
	// JitterPct > 1 is nonsensical input. Without clamping, the jitter factor
	// (1 - JitterPct + rand*2*JitterPct) can go negative and yield a negative
	// duration whose Timer fires immediately, defeating the backoff. The clamp
	// in init() bounds JitterPct to 1, so every value stays in [0, 2*Initial].
	b := Backoff{
		Initial:   10 * time.Second,
		Max:       10 * time.Second, // hold at 10s so each call jitters around 10s
		JitterPct: 5.0,              // way out of range
	}
	for i := 0; i < 200; i++ {
		d := b.Next()
		if d <= 0 {
			t.Fatalf("iteration %d: non-positive duration %v (negative defeats backoff)", i, d)
		}
		// With JitterPct clamped to 1, factor ∈ [0,2], so d ∈ [0, 20s].
		if d > 20*time.Second {
			t.Fatalf("iteration %d: %v exceeds clamped upper bound 20s", i, d)
		}
	}
}

func TestJitterPctNegativeTreatedAsZero(t *testing.T) {
	// A negative JitterPct should behave like no jitter, not amplify the result.
	b := Backoff{
		Initial:   time.Second,
		Max:       time.Second,
		JitterPct: -0.5,
	}
	for i := 0; i < 20; i++ {
		if got := b.Next(); got != time.Second {
			t.Fatalf("iteration %d: expected exact 1s with negative jitter, got %v", i, got)
		}
	}
}

func TestCustomInitialAndMax(t *testing.T) {
	b := Backoff{
		Initial:   500 * time.Millisecond,
		Max:       3 * time.Second,
		JitterPct: 0,
	}
	expected := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		3 * time.Second,
		3 * time.Second,
	}
	for i, want := range expected {
		got := b.Next()
		if got != want {
			t.Fatalf("step %d: got %v, want %v", i, got, want)
		}
	}
}

func TestZeroJitterExactValues(t *testing.T) {
	b := Backoff{
		Initial:   100 * time.Millisecond,
		Max:       time.Second,
		JitterPct: 0,
	}
	// Verify exact values with no jitter.
	for i := 0; i < 20; i++ {
		d := b.Next()
		// All values must be exact multiples, no randomness.
		switch d {
		case 100 * time.Millisecond,
			200 * time.Millisecond,
			400 * time.Millisecond,
			800 * time.Millisecond,
			time.Second:
			// ok
		default:
			t.Fatalf("iteration %d: unexpected value %v (should be exact)", i, d)
		}
	}
}

func TestWaitCancelledContext(t *testing.T) {
	b := Backoff{
		Initial:   10 * time.Second, // long enough that we'd notice if it actually waited
		JitterPct: 0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	err := b.Wait(ctx)
	elapsed := time.Since(start)

	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Wait should have returned immediately, took %v", elapsed)
	}
}

func TestWaitReturnsAfterDuration(t *testing.T) {
	b := Backoff{
		Initial:   50 * time.Millisecond,
		Max:       50 * time.Millisecond,
		JitterPct: 0,
	}

	start := time.Now()
	err := b.Wait(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("Wait returned too quickly: %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("Wait took too long: %v", elapsed)
	}
}

func TestWaitAdvancesBackoff(t *testing.T) {
	b := Backoff{
		Initial:   10 * time.Millisecond,
		Max:       100 * time.Millisecond,
		JitterPct: 0,
	}

	// Wait consumes the first level (10ms).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = b.Wait(ctx)

	// Next call should return 20ms (advanced by Wait).
	got := b.Next()
	if got != 20*time.Millisecond {
		t.Fatalf("after Wait, expected 20ms, got %v", got)
	}
}
