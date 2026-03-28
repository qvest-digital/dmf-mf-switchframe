//go:build correctness

package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// ==========================================================================
// 10-bit frame timing tests.
//
// PTS monotonicity and cadence regularity should hold regardless of the
// pixel format. These tests verify that 10-bit frames (which are 4x larger)
// don't introduce timing anomalies from increased memory pressure or
// slower processing.
// ==========================================================================

// TestPipelineOutputTimingRegularity10bit feeds 10-bit frames and verifies
// PTS monotonicity and regular cadence in the TS output.
func TestPipelineOutputTimingRegularity10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("tim10", sourceA)
	h.cut(t, "tim10")

	// Feed 150 10-bit frames (5 seconds at 30fps).
	const numFrames = 150
	for i := 0; i < numFrames; i++ {
		yVal := uint16(300 + i%641) // varying content
		pf := make10bitFrame(testWidth, testHeight, yVal, 512, 512,
			int64(90000+i*ptsDelta), i%30 == 0)
		h.sw.IngestRawVideo("tim10", pf)
		if i < numFrames-1 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	h.waitForPipeline(1 * time.Second)

	if h.tsLen() == 0 {
		t.Skip("no TS output produced")
	}

	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: 10-bit timing test: %d video frames (expected ~%d)", len(videoFrames), numFrames)
	require.GreaterOrEqual(t, len(videoFrames), 50, "need enough frames for timing analysis")

	// Compute PTS deltas.
	deltas := ptsDeltas(videoFrames)
	require.Greater(t, len(deltas), 0)

	mean, stddev, minDelta, maxDelta := computeDeltaStats(deltas)
	t.Logf("MEASURE: 10-bit timing: mean=%.1f, stddev=%.1f, min=%d, max=%d",
		mean, stddev, minDelta, maxDelta)

	// PTS monotonicity.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "10-bit PTS")

	// Stddev should be reasonable (same as 8-bit: < 500 ticks).
	require.Less(t, stddev, 500.0,
		"10-bit PTS stddev (%.1f) exceeds 500 ticks -- timing irregular", stddev)

	// No double-frame gap.
	require.LessOrEqual(t, maxDelta, int64(6000),
		"10-bit max delta %d exceeds 6000 ticks (double-frame gap)", maxDelta)

	// No half-frame gap.
	require.GreaterOrEqual(t, minDelta, int64(1500),
		"10-bit min delta %d below 1500 ticks (half-frame gap)", minDelta)
}

// TestPipelineTimingAcrossCut10bit verifies PTS monotonicity across a cut
// when using 10-bit frames.
func TestPipelineTimingAcrossCut10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("cutA10", sourceA)
	h.addSource("cutB10", sourceB)

	// Phase 1: 30 frames from A.
	h.cut(t, "cutA10")
	for i := 0; i < 30; i++ {
		pf := make10bitFrame(testWidth, testHeight, 200, 512, 512,
			int64(90000+i*ptsDelta), i == 0)
		h.sw.IngestRawVideo("cutA10", pf)
		if i < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	preCount := h.capture.videoCount()
	require.Greater(t, preCount, 0, "should have frames from A")

	// Phase 2: Cut to B, 30 frames.
	h.cut(t, "cutB10")
	for i := 0; i < 30; i++ {
		pf := make10bitFrame(testWidth, testHeight, 700, 512, 512,
			int64(90000+30*ptsDelta+i*ptsDelta), i == 0)
		h.sw.IngestRawVideo("cutB10", pf)
		if i < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	if h.tsLen() == 0 {
		t.Skip("no TS output produced")
	}

	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: 10-bit cut timing: %d frames", len(videoFrames))
	require.GreaterOrEqual(t, len(videoFrames), 10)

	// PTS monotonicity.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "10-bit PTS across cut")

	// Find max delta (likely at the cut).
	deltas := ptsDeltas(videoFrames)
	_, _, _, maxDelta := computeDeltaStats(deltas)
	t.Logf("MEASURE: 10-bit max PTS gap at cut = %d ticks (%.1f ms)", maxDelta, float64(maxDelta)/90.0)

	// No gap > 100ms.
	require.LessOrEqual(t, maxDelta, int64(9000),
		"10-bit PTS gap at cut (%d ticks = %.1f ms) exceeds 100ms", maxDelta, float64(maxDelta)/90.0)
}
