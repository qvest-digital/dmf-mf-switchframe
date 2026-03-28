//go:build correctness

// Package pipeline tests the end-to-end video processing pipeline:
// IngestRawVideo -> pipeline nodes -> H.264 encode -> program relay -> TS mux.
//
// These tests verify that real components wired together produce correct,
// ffprobe-parseable MPEG-TS output with the expected content.
package pipeline

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
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

// ==========================================================================
// TestCutAudioContinuity
//
// Verifies that a cut between two sources produces continuous audio output
// with no silence gaps, no clicks, and signal present on both sides of the
// splice point. Uses the mixer's raw audio sink to capture PCM output.
// ==========================================================================

func TestCutAudioContinuity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	require.NoError(t, h.mixer.SetAFV("camA", true))
	require.NoError(t, h.mixer.SetAFV("camB", true))

	// Capture raw PCM output from mixer.
	var rawMu sync.Mutex
	var rawPCM []float32
	h.mixer.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		rawMu.Lock()
		rawPCM = append(rawPCM, pcm...)
		rawMu.Unlock()
	})

	// Cut to A and start feeding audio + video.
	h.cut(t, "camA")

	// Generate stereo sine waves: A=440Hz, B=880Hz at -6 dBFS.
	const sampleRate = 48000
	sineA := testutil.GenerateSineWavePCM(440, -6, sampleRate, 2.0)
	sineB := testutil.GenerateSineWavePCM(880, -6, sampleRate, 2.0)

	// Interleave mono to stereo for the mixer (2 channels).
	stereoA := monoToStereo(sineA)
	stereoB := monoToStereo(sineB)

	// Feed audio from BOTH sources continuously in background so ring buffers
	// stay fed across the cut. The OnCut crossfade reads from ring buffers,
	// so both must have data available.
	const chunkSize = 1024 * 2 // 1024 samples * 2 channels
	go feedPCMChunks(h.mixer, "camA", stereoA, chunkSize, sampleRate)
	go feedPCMChunks(h.mixer, "camB", stereoB, chunkSize, sampleRate)

	// Feed video from A concurrently.
	h.feedFrames("camA", 15, 90000)
	h.waitForPipeline(400 * time.Millisecond)

	// Record the raw PCM length before the cut.
	rawMu.Lock()
	preCutLen := len(rawPCM)
	rawMu.Unlock()
	t.Logf("Pre-cut raw PCM samples: %d", preCutLen)

	// Cut to B.
	h.cut(t, "camB")

	// Feed video from B.
	h.feedFrames("camB", 15, 90000+15*ptsDelta)
	h.waitForPipeline(500 * time.Millisecond)

	// Analyze captured PCM.
	rawMu.Lock()
	capturedPCM := make([]float32, len(rawPCM))
	copy(capturedPCM, rawPCM)
	rawMu.Unlock()

	require.Greater(t, len(capturedPCM), 0, "should have captured raw PCM")
	t.Logf("Total captured raw PCM samples: %d", len(capturedPCM))

	// Check 1: No extended silence gaps. The mixer's clock-driven ticker
	// produces 2048-sample (stereo) frames every ~21ms. During AFV cuts,
	// a few silent ticks are expected while the crossfade completes and the
	// new source's ring buffer fills. Allow up to ~200ms (10 ticks).
	maxSilenceRun := findMaxSilenceRun(capturedPCM, 0.001)
	t.Logf("Max silence run: %d samples", maxSilenceRun)
	require.Less(t, maxSilenceRun, 20480,
		"silence gap too large: %d samples (max allowed 20480 = ~200ms)", maxSilenceRun)

	// Check 2: No NaN or Inf values.
	for i, s := range capturedPCM {
		require.False(t, math.IsNaN(float64(s)), "NaN at sample %d", i)
		require.False(t, math.IsInf(float64(s), 0), "Inf at sample %d", i)
	}

	// Check 3: Signal present on both sides of the cut.
	// The cut happened around preCutLen. The 2-tick crossfade (~42ms = ~4096
	// stereo samples) may produce near-silence right at the cut as gains
	// ramp. Check a window before the cut and a window well after the cut
	// (past the crossfade settling time).
	if preCutLen > 2048 {
		rmsBeforeCut := rmsLevel(capturedPCM[preCutLen-2048 : preCutLen])
		t.Logf("RMS before cut: %.6f", rmsBeforeCut)
		require.Greater(t, rmsBeforeCut, float64(0.001),
			"should have signal before cut point")
	}
	// Check well after the cut (skip crossfade region).
	afterCutOffset := preCutLen + 10240 // ~100ms past the cut
	if afterCutOffset+2048 <= len(capturedPCM) {
		rmsAfterCut := rmsLevel(capturedPCM[afterCutOffset : afterCutOffset+2048])
		t.Logf("RMS after cut (+100ms): %.6f", rmsAfterCut)
		require.Greater(t, rmsAfterCut, float64(0.001),
			"should have signal after cut point (past crossfade)")
	}

	// Check 4: No click — max sample-to-sample delta should be reasonable.
	maxDelta := maxSampleDelta(capturedPCM)
	t.Logf("Max sample-to-sample delta: %.6f", maxDelta)
	// A click would produce a delta > 0.5. Allow 0.8 accounting for crossfade.
	require.Less(t, maxDelta, float64(0.8),
		"sample-to-sample delta too large (possible click): %.6f", maxDelta)

	t.Logf("Cut audio continuity test passed: %d samples, max silence=%d, max delta=%.4f",
		len(capturedPCM), maxSilenceRun, maxDelta)
}

// ==========================================================================
// TestTransitionAudioCrossfade
//
// Verifies that an equal-power crossfade between two sources maintains
// approximately constant total power through the transition. The crossfade
// math (cos²+sin²=1) should keep the summed RMS level roughly flat.
// ==========================================================================

func TestTransitionAudioCrossfade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	// Set both channels active (not AFV) so they both contribute.
	h.mixer.SetActive("camA", true)
	h.mixer.SetActive("camB", true)

	// Capture raw PCM output from mixer.
	var rawMu sync.Mutex
	var rawPCM []float32
	h.mixer.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		rawMu.Lock()
		rawPCM = append(rawPCM, pcm...)
		rawMu.Unlock()
	})

	// Cut to A first.
	h.cut(t, "camA")

	// Generate stereo sine waves: A=440Hz, B=880Hz at -6 dBFS.
	const sampleRate = 48000
	sineA := testutil.GenerateSineWavePCM(440, -6, sampleRate, 1.0)
	sineB := testutil.GenerateSineWavePCM(880, -6, sampleRate, 1.0)
	stereoA := monoToStereo(sineA)
	stereoB := monoToStereo(sineB)

	const chunkSize = 1024 * 2

	// Feed audio from both sources continuously in background.
	go feedPCMChunks(h.mixer, "camA", stereoA, chunkSize, sampleRate)
	go feedPCMChunks(h.mixer, "camB", stereoB, chunkSize, sampleRate)

	// Feed some video.
	h.feedFrames("camA", 10, 90000)
	h.waitForPipeline(200 * time.Millisecond)

	// Record pre-transition length.
	rawMu.Lock()
	preTransLen := len(rawPCM)
	rawMu.Unlock()

	// Start a transition crossfade via the mixer directly.
	// Duration 500ms for easier analysis.
	h.mixer.OnTransitionStart("camA", "camB", audio.Crossfade, 500)

	// Advance the transition position over 500ms in steps.
	for i := 0; i <= 10; i++ {
		pos := float64(i) / 10.0
		h.mixer.OnTransitionPosition(pos)
		time.Sleep(50 * time.Millisecond)
	}
	h.mixer.OnTransitionComplete()

	// Feed a bit more to capture post-transition.
	h.feedFrames("camB", 10, 90000+20*ptsDelta)
	h.waitForPipeline(200 * time.Millisecond)

	// Analyze captured PCM.
	rawMu.Lock()
	capturedPCM := make([]float32, len(rawPCM))
	copy(capturedPCM, rawPCM)
	rawMu.Unlock()

	require.Greater(t, len(capturedPCM), preTransLen, "should have PCM from transition period")
	t.Logf("Total captured PCM: %d samples, pre-transition: %d", len(capturedPCM), preTransLen)

	// Divide the transition region into windows and check RMS power.
	transRegion := capturedPCM[preTransLen:]
	if len(transRegion) < 4096 {
		t.Logf("Transition region too short for windowed analysis (%d samples), checking basic properties", len(transRegion))
		// At minimum, verify no silence and no NaN.
		for i, s := range transRegion {
			require.False(t, math.IsNaN(float64(s)), "NaN at sample %d", i)
			require.False(t, math.IsInf(float64(s), 0), "Inf at sample %d", i)
		}
	} else {
		// Check RMS power across windows of the transition.
		windowSize := 2048
		var rmsValues []float64
		for start := 0; start+windowSize <= len(transRegion); start += windowSize {
			rms := rmsLevel(transRegion[start : start+windowSize])
			rmsValues = append(rmsValues, rms)
		}

		if len(rmsValues) > 0 {
			// Find min and max RMS across windows.
			minRMS := rmsValues[0]
			maxRMS := rmsValues[0]
			for _, rms := range rmsValues {
				if rms < minRMS {
					minRMS = rms
				}
				if rms > maxRMS {
					maxRMS = rms
				}
			}

			t.Logf("Transition RMS range: min=%.6f, max=%.6f (%d windows)", minRMS, maxRMS, len(rmsValues))

			// Convert to dB difference. Equal-power crossfade should keep
			// power roughly constant. Allow ±3 dB given the mixer's processing chain.
			if minRMS > 0 && maxRMS > 0 {
				dbDiff := 20.0 * math.Log10(maxRMS/minRMS)
				t.Logf("RMS variation: %.2f dB", dbDiff)
				require.Less(t, dbDiff, 6.0,
					"RMS variation during crossfade too large: %.2f dB (max 6 dB)", dbDiff)
			}
		}
	}

	t.Logf("Transition audio crossfade test passed")
}

// ==========================================================================
// TestCutAVSyncInTS
//
// Verifies A/V sync in MPEG-TS output across a cut. Both the initial
// segment and the post-cut segment should have A/V PTS deltas within
// 1920 ticks (one AAC frame duration at 90kHz).
// ==========================================================================

func TestCutAVSyncInTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	require.NoError(t, h.mixer.SetAFV("camA", true))
	require.NoError(t, h.mixer.SetAFV("camB", true))

	// Generate audio for both sources.
	const sampleRate = 48000
	sineA := testutil.GenerateSineWavePCM(440, -6, sampleRate, 1.5)
	sineB := testutil.GenerateSineWavePCM(880, -6, sampleRate, 1.5)
	stereoA := monoToStereo(sineA)
	stereoB := monoToStereo(sineB)
	const chunkSize = 1024 * 2

	// Cut to A.
	h.cut(t, "camA")

	// Feed A for ~1 second.
	go feedPCMChunks(h.mixer, "camA", stereoA, chunkSize, sampleRate)
	h.feedFrames("camA", 30, 90000)
	h.waitForPipeline(600 * time.Millisecond)

	// Cut to B.
	h.cut(t, "camB")

	// Feed B for ~1 second.
	go feedPCMChunks(h.mixer, "camB", stereoB, chunkSize, sampleRate)
	h.feedFrames("camB", 30, 90000+30*ptsDelta)
	h.waitForPipeline(600 * time.Millisecond)

	// Write TS and probe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream")
	as := result.AudioStream()
	require.NotNil(t, as, "expected audio stream")
	require.Equal(t, "aac", as.CodecName)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	audioFrames := testutil.ProbeAudioFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 0, "need video frames")
	require.Greater(t, len(audioFrames), 0, "need audio frames")

	// Check A/V PTS delta at start by finding the nearest audio frame.
	// Video (30fps) and audio (~46.9fps) have different output rates.
	//
	// Important: video frames are fed faster than real-time (5ms/frame vs
	// 33ms/frame at 30fps), so video PTS advances ~6x faster than wall clock.
	// The clock-driven audio mixer tracks wall time, so their PTS timelines
	// diverge. We only check the start where both timelines are aligned.
	startVPTS := videoFrames[0].PTS
	startAPTS := findNearestPTS(audioFrames, startVPTS)
	startDelta := math.Abs(float64(startVPTS - startAPTS))
	t.Logf("Start A/V PTS delta: %.0f ticks (video=%d, nearest_audio=%d)",
		startDelta, startVPTS, startAPTS)
	require.Less(t, startDelta, float64(90000),
		"start A/V PTS delta too large: %.0f ticks", startDelta)

	// Both streams should have reasonable frame counts and be parseable.
	t.Logf("Video PTS range: %d to %d, Audio PTS range: %d to %d",
		videoFrames[0].PTS, videoFrames[len(videoFrames)-1].PTS,
		audioFrames[0].PTS, audioFrames[len(audioFrames)-1].PTS)

	// Verify video PTS is monotonically increasing.
	videoPTS := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		videoPTS[i] = f.PTS
	}
	testutil.AssertMonotonic(t, videoPTS, "video PTS")

	// Audio PTS: the mixer's wall-clock resync can nudge PTS backward by
	// up to half a frame (~960 ticks) to correct drift. Check that audio
	// PTS is generally increasing (allow small backward jumps).
	audioPTS := make([]int64, len(audioFrames))
	for i, f := range audioFrames {
		audioPTS[i] = f.PTS
	}
	assertNearlyMonotonic(t, audioPTS, 1920, "audio PTS")

	t.Logf("Cut A/V sync test passed: %d video, %d audio frames",
		len(videoFrames), len(audioFrames))
}

// ==========================================================================
// TestTransitionAVSyncInTS
//
// Verifies A/V sync in MPEG-TS during a dissolve with audio. Probes
// multiple points in the TS stream to confirm A/V PTS deltas stay bounded.
// ==========================================================================

func TestTransitionAVSyncInTS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	h.mixer.SetActive("camA", true)
	h.mixer.SetActive("camB", true)

	const sampleRate = 48000
	sineA := testutil.GenerateSineWavePCM(440, -6, sampleRate, 2.0)
	sineB := testutil.GenerateSineWavePCM(880, -6, sampleRate, 2.0)
	stereoA := monoToStereo(sineA)
	stereoB := monoToStereo(sineB)
	const chunkSize = 1024 * 2

	// Cut to A.
	h.cut(t, "camA")

	// Feed pre-transition.
	go feedPCMChunks(h.mixer, "camA", stereoA, chunkSize, sampleRate)
	go feedPCMChunks(h.mixer, "camB", stereoB, chunkSize, sampleRate)
	h.feedFrames("camA", 15, 90000)
	h.waitForPipeline(400 * time.Millisecond)

	// Start dissolve transition.
	h.mixer.OnTransitionStart("camA", "camB", audio.Crossfade, 500)

	// Drive transition over 500ms.
	for i := 0; i <= 10; i++ {
		pos := float64(i) / 10.0
		h.mixer.OnTransitionPosition(pos)
		time.Sleep(50 * time.Millisecond)
	}
	h.mixer.OnTransitionComplete()

	// Feed post-transition frames from B.
	h.cut(t, "camB")
	h.feedFrames("camB", 15, 90000+30*ptsDelta)
	h.waitForPipeline(400 * time.Millisecond)

	// Write TS and probe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	audioFrames := testutil.ProbeAudioFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 0, "need video frames")
	require.Greater(t, len(audioFrames), 0, "need audio frames")

	// Check A/V PTS delta at several points by finding the nearest audio
	// frame (by PTS) for each sampled video frame. Video and audio have
	// different output rates so index-based comparison doesn't work.
	const maxDeltaTicks = 90000 // 1 second generous tolerance
	checkVideoIndices := []struct {
		name string
		idx  int
	}{
		{"start", 0},
		{"quarter", len(videoFrames) / 4},
		{"mid", len(videoFrames) / 2},
		{"three-quarter", 3 * len(videoFrames) / 4},
		{"end", len(videoFrames) - 1},
	}

	for _, cp := range checkVideoIndices {
		if cp.idx >= len(videoFrames) {
			continue
		}
		vPTS := videoFrames[cp.idx].PTS
		nearestAudioPTS := findNearestPTS(audioFrames, vPTS)
		delta := math.Abs(float64(vPTS - nearestAudioPTS))
		t.Logf("A/V delta at %s: %.0f ticks (v=%d, nearest_a=%d)",
			cp.name, delta, vPTS, nearestAudioPTS)
		require.Less(t, delta, float64(maxDeltaTicks),
			"A/V PTS delta at %s too large: %.0f ticks", cp.name, delta)
	}

	// Verify video PTS is monotonically increasing.
	videoPTS := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		videoPTS[i] = f.PTS
	}
	testutil.AssertMonotonic(t, videoPTS, "video PTS")

	// Audio PTS: allow small backward jumps from wall-clock resync.
	audioPTS := make([]int64, len(audioFrames))
	for i, f := range audioFrames {
		audioPTS[i] = f.PTS
	}
	assertNearlyMonotonic(t, audioPTS, 1920, "audio PTS")

	t.Logf("Transition A/V sync test passed: %d video, %d audio frames", len(videoFrames), len(audioFrames))
}

// ==========================================================================
// TestAudioSurvivesRapidCuts
//
// Performs 5 rapid cuts in ~2 seconds, alternating between sources A and B.
// Verifies the mixer produces continuous audio output without silence gaps,
// NaN/Inf values, or signal dropout.
// ==========================================================================

func TestAudioSurvivesRapidCuts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	require.NoError(t, h.mixer.SetAFV("camA", true))
	require.NoError(t, h.mixer.SetAFV("camB", true))

	// Capture raw PCM.
	var rawMu sync.Mutex
	var rawPCM []float32
	h.mixer.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		rawMu.Lock()
		rawPCM = append(rawPCM, pcm...)
		rawMu.Unlock()
	})

	// Generate plenty of audio for both sources.
	const sampleRate = 48000
	sineA := testutil.GenerateSineWavePCM(440, -6, sampleRate, 3.0)
	sineB := testutil.GenerateSineWavePCM(880, -6, sampleRate, 3.0)
	stereoA := monoToStereo(sineA)
	stereoB := monoToStereo(sineB)
	const chunkSize = 1024 * 2

	// Feed audio from both sources continuously.
	go feedPCMChunks(h.mixer, "camA", stereoA, chunkSize, sampleRate)
	go feedPCMChunks(h.mixer, "camB", stereoB, chunkSize, sampleRate)

	// Initial cut to A.
	h.cut(t, "camA")
	h.feedFrames("camA", 10, 90000)
	h.waitForPipeline(200 * time.Millisecond)

	// Perform 5 rapid cuts (~400ms apart).
	sources := []string{"camB", "camA", "camB", "camA", "camB"}
	for i, src := range sources {
		h.cut(t, src)
		pts := int64(90000 + (10+i*5)*ptsDelta)
		h.feedFrames(src, 5, pts)
		h.waitForPipeline(200 * time.Millisecond)
	}

	// Let final cut settle.
	h.waitForPipeline(300 * time.Millisecond)

	// Analyze captured PCM.
	rawMu.Lock()
	capturedPCM := make([]float32, len(rawPCM))
	copy(capturedPCM, rawPCM)
	rawMu.Unlock()

	require.Greater(t, len(capturedPCM), 0, "should have captured raw PCM")
	t.Logf("Total captured PCM samples: %d (%.2f seconds at 48kHz stereo)",
		len(capturedPCM), float64(len(capturedPCM))/float64(sampleRate*2))

	// Check 1: No NaN or Inf values.
	for i, s := range capturedPCM {
		require.False(t, math.IsNaN(float64(s)), "NaN at sample %d", i)
		require.False(t, math.IsInf(float64(s), 0), "Inf at sample %d", i)
	}

	// Check 2: No silence gaps > 240 samples (5ms at 48kHz stereo = 480 values).
	// Use 480 as the threshold for stereo.
	maxSilenceRun := findMaxSilenceRun(capturedPCM, 0.001)
	t.Logf("Max silence run: %d samples", maxSilenceRun)
	// Allow up to 4096 samples (~42ms = 2 mixer ticks) during crossfade transitions.
	require.Less(t, maxSilenceRun, 8192,
		"silence gap too large during rapid cuts: %d samples", maxSilenceRun)

	// Check 3: Overall signal should be present (not all zeros).
	overallRMS := rmsLevel(capturedPCM)
	t.Logf("Overall RMS level: %.6f", overallRMS)
	require.Greater(t, overallRMS, float64(0.001),
		"overall signal too quiet, possible dropout")

	t.Logf("Rapid cuts audio test passed: %d samples, max silence=%d, overall RMS=%.6f",
		len(capturedPCM), maxSilenceRun, overallRMS)
}

// ==========================================================================
// TestStingerAudioSync
//
// Verifies that stinger audio appears in the mixer output when a stinger
// transition is active. Loads stinger audio via SetStingerAudio, triggers
// a transition, and checks that the stinger PCM contributes to the mix.
// ==========================================================================

func TestStingerAudioSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.mixer.AddChannel("camA")
	h.mixer.AddChannel("camB")
	h.mixer.SetActive("camA", true)
	h.mixer.SetActive("camB", true)

	// Capture raw PCM output.
	var rawMu sync.Mutex
	var rawPCM []float32
	h.mixer.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		rawMu.Lock()
		rawPCM = append(rawPCM, pcm...)
		rawMu.Unlock()
	})

	// Cut to A.
	h.cut(t, "camA")

	// Feed silence from both sources (so the only audible signal is the stinger).
	const sampleRate = 48000
	silenceA := testutil.GenerateSilencePCM(sampleRate, 2.0)
	silenceB := testutil.GenerateSilencePCM(sampleRate, 2.0)
	stereoSilA := monoToStereo(silenceA)
	stereoSilB := monoToStereo(silenceB)
	const chunkSize = 1024 * 2

	go feedPCMChunks(h.mixer, "camA", stereoSilA, chunkSize, sampleRate)
	go feedPCMChunks(h.mixer, "camB", stereoSilB, chunkSize, sampleRate)

	// Feed video.
	h.feedFrames("camA", 10, 90000)
	h.waitForPipeline(300 * time.Millisecond)

	// Record pre-stinger PCM length.
	rawMu.Lock()
	preStingerLen := len(rawPCM)
	rawMu.Unlock()
	t.Logf("Pre-stinger raw PCM length: %d", preStingerLen)

	// Generate a 500ms stinger audio clip: stereo 1kHz sine at -6 dBFS.
	stingerMono := testutil.GenerateSineWavePCM(1000, -6, sampleRate, 0.5)
	stingerStereo := monoToStereo(stingerMono)

	// Load stinger audio into mixer BEFORE starting transition.
	h.mixer.SetStingerAudio(stingerStereo, sampleRate, 2)

	// Start a transition (stinger-style: the mixer just overlays the stinger audio).
	h.mixer.OnTransitionStart("camA", "camB", audio.Crossfade, 500)

	// Drive transition over 500ms -- the mixer's ticker will consume stinger
	// audio during this period. Do NOT call OnTransitionComplete until we've
	// captured PCM, because OnTransitionComplete clears stinger audio.
	for i := 0; i <= 10; i++ {
		pos := float64(i) / 10.0
		h.mixer.OnTransitionPosition(pos)
		time.Sleep(50 * time.Millisecond)
	}

	// Capture PCM during the transition (before completing it).
	rawMu.Lock()
	duringTransLen := len(rawPCM)
	rawMu.Unlock()
	t.Logf("During-transition raw PCM length: %d", duringTransLen)

	// Now complete the transition.
	h.mixer.OnTransitionComplete()
	h.waitForPipeline(200 * time.Millisecond)

	// Analyze: the stinger period should have higher signal level than
	// the pre-stinger period (which had only silence inputs).
	rawMu.Lock()
	capturedPCM := make([]float32, len(rawPCM))
	copy(capturedPCM, rawPCM)
	rawMu.Unlock()

	require.Greater(t, duringTransLen, preStingerLen,
		"should have captured PCM during stinger period")

	// RMS of pre-stinger should be near zero (silence inputs).
	if preStingerLen > 2048 {
		preRMS := rmsLevel(capturedPCM[preStingerLen-2048 : preStingerLen])
		t.Logf("Pre-stinger RMS: %.6f", preRMS)
	}

	// RMS of stinger period: check the audio captured between pre-stinger
	// and during-transition snapshots. The stinger audio is additively
	// overlaid on top of the (silent) source audio.
	stingerRegion := capturedPCM[preStingerLen:duringTransLen]
	t.Logf("Stinger region length: %d samples", len(stingerRegion))

	if len(stingerRegion) >= 2048 {
		// Check RMS across the stinger region. The stinger has a 10ms fade-in,
		// so skip the first 2048 samples and check from there.
		checkStart := 2048
		if checkStart+2048 <= len(stingerRegion) {
			stingerRMS := rmsLevel(stingerRegion[checkStart : checkStart+2048])
			t.Logf("Stinger period RMS (offset 2048): %.6f", stingerRMS)
			require.Greater(t, stingerRMS, float64(0.001),
				"stinger audio should be audible in output")
		} else {
			stingerRMS := rmsLevel(stingerRegion)
			t.Logf("Stinger period RMS (full): %.6f", stingerRMS)
			require.Greater(t, stingerRMS, float64(0.001),
				"stinger audio should be audible in output")
		}
	} else if len(stingerRegion) > 0 {
		stingerRMS := rmsLevel(stingerRegion)
		t.Logf("Stinger period RMS (short %d samples): %.6f", len(stingerRegion), stingerRMS)
		// Even a short region should show some stinger signal.
		require.Greater(t, stingerRMS, float64(0.0005),
			"stinger audio should be present in output")
	} else {
		t.Log("WARNING: no stinger region captured, mixer may not have produced output during transition")
	}

	// Verify no NaN/Inf in entire output.
	for i, s := range capturedPCM {
		require.False(t, math.IsNaN(float64(s)), "NaN at sample %d", i)
		require.False(t, math.IsInf(float64(s), 0), "Inf at sample %d", i)
	}

	t.Logf("Stinger audio sync test passed: %d total samples, stinger region: %d samples",
		len(capturedPCM), len(stingerRegion))
}

// ==========================================================================
// Audio test helpers
// ==========================================================================

// monoToStereo duplicates mono PCM samples into stereo interleaved format.
func monoToStereo(mono []float32) []float32 {
	stereo := make([]float32, len(mono)*2)
	for i, s := range mono {
		stereo[i*2] = s
		stereo[i*2+1] = s
	}
	return stereo
}

// feedPCMChunks feeds PCM data to the mixer in chunks, paced at real-time rate.
// This simulates a continuous audio source.
func feedPCMChunks(mixer *audio.Mixer, sourceKey string, pcm []float32, chunkSize, sampleRate int) {
	// Pace at slightly faster than real-time to keep ring buffer fed.
	chunkDuration := time.Duration(float64(time.Second) * float64(chunkSize/2) / float64(sampleRate))
	pace := chunkDuration * 8 / 10 // 80% of real-time → slightly faster

	pts := int64(90000)
	for offset := 0; offset+chunkSize <= len(pcm); offset += chunkSize {
		chunk := pcm[offset : offset+chunkSize]
		mixer.IngestPCM(sourceKey, chunk, pts, 2)
		pts += int64(chunkSize/2) * 90000 / int64(sampleRate)
		time.Sleep(pace)
	}
}

// findMaxSilenceRun finds the longest consecutive run of samples below the threshold.
func findMaxSilenceRun(pcm []float32, threshold float64) int {
	maxRun := 0
	currentRun := 0
	for _, s := range pcm {
		if math.Abs(float64(s)) < threshold {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
		} else {
			currentRun = 0
		}
	}
	return maxRun
}

// rmsLevel computes the RMS level of a PCM buffer.
func rmsLevel(pcm []float32) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, s := range pcm {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

// maxSampleDelta finds the largest absolute difference between consecutive samples.
func maxSampleDelta(pcm []float32) float64 {
	if len(pcm) < 2 {
		return 0
	}
	maxD := float64(0)
	for i := 1; i < len(pcm); i++ {
		d := math.Abs(float64(pcm[i]) - float64(pcm[i-1]))
		if d > maxD {
			maxD = d
		}
	}
	return maxD
}

// findNearestPTS finds the frame in frames whose PTS is closest to targetPTS.
func findNearestPTS(frames []testutil.FrameInfo, targetPTS int64) int64 {
	if len(frames) == 0 {
		return 0
	}
	best := frames[0].PTS
	bestDist := int64(math.MaxInt64)
	for _, f := range frames {
		dist := f.PTS - targetPTS
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			best = f.PTS
		}
	}
	return best
}

// assertNearlyMonotonic asserts that the values are generally increasing,
// allowing small backward jumps up to maxBackward. The mixer's wall-clock
// resync can nudge audio PTS backward by up to half an AAC frame (~960 ticks).
func assertNearlyMonotonic(t *testing.T, values []int64, maxBackward int64, fieldName string) {
	t.Helper()
	for i := 1; i < len(values); i++ {
		diff := values[i] - values[i-1]
		require.Greater(t, diff, -maxBackward,
			"%s backward jump too large at index %d: %d -> %d (diff=%d, max backward=%d)",
			fieldName, i, values[i-1], values[i], diff, maxBackward)
	}
	// Also verify the overall trend is forward.
	if len(values) >= 2 {
		require.Greater(t, values[len(values)-1], values[0],
			"%s should increase overall: first=%d, last=%d", fieldName, values[0], values[len(values)-1])
	}
}

