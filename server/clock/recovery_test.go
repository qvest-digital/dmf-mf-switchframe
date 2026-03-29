package clock

import (
	"math/rand"
	"testing"
	"time"
)

func TestRecoverySnaps2997(t *testing.T) {
	cr := NewClockRecovery(30, 1, 30)
	now := time.Now()
	interval := time.Duration(33_370_000) // 33.37ms → 29.97fps

	for i := 0; i < 64; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval)
	}

	num, den := cr.Rate()
	if num != 30000 || den != 1001 {
		t.Errorf("expected 30000/1001, got %d/%d", num, den)
	}
}

func TestRecoverySnaps25(t *testing.T) {
	cr := NewClockRecovery(30, 1, 30)
	now := time.Now()
	interval := 40 * time.Millisecond

	for i := 0; i < 64; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval)
	}

	num, den := cr.Rate()
	if num != 25 || den != 1 {
		t.Errorf("expected 25/1, got %d/%d", num, den)
	}
}

func TestRecoverySnaps5994(t *testing.T) {
	cr := NewClockRecovery(60, 1, 30)
	now := time.Now()
	interval := time.Duration(16_680_000) // 16.68ms → 59.94fps

	for i := 0; i < 64; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval)
	}

	num, den := cr.Rate()
	if num != 60000 || den != 1001 {
		t.Errorf("expected 60000/1001, got %d/%d", num, den)
	}
}

func TestRecoverySnaps30(t *testing.T) {
	cr := NewClockRecovery(30, 1, 30)
	now := time.Now()
	interval := time.Duration(33_333_333) // 33.33ms → 30fps

	for i := 0; i < 64; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval)
	}

	num, den := cr.Rate()
	if num != 30 || den != 1 {
		t.Errorf("expected 30/1, got %d/%d", num, den)
	}
}

func TestRecoveryWithJitter(t *testing.T) {
	cr := NewClockRecovery(30, 1, 30)
	now := time.Now()
	baseInterval := time.Duration(33_370_000) // 33.37ms → 29.97fps
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 64; i++ {
		cr.RecordArrival(now)
		jitter := time.Duration(rng.Int63n(4_000_000) - 2_000_000) // ±2ms
		now = now.Add(baseInterval + jitter)
	}

	num, den := cr.Rate()
	if num != 30000 || den != 1001 {
		t.Errorf("expected 30000/1001, got %d/%d (measured=%.3f)", num, den, cr.MeasuredFPS())
	}
}

func TestRecoveryRateChange(t *testing.T) {
	cr := NewClockRecovery(30000, 1001, 30)
	now := time.Now()

	// Feed 60 frames at 29.97fps
	interval2997 := time.Duration(33_370_000)
	for i := 0; i < 60; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval2997)
	}

	num, den := cr.Rate()
	if num != 30000 || den != 1001 {
		t.Errorf("phase 1: expected 30000/1001, got %d/%d", num, den)
	}

	// Feed 60 frames at 25fps
	interval25 := 40 * time.Millisecond
	for i := 0; i < 60; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval25)
	}

	num, den = cr.Rate()
	if num != 25 || den != 1 {
		t.Errorf("phase 2: expected 25/1, got %d/%d (measured=%.3f)", num, den, cr.MeasuredFPS())
	}
}

func TestRecoveryStabilityThreshold(t *testing.T) {
	// Use a higher threshold (60) so we can test the boundary clearly.
	// The window is 64 entries, so after 64 frames at 25fps the measurement
	// will be clean. We test that 10 clean measurements don't trigger a
	// change, but 60 do.
	cr := NewClockRecovery(30000, 1001, 60)
	now := time.Now()

	// Fill the window with 29.97fps first
	interval2997 := time.Duration(33_370_000)
	for i := 0; i < 64; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval2997)
	}

	num, den := cr.Rate()
	if num != 30000 || den != 1001 {
		t.Fatalf("setup: expected 30000/1001, got %d/%d", num, den)
	}

	// Fill window completely with 25fps (64 frames) so measurement is clean,
	// but that's only 64 consecutive measurements at 25fps — still below
	// threshold of 60... except the first ~16 frames won't snap to 25
	// because the window still has old data. We need to track how many
	// actually snap to 25.
	//
	// Instead: feed frames at 25fps. After the window flushes (64 frames),
	// measurements snap to 25. We need 60 consecutive snaps to 25 to change.
	// So after 64+10 = 74 frames, we should NOT have changed (only ~10 snaps).
	// After 64+60 = 124 frames total, we should have changed.

	interval25 := 40 * time.Millisecond

	// Feed 74 frames at 25fps — window flushed but only ~10 clean measurements
	for i := 0; i < 74; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval25)
	}

	num, den = cr.Rate()
	if num != 30000 || den != 1001 {
		t.Errorf("after 74 frames at 25fps: expected 30000/1001 (unchanged), got %d/%d", num, den)
	}

	// Feed 50 more at 25fps — total ~60 clean measurements, should change
	for i := 0; i < 50; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval25)
	}

	num, den = cr.Rate()
	if num != 25 || den != 1 {
		t.Errorf("after 124 frames at 25fps: expected 25/1, got %d/%d (measured=%.3f)", num, den, cr.MeasuredFPS())
	}
}

func TestRecoveryInitialRate(t *testing.T) {
	cr := NewClockRecovery(30000, 1001, 30)
	num, den := cr.Rate()
	if num != 30000 || den != 1001 {
		t.Errorf("expected initial 30000/1001, got %d/%d", num, den)
	}
}

func TestSnapToStandard(t *testing.T) {
	tests := []struct {
		measured    float64
		expectedNum int
		expectedDen int
	}{
		{29.95, 30000, 1001},
		{24.98, 25, 1},
		{59.90, 60000, 1001},
		{30.02, 30, 1},
		{23.98, 24000, 1001},
	}

	for _, tt := range tests {
		num, den := SnapToStandard(tt.measured)
		if num != tt.expectedNum || den != tt.expectedDen {
			t.Errorf("SnapToStandard(%.2f) = %d/%d, want %d/%d",
				tt.measured, num, den, tt.expectedNum, tt.expectedDen)
		}
	}
}

func TestRecoveryNotEnoughSamples(t *testing.T) {
	cr := NewClockRecovery(30, 1, 30)
	now := time.Now()
	interval := 40 * time.Millisecond // 25fps

	// Only 5 arrivals — not enough for measurement (need >= 16)
	for i := 0; i < 5; i++ {
		cr.RecordArrival(now)
		now = now.Add(interval)
	}

	num, den := cr.Rate()
	if num != 30 || den != 1 {
		t.Errorf("expected initial 30/1 (not enough samples), got %d/%d", num, den)
	}
}
