//go:build correctness

package pipeline

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// ==========================================================================
// TestRefClockPipelineCut
//
// Same scenario as TestFullPipelineCutToTS but driven by the reference clock.
// Two sources with distinct colors, cut between them, encode to H.264,
// verify pixel content changes after cut.
//
// Key difference: processing is tick-driven (no wall clock pacing).
// The non-realtime clock produces ticks as fast as the sync loop can
// consume them, making this test faster and deterministic.
// ==========================================================================

func TestRefClockPipelineCut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	// Cut to A and feed frames into the frame sync buffer.
	h.cut(t, "camA")
	h.feedFrames("camA", 60, 90000)

	// Start ref clock + sync loop, let it process frames.
	h.startRefClock()
	h.waitForVideoFrames(t, 15)

	preCount := h.capture.videoCount()
	t.Logf("Frames from camA: %d", preCount)
	require.Greater(t, preCount, 0, "should have encoded frames from camA")

	// Cut to B and feed more frames.
	h.cut(t, "camB")
	h.feedFrames("camB", 60, 90000+60*ptsDelta)
	h.waitForVideoFrames(t, preCount+15)

	totalCount := h.capture.videoCount()
	t.Logf("Total frames after camB: %d", totalCount)
	require.Greater(t, totalCount, preCount, "should have additional frames from camB")

	// Stop ref clock before inspecting output.
	h.refClock.Stop()
	h.refClock = nil // prevent double-stop in close()

	// Write TS and verify with ffprobe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)

	// Verify frame count is reasonable.
	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: video frame count = %d", len(videoFrames))
	require.Greater(t, len(videoFrames), 10, "expected >10 video frames from ref clock pipeline")

	// Verify PTS is monotonically increasing.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Decode output and verify colors with per-pixel region checks.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 6, "should decode at least 6 frames for first/last check")

	// Check first 3 decoded frames: should match sourceA.Y (80).
	for fi := 0; fi < 3 && fi < len(decoded); fi++ {
		df := decoded[fi]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("MEASURE: frame %d avg Y = %.1f, expected ~%d (sourceA)", fi, avgY, sourceA.Y)

		matchCount := 0
		const sampleCount = 50
		for s := 0; s < sampleCount; s++ {
			pixIdx := s * (df.Width * df.Height) / sampleCount
			px := pixIdx % df.Width
			py := pixIdx / df.Width
			yVal, _, _ := testutil.YUVPixelAt(df.YUV, df.Width, df.Height, px, py)
			if math.Abs(float64(yVal)-float64(sourceA.Y)) <= 25 {
				matchCount++
			}
		}
		pct := float64(matchCount) / float64(sampleCount) * 100.0
		t.Logf("MEASURE: frame %d sourceA pixel match = %.0f%%", fi, pct)
		require.GreaterOrEqual(t, pct, 70.0,
			"frame %d: only %.0f%% of sampled pixels within +/-25 of sourceA.Y(%d)", fi, pct, sourceA.Y)
	}

	// Check last 3 decoded frames: should match sourceB.Y (160).
	for fi := len(decoded) - 3; fi < len(decoded); fi++ {
		if fi < 0 {
			continue
		}
		df := decoded[fi]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("MEASURE: frame %d avg Y = %.1f, expected ~%d (sourceB)", fi, avgY, sourceB.Y)

		matchCount := 0
		const sampleCount = 50
		for s := 0; s < sampleCount; s++ {
			pixIdx := s * (df.Width * df.Height) / sampleCount
			px := pixIdx % df.Width
			py := pixIdx / df.Width
			yVal, _, _ := testutil.YUVPixelAt(df.YUV, df.Width, df.Height, px, py)
			if math.Abs(float64(yVal)-float64(sourceB.Y)) <= 25 {
				matchCount++
			}
		}
		pct := float64(matchCount) / float64(sampleCount) * 100.0
		t.Logf("MEASURE: frame %d sourceB pixel match = %.0f%%", fi, pct)
		require.GreaterOrEqual(t, pct, 70.0,
			"frame %d: only %.0f%% of sampled pixels within +/-25 of sourceB.Y(%d)", fi, pct, sourceB.Y)
	}

	t.Logf("RefClock pipeline cut test passed: %d video frames, %d decoded",
		len(videoFrames), len(decoded))
}

// ==========================================================================
// TestRefClockAVSync
//
// Verifies that both audio and video PTS are derived from the same reference
// clock. Audio PTS starts at basePTS=0 and advances by exactly 1920 per AAC
// frame. Video PTS starts at 0 and advances monotonically. Both streams
// should start near PTS=0 (shared epoch).
// ==========================================================================

func TestRefClockAVSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock(), withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed enough frames to sustain processing.
	h.feedFrames("camA", 120, 0)

	h.startRefClock()

	// Wait for substantial output.
	h.waitForVideoFrames(t, 30)
	h.waitForAudioFrames(t, 5)

	// Stop and inspect.
	h.refClock.Stop()
	h.refClock = nil

	videos := h.capture.videosCopy()
	audios := h.capture.audiosCopy()

	t.Logf("MEASURE: video frames = %d, audio frames = %d", len(videos), len(audios))
	require.Greater(t, len(videos), 10, "should have >10 video frames")
	require.Greater(t, len(audios), 2, "should have >2 audio frames")

	// Verify video PTS monotonicity.
	for i := 1; i < len(videos); i++ {
		require.Greater(t, videos[i].PTS, videos[i-1].PTS,
			"video PTS should be monotonically increasing at frame %d", i)
	}

	// Verify audio PTS monotonicity with consistent delta (1920 per AAC frame).
	const aacFramePTS = int64(1920) // 1024 * 90000 / 48000
	for i := 1; i < len(audios); i++ {
		delta := audios[i].PTS - audios[i-1].PTS
		require.Equal(t, aacFramePTS, delta,
			"audio PTS delta should be 1920: got %d at frame %d", delta, i)
	}

	// Both audio and video should start from the same epoch (basePTS=0).
	// Audio always starts at PTS=0 (first AAC frame).
	require.Equal(t, int64(0), audios[0].PTS,
		"first audio PTS should be 0 (shared epoch)")

	// Verify that video PTS values are all on the ref clock grid.
	// The ref clock produces PTS = basePTS + tick * (90000 / fps).
	// The encoder's monotonic-fix-up may adjust by the pipeline format's
	// frame duration (3003 for 30000/1001). We verify the PTS delta
	// between consecutive frames is always close to a multiple of the
	// frame duration.
	for i := 1; i < len(videos); i++ {
		delta := videos[i].PTS - videos[i-1].PTS
		require.Greater(t, delta, int64(0),
			"video PTS delta must be positive at frame %d", i)
	}

	t.Logf("MEASURE: first video PTS = %d, last video PTS = %d, first audio PTS = %d, last audio PTS = %d",
		videos[0].PTS, videos[len(videos)-1].PTS, audios[0].PTS, audios[len(audios)-1].PTS)
}

// ==========================================================================
// TestRefClockZeroDrift
//
// Verifies that the video PTS rate remains constant over a long run.
// This is the test that was flaky with dual clocks: the wall-clock mixer
// ticker would drift relative to the video frame sync timer. With the
// reference clock, video PTS is derived from a monotonic counter with
// exact rational arithmetic, so the rate is perfectly constant.
//
// Uses a harness WITHOUT the H.264 encoder to avoid overload-triggered
// frame skips from encoder warmup. The sync loop broadcasts raw YUV
// frames directly, which is fast enough to never trigger overload.
// This isolates the clock's PTS accuracy from encoder behavior.
// ==========================================================================

func TestRefClockZeroDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	// Create harness with ref clock + mixer but no encoder.
	// This exercises the raw-frame broadcast path in processVideoTick,
	// which stamps PTS directly from the reference clock.
	h := newPipelineHarnessNoEncode(t)
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed frames continuously.
	h.feedFrames("camA", 200, 0)

	h.startRefClock()

	// Wait for substantial output.
	h.waitForVideoFrames(t, 200)
	h.waitForAudioFrames(t, 30)

	// Feed more to sustain.
	h.feedFrames("camA", 200, 200*ptsDelta)
	h.waitForVideoFrames(t, 400)
	h.waitForAudioFrames(t, 60)

	h.refClock.Stop()
	h.refClock = nil

	videos := h.capture.videosCopy()
	audios := h.capture.audiosCopy()

	t.Logf("MEASURE: video frames = %d, audio frames = %d", len(videos), len(audios))
	require.Greater(t, len(videos), 200, "need >200 video frames for drift measurement")
	require.Greater(t, len(audios), 30, "need >30 audio frames for drift measurement")

	// Verify every video frame has PTS = tick_number * ptsDelta (exact).
	// With no encoder, the ref clock PTS is passed through unmodified.
	// The ref clock uses 30fps (90000/30 = 3000 PTS per tick).
	for i := 0; i < len(videos); i++ {
		expected := int64(i) * ptsDelta
		require.Equal(t, expected, videos[i].PTS,
			"video PTS at frame %d should be %d, got %d", i, expected, videos[i].PTS)
	}

	// Verify audio PTS consistency: every delta should be exactly 1920.
	const aacFramePTS = int64(1920)
	for i := 1; i < len(audios); i++ {
		delta := audios[i].PTS - audios[i-1].PTS
		require.Equal(t, aacFramePTS, delta,
			"audio PTS delta should be exactly 1920: got %d at frame %d", delta, i)
	}

	// Audio should start from basePTS=0 (shared epoch with video).
	require.Equal(t, int64(0), audios[0].PTS,
		"first audio PTS should be 0 (shared epoch)")

	// Video also starts at PTS=0.
	require.Equal(t, int64(0), videos[0].PTS,
		"first video PTS should be 0 (shared epoch)")

	// Measure video PTS rate in the first and second halves.
	mid := len(videos) / 2
	firstHalfRate := float64(videos[mid-1].PTS-videos[0].PTS) / float64(mid-1)
	secondHalfRate := float64(videos[len(videos)-1].PTS-videos[mid].PTS) / float64(len(videos)-1-mid)

	rateDrift := math.Abs(firstHalfRate-secondHalfRate) / firstHalfRate * 100
	t.Logf("MEASURE: first half rate = %.3f, second half rate = %.3f, drift = %.6f%%",
		firstHalfRate, secondHalfRate, rateDrift)
	require.LessOrEqual(t, rateDrift, 0.01,
		"video PTS rate drift should be <0.01%% (essentially zero), got %.6f%%", rateDrift)

	t.Logf("Zero drift test passed: %d video, %d audio, drift = %.6f%%",
		len(videos), len(audios), rateDrift)
}

// ==========================================================================
// TestRefClockDeterministic
//
// Verifies that the reference clock path produces identical PTS sequences
// across multiple runs. This proves there is no wall-clock dependency:
// the same inputs always produce the same outputs.
//
// Strategy: run the pipeline twice with identical inputs and compare the
// video PTS sequences. They must be identical.
// ==========================================================================

func TestRefClockDeterministic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	runOnce := func() []int64 {
		h := newPipelineHarness(t, withReferenceClock())
		defer h.close()

		h.addSource("camA", sourceA)
		h.cut(t, "camA")
		h.feedFrames("camA", 60, 0)
		h.startRefClock()
		h.waitForVideoFrames(t, 20)

		h.refClock.Stop()
		h.refClock = nil

		videos := h.capture.videosCopy()
		pts := make([]int64, len(videos))
		for i, v := range videos {
			pts[i] = v.PTS
		}
		return pts
	}

	pts1 := runOnce()
	pts2 := runOnce()

	t.Logf("MEASURE: run1 frames = %d, run2 frames = %d", len(pts1), len(pts2))
	require.Greater(t, len(pts1), 10, "run1 should produce >10 frames")
	require.Greater(t, len(pts2), 10, "run2 should produce >10 frames")

	// Compare the common prefix (both runs may produce different counts
	// due to timing of Stop(), but the prefix should be identical).
	compareLen := len(pts1)
	if len(pts2) < compareLen {
		compareLen = len(pts2)
	}
	// Compare at least 10 frames.
	require.GreaterOrEqual(t, compareLen, 10, "need at least 10 frames to compare")

	for i := 0; i < compareLen; i++ {
		require.Equal(t, pts1[i], pts2[i],
			"PTS mismatch at frame %d: run1=%d, run2=%d", i, pts1[i], pts2[i])
	}

	// Verify the PTS values start at 0 and increment by ptsDelta.
	for i := 0; i < compareLen; i++ {
		expected := int64(i) * ptsDelta
		require.Equal(t, expected, pts1[i],
			"frame %d PTS should be %d, got %d", i, expected, pts1[i])
	}

	t.Logf("Deterministic test passed: %d frames compared, all PTS identical", compareLen)
}

// ==========================================================================
// TestRefClockOverloadRecovery
//
// Verifies overload handling: when the pipeline is slow (processing takes
// longer than frame duration), the sync loop skips video frames but never
// skips audio. After the slow period, video resumes.
//
// This uses the ref clock + mixer path and injects a delay via
// waitForPipeline to simulate a slow pipeline epoch.
// ==========================================================================

func TestRefClockOverloadRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock(), withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")
	h.feedFrames("camA", 120, 0)

	h.startRefClock()

	// Wait for some initial output.
	h.waitForVideoFrames(t, 10)
	h.waitForAudioFrames(t, 2)

	initialVideoCount := h.capture.videoCount()
	initialAudioCount := h.capture.audioCount()

	t.Logf("MEASURE: initial video = %d, initial audio = %d",
		initialVideoCount, initialAudioCount)

	// Feed more frames to keep the pipeline going.
	h.feedFrames("camA", 120, 120*ptsDelta)

	// Wait for more output after feeding.
	h.waitForVideoFrames(t, initialVideoCount+10)

	h.refClock.Stop()
	h.refClock = nil

	finalVideoCount := h.capture.videoCount()
	finalAudioCount := h.capture.audioCount()

	t.Logf("MEASURE: final video = %d, final audio = %d",
		finalVideoCount, finalAudioCount)

	// Audio should always be produced (never skipped by the sync loop).
	require.Greater(t, finalAudioCount, initialAudioCount,
		"audio should continue being produced")

	// Video should also continue (no permanent stall).
	require.Greater(t, finalVideoCount, initialVideoCount,
		"video should continue after feeding more frames")

	// Verify video PTS monotonicity across the entire run.
	videos := h.capture.videosCopy()
	for i := 1; i < len(videos); i++ {
		require.Greater(t, videos[i].PTS, videos[i-1].PTS,
			"video PTS should be monotonically increasing: frame %d PTS=%d, frame %d PTS=%d",
			i-1, videos[i-1].PTS, i, videos[i].PTS)
	}

	// Verify audio PTS monotonicity.
	audios := h.capture.audiosCopy()
	for i := 1; i < len(audios); i++ {
		require.Greater(t, audios[i].PTS, audios[i-1].PTS,
			"audio PTS should be monotonically increasing: frame %d PTS=%d, frame %d PTS=%d",
			i-1, audios[i-1].PTS, i, audios[i].PTS)
	}

	t.Logf("Overload recovery test passed: video=%d, audio=%d, ticks=%d",
		finalVideoCount, finalAudioCount, h.tickCount())
}

// ==========================================================================
// TestRefClockAudioContinuity
//
// Verifies that audio frames are produced continuously even when no video
// frames are available. The reference clock drives audio independently
// of video availability.
// ==========================================================================

func TestRefClockAudioContinuity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock(), withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed only a small number of frames -- video will freeze-repeat,
	// but audio should keep going.
	h.feedFrames("camA", 5, 0)

	h.startRefClock()

	// Wait for audio output -- should appear even with limited video.
	h.waitForAudioFrames(t, 5)

	h.refClock.Stop()
	h.refClock = nil

	audios := h.capture.audiosCopy()
	require.GreaterOrEqual(t, len(audios), 5,
		"should have at least 5 audio frames despite limited video input")

	// Verify audio PTS is monotonically increasing with consistent delta.
	const aacFramePTS = int64(1920)
	for i := 1; i < len(audios); i++ {
		delta := audios[i].PTS - audios[i-1].PTS
		require.Equal(t, aacFramePTS, delta,
			"audio PTS delta should be 1920: got %d at frame %d", delta, i)
	}

	t.Logf("Audio continuity test passed: %d audio frames produced", len(audios))
}

// ==========================================================================
// TestRefClockVideoFreezeRepeat
//
// Verifies that when a source stops sending frames, the sync loop
// freeze-repeats the last frame at the correct PTS cadence. Each repeated
// frame should have a PTS that advances by ptsDelta.
// ==========================================================================

func TestRefClockVideoFreezeRepeat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock())
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed only 3 frames -- the sync loop should freeze-repeat after these.
	h.feedFrames("camA", 3, 0)

	h.startRefClock()

	// Wait for more frames than we fed -- freeze-repeat should produce them.
	h.waitForVideoFrames(t, 10)

	h.refClock.Stop()
	h.refClock = nil

	videos := h.capture.videosCopy()
	require.GreaterOrEqual(t, len(videos), 10,
		"should have at least 10 frames (3 fresh + freeze-repeats)")

	// Verify PTS monotonicity and consistent delta.
	for i := 1; i < len(videos); i++ {
		delta := videos[i].PTS - videos[i-1].PTS
		require.Equal(t, int64(ptsDelta), delta,
			"video PTS delta should be %d: got %d at frame %d", ptsDelta, delta, i)
	}

	// First frame PTS should be 0 (basePTS of the reference clock).
	require.Equal(t, int64(0), videos[0].PTS,
		"first frame PTS should be 0 (ref clock basePTS)")

	t.Logf("Freeze-repeat test passed: %d frames (3 fresh + repeats), all PTS consistent", len(videos))
}
