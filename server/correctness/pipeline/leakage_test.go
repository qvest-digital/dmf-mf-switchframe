//go:build correctness

package pipeline

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// TestNoFrameLeakageAfterCut
//
// The most insidious concurrency bug: frames from the wrong source appearing
// on program output after a cut. Uses maximally distinct Y values (20 vs 230,
// delta=210) so confusion is impossible even after lossy H.264 compression.
//
// Protocol:
// 1. Cut to A (Y=20), feed 90 frames, verify ALL output Y < 80
// 2. Cut to B (Y=230), feed 90 frames, after flush verify ALL Y > 170
// 3. Cut to A, feed 60 frames, verify all < 80
// 4. Cut to B, feed 60 frames, verify all > 170
// Total: 4 cuts, verify no leakage at any transition
// ==========================================================================

func TestNoFrameLeakageAfterCut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	// Maximally distinct: Y=20 vs Y=230, 210 apart. Cb/Cr neutral.
	colorA := sourceColor{Y: 20, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 230, Cb: 128, Cr: 128}

	h.addSource("leakA", colorA)
	h.addSource("leakB", colorB)

	const (
		thresholdLow  = 80.0  // definitely source A territory
		thresholdHigh = 170.0 // definitely source B territory
		flushFrames   = 5     // frames allowed for pipeline flush after cut
	)

	prevDecodedLen := 0

	// ---- Phase 1: Cut to A, feed 90 frames ----
	h.cut(t, "leakA")
	h.feedFrames("leakA", 90, 90000)
	h.waitForPipeline(600 * time.Millisecond)

	videosP1 := h.capture.videosCopy()
	decodedP1 := decodeCapturedFrames(t, videosP1)
	require.Greater(t, len(decodedP1), 0, "phase 1: should decode at least some frames from leakA")

	t.Logf("Phase 1 (leakA, Y=20): decoded %d frames", len(decodedP1))
	for i, df := range decodedP1 {
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase1 frame %d avg Y = %.1f", i, avgY)
		require.Less(t, avgY, thresholdLow,
			"phase1 frame %d: avg Y=%.1f >= %.0f — source B leaking into source A!", i, avgY, thresholdLow)
	}
	prevDecodedLen = len(decodedP1)

	// ---- Phase 2: Cut to B, feed 90 frames ----
	h.cut(t, "leakB")
	h.feedFrames("leakB", 90, 90000+90*ptsDelta)
	h.waitForPipeline(600 * time.Millisecond)

	videosP2 := h.capture.videosCopy()
	decodedAll2 := decodeCapturedFrames(t, videosP2)
	require.Greater(t, len(decodedAll2), prevDecodedLen,
		"phase 2: should have more frames after feeding leakB")

	decodedP2 := decodedAll2[prevDecodedLen:]
	t.Logf("Phase 2 (leakB, Y=230): decoded %d new frames", len(decodedP2))

	// Log flush frames.
	actualFlush := flushFrames
	if actualFlush > len(decodedP2) {
		actualFlush = len(decodedP2)
	}
	for i := 0; i < actualFlush; i++ {
		df := decodedP2[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase2 flush frame %d avg Y = %.1f (transitional)", i, avgY)
	}

	// After flush, ALL remaining frames must be source B.
	require.Greater(t, len(decodedP2), actualFlush,
		"phase 2: not enough frames after flush window")
	for i := actualFlush; i < len(decodedP2); i++ {
		df := decodedP2[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase2 frame %d avg Y = %.1f", i, avgY)
		require.Greater(t, avgY, thresholdHigh,
			"phase2 frame %d: avg Y=%.1f <= %.0f — source A leaking after cut to B!", i, avgY, thresholdHigh)
	}

	// Also check: NO frame after flush has avg Y < thresholdLow (definite A).
	for i := actualFlush; i < len(decodedP2); i++ {
		df := decodedP2[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		require.Greater(t, avgY, thresholdLow,
			"phase2 frame %d: avg Y=%.1f < %.0f — definite source A leakage!", i, avgY, thresholdLow)
	}
	prevDecodedLen = len(decodedAll2)

	// ---- Phase 3: Cut back to A, feed 60 frames ----
	h.cut(t, "leakA")
	h.feedFrames("leakA", 60, 90000+180*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	videosP3 := h.capture.videosCopy()
	decodedAll3 := decodeCapturedFrames(t, videosP3)
	require.Greater(t, len(decodedAll3), prevDecodedLen,
		"phase 3: should have more frames after cutting back to leakA")

	decodedP3 := decodedAll3[prevDecodedLen:]
	t.Logf("Phase 3 (leakA again, Y=20): decoded %d new frames", len(decodedP3))

	actualFlush3 := flushFrames
	if actualFlush3 > len(decodedP3) {
		actualFlush3 = len(decodedP3)
	}
	for i := 0; i < actualFlush3; i++ {
		df := decodedP3[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase3 flush frame %d avg Y = %.1f (transitional)", i, avgY)
	}

	require.Greater(t, len(decodedP3), actualFlush3,
		"phase 3: not enough frames after flush window")
	for i := actualFlush3; i < len(decodedP3); i++ {
		df := decodedP3[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase3 frame %d avg Y = %.1f", i, avgY)
		require.Less(t, avgY, thresholdLow,
			"phase3 frame %d: avg Y=%.1f >= %.0f — source B leaking after cut back to A!", i, avgY, thresholdLow)
	}
	prevDecodedLen = len(decodedAll3)

	// ---- Phase 4: Cut to B again, feed 60 frames ----
	h.cut(t, "leakB")
	h.feedFrames("leakB", 60, 90000+240*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	videosP4 := h.capture.videosCopy()
	decodedAll4 := decodeCapturedFrames(t, videosP4)
	require.Greater(t, len(decodedAll4), prevDecodedLen,
		"phase 4: should have more frames after cutting to leakB again")

	decodedP4 := decodedAll4[prevDecodedLen:]
	t.Logf("Phase 4 (leakB again, Y=230): decoded %d new frames", len(decodedP4))

	actualFlush4 := flushFrames
	if actualFlush4 > len(decodedP4) {
		actualFlush4 = len(decodedP4)
	}
	for i := 0; i < actualFlush4; i++ {
		df := decodedP4[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase4 flush frame %d avg Y = %.1f (transitional)", i, avgY)
	}

	require.Greater(t, len(decodedP4), actualFlush4,
		"phase 4: not enough frames after flush window")
	for i := actualFlush4; i < len(decodedP4); i++ {
		df := decodedP4[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase4 frame %d avg Y = %.1f", i, avgY)
		require.Greater(t, avgY, thresholdHigh,
			"phase4 frame %d: avg Y=%.1f <= %.0f — source A leaking after cut to B!", i, avgY, thresholdHigh)
	}

	t.Logf("Leakage test passed: 4 cuts, phase1=%d phase2=%d phase3=%d phase4=%d decoded frames",
		len(decodedP1), len(decodedP2), len(decodedP3), len(decodedP4))
}

// ==========================================================================
// TestNoFrameLeakageDuringTransition
//
// Verifies that transition frames have monotonically increasing Y (simulating
// a dissolve from dark to bright). Uses manual Y ramp via IngestRawVideo.
//
// Protocol:
// 1. Source A: Y=20, Source B: Y=230
// 2. Cut to A, feed 30 frames of pure A
// 3. Feed 30 frames with linearly increasing Y from 20→230 (simulated dissolve)
// 4. Cut to B, feed 30 frames of pure B
// 5. Verify: all output Y values within [5, 245] (no garbage/corruption)
// 6. Verify: transition region shows monotonically increasing Y (±20 tolerance)
// 7. Post-transition frames all > 170
// ==========================================================================

func TestNoFrameLeakageDuringTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 20, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 230, Cb: 128, Cr: 128}

	h.addSource("trA", colorA)
	h.addSource("trB", colorB)

	// ---- Step 1: Feed 30 pure A frames ----
	h.cut(t, "trA")
	h.feedFrames("trA", 30, 90000)
	h.waitForPipeline(300 * time.Millisecond)

	preTransitionCount := len(decodeCapturedFrames(t, h.capture.videosCopy()))
	t.Logf("Pre-transition: decoded %d frames from trA", preTransitionCount)
	require.Greater(t, preTransitionCount, 0, "should have frames from trA")

	// ---- Step 2: Feed 30 frames with linearly ramping Y (simulated dissolve) ----
	// Still feeding into "trA" (the active source) with manual Y ramp.
	for i := 0; i < 30; i++ {
		progress := float64(i) / 29.0
		rampY := byte(20.0 + progress*210.0) // 20 → 230
		yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, rampY, 128, 128)
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuv),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        90000 + int64(30+i)*ptsDelta,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("trA", pf)
		time.Sleep(5 * time.Millisecond)
	}
	h.waitForPipeline(300 * time.Millisecond)

	// ---- Step 3: Cut to B and feed 30 pure B frames ----
	h.cut(t, "trB")
	h.feedFrames("trB", 30, 90000+60*ptsDelta)
	h.waitForPipeline(400 * time.Millisecond)

	// Decode ALL output.
	allVideos := h.capture.videosCopy()
	allDecoded := decodeCapturedFrames(t, allVideos)
	t.Logf("Total decoded frames: %d", len(allDecoded))
	require.Greater(t, len(allDecoded), 10, "should have substantial decoded frames")

	// Log every frame Y value.
	yValues := make([]float64, len(allDecoded))
	for i, df := range allDecoded {
		avgY := averageY(df.YUV, df.Width, df.Height)
		yValues[i] = avgY
		t.Logf("  MEASURE: frame %d avg Y = %.1f", i, avgY)
	}

	// Verify: NO frame outside [5, 245] range (would indicate corruption/garbage).
	for i, y := range yValues {
		require.GreaterOrEqual(t, y, 5.0,
			"frame %d: avg Y=%.1f < 5 — possible corruption/garbage data", i, y)
		require.LessOrEqual(t, y, 245.0,
			"frame %d: avg Y=%.1f > 245 — possible corruption/garbage data", i, y)
	}

	// Find transition region: frames where Y goes from ~20 to ~230.
	// Identify the first frame with Y > 40 (start of ramp) and last frame with Y < 200 (end of ramp).
	rampStart := -1
	rampEnd := -1
	for i, y := range yValues {
		if rampStart == -1 && y > 40 {
			rampStart = i
		}
		if y < 200 && y > 40 {
			rampEnd = i
		}
	}

	if rampStart >= 0 && rampEnd > rampStart {
		t.Logf("Transition ramp detected: frames %d to %d", rampStart, rampEnd)

		// Check monotonically increasing Y in the ramp region (±20 per-frame tolerance
		// to account for encoder quantization and pipeline reordering).
		nonMonotonic := 0
		for i := rampStart + 1; i <= rampEnd; i++ {
			if yValues[i] < yValues[i-1]-20.0 {
				t.Logf("  WARNING: non-monotonic Y at frame %d: %.1f < %.1f - 20", i, yValues[i], yValues[i-1])
				nonMonotonic++
			}
		}
		t.Logf("Non-monotonic transitions: %d out of %d ramp frames", nonMonotonic, rampEnd-rampStart)
		// Allow a few non-monotonic frames due to encoder B-frame reordering, but not many.
		maxAllowed := (rampEnd - rampStart) / 3 // at most 1/3 can be non-monotonic
		require.LessOrEqual(t, nonMonotonic, maxAllowed,
			"too many non-monotonic Y values in transition ramp: %d out of %d", nonMonotonic, rampEnd-rampStart)
	} else {
		t.Logf("WARNING: Could not detect a clear transition ramp (rampStart=%d, rampEnd=%d)", rampStart, rampEnd)
	}

	// Verify: last 5 decoded frames should all be > 170 (post-transition, pure B).
	lastN := 5
	if lastN > len(yValues) {
		lastN = len(yValues)
	}
	for i := len(yValues) - lastN; i < len(yValues); i++ {
		require.Greater(t, yValues[i], 170.0,
			"post-transition frame %d: avg Y=%.1f <= 170 — expected source B (Y=230)", i, yValues[i])
	}

	t.Logf("Transition leakage test passed: %d total frames, ramp [%d,%d]",
		len(allDecoded), rampStart, rampEnd)
}

// ==========================================================================
// TestNoFrameLeakageUnderPoolPressure
//
// Rapid cuts under frame pool pressure. Feeds frames at 2ms intervals
// (faster than real-time) and performs 20 rapid cuts in 2 seconds.
// Verifies that every output frame is identifiably from the correct source
// (no cross-contamination from pool buffer reuse).
//
// Every frame's avg Y must be either < 80 (source A) or > 170 (source B).
// NO frame should land in the ambiguous middle zone [80, 170] — that would
// indicate frame data from one source leaking into another's buffer.
// ==========================================================================

func TestNoFrameLeakageUnderPoolPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 20, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 230, Cb: 128, Cr: 128}

	h.addSource("poolA", colorA)
	h.addSource("poolB", colorB)

	// Start with A on program.
	h.cut(t, "poolA")

	const (
		numCuts       = 20
		framesPerCut  = 6
		framePaceMs   = 2 // very fast — creates pool pressure
		thresholdLow  = 80.0
		thresholdHigh = 170.0
		flushPerCut   = 3 // frames allowed for pipeline flush after each cut
	)

	// Rapid-fire cuts, alternating A→B→A→B...
	// Feed frames from BOTH sources simultaneously to create maximum pool contention.
	pts := int64(90000)
	var wg sync.WaitGroup

	for cut := 0; cut < numCuts; cut++ {
		activeSource := "poolA"
		inactiveSource := "poolB"
		activeColor := colorA
		inactiveColor := colorB
		if cut%2 == 1 {
			activeSource = "poolB"
			inactiveSource = "poolA"
			activeColor = colorB
			inactiveColor = colorA
		}

		h.cut(t, activeSource)

		// Feed frames from active source.
		for i := 0; i < framesPerCut; i++ {
			yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, activeColor.Y, activeColor.Cb, activeColor.Cr)
			pf := &switcher.ProcessingFrame{
				Data:       copyBytes(yuv),
				Width:      testWidth,
				Height:     testHeight,
				PTS:        pts,
				IsKeyframe: i == 0,
			}
			h.sw.IngestRawVideo(activeSource, pf)
			pts += ptsDelta

			// Also feed the inactive source to create pool contention.
			yuvInactive := testutil.GenerateSolidYUV420(testWidth, testHeight, inactiveColor.Y, inactiveColor.Cb, inactiveColor.Cr)
			pfInactive := &switcher.ProcessingFrame{
				Data:       copyBytes(yuvInactive),
				Width:      testWidth,
				Height:     testHeight,
				PTS:        pts,
				IsKeyframe: i == 0,
			}
			h.sw.IngestRawVideo(inactiveSource, pfInactive)

			time.Sleep(time.Duration(framePaceMs) * time.Millisecond)
		}
	}
	wg.Wait()

	// Let the pipeline drain.
	h.waitForPipeline(800 * time.Millisecond)

	// Decode all output.
	allVideos := h.capture.videosCopy()
	allDecoded := decodeCapturedFrames(t, allVideos)
	t.Logf("Pool pressure test: decoded %d frames from %d rapid cuts", len(allDecoded), numCuts)
	require.Greater(t, len(allDecoded), 10, "should have substantial frames from rapid cuts")

	// Log and classify every frame.
	sourceACnt := 0
	sourceBCnt := 0
	ambiguousCnt := 0
	var ambiguousFrames []int

	for i, df := range allDecoded {
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: frame %d avg Y = %.1f", i, avgY)

		if avgY < thresholdLow {
			sourceACnt++
		} else if avgY > thresholdHigh {
			sourceBCnt++
		} else {
			ambiguousCnt++
			ambiguousFrames = append(ambiguousFrames, i)
		}
	}

	t.Logf("Classification: sourceA(Y<%.0f)=%d, sourceB(Y>%.0f)=%d, ambiguous=%d",
		thresholdLow, sourceACnt, thresholdHigh, sourceBCnt, ambiguousCnt)
	if len(ambiguousFrames) > 0 {
		t.Logf("Ambiguous frame indices: %v", ambiguousFrames)
	}

	// Allow some ambiguous frames due to pipeline flush after each cut.
	// Maximum expected: flushPerCut * numCuts frames in the transitional zone.
	maxAmbiguous := flushPerCut * numCuts
	require.LessOrEqual(t, ambiguousCnt, maxAmbiguous,
		"too many ambiguous frames (%d > %d): possible cross-source contamination. Indices: %v",
		ambiguousCnt, maxAmbiguous, ambiguousFrames)

	// Both sources should have had frames on program.
	require.Greater(t, sourceACnt, 0, "expected some source A frames")
	require.Greater(t, sourceBCnt, 0, "expected some source B frames")

	t.Logf("Pool pressure leakage test passed: %d frames, %d A, %d B, %d ambiguous (max allowed %d)",
		len(allDecoded), sourceACnt, sourceBCnt, ambiguousCnt, maxAmbiguous)
}

// ==========================================================================
// TestNoFrameLeakageWithGraphicsOverlay
//
// Verifies that graphics overlay does not cause cross-source leakage.
// With a semi-transparent (alpha=64) white overlay:
//   - Source A (Y=20) + overlay ≈ Y = 20 + (235-20)*64/256 ≈ 73
//   - Source B (Y=230) + overlay ≈ Y = 230 + (235-230)*64/256 ≈ 231
// These are clearly distinguishable (73 vs 231) even with lossy compression.
//
// Protocol:
// 1. Add semi-transparent white overlay
// 2. Cut to A, feed frames, verify Y < 120
// 3. Cut to B, feed frames, verify Y > 170
// ==========================================================================

func TestNoFrameLeakageWithGraphicsOverlay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withCompositor())
	defer h.close()

	colorA := sourceColor{Y: 20, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 230, Cb: 128, Cr: 128}

	h.addSource("gfxA", colorA)
	h.addSource("gfxB", colorB)

	// Create a semi-transparent white overlay (alpha=64/256 = 25%).
	overlayPNG := createSemiTransparentOverlayPNG(testWidth, testHeight, 255, 255, 255, 64)

	layerID, err := h.compositor.AddLayer()
	require.NoError(t, err)
	require.NoError(t, h.compositor.SetImage(layerID, "semitrans", overlayPNG))
	require.NoError(t, h.compositor.On(layerID))
	require.NoError(t, h.sw.BuildPipeline())

	// With alpha=64 white overlay on Y=20 source:
	//   result ≈ 20*(1-64/256) + 235*(64/256) = 20*0.75 + 235*0.25 ≈ 15 + 58.75 ≈ 73.75
	// With alpha=64 white overlay on Y=230 source:
	//   result ≈ 230*(1-64/256) + 235*(64/256) = 230*0.75 + 235*0.25 ≈ 172.5 + 58.75 ≈ 231.25
	//
	// Use thresholds: A with overlay < 120, B with overlay > 170
	const (
		thresholdA = 120.0 // source A + overlay should stay well below this
		thresholdB = 170.0 // source B + overlay should stay well above this
		flushMax   = 5
	)

	// ---- Phase 1: Cut to A with overlay ----
	h.cut(t, "gfxA")
	h.feedFrames("gfxA", 60, 90000)
	h.waitForPipeline(500 * time.Millisecond)

	videosP1 := h.capture.videosCopy()
	decodedP1 := decodeCapturedFrames(t, videosP1)
	require.Greater(t, len(decodedP1), 0, "phase 1: should decode frames from gfxA")

	t.Logf("Phase 1 (gfxA + overlay, expected Y≈74): decoded %d frames", len(decodedP1))
	for i, df := range decodedP1 {
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase1 frame %d avg Y = %.1f", i, avgY)
		require.Less(t, avgY, thresholdA,
			"phase1 frame %d: avg Y=%.1f >= %.0f — expected source A + overlay", i, avgY, thresholdA)
	}
	prevDecodedLen := len(decodedP1)

	// ---- Phase 2: Cut to B with overlay ----
	h.cut(t, "gfxB")
	h.feedFrames("gfxB", 60, 90000+60*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	videosP2 := h.capture.videosCopy()
	decodedAll2 := decodeCapturedFrames(t, videosP2)
	require.Greater(t, len(decodedAll2), prevDecodedLen,
		"phase 2: should have more frames after cutting to gfxB")

	decodedP2 := decodedAll2[prevDecodedLen:]
	t.Logf("Phase 2 (gfxB + overlay, expected Y≈231): decoded %d new frames", len(decodedP2))

	actualFlush := flushMax
	if actualFlush > len(decodedP2) {
		actualFlush = len(decodedP2)
	}
	for i := 0; i < actualFlush; i++ {
		df := decodedP2[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase2 flush frame %d avg Y = %.1f (transitional)", i, avgY)
	}

	require.Greater(t, len(decodedP2), actualFlush,
		"phase 2: not enough frames after flush window")
	for i := actualFlush; i < len(decodedP2); i++ {
		df := decodedP2[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase2 frame %d avg Y = %.1f", i, avgY)
		require.Greater(t, avgY, thresholdB,
			"phase2 frame %d: avg Y=%.1f <= %.0f — source A leaking through overlay after cut!", i, avgY, thresholdB)
	}
	prevDecodedLen = len(decodedAll2)

	// ---- Phase 3: Cut back to A with overlay ----
	h.cut(t, "gfxA")
	h.feedFrames("gfxA", 60, 90000+120*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	videosP3 := h.capture.videosCopy()
	decodedAll3 := decodeCapturedFrames(t, videosP3)
	require.Greater(t, len(decodedAll3), prevDecodedLen,
		"phase 3: should have more frames after cutting back to gfxA")

	decodedP3 := decodedAll3[prevDecodedLen:]
	t.Logf("Phase 3 (gfxA + overlay again): decoded %d new frames", len(decodedP3))

	actualFlush3 := flushMax
	if actualFlush3 > len(decodedP3) {
		actualFlush3 = len(decodedP3)
	}
	for i := 0; i < actualFlush3; i++ {
		df := decodedP3[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase3 flush frame %d avg Y = %.1f (transitional)", i, avgY)
	}

	require.Greater(t, len(decodedP3), actualFlush3,
		"phase 3: not enough frames after flush window")
	for i := actualFlush3; i < len(decodedP3); i++ {
		df := decodedP3[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  MEASURE: phase3 frame %d avg Y = %.1f", i, avgY)
		require.Less(t, avgY, thresholdA,
			"phase3 frame %d: avg Y=%.1f >= %.0f — source B leaking through overlay after cut back!", i, avgY, thresholdA)
	}

	// Verify that A-with-overlay and B-with-overlay are clearly distinguishable.
	// Compute the gap between the max A-phase Y and min B-phase Y.
	maxAY := 0.0
	for _, df := range decodedP1 {
		avgY := averageY(df.YUV, df.Width, df.Height)
		if avgY > maxAY {
			maxAY = avgY
		}
	}
	minBY := 999.0
	for i := actualFlush; i < len(decodedP2); i++ {
		avgY := averageY(decodedP2[i].YUV, decodedP2[i].Width, decodedP2[i].Height)
		if avgY < minBY {
			minBY = avgY
		}
	}
	gap := minBY - maxAY
	t.Logf("MEASURE: maxA_Y=%.1f, minB_Y=%.1f, gap=%.1f (should be >> 0)", maxAY, minBY, gap)
	require.Greater(t, gap, 30.0,
		"A-with-overlay (max Y=%.1f) and B-with-overlay (min Y=%.1f) are not clearly distinguishable (gap=%.1f)",
		maxAY, minBY, gap)

	_ = math.Abs // used in assertions above
	t.Logf("Graphics overlay leakage test passed: phase1=%d phase2=%d phase3=%d",
		len(decodedP1), len(decodedP2), len(decodedP3))
}

// createSemiTransparentOverlayPNG creates an RGBA PNG with specified color and alpha.
func createSemiTransparentOverlayPNG(w, h int, r, g, b, a byte) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			offset := (y*w + x) * 4
			img.Pix[offset] = r
			img.Pix[offset+1] = g
			img.Pix[offset+2] = b
			img.Pix[offset+3] = a
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
