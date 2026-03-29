package switcher

import (
	"testing"
)

// TestDriftEstimator_ZeroDrift verifies that a source running at exactly
// the nominal frame rate reports ~0 PPM drift.
func TestDriftEstimator_ZeroDrift(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	// Simulate 200 frames at exactly 24fps.
	// PTS interval: 90000/24 = 3750 ticks per frame.
	// Wall interval: 1/24 sec = 41,666,667 ns per frame.
	ptsInterval := int64(3750)
	wallIntervalNs := int64(41_666_667)

	for i := 0; i < 200; i++ {
		pts := int64(i) * ptsInterval
		wallNs := int64(i) * wallIntervalNs
		d.sample(pts, wallNs)
	}

	drift := d.DriftPPM()
	if drift < -5 || drift > 5 {
		t.Errorf("expected ~0 PPM drift, got %d", drift)
	}
}

// TestDriftEstimator_PositiveDrift simulates a source clock that is ~100ppm
// faster than the wall clock. The wall intervals are slightly longer than
// expected, so the source appears to be running ahead.
func TestDriftEstimator_PositiveDrift(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	ptsInterval := int64(3750) // 24fps in 90kHz
	// Wall clock is slightly slower: each interval is 100ppm longer.
	// 41,666,667 ns * 100/1e6 = ~4.167 ns per frame of extra wall time.
	wallIntervalNs := int64(41_666_667 + 4167)

	for i := 0; i < 500; i++ {
		pts := int64(i) * ptsInterval
		wallNs := int64(i) * wallIntervalNs
		d.sample(pts, wallNs)
	}

	drift := d.DriftPPM()
	if drift < 80 || drift > 120 {
		t.Errorf("expected ~100 PPM drift, got %d", drift)
	}
}

// TestDriftEstimator_NegativeDrift simulates a source clock that is ~50ppm
// slower than the wall clock. The wall intervals are shorter than expected.
func TestDriftEstimator_NegativeDrift(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	ptsInterval := int64(3750) // 24fps in 90kHz
	// Wall clock is slightly faster: each interval is 50ppm shorter.
	wallIntervalNs := int64(41_666_667 - 2083)

	for i := 0; i < 500; i++ {
		pts := int64(i) * ptsInterval
		wallNs := int64(i) * wallIntervalNs
		d.sample(pts, wallNs)
	}

	drift := d.DriftPPM()
	if drift > -30 || drift < -70 {
		t.Errorf("expected ~-50 PPM drift, got %d", drift)
	}
}

// TestDriftEstimator_NeedsMinSamples verifies that the estimator does not
// report drift until at least 100 samples have been collected (too few
// samples produce unreliable estimates).
func TestDriftEstimator_NeedsMinSamples(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	// 50 samples — below the 100-sample threshold.
	for i := 0; i < 50; i++ {
		d.sample(int64(i)*3750, int64(i)*41_666_667)
	}

	if d.DriftPPM() != 0 {
		t.Errorf("expected 0 drift with only %d samples, got %d", d.Samples(), d.DriftPPM())
	}
	if d.Samples() != 50 {
		t.Errorf("expected 50 samples, got %d", d.Samples())
	}
}

// TestDriftEstimator_ExactlyAtThreshold verifies behavior at exactly 100 samples.
func TestDriftEstimator_ExactlyAtThreshold(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	// 99 samples — should not compute drift yet.
	for i := 0; i < 99; i++ {
		d.sample(int64(i)*3750, int64(i)*41_666_667)
	}
	if d.DriftPPM() != 0 {
		t.Errorf("expected 0 drift at 99 samples, got %d", d.DriftPPM())
	}

	// 100th sample — now drift should be computed.
	d.sample(int64(99)*3750, int64(99)*41_666_667)
	if d.Samples() != 100 {
		t.Errorf("expected 100 samples, got %d", d.Samples())
	}
	// With perfect timing, drift should be ~0.
	drift := d.DriftPPM()
	if drift < -5 || drift > 5 {
		t.Errorf("expected ~0 PPM drift at threshold, got %d", drift)
	}
}

// TestDriftEstimator_Reset verifies that a fresh estimator starts with
// zero samples and zero drift.
func TestDriftEstimator_Reset(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	if d.Samples() != 0 {
		t.Fatalf("fresh estimator should have 0 samples, got %d", d.Samples())
	}
	if d.DriftPPM() != 0 {
		t.Fatalf("fresh estimator should have 0 drift, got %d", d.DriftPPM())
	}

	// First sample initializes.
	d.sample(0, 0)
	if d.Samples() != 1 {
		t.Fatalf("expected 1 sample after first call, got %d", d.Samples())
	}

	// Second sample.
	d.sample(3750, 41_666_667)
	if d.Samples() != 2 {
		t.Fatalf("expected 2 samples, got %d", d.Samples())
	}
}

// TestDriftEstimator_LargeDataset_59_94fps tests convergence with a large
// number of frames at 59.94fps (common broadcast rate) with a small
// positive drift.
func TestDriftEstimator_LargeDataset_59_94fps(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	// 59.94fps → 90000/59.94 ≈ 1501.5 PTS ticks per frame.
	// Use integer PTS interval of 1501 ticks. The "ideal" wall time for this
	// PTS interval is: 1501 * 1e9 / 90000 = 16,677,778 ns per frame.
	// We must use this as the base wall interval (not 1/59.94) to be consistent
	// with how the drift estimator computes expectedWallNs from PTS elapsed.
	ptsInterval := int64(1501)
	// Base wall interval matching PTS exactly (zero drift).
	baseWallNs := int64(16_677_778) // 1501 * 1e9 / 90000
	// Add +10ppm drift to wall clock.
	wallIntervalNs := baseWallNs + baseWallNs*10/1_000_000

	for i := 0; i < 10000; i++ {
		d.sample(int64(i)*ptsInterval, int64(i)*wallIntervalNs)
	}

	drift := d.DriftPPM()
	if drift < 5 || drift > 15 {
		t.Errorf("expected ~10 PPM drift, got %d", drift)
	}
}

// TestDriftEstimator_30fps_ZeroDrift verifies correct operation at 30fps,
// the most common non-broadcast frame rate.
func TestDriftEstimator_30fps_ZeroDrift(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	// 30fps → 90000/30 = 3000 PTS ticks per frame.
	// Wall interval: 1/30 sec = 33,333,333 ns.
	ptsInterval := int64(3000)
	wallIntervalNs := int64(33_333_333)

	for i := 0; i < 300; i++ {
		d.sample(int64(i)*ptsInterval, int64(i)*wallIntervalNs)
	}

	drift := d.DriftPPM()
	if drift < -5 || drift > 5 {
		t.Errorf("expected ~0 PPM drift at 30fps, got %d", drift)
	}
}

// TestDriftEstimator_NonZeroStart verifies that the estimator works correctly
// when PTS and wall clock don't start at zero (common in mid-stream joins).
func TestDriftEstimator_NonZeroStart(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	basePTS := int64(1_000_000) // arbitrary non-zero start PTS
	baseWall := int64(5_000_000_000)
	ptsInterval := int64(3750)
	wallIntervalNs := int64(41_666_667)

	for i := 0; i < 200; i++ {
		pts := basePTS + int64(i)*ptsInterval
		wallNs := baseWall + int64(i)*wallIntervalNs
		d.sample(pts, wallNs)
	}

	drift := d.DriftPPM()
	if drift < -5 || drift > 5 {
		t.Errorf("expected ~0 PPM drift with non-zero start, got %d", drift)
	}
}

// TestDriftEstimator_LargeDrift_1000ppm verifies detection of a relatively
// large drift (1000 PPM = 0.1%). This is well beyond the range of a stable
// clock but tests the estimator's dynamic range.
func TestDriftEstimator_LargeDrift_1000ppm(t *testing.T) {
	t.Parallel()
	var d driftEstimator

	ptsInterval := int64(3750)
	// 1000ppm slower wall clock: each interval is 1000ppm longer.
	wallIntervalNs := int64(41_666_667 + 41_667)

	for i := 0; i < 500; i++ {
		d.sample(int64(i)*ptsInterval, int64(i)*wallIntervalNs)
	}

	drift := d.DriftPPM()
	if drift < 900 || drift > 1100 {
		t.Errorf("expected ~1000 PPM drift, got %d", drift)
	}
}
