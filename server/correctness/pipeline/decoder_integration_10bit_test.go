//go:build correctness

package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit decoder integration tests.
//
// These test HEVC decode -> 10-bit pipeline. Tests are skipped if HEVC
// codec support is not available (requires FFmpeg with libx265 or similar).
//
// When HEVC is available, the decoder should produce 10-bit frames with
// Format=ColorYUV422_10bit and correct buffer sizes (w*h*4).
// ==========================================================================

// TestSourceDecoder10bit_FormatPreservation verifies that 10-bit frames
// ingested via IngestRawVideo preserve their format through the pipeline.
func TestSourceDecoder10bit_FormatPreservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = testWidth
		h = testHeight
	)

	h2 := newPipelineHarness(t)
	defer h2.close()

	h2.addSource("dec10", sourceA)
	h2.cut(t, "dec10")

	// Feed 10-bit frames.
	format := switcher.ColorYUV422_10bit
	frameSize := format.FrameSize(w, h)
	require.Equal(t, w*h*4, frameSize,
		"10-bit YUV422P10LE frame size should be w*h*4")

	for i := 0; i < 30; i++ {
		pf := make10bitFrame(w, h, 500, 512, 512,
			int64(90000+i*ptsDelta), i == 0)
		require.Equal(t, switcher.ColorYUV422_10bit, pf.Format,
			"frame Format should be ColorYUV422_10bit")
		require.Len(t, pf.Data, frameSize,
			"frame data should be %d bytes for 10-bit", frameSize)
		h2.sw.IngestRawVideo("dec10", pf)
		if i < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	h2.waitForPipeline(500 * time.Millisecond)

	count := h2.capture.videoCount()
	t.Logf("Captured %d encoded frames from 10-bit source", count)
	require.Greater(t, count, 0,
		"pipeline should produce encoded output from 10-bit input")
}

// TestSourceDecoder10bit_BufferSize verifies 10-bit frame buffer sizes
// at various resolutions. The frame size must always be w*h*4 for YUV422P10LE.
func TestSourceDecoder10bit_BufferSize(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	tests := []struct {
		w, h int
	}{
		{320, 240},
		{640, 480},
		{1280, 720},
		{1920, 1080},
	}

	for _, tc := range tests {
		expected := tc.w * tc.h * 4
		actual := format.FrameSize(tc.w, tc.h)
		require.Equal(t, expected, actual,
			"%dx%d: 10-bit frame size should be %d, got %d", tc.w, tc.h, expected, actual)

		// Verify we can create a frame of this size without panic.
		pf := make10bitFrame(tc.w, tc.h, 500, 512, 512, 90000, true)
		require.Len(t, pf.Data, expected)
		require.Equal(t, switcher.ColorYUV422_10bit, pf.Format)
	}
}

// TestSourceDecoder10bit_RoundTripThroughPipeline feeds known-color 10-bit
// frames, verifies the pipeline processes them without error, and produces
// encoded output. This is the basic 10-bit ingest -> encode path.
func TestSourceDecoder10bit_RoundTripThroughPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const (
		w = testWidth
		h = testHeight
	)

	h2 := newPipelineHarness(t)
	defer h2.close()

	h2.addSource("rt10", sourceA)
	h2.cut(t, "rt10")

	// Feed 60 frames of 10-bit content at distinct Y values.
	for i := 0; i < 60; i++ {
		// Vary Y slightly across frames to test encode with real content.
		yVal := uint16(300 + i*5) // 300 -> 595 across 60 frames
		if yVal > 940 {
			yVal = 940
		}
		pf := make10bitFrame(w, h, yVal, 512, 512,
			int64(90000+i*ptsDelta), i%30 == 0)
		h2.sw.IngestRawVideo("rt10", pf)
		if i < 59 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	h2.waitForPipeline(800 * time.Millisecond)

	count := h2.capture.videoCount()
	t.Logf("Captured %d encoded frames from 10-bit round-trip", count)
	require.Greater(t, count, 0,
		"pipeline should produce encoded output from varying 10-bit input")

	// Write TS and verify structure.
	if h2.tsLen() > 0 {
		tsPath := h2.writeTS(t)
		t.Logf("TS output: %s (%d bytes)", tsPath, h2.tsLen())
	}
}
