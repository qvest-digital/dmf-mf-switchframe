//go:build correctness

// Tests for frame timing regularity and cadence through the full pipeline.
// Irregular output PTS causes visible judder; these tests measure delta
// statistics and assert uniformity.
package pipeline

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// computeDeltaStats computes mean, stddev, min, max of PTS deltas.
func computeDeltaStats(deltas []int64) (mean, stddev float64, min, max int64) {
	if len(deltas) == 0 {
		return 0, 0, 0, 0
	}

	min = deltas[0]
	max = deltas[0]
	var sum int64
	for _, d := range deltas {
		sum += d
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	mean = float64(sum) / float64(len(deltas))

	var varianceSum float64
	for _, d := range deltas {
		diff := float64(d) - mean
		varianceSum += diff * diff
	}
	stddev = math.Sqrt(varianceSum / float64(len(deltas)))

	return mean, stddev, min, max
}

// ptsDeltas returns the deltas between consecutive PTS values.
func ptsDeltas(frames []testutil.FrameInfo) []int64 {
	if len(frames) < 2 {
		return nil
	}
	deltas := make([]int64, len(frames)-1)
	for i := 1; i < len(frames); i++ {
		deltas[i-1] = frames[i].PTS - frames[i-1].PTS
	}
	return deltas
}

// ==========================================================================
// TestPipelineOutputTimingRegularity
//
// Feeds 150 frames (5 seconds at 30fps) through the pipeline harness,
// captures TS output, probes PTS via ffprobe, and asserts regularity.
//
// Expected delta: 3000 ticks (90kHz / 30fps).
// With wall-clock PTS, deltas vary based on real elapsed time.
// ==========================================================================

func TestPipelineOutputTimingRegularity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed 150 frames at ~5ms pacing (5 seconds at 30fps nominal).
	h.feedFrames("camA", 150, 90000)
	h.waitForPipeline(1 * time.Second)

	// Write TS and probe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: total video frames = %d (expected ~150)", len(videoFrames))
	require.GreaterOrEqual(t, len(videoFrames), 50, "need enough frames for timing analysis")

	// Compute PTS deltas.
	deltas := ptsDeltas(videoFrames)
	require.Greater(t, len(deltas), 0, "need at least one delta")

	mean, stddev, minDelta, maxDelta := computeDeltaStats(deltas)
	t.Logf("MEASURE: mean delta = %.1f, stddev = %.1f, min = %d, max = %d", mean, stddev, minDelta, maxDelta)
	t.Logf("MEASURE: expected delta = %d ticks (90kHz/30fps)", ptsDelta)

	// Log first 20 deltas for inspection.
	logLimit := 20
	if logLimit > len(deltas) {
		logLimit = len(deltas)
	}
	for i := 0; i < logLimit; i++ {
		t.Logf("MEASURE: delta[%d] = %d", i, deltas[i])
	}

	// Assert: stddev < 500 ticks (reasonable for wall-clock-driven PTS).
	require.Less(t, stddev, 500.0,
		"stddev of PTS deltas (%.1f) exceeds 500 ticks -- timing is irregular", stddev)

	// Assert: no individual delta > 6000 (no double-frame gap).
	require.LessOrEqual(t, maxDelta, int64(6000),
		"max delta %d exceeds 6000 ticks (double-frame gap)", maxDelta)

	// Assert: no individual delta < 1500 (no half-frame gap).
	require.GreaterOrEqual(t, minDelta, int64(1500),
		"min delta %d below 1500 ticks (half-frame gap)", minDelta)

	t.Logf("Timing regularity test passed: %d frames, mean=%.1f, stddev=%.1f, range=[%d, %d]",
		len(videoFrames), mean, stddev, minDelta, maxDelta)
}

// ==========================================================================
// TestPipelineTimingAcrossCut
//
// Feeds 30 frames from source A, cuts to source B, feeds 30 more frames.
// Verifies PTS monotonicity across the cut and measures the gap.
// ==========================================================================

func TestPipelineTimingAcrossCut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	// Phase 1: 30 frames from A.
	h.cut(t, "camA")
	h.feedFrames("camA", 30, 90000)
	h.waitForPipeline(500 * time.Millisecond)

	preCount := h.capture.videoCount()
	t.Logf("MEASURE: frames from camA = %d", preCount)
	require.Greater(t, preCount, 0, "should have frames from camA")

	// Phase 2: Cut to B and feed 30 more frames.
	h.cut(t, "camB")
	h.feedFrames("camB", 30, 90000+30*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	totalCount := h.capture.videoCount()
	t.Logf("MEASURE: total frames = %d", totalCount)

	// Write TS and probe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: total TS video frames = %d", len(videoFrames))
	require.GreaterOrEqual(t, len(videoFrames), 10, "need enough frames for cut timing analysis")

	// Verify PTS is monotonically increasing across the cut.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Compute all deltas and find the maximum (which is likely at the cut point).
	deltas := ptsDeltas(videoFrames)
	mean, stddev, minDelta, maxDelta := computeDeltaStats(deltas)
	t.Logf("MEASURE: mean delta = %.1f, stddev = %.1f, min = %d, max = %d", mean, stddev, minDelta, maxDelta)

	// Find the cut point: the delta that is largest (likely the gap at the cut).
	var cutDelta int64
	cutIdx := 0
	for i, d := range deltas {
		if d > cutDelta {
			cutDelta = d
			cutIdx = i
		}
	}
	t.Logf("MEASURE: PTS gap at cut = %d ticks (expected ~%d), at frame index %d", cutDelta, ptsDelta, cutIdx)

	// Assert: no gap > 9000 ticks (100ms). One extra frame delay is OK.
	require.LessOrEqual(t, cutDelta, int64(9000),
		"PTS gap at cut (%d ticks = %.1f ms) exceeds 100ms", cutDelta, float64(cutDelta)/90.0)

	t.Logf("Cut timing test passed: max gap = %d ticks (%.1f ms)", cutDelta, float64(cutDelta)/90.0)
}

// ==========================================================================
// TestPipelineTimingAcrossTransition
//
// Starts a dissolve transition, feeds frames from both sources, and
// verifies PTS advances continuously during blending (no stall).
// ==========================================================================

func TestPipelineTimingAcrossTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 40, Cb: 200, Cr: 200}
	colorB := sourceColor{Y: 220, Cb: 40, Cr: 40}

	h.addSource("srcA", colorA)
	h.addSource("srcB", colorB)

	// Configure real transition engine.
	h.sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return codec.NewFFmpegDecoder(nil)
		},
	})

	// Phase 1: Establish A on program (15 frames).
	h.cut(t, "srcA")
	h.feedFrames("srcA", 15, 90000)
	h.waitForPipeline(300 * time.Millisecond)

	preTransCount := h.capture.videoCount()
	t.Logf("MEASURE: pre-transition frames = %d", preTransCount)
	require.Greater(t, preTransCount, 0, "should have frames from srcA")

	// Phase 2: Start dissolve, feed both sources, advance position.
	require.NoError(t, h.sw.SetPreview(t.Context(), "srcB"))
	err := h.sw.StartTransition(t.Context(), "srcB", "mix", 10000, "")
	require.NoError(t, err, "should start dissolve transition")

	basePTS := int64(90000 + 15*ptsDelta)
	for i := 0; i < 30; i++ {
		pts := basePTS + int64(i)*ptsDelta
		pos := float64(i) / 29.0

		// Feed FROM source.
		yuvA := testutil.GenerateSolidYUV420(testWidth, testHeight, colorA.Y, colorA.Cb, colorA.Cr)
		pfA := &switcher.ProcessingFrame{
			YUV:        yuvA,
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("srcA", pfA)

		// Advance transition position.
		require.NoError(t, h.sw.SetTransitionPosition(t.Context(), pos))

		// Feed TO source.
		yuvB := testutil.GenerateSolidYUV420(testWidth, testHeight, colorB.Y, colorB.Cb, colorB.Cr)
		pfB := &switcher.ProcessingFrame{
			YUV:        yuvB,
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("srcB", pfB)

		time.Sleep(5 * time.Millisecond)
	}

	h.waitForPipeline(500 * time.Millisecond)

	// Phase 3: Post-transition frames from B.
	h.feedFrames("srcB", 15, basePTS+30*ptsDelta)
	h.waitForPipeline(300 * time.Millisecond)

	// Write TS and probe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: total TS video frames = %d (expected ~60)", len(videoFrames))
	require.GreaterOrEqual(t, len(videoFrames), 20, "need enough frames for transition timing analysis")

	// Verify PTS is monotonically increasing through transition.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Compute deltas.
	deltas := ptsDeltas(videoFrames)
	mean, stddev, minDelta, maxDelta := computeDeltaStats(deltas)
	t.Logf("MEASURE: mean delta = %.1f, stddev = %.1f, min = %d, max = %d", mean, stddev, minDelta, maxDelta)

	// Log deltas during the transition phase (roughly the middle third).
	transStart := len(deltas) / 4
	transEnd := 3 * len(deltas) / 4
	if transEnd > len(deltas) {
		transEnd = len(deltas)
	}
	t.Logf("MEASURE: transition-phase deltas (indices %d-%d):", transStart, transEnd-1)
	for i := transStart; i < transEnd; i++ {
		t.Logf("  delta[%d] = %d", i, deltas[i])
	}

	// Assert: no stall during transition (no delta > 6000 = 2x normal).
	require.LessOrEqual(t, maxDelta, int64(6000),
		"max delta %d during transition exceeds 6000 ticks (double-frame gap)", maxDelta)

	// Assert: no collapsed frames (no delta < 1500 = half normal).
	require.GreaterOrEqual(t, minDelta, int64(1500),
		"min delta %d below 1500 ticks (half-frame gap)", minDelta)

	// Assert: reasonable overall stddev.
	require.Less(t, stddev, 1000.0,
		"stddev of PTS deltas (%.1f) exceeds 1000 ticks during transition -- timing unstable", stddev)

	t.Logf("Transition timing test passed: %d frames, mean=%.1f, stddev=%.1f, range=[%d, %d]",
		len(videoFrames), mean, stddev, minDelta, maxDelta)
}

// ==========================================================================
// TestAudioTimingRegularity
//
// Feeds audio through the mixer for ~2 seconds, captures AAC output,
// probes PTS, and asserts regular cadence.
//
// Expected: 1920 ticks per AAC frame (1024 samples at 48kHz in 90kHz clock).
// ==========================================================================

func TestAudioTimingRegularity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)

	// Set up mixer channel.
	h.mixer.AddChannel("camA")
	require.NoError(t, h.mixer.SetAFV("camA", true))

	// Cut to A and feed video frames to seed PTS and drive processing.
	h.cut(t, "camA")
	h.feedFrames("camA", 60, 90000)

	// Wait for mixer ticker to produce audio output (~21ms cadence, 2 seconds).
	h.waitForPipeline(2500 * time.Millisecond)

	// Write TS and probe audio frames.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	audioFrames := testutil.ProbeAudioFrames(t, tsPath)
	t.Logf("MEASURE: total audio frames = %d", len(audioFrames))
	require.GreaterOrEqual(t, len(audioFrames), 10, "need enough audio frames for timing analysis")

	// Compute audio PTS deltas.
	deltas := ptsDeltas(audioFrames)
	require.Greater(t, len(deltas), 0, "need at least one audio delta")

	mean, stddev, minDelta, maxDelta := computeDeltaStats(deltas)
	t.Logf("MEASURE: audio mean delta = %.1f, stddev = %.1f", mean, stddev)
	t.Logf("MEASURE: audio min = %d, max = %d, expected = 1920 ticks (1024 samples at 48kHz)", minDelta, maxDelta)

	// Log first 20 audio deltas.
	logLimit := 20
	if logLimit > len(deltas) {
		logLimit = len(deltas)
	}
	for i := 0; i < logLimit; i++ {
		t.Logf("MEASURE: audio delta[%d] = %d", i, deltas[i])
	}

	// Assert: stddev < 100 ticks for audio (should be very regular).
	require.Less(t, stddev, 100.0,
		"audio stddev of PTS deltas (%.1f) exceeds 100 ticks -- audio timing is irregular", stddev)

	// Assert: no delta > 3840 (2x normal = 2 AAC frames).
	require.LessOrEqual(t, maxDelta, int64(3840),
		"audio max delta %d exceeds 3840 ticks (2x normal)", maxDelta)

	// Assert: no delta < 960 (0.5x normal = half AAC frame).
	require.GreaterOrEqual(t, minDelta, int64(960),
		"audio min delta %d below 960 ticks (0.5x normal)", minDelta)

	t.Logf("Audio timing test passed: %d frames, mean=%.1f, stddev=%.1f, range=[%d, %d]",
		len(audioFrames), mean, stddev, minDelta, maxDelta)
}
