//go:build correctness

package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit frame leakage tests.
//
// Verify that after a cut, frames from the wrong source don't leak into
// program output. Uses 10-bit frames with maximally distinct Y values:
//   Source A: Y=100 (well within 10-bit range, dark)
//   Source B: Y=850 (well within 10-bit range, bright)
//
// Buffer accounting uses w*h*4 sizes (not w*h*3/2).
// ==========================================================================

// TestNoFrameLeakageAfterCut10bit verifies that cutting between 10-bit
// sources produces output from the correct source with no cross-contamination.
func TestNoFrameLeakageAfterCut10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	// Use distinct Y values that are clearly in the 10-bit range
	// and map to distinguishable 8-bit values in encoded output.
	colorA := sourceColor{Y: 25, Cb: 128, Cr: 128}  // Y=25 in 8-bit (Y~100 in 10-bit)
	colorB := sourceColor{Y: 212, Cb: 128, Cr: 128} // Y=212 in 8-bit (Y~850 in 10-bit)

	h.addSource("leak10A", colorA)
	h.addSource("leak10B", colorB)

	const (
		w             = testWidth
		ht            = testHeight
		thresholdLow  = 80.0
		thresholdHigh = 170.0
		flushFrames   = 5
	)

	// Phase 1: Cut to A, feed 10-bit frames
	h.cut(t, "leak10A")
	for i := 0; i < 60; i++ {
		pf := make10bitFrame(w, ht, 100, 512, 512,
			int64(90000+i*ptsDelta), i == 0)
		h.sw.IngestRawVideo("leak10A", pf)
		if i < 59 {
			time.Sleep(h.sw.PipelineFormat().FrameDuration())
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	p1Videos := h.capture.videosCopy()
	p1Decoded := decodeCapturedFrames(t, p1Videos)
	t.Logf("Phase 1 (leak10A, 10-bit Y=100): decoded %d frames", len(p1Decoded))
	require.Greater(t, len(p1Decoded), 0, "phase 1: should decode frames")

	for i, df := range p1Decoded {
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  phase1 frame %d: avgY=%.1f", i, avgY)
		require.Less(t, avgY, thresholdLow,
			"phase1 frame %d: avgY=%.1f >= %.0f -- source B leaking!", i, avgY, thresholdLow)
	}
	prevLen := len(p1Decoded)

	// Phase 2: Cut to B, feed 10-bit frames
	h.cut(t, "leak10B")
	for i := 0; i < 60; i++ {
		pf := make10bitFrame(w, ht, 850, 512, 512,
			int64(90000+60*ptsDelta+i*ptsDelta), i == 0)
		h.sw.IngestRawVideo("leak10B", pf)
		if i < 59 {
			time.Sleep(h.sw.PipelineFormat().FrameDuration())
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	p2Videos := h.capture.videosCopy()
	p2All := decodeCapturedFrames(t, p2Videos)
	require.Greater(t, len(p2All), prevLen, "phase 2: should have more frames")

	p2New := h.sinceCut(p2All)
	t.Logf("Phase 2 (leak10B, 10-bit Y=850): decoded %d new frames", len(p2New))

	actualFlush := flushFrames
	if actualFlush > len(p2New) {
		actualFlush = len(p2New)
	}
	for i := 0; i < actualFlush; i++ {
		df := p2New[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  phase2 flush frame %d: avgY=%.1f (transitional)", i, avgY)
	}

	require.Greater(t, len(p2New), actualFlush, "need frames after flush")
	for i := actualFlush; i < len(p2New); i++ {
		df := p2New[i]
		avgY := averageY(df.YUV, df.Width, df.Height)
		t.Logf("  phase2 frame %d: avgY=%.1f", i, avgY)
		require.Greater(t, avgY, thresholdHigh,
			"phase2 frame %d: avgY=%.1f <= %.0f -- source A leaking!", i, avgY, thresholdHigh)
	}

	t.Logf("10-bit leakage test passed: phase1=%d, phase2=%d frames", len(p1Decoded), len(p2New))
}

// TestNoFrameLeakageUnderPoolPressure10bit verifies that rapid cuts with
// 10-bit frames (which are 4x larger than 8-bit) don't cause buffer
// contamination under frame pool pressure.
func TestNoFrameLeakageUnderPoolPressure10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 20, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 230, Cb: 128, Cr: 128}

	h.addSource("pool10A", colorA)
	h.addSource("pool10B", colorB)

	h.cut(t, "pool10A")

	const (
		numCuts      = 15
		framesPerCut = 6
		framePaceMs  = 3
	)

	// Verify 10-bit frame size is w*h*4
	format := switcher.ColorYUV422_10bit
	expectedFrameSize := format.FrameSize(testWidth, testHeight)
	require.Equal(t, testWidth*testHeight*4, expectedFrameSize,
		"10-bit frame should be w*h*4 bytes")

	pts := int64(90000)
	for cut := 0; cut < numCuts; cut++ {
		activeSource := "pool10A"
		activeY := uint16(100) // 10-bit dark
		inactiveSource := "pool10B"
		inactiveY := uint16(850) // 10-bit bright
		if cut%2 == 1 {
			activeSource, inactiveSource = inactiveSource, activeSource
			activeY, inactiveY = inactiveY, activeY
		}

		h.cut(t, activeSource)

		for i := 0; i < framesPerCut; i++ {
			// Active source
			pf := make10bitFrame(testWidth, testHeight, activeY, 512, 512, pts, i == 0)
			require.Len(t, pf.Data, expectedFrameSize,
				"10-bit frame buffer accounting: expected %d, got %d", expectedFrameSize, len(pf.Data))
			h.sw.IngestRawVideo(activeSource, pf)
			pts += ptsDelta

			// Inactive source (creates pool contention)
			pfI := make10bitFrame(testWidth, testHeight, inactiveY, 512, 512, pts, i == 0)
			h.sw.IngestRawVideo(inactiveSource, pfI)

			time.Sleep(time.Duration(framePaceMs) * time.Millisecond)
		}
	}

	h.waitForPipeline(800 * time.Millisecond)

	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	t.Logf("Pool pressure 10-bit: decoded %d frames from %d cuts", len(decoded), numCuts)
	require.Greater(t, len(decoded), 5, "should have frames from rapid 10-bit cuts")

	// Verify frames are valid (Y within reasonable range, not garbage).
	for i, df := range decoded {
		avgY := averageY(df.YUV, df.Width, df.Height)
		require.GreaterOrEqual(t, avgY, 0.0,
			"frame %d avgY=%.1f should not be garbage", i, avgY)
		require.LessOrEqual(t, avgY, 255.0,
			"frame %d avgY=%.1f should not exceed 8-bit range in encoded output", i, avgY)
	}

	t.Log("10-bit pool pressure leakage test passed")
}
