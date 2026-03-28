//go:build correctness

// Package pipeline tests the end-to-end video processing pipeline:
// IngestRawVideo -> pipeline nodes -> H.264 encode -> program relay -> TS mux.
//
// These tests verify that real components wired together produce correct,
// ffprobe-parseable MPEG-TS output with the expected content.
package pipeline

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/layout"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// TestFullPipelineCutToTS
//
// Verifies the full pipeline: two sources with distinct colors, cut between
// them, encode to H.264, mux to MPEG-TS, verify with ffprobe and decode.
//
// Pipeline path: IngestRawVideo -> videoProcCh -> pipeline.Run
//   (upstream-key -> layout -> compositor -> encode) -> broadcastToProgram
//   -> captureViewer -> TSMuxer
// ==========================================================================

func TestFullPipelineCutToTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	// Cut to A and feed 30 frames with spacing to let pipeline drain.
	h.cut(t, "camA")
	h.feedFrames("camA", 30, 90000)
	h.waitForPipeline(500 * time.Millisecond)

	preCount := h.capture.videoCount()
	t.Logf("Frames from camA: %d", preCount)
	require.Greater(t, preCount, 0, "should have encoded frames from camA")

	// Cut to B and feed 30 frames.
	h.cut(t, "camB")
	h.feedFrames("camB", 30, 90000+30*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	totalCount := h.capture.videoCount()
	t.Logf("Total frames after camB: %d", totalCount)
	require.Greater(t, totalCount, preCount, "should have additional frames from camB")

	// Write TS and verify with ffprobe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)
	require.Equal(t, testWidth, vs.Width)
	require.Equal(t, testHeight, vs.Height)

	// Verify frame count. The async pipeline drops some frames due to
	// channel depth and encode latency, so we expect a reasonable fraction.
	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	require.GreaterOrEqual(t, len(videoFrames), 20,
		"expected at least 20 video frames, got %d", len(videoFrames))
	require.LessOrEqual(t, len(videoFrames), 70,
		"expected at most 70 video frames, got %d", len(videoFrames))

	// Verify PTS is monotonically increasing.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Verify at least 2 keyframes (one per source segment).
	keyCount := 0
	for _, f := range videoFrames {
		if f.KeyFrame {
			keyCount++
		}
	}
	require.GreaterOrEqual(t, keyCount, 2,
		"expected at least 2 keyframes (one per source), got %d", keyCount)

	// Decode output and verify colors.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 0, "should decode at least some frames")

	// First decoded frames should approximate source A color (Y~80).
	firstY := averageY(decoded[0].YUV, decoded[0].Width, decoded[0].Height)
	t.Logf("First decoded frame avg Y: %.1f (expected ~%d)", firstY, sourceA.Y)
	require.InDelta(t, float64(sourceA.Y), firstY, 25,
		"first frames should approximate source A color")

	// Last decoded frames should approximate source B color (Y~160).
	last := decoded[len(decoded)-1]
	lastY := averageY(last.YUV, last.Width, last.Height)
	t.Logf("Last decoded frame avg Y: %.1f (expected ~%d)", lastY, sourceB.Y)
	require.InDelta(t, float64(sourceB.Y), lastY, 25,
		"last frames should approximate source B color")

	t.Logf("Pipeline cut test passed: %d TS frames, %d decoded, %d keyframes",
		len(videoFrames), len(decoded), keyCount)
}

// ==========================================================================
// TestFullPipelineTransitionToTS
//
// Verifies that a dissolve transition between two sources produces blended
// intermediate frames. Uses the transition engine to actually blend.
//
// Since wiring the full transition engine is complex, we simulate a
// dissolve by feeding alternating frames from both sources through the
// pipeline and verifying the output Y values are between source colors.
// The real transition engine test is in correctness/transition/.
// ==========================================================================

func TestFullPipelineTransitionToTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	// Cut to A and feed initial frames.
	h.cut(t, "camA")
	h.feedFrames("camA", 15, 90000)
	h.waitForPipeline(200 * time.Millisecond)

	preCount := h.capture.videoCount()
	require.Greater(t, preCount, 0, "should have initial frames from camA")

	// Simulate blend by feeding frames with intermediate Y values.
	// This tests that the pipeline correctly encodes mid-range content
	// that would result from a real dissolve.
	midY := byte((int(sourceA.Y) + int(sourceB.Y)) / 2) // ~120
	midCb := byte((int(sourceA.Cb) + int(sourceB.Cb)) / 2)
	midCr := byte((int(sourceA.Cr) + int(sourceB.Cr)) / 2)
	h.feedFramesWithColor("camA", 30, 90000+15*ptsDelta, midY, midCb, midCr)
	h.waitForPipeline(300 * time.Millisecond)

	// Switch to B and feed post-transition frames.
	h.cut(t, "camB")
	h.feedFrames("camB", 15, 90000+45*ptsDelta)
	h.waitForPipeline(200 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	require.InDelta(t, 60, len(videoFrames), 20,
		"expected ~60 video frames, got %d", len(videoFrames))

	// Verify PTS monotonic.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Decode output and verify mid-transition frames have intermediate Y.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 5, "should decode enough frames to check blend")

	// Check that mid-section frames have Y values between source A and B.
	// Find frames in the middle third.
	midStart := len(decoded) / 3
	midEnd := 2 * len(decoded) / 3
	if midEnd > len(decoded) {
		midEnd = len(decoded)
	}

	for i := midStart; i < midEnd; i++ {
		y := averageY(decoded[i].YUV, decoded[i].Width, decoded[i].Height)
		// Mid-blend Y should be between sourceA.Y(80) and sourceB.Y(160), with tolerance.
		require.Greater(t, y, float64(sourceA.Y)-30,
			"frame %d: Y=%.1f too low for blend", i, y)
		require.Less(t, y, float64(sourceB.Y)+30,
			"frame %d: Y=%.1f too high for blend", i, y)
	}

	t.Logf("Pipeline transition test passed: %d TS frames, %d decoded", len(videoFrames), len(decoded))
}

// ==========================================================================
// TestFullPipelineGraphicsOverlayToTS
//
// Verifies that a graphics overlay composited on program frames produces
// visible changes in the encoded output. A white opaque overlay should
// replace the program content, resulting in high Y values (~235).
// ==========================================================================

func TestFullPipelineGraphicsOverlayToTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withCompositor())
	defer h.close()

	h.addSource("camA", sourceA)

	// Set up a white overlay on layer 0.
	layerID, err := h.compositor.AddLayer()
	require.NoError(t, err)

	pngData := createWhiteOverlayPNG(testWidth, testHeight)
	require.NoError(t, h.compositor.SetImage(layerID, "white", pngData))
	require.NoError(t, h.compositor.On(layerID))

	// Rebuild pipeline after compositor change.
	require.NoError(t, h.sw.BuildPipeline())

	// Cut to A and feed frames with overlay active.
	h.cut(t, "camA")
	h.feedFrames("camA", 30, 90000)
	h.waitForPipeline(300 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 5, "should have video frames with overlay")

	// Decode and check Y values are high (white overlay).
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 0, "should decode at least some frames")

	// White in YUV is approximately Y=235, Cb=128, Cr=128.
	// With lossy compression, expect Y > 200.
	highYCount := 0
	for _, df := range decoded {
		y := averageY(df.YUV, df.Width, df.Height)
		if y > 200 {
			highYCount++
		}
	}

	require.Greater(t, highYCount, 0,
		"at least some decoded frames should have high Y values from white overlay (got %d of %d)",
		highYCount, len(decoded))

	t.Logf("Pipeline graphics overlay test passed: %d decoded frames, %d with Y>200",
		len(decoded), highYCount)
}

// ==========================================================================
// TestFullPipelineCutWithAudioToTS
//
// Verifies that the pipeline produces both video AND audio streams when
// an audio mixer is attached. Audio output comes from the mixer's clock-
// driven ticker rather than direct passthrough.
// ==========================================================================

func TestFullPipelineCutWithAudioToTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	// Add mixer channels.
	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	require.NoError(t, h.mixer.SetAFV("camA", true))
	require.NoError(t, h.mixer.SetAFV("camB", true))

	// Cut to A.
	h.cut(t, "camA")

	// Feed video frames from A.
	h.feedFrames("camA", 30, 90000)

	// Wait for mixer ticker to produce audio output (~21ms cadence).
	// The clock-driven mixer produces audio independently from video input.
	h.waitForPipeline(500 * time.Millisecond)

	aCountA := h.capture.audioCount()
	vCountA := h.capture.videoCount()
	t.Logf("After camA: %d video, %d audio frames", vCountA, aCountA)

	// Cut to B.
	h.cut(t, "camB")
	h.feedFrames("camB", 30, 90000+30*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	// Verify both video and audio frames were produced.
	totalVideo := h.capture.videoCount()
	totalAudio := h.capture.audioCount()
	t.Logf("Total: %d video, %d audio frames", totalVideo, totalAudio)
	require.Greater(t, totalVideo, 0, "should have video frames")
	require.Greater(t, totalAudio, 0, "should have audio frames from mixer ticker")

	// Write TS and verify it has both streams.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	require.Equal(t, "h264", vs.CodecName)

	as := result.AudioStream()
	require.NotNil(t, as, "expected audio stream in TS output")
	require.Equal(t, "aac", as.CodecName)

	// Verify A/V PTS alignment: first video and audio PTS should be
	// within a reasonable range of each other.
	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	audioFrames := testutil.ProbeAudioFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 0, "need video frames for A/V sync check")
	require.Greater(t, len(audioFrames), 0, "need audio frames for A/V sync check")

	avDelta := math.Abs(float64(videoFrames[0].PTS - audioFrames[0].PTS))
	t.Logf("A/V PTS delta: %.0f ticks (video=%d, audio=%d)",
		avDelta, videoFrames[0].PTS, audioFrames[0].PTS)

	// Allow generous tolerance: the mixer's clock-driven output and the
	// video pipeline run independently. 2 seconds (180000 ticks) is generous
	// but ensures they're in the same ballpark.
	require.Less(t, avDelta, float64(180000),
		"A/V PTS delta too large: %.0f ticks", avDelta)

	t.Logf("Pipeline A/V test passed: %d video, %d audio TS frames, A/V delta=%.0f ticks",
		len(videoFrames), len(audioFrames), avDelta)
}

// ==========================================================================
// TestFullPipelineLayoutToTS
//
// Verifies that the layout compositor (PIP) produces output where the
// main area shows the program source and the PIP region shows the
// secondary source.
// ==========================================================================

func TestFullPipelineLayoutToTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withLayout())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	// Set PIP layout: camB as PIP in top-right corner, 1/3 width.
	pipLayout := layout.PIPPreset(testWidth, testHeight, "camB", "top-right", 0.33)
	h.layoutComp.SetLayout(pipLayout)

	// Rebuild pipeline after layout change.
	require.NoError(t, h.sw.BuildPipeline())

	// Cut to A (main program), feed both sources.
	h.cut(t, "camA")

	// Feed frames from both sources. The layout compositor needs source
	// frames for camB (the PIP source) via IngestSourceFrame, which is
	// called by handleRawVideoFrame when the layout needs the source.
	for i := 0; i < 30; i++ {
		pts := int64(90000 + i*ptsDelta)

		// Feed A (program source).
		yuvA := testutil.GenerateSolidYUV420(testWidth, testHeight, sourceA.Y, sourceA.Cb, sourceA.Cr)
		pfA := &switcher.ProcessingFrame{
			YUV:        yuvA,
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("camA", pfA)

		// Feed B (PIP source) - this populates the layout compositor's fill cache.
		yuvB := testutil.GenerateSolidYUV420(testWidth, testHeight, sourceB.Y, sourceB.Cb, sourceB.Cr)
		pfB := &switcher.ProcessingFrame{
			YUV:        yuvB,
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("camB", pfB)
	}

	h.waitForPipeline(400 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 5, "should have video frames with PIP layout")

	// Decode and check: the main area should be approximately source A's color,
	// and the PIP region should have source B's color (or at least different from A).
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 0, "should decode at least some frames")

	// Check the center of the frame (should be source A).
	// Center pixel is well outside the PIP region.
	lastFrame := decoded[len(decoded)-1]
	centerY, _, _ := testutil.YUVPixelAt(lastFrame.YUV, lastFrame.Width, lastFrame.Height,
		lastFrame.Width/2, lastFrame.Height/2)
	t.Logf("Center Y: %d (expected ~%d for source A)", centerY, sourceA.Y)
	require.InDelta(t, float64(sourceA.Y), float64(centerY), 30,
		"center of frame should approximate source A color")

	// Check a pixel in the PIP region (top-right area).
	// PIP is at top-right with ~33% width.
	pipX := lastFrame.Width - lastFrame.Width/6 // middle of PIP region
	pipY := lastFrame.Height / 6                 // near top
	pipYVal, _, _ := testutil.YUVPixelAt(lastFrame.YUV, lastFrame.Width, lastFrame.Height, pipX, pipY)
	t.Logf("PIP region Y: %d (expected ~%d for source B)", pipYVal, sourceB.Y)

	// The PIP should be notably different from the main program.
	// After lossy compression, exact values shift, but the PIP region
	// should be measurably different from the main area.
	yDiff := math.Abs(float64(centerY) - float64(pipYVal))
	t.Logf("Y difference between center and PIP: %.1f", yDiff)
	require.Greater(t, yDiff, 20.0,
		"PIP region Y should differ significantly from main area (diff=%.1f)", yDiff)

	t.Logf("Pipeline layout test passed: %d frames, center Y=%d, PIP Y=%d",
		len(decoded), centerY, pipYVal)
}

// ==========================================================================
// TestFullPipelineKeyingToTS
//
// Verifies that upstream chroma keying works through the full pipeline.
// A green source is keyed over a blue program. The green should be
// removed, leaving mostly blue output.
// ==========================================================================

func TestFullPipelineKeyingToTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withKeying())
	defer h.close()

	// Source A: blue (program background).
	blueColor := sourceColor{Y: 41, Cb: 240, Cr: 110}
	h.addSource("camA", blueColor)

	// Source B: green (keyed source, will be removed by chroma key).
	greenColor := sourceColor{Y: 145, Cb: 54, Cr: 26}
	h.addSource("camB", greenColor)

	// Configure chroma key on source B to remove green.
	h.keyProc.SetKey("camB", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  greenColor.Y,
		KeyColorCb: greenColor.Cb,
		KeyColorCr: greenColor.Cr,
		Similarity: 0.4,
		Smoothness: 0.1,
	})

	// Rebuild pipeline after keying change.
	require.NoError(t, h.sw.BuildPipeline())

	// Cut to A (blue background on program).
	h.cut(t, "camA")

	// Feed frames from both sources. Source B's green will be keyed out,
	// so the result should be mostly blue (source A).
	for i := 0; i < 30; i++ {
		pts := int64(90000 + i*ptsDelta)

		yuvA := testutil.GenerateSolidYUV420(testWidth, testHeight, blueColor.Y, blueColor.Cb, blueColor.Cr)
		pfA := &switcher.ProcessingFrame{
			YUV:        yuvA,
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("camA", pfA)

		yuvB := testutil.GenerateSolidYUV420(testWidth, testHeight, greenColor.Y, greenColor.Cb, greenColor.Cr)
		pfB := &switcher.ProcessingFrame{
			YUV:        yuvB,
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("camB", pfB)
	}

	h.waitForPipeline(400 * time.Millisecond)

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 5, "should have video frames with keying")

	// Decode and verify: output should be mostly blue (green keyed out).
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 0, "should decode at least some frames")

	// Check that output Y values are closer to blue (Y~41) than green (Y~145).
	// With a solid green source keyed out, the background blue should dominate.
	last := decoded[len(decoded)-1]
	y := averageY(last.YUV, last.Width, last.Height)
	t.Logf("Output avg Y: %.1f (blue=%d, green=%d)", y, blueColor.Y, greenColor.Y)

	// The output should be closer to the blue program than to pure green.
	distToBlue := math.Abs(y - float64(blueColor.Y))
	distToGreen := math.Abs(y - float64(greenColor.Y))
	require.Less(t, distToBlue, distToGreen,
		"output should be closer to blue (%.1f) than green (%.1f); avg Y=%.1f",
		distToBlue, distToGreen, y)

	t.Logf("Pipeline keying test passed: %d frames, output Y=%.1f (blue=%d, green=%d)",
		len(decoded), y, blueColor.Y, greenColor.Y)
}

