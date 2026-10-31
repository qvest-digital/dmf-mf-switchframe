//go:build correctness

package pipeline

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit long-run pipeline tests.
//
// Verify that the pipeline handles 1000+ 10-bit frames without drift,
// crashes, or memory leaks. 10-bit frames are 4x larger than 8-bit
// (w*h*4 vs w*h*3/2), so memory pressure is significantly higher.
// ==========================================================================

// TestPipeline10bit_LongRunNoCrash feeds 1000 10-bit frames through the
// pipeline and verifies no crashes, no panics, and consistent output.
func TestPipeline10bit_LongRunNoCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("long10", sourceA)
	h.cut(t, "long10")

	const (
		w           = testWidth
		ht          = testHeight
		totalFrames = 1000
		batchSize   = 50
	)

	format := switcher.ColorYUV422_10bit
	frameSize := format.FrameSize(w, ht)
	require.Equal(t, w*ht*4, frameSize, "10-bit frame size check")

	pts := int64(90000)
	for fed := 0; fed < totalFrames; fed += batchSize {
		n := batchSize
		if fed+n > totalFrames {
			n = totalFrames - fed
		}

		for i := 0; i < n; i++ {
			// Vary Y slightly to avoid trivial encoding
			yVal := uint16(300 + (fed+i)%641) // 300 -> 940 cycling
			pf := make10bitFrame(w, ht, yVal, 512, 512, pts, (fed+i)%30 == 0)
			h.sw.IngestRawVideo("long10", pf)
			pts += ptsDelta
			time.Sleep(h.sw.PipelineFormat().FrameDuration())
		}
	}

	h.waitForPipeline(500 * time.Millisecond)

	count := h.capture.videoCount()
	t.Logf("MEASURE: 10-bit long run: %d frames fed, %d encoded frames captured", totalFrames, count)
	require.Greater(t, count, 100,
		"expected substantial output from %d input frames", totalFrames)
}

// TestPipeline10bit_LongRunMemoryStability verifies that encoding 10-bit
// frames through the pipeline does not cause unbounded memory growth.
// 10-bit frames are 4x the size of 8-bit, so this is a more aggressive
// memory test.
func TestPipeline10bit_LongRunMemoryStability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	// Force GC before measuring baseline.
	runtime.GC()
	runtime.GC()
	var memStart runtime.MemStats
	runtime.ReadMemStats(&memStart)

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("mem10", sourceA)
	h.cut(t, "mem10")

	const (
		w           = testWidth
		ht          = testHeight
		totalFrames = 600 // ~20 seconds at 30fps
		batchSize   = 30
	)

	pts := int64(90000)
	for fed := 0; fed < totalFrames; fed += batchSize {
		n := batchSize
		if fed+n > totalFrames {
			n = totalFrames - fed
		}
		for i := 0; i < n; i++ {
			yVal := uint16(400 + (fed+i)%541) // vary within legal range
			pf := make10bitFrame(w, ht, yVal, 512, 512, pts, (fed+i)%30 == 0)
			h.sw.IngestRawVideo("mem10", pf)
			pts += ptsDelta
			time.Sleep(h.sw.PipelineFormat().FrameDuration())
		}
	}

	h.waitForPipeline(500 * time.Millisecond)

	require.Greater(t, h.capture.videoCount(), 50,
		"should produce output for memory test")

	// Measure heap after.
	runtime.GC()
	runtime.GC()
	var memEnd runtime.MemStats
	runtime.ReadMemStats(&memEnd)

	heapStartMB := memStart.HeapAlloc / (1024 * 1024)
	heapEndMB := memEnd.HeapAlloc / (1024 * 1024)
	deltaMB := int64(memEnd.HeapAlloc) - int64(memStart.HeapAlloc)

	t.Logf("MEASURE: heap start=%dMB, end=%dMB, delta=%dMB",
		heapStartMB, heapEndMB, deltaMB/(1024*1024))
	t.Logf("MEASURE: 10-bit frame size = %d bytes (vs 8-bit = %d bytes)",
		switcher.ColorYUV422_10bit.FrameSize(w, ht),
		w*ht*3/2)

	// Allow more headroom for 10-bit (frames are 4x larger): 80MB vs 50MB.
	// Evaluate unconditionally (signed delta) — the old `if deltaMB > 0` guard
	// skipped the check whenever GC reclaimed more than allocated at the sample
	// point, hiding a real leak. A shrinking heap passes trivially; only growth
	// past the bound fails. (HeapAlloc still cannot see off-heap FramePool leaks.)
	require.Less(t, deltaMB, int64(80*1024*1024),
		"heap grew by %dMB during 10-bit long run -- possible memory leak", deltaMB/(1024*1024))
}

// TestPipeline10bit_PeriodicCuts feeds 10-bit frames with periodic cuts
// between two sources. Verifies the correct source is on program after
// each cut.
func TestPipeline10bit_PeriodicCuts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 40, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 220, Cb: 128, Cr: 128}
	h.addSource("cut10A", colorA)
	h.addSource("cut10B", colorB)

	const (
		w            = testWidth
		ht           = testHeight
		framesPerSeg = 60
		numSegments  = 10
		flushFrames  = 8
	)

	prevDecodedLen := 0
	pts := int64(90000)
	totalOK := 0
	totalBAD := 0

	for seg := 0; seg < numSegments; seg++ {
		activeSource := "cut10A"
		activeY := uint16(160) // dark in 10-bit
		thresholdLow := 100.0
		if seg%2 == 1 {
			activeSource = "cut10B"
			activeY = 880 // bright in 10-bit
		}

		h.cut(t, activeSource)

		// Feed 10-bit frames.
		for i := 0; i < framesPerSeg; i++ {
			pf := make10bitFrame(w, ht, activeY, 512, 512, pts, i == 0)
			h.sw.IngestRawVideo(activeSource, pf)
			pts += ptsDelta
			if i < framesPerSeg-1 {
				time.Sleep(h.sw.PipelineFormat().FrameDuration())
			}
		}

		h.waitForPipeline(300 * time.Millisecond)

		allVideos := h.capture.videosCopy()
		allDecoded := decodeCapturedFrames(t, allVideos)

		if len(allDecoded) <= prevDecodedLen {
			continue
		}

		newFrames := allDecoded[prevDecodedLen:]
		prevDecodedLen = len(allDecoded)

		checkStart := len(newFrames) - 10
		if checkStart < flushFrames {
			checkStart = flushFrames
		}
		if checkStart >= len(newFrames) {
			checkStart = 0
		}

		for i := checkStart; i < len(newFrames); i++ {
			df := newFrames[i]
			avgY := averageY(df.YUV, df.Width, df.Height)
			if seg%2 == 0 {
				if avgY < thresholdLow {
					totalOK++
				} else {
					totalBAD++
				}
			} else {
				if avgY > 160.0 {
					totalOK++
				} else {
					totalBAD++
				}
			}
		}
	}

	t.Logf("MEASURE: 10-bit periodic cuts: OK=%d, BAD=%d across %d segments", totalOK, totalBAD, numSegments)
	require.Equal(t, 0, totalBAD, "found %d frames with wrong source color in 10-bit cuts", totalBAD)
	require.Greater(t, totalOK, numSegments, "should verify meaningful frames per segment")
}
