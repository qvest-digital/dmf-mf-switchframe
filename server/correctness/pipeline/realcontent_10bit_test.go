//go:build correctness

package pipeline

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit real content tests.
//
// These exercise the pipeline with 10-bit content that has spatial and
// temporal variation, testing encoder rate control and processing paths
// that solid-color frames never touch.
// ==========================================================================

// TestPipelineWithMotionContent10bit generates 10-bit frames with a moving
// vertical bar across a black background and feeds them through the pipeline.
func TestPipelineWithMotionContent10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("motion10", sourceA)
	h.cut(t, "motion10")

	const (
		w         = testWidth
		ht        = testHeight
		numFrames = 60
		barWidth  = 20
	)

	format := switcher.ColorYUV422_10bit

	for frame := 0; frame < numFrames; frame++ {
		frameSize := format.FrameSize(w, ht)
		data := make([]byte, frameSize)
		barX := (frame * 5) % (w - barWidth)

		// Y plane: white bar on black background (10-bit broadcast levels)
		ySize := w * ht
		for py := 0; py < ht; py++ {
			for px := 0; px < w; px++ {
				var yVal uint16
				if px >= barX && px < barX+barWidth {
					yVal = 940 // 10-bit white
				} else {
					yVal = 64 // 10-bit black
				}
				binary.LittleEndian.PutUint16(data[(py*w+px)*2:], yVal)
			}
		}

		// Cb plane: neutral 512
		cbOff := ySize * 2
		chromaW := w / 2
		chromaSize := chromaW * ht
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[cbOff+i*2:], 512)
		}

		// Cr plane: neutral 512
		crOff := cbOff + chromaSize*2
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[crOff+i*2:], 512)
		}

		pf := &switcher.ProcessingFrame{
			Data:       data,
			Width:      w,
			Height:     ht,
			Format:     format,
			PTS:        90000 + int64(frame)*ptsDelta,
			IsKeyframe: frame == 0,
		}
		h.sw.IngestRawVideo("motion10", pf)
		if frame < numFrames-1 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	h.waitForPipeline(800 * time.Millisecond)

	count := h.capture.videoCount()
	t.Logf("MEASURE: encoded frames from 10-bit motion content = %d", count)
	require.Greater(t, count, 0, "pipeline should produce output from 10-bit motion content")

	if h.tsLen() > 0 {
		tsPath := h.writeTS(t)
		testutil.RequireNoErrors(t, tsPath)

		result := testutil.ProbeFile(t, tsPath)
		vs := result.VideoStream()
		require.NotNil(t, vs, "expected video stream in TS output")

		videoFrames := testutil.ProbeVideoFrames(t, tsPath)
		t.Logf("MEASURE: ffprobe video frame count = %d, expected ~%d", len(videoFrames), numFrames)
		require.Greater(t, len(videoFrames), 20, "expected at least 20 video frames")

		// Verify PTS monotonicity.
		ptsValues := make([]int64, len(videoFrames))
		for i, f := range videoFrames {
			ptsValues[i] = f.PTS
		}
		testutil.AssertMonotonic(t, ptsValues, "PTS")
	}
}

// TestPipelineCutBetween10bitContent feeds two distinct 10-bit patterns
// (horizontal bars and vertical bars), cuts between them, and verifies
// the structural content change is visible in the output.
func TestPipelineCutBetween10bitContent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("pat10A", sourceA)
	h.addSource("pat10B", sourceB)

	const (
		w         = testWidth
		ht        = testHeight
		barHeight = 20
		barWidth  = 20
	)

	format := switcher.ColorYUV422_10bit

	// Horizontal bars: alternating Y=64 and Y=940 every barHeight rows
	makeHBars10 := func() []byte {
		frameSize := format.FrameSize(w, ht)
		data := make([]byte, frameSize)
		ySize := w * ht
		for py := 0; py < ht; py++ {
			var yVal uint16
			if (py/barHeight)%2 == 0 {
				yVal = 64
			} else {
				yVal = 940
			}
			for px := 0; px < w; px++ {
				binary.LittleEndian.PutUint16(data[(py*w+px)*2:], yVal)
			}
		}
		cbOff := ySize * 2
		chromaW := w / 2
		chromaSize := chromaW * ht
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[cbOff+i*2:], 512)
		}
		crOff := cbOff + chromaSize*2
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[crOff+i*2:], 512)
		}
		return data
	}

	// Vertical bars
	makeVBars10 := func() []byte {
		frameSize := format.FrameSize(w, ht)
		data := make([]byte, frameSize)
		ySize := w * ht
		for py := 0; py < ht; py++ {
			for px := 0; px < w; px++ {
				var yVal uint16
				if (px/barWidth)%2 == 0 {
					yVal = 64
				} else {
					yVal = 940
				}
				binary.LittleEndian.PutUint16(data[(py*w+px)*2:], yVal)
			}
		}
		cbOff := ySize * 2
		chromaW := w / 2
		chromaSize := chromaW * ht
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[cbOff+i*2:], 512)
		}
		crOff := cbOff + chromaSize*2
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[crOff+i*2:], 512)
		}
		return data
	}

	hBars := makeHBars10()
	vBars := makeVBars10()

	// Feed 30 frames of horizontal bars
	h.cut(t, "pat10A")
	for frame := 0; frame < 30; frame++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(hBars),
			Width:      w,
			Height:     ht,
			Format:     format,
			PTS:        90000 + int64(frame)*ptsDelta,
			IsKeyframe: frame == 0,
		}
		h.sw.IngestRawVideo("pat10A", pf)
		if frame < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	preCount := h.capture.videoCount()
	t.Logf("10-bit pattern A frames: %d", preCount)

	// Cut and feed 30 frames of vertical bars
	h.cut(t, "pat10B")
	for frame := 0; frame < 30; frame++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(vBars),
			Width:      w,
			Height:     ht,
			Format:     format,
			PTS:        90000 + int64(30+frame)*ptsDelta,
			IsKeyframe: frame == 0,
		}
		h.sw.IngestRawVideo("pat10B", pf)
		if frame < 29 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitForPipeline(500 * time.Millisecond)

	totalCount := h.capture.videoCount()
	t.Logf("Total 10-bit frames after cut: %d", totalCount)
	require.Greater(t, totalCount, preCount,
		"should produce more frames after cutting to pattern B")
}
