//go:build correctness

package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit resolution change tests.
//
// Verify the pipeline handles resolution changes (1080p -> 720p) with
// 10-bit frames. The scaler must produce correct 10-bit output after
// the format change, and the output frame size must always be w*h*4.
// ==========================================================================

// TestResolutionChange10bit feeds 10-bit frames at one resolution, then
// changes to another, verifying the pipeline adapts without crashing.
func TestResolutionChange10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		pipeW = testWidth  // 320
		pipeH = testHeight // 240
	)

	rh := newResolutionHarness(t, pipeW, pipeH)
	defer rh.close()

	rh.addSource("res10A")
	rh.addSource("res10B")

	format := switcher.ColorYUV422_10bit

	// Phase 1: Feed 10-bit frames at native pipeline resolution.
	rh.cut(t, "res10A")
	for i := 0; i < 30; i++ {
		pf := make10bitFrame(pipeW, pipeH, 500, 512, 512,
			int64(90000+i*3000), i == 0)
		rh.sw.IngestRawVideo("res10A", pf)
		if i < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	rh.waitForPipeline(600 * time.Millisecond)

	preCount := rh.capture.videoCount()
	t.Logf("MEASURE: frames from res10A (%dx%d): %d", pipeW, pipeH, preCount)

	// Phase 2: Cut to B with 10-bit frames at DIFFERENT resolution (640x480).
	rh.cut(t, "res10B")
	bigW, bigH := 640, 480
	bigFrameSize := format.FrameSize(bigW, bigH)
	require.Equal(t, bigW*bigH*4, bigFrameSize,
		"10-bit frame at %dx%d should be %d bytes", bigW, bigH, bigW*bigH*4)

	for i := 0; i < 30; i++ {
		pf := make10bitFrame(bigW, bigH, 700, 512, 512,
			int64(90000+30*3000+i*3000), i == 0)
		require.Len(t, pf.Data, bigFrameSize)
		rh.sw.IngestRawVideo("res10B", pf)
		if i < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	rh.waitForPipeline(600 * time.Millisecond)

	totalCount := rh.capture.videoCount()
	t.Logf("MEASURE: total frames after res10B (%dx%d): %d", bigW, bigH, totalCount)

	// The pipeline should produce output from at least the native-resolution source.
	require.Greater(t, preCount, 0,
		"should produce frames from native-resolution 10-bit source")

	// If we have TS output, verify it's valid.
	if rh.tsLen() > 0 {
		tsPath := rh.writeTS(t)
		testutil.RequireNoErrors(t, tsPath)

		result := testutil.ProbeFile(t, tsPath)
		vs := result.VideoStream()
		require.NotNil(t, vs, "expected video stream")

		t.Logf("MEASURE: output resolution = %dx%d (pipeline = %dx%d)",
			vs.Width, vs.Height, pipeW, pipeH)

		// Output should match pipeline format, not source format.
		require.Equal(t, pipeW, vs.Width)
		require.Equal(t, pipeH, vs.Height)
	}

	t.Log("PASS: No crash on 10-bit resolution change")
}

// TestResolution10bit_FrameSizeConsistency verifies that 10-bit frame sizes
// are always w*h*4 at multiple resolutions, contrasting with 8-bit w*h*3/2.
func TestResolution10bit_FrameSizeConsistency(t *testing.T) {
	t.Parallel()

	format8 := switcher.ColorYUV420_8bit
	format10 := switcher.ColorYUV422_10bit

	resolutions := []struct{ w, h int }{
		{320, 240},
		{640, 480},
		{1280, 720},
		{1920, 1080},
	}

	for _, r := range resolutions {
		size8 := format8.FrameSize(r.w, r.h)
		size10 := format10.FrameSize(r.w, r.h)

		expected8 := r.w * r.h * 3 / 2
		expected10 := r.w * r.h * 4

		require.Equal(t, expected8, size8,
			"%dx%d: 8-bit size should be w*h*3/2 = %d", r.w, r.h, expected8)
		require.Equal(t, expected10, size10,
			"%dx%d: 10-bit size should be w*h*4 = %d", r.w, r.h, expected10)

		// 10-bit frame should be larger than 8-bit
		// Ratio: (w*h*4) / (w*h*3/2) = 8/3 ≈ 2.67x
		ratio := float64(size10) / float64(size8)
		require.InDelta(t, 8.0/3.0, ratio, 0.01,
			"%dx%d: 10-bit/8-bit size ratio should be 8/3", r.w, r.h)

		t.Logf("%dx%d: 8-bit=%d bytes, 10-bit=%d bytes (%.2fx)", r.w, r.h, size8, size10, ratio)
	}
}

// TestResolution10bit_OddDimensions verifies 10-bit frame creation works
// with non-standard resolutions (not multiple of 16).
func TestResolution10bit_OddDimensions(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	// Even dimensions that are not multiples of 16
	oddResolutions := []struct{ w, h int }{
		{318, 238},
		{1278, 718},
		{2, 2}, // minimum
	}

	for _, r := range oddResolutions {
		size := format.FrameSize(r.w, r.h)
		require.Equal(t, r.w*r.h*4, size,
			"%dx%d: 10-bit frame should be w*h*4", r.w, r.h)

		// Verify we can create a frame without panic.
		pf := make10bitFrame(r.w, r.h, 500, 512, 512, 90000, true)
		require.Len(t, pf.Data, size)
		require.Equal(t, switcher.ColorYUV422_10bit, pf.Format)

		t.Logf("%dx%d: 10-bit frame created OK, %d bytes", r.w, r.h, size)
	}
}
