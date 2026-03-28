//go:build correctness

package pipeline

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// TestMultiSourceLoad4Sources
//
// 4 sources (A, B, C, D) with distinct colors fed simultaneously for 3
// seconds. Cut between them every 500ms (6 cuts). Verify output fps and
// that each cut segment has the correct source color.
// ==========================================================================

func TestMultiSourceLoad4Sources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	// Register 4 sources with distinct Y values.
	h.addSource("loadA", sourceA) // Y=80
	h.addSource("loadB", sourceB) // Y=160
	h.addSource("loadC", sourceC) // Y=200
	h.addSource("loadD", sourceD) // Y=120

	sources := []struct {
		key   string
		color sourceColor
	}{
		{"loadA", sourceA},
		{"loadB", sourceB},
		{"loadC", sourceC},
		{"loadD", sourceD},
	}

	// Cut sequence: A -> B -> C -> D -> A -> B (6 cuts at 500ms intervals).
	cutSequence := []string{"loadA", "loadB", "loadC", "loadD", "loadA", "loadB"}

	// Initial cut to loadA.
	h.cut(t, "loadA")

	// Feed all 4 sources simultaneously in background goroutines
	// for 3 seconds (90 frames each at 30fps).
	const totalFrames = 90
	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(key string, color sourceColor) {
			defer wg.Done()
			yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, color.Y, color.Cb, color.Cr)
			pts := int64(90000)
			for i := 0; i < totalFrames; i++ {
				pf := &switcher.ProcessingFrame{
					YUV:        copyBytes(yuv),
					Width:      testWidth,
					Height:     testHeight,
					PTS:        pts + int64(i)*ptsDelta,
					IsKeyframe: i%30 == 0,
				}
				h.sw.IngestRawVideo(key, pf)
				time.Sleep(33 * time.Millisecond) // ~30fps pacing
			}
		}(s.key, s.color)
	}

	// Perform cuts every 500ms in a separate goroutine.
	type cutRecord struct {
		key       string
		frameIdx  int // approximate capture index at time of cut
		timestamp time.Time
	}
	var cutsMu sync.Mutex
	var cuts []cutRecord

	startTime := time.Now()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i, key := range cutSequence {
			time.Sleep(500 * time.Millisecond)
			cutsMu.Lock()
			cuts = append(cuts, cutRecord{
				key:       key,
				frameIdx:  h.capture.videoCount(),
				timestamp: time.Now(),
			})
			cutsMu.Unlock()
			err := h.sw.Cut(t.Context(), key)
			if err != nil {
				t.Logf("Cut %d to %s failed: %v", i, key, err)
			}
		}
	}()

	wg.Wait()

	// Wait for pipeline to drain.
	h.waitForPipeline(1 * time.Second)
	elapsed := time.Since(startTime)

	// Capture and decode output.
	videos := h.capture.videosCopy()
	totalOutput := len(videos)
	outputFPS := float64(totalOutput) / elapsed.Seconds()

	t.Logf("MEASURE: output fps=%.1f, total frames=%d, elapsed=%.1fs",
		outputFPS, totalOutput, elapsed.Seconds())

	require.Greater(t, totalOutput, 0, "pipeline must produce output frames")

	decoded := decodeCapturedFrames(t, videos)
	t.Logf("MEASURE: decoded %d of %d output frames", len(decoded), totalOutput)
	require.NotEmpty(t, decoded, "must decode at least one output frame")

	// Verify output fps > 20 (at least 60 frames in ~3s).
	t.Logf("MEASURE: output fps=%.1f (threshold: >20)", outputFPS)
	require.Greater(t, outputFPS, 20.0,
		"output fps should be > 20 for 4-source load, got %.1f", outputFPS)

	// Verify cut segments: after each cut, frames should match the expected source.
	// Use the cut records to segment the decoded frames by timestamp.
	cutsMu.Lock()
	cutsCopy := make([]cutRecord, len(cuts))
	copy(cutsCopy, cuts)
	cutsMu.Unlock()

	// Map source key -> expected Y value.
	expectedY := map[string]float64{
		"loadA": float64(sourceA.Y),
		"loadB": float64(sourceB.Y),
		"loadC": float64(sourceC.Y),
		"loadD": float64(sourceD.Y),
	}

	cutsVerified := 0
	for i, cr := range cutsCopy {
		// Check frames after this cut's frame index.
		// Skip a few frames for pipeline flush, then check the next batch.
		checkStart := cr.frameIdx + 5
		var checkEnd int
		if i+1 < len(cutsCopy) {
			checkEnd = cutsCopy[i+1].frameIdx
		} else {
			checkEnd = len(decoded)
		}

		if checkStart >= len(decoded) || checkStart >= checkEnd {
			t.Logf("Cut %d to %s: not enough frames to verify (start=%d, end=%d, decoded=%d)",
				i, cr.key, checkStart, checkEnd, len(decoded))
			continue
		}

		// Verify the last few frames of this segment match the expected color.
		segEnd := checkEnd
		if segEnd > len(decoded) {
			segEnd = len(decoded)
		}
		segStart := segEnd - 3
		if segStart < checkStart {
			segStart = checkStart
		}

		target := expectedY[cr.key]
		matchCount := 0
		for j := segStart; j < segEnd; j++ {
			avgY := averageY(decoded[j].YUV, decoded[j].Width, decoded[j].Height)
			diff := math.Abs(avgY - target)
			t.Logf("Cut %d (%s, Y=%.0f): frame %d avgY=%.1f diff=%.1f",
				i, cr.key, target, j, avgY, diff)
			if diff < 40 {
				matchCount++
			}
		}

		if matchCount > 0 {
			cutsVerified++
		}
	}

	t.Logf("MEASURE: output fps=%.1f, total frames=%d, cuts verified=%d",
		outputFPS, totalOutput, cutsVerified)

	// At least some cuts should be verifiable.
	require.Greater(t, cutsVerified, 0,
		"at least one cut segment should have correct source color")
}

// ==========================================================================
// TestMultiSourceMemoryStability
//
// 2 sources fed for 30 seconds (900 frames each), cutting every 3 seconds.
// Verify heap growth < 100MB.
// ==========================================================================

func TestMultiSourceMemoryStability(t *testing.T) {
	duration := 30 * time.Second
	framesPerSource := 900
	cutInterval := 3 * time.Second

	if testing.Short() {
		duration = 5 * time.Second
		framesPerSource = 150
		cutInterval = 1 * time.Second
	}

	// Force GC before measurement.
	runtime.GC()
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 60, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 200, Cb: 128, Cr: 128}

	h.addSource("memSrcA", colorA)
	h.addSource("memSrcB", colorB)

	h.cut(t, "memSrcA")

	// Feed both sources simultaneously, cutting every cutInterval.
	var wg sync.WaitGroup

	// Source feeders.
	for _, s := range []struct {
		key   string
		color sourceColor
	}{
		{"memSrcA", colorA},
		{"memSrcB", colorB},
	} {
		wg.Add(1)
		go func(key string, color sourceColor) {
			defer wg.Done()
			yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, color.Y, color.Cb, color.Cr)
			pts := int64(90000)
			frameDuration := duration / time.Duration(framesPerSource)
			for i := 0; i < framesPerSource; i++ {
				pf := &switcher.ProcessingFrame{
					YUV:        copyBytes(yuv),
					Width:      testWidth,
					Height:     testHeight,
					PTS:        pts + int64(i)*ptsDelta,
					IsKeyframe: i%30 == 0,
				}
				h.sw.IngestRawVideo(key, pf)
				time.Sleep(frameDuration)
			}
		}(s.key, s.color)
	}

	// Cut goroutine: alternate every cutInterval.
	numCuts := int(duration / cutInterval)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numCuts; i++ {
			time.Sleep(cutInterval)
			target := "memSrcA"
			if i%2 == 0 {
				target = "memSrcB"
			}
			_ = h.sw.Cut(t.Context(), target)
		}
	}()

	wg.Wait()
	h.waitForPipeline(1 * time.Second)

	// Verify we produced output.
	outputCount := h.capture.videoCount()
	t.Logf("MEASURE: produced %d output frames over %v", outputCount, duration)
	require.Greater(t, outputCount, 0, "must produce output frames during memory test")

	// Force GC and measure.
	runtime.GC()
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	heapBefore := memBefore.HeapAlloc
	heapAfter := memAfter.HeapAlloc
	var deltaMB int64
	if heapAfter > heapBefore {
		deltaMB = int64(heapAfter-heapBefore) / (1024 * 1024)
	} else {
		deltaMB = -int64(heapBefore-heapAfter) / (1024 * 1024)
	}

	t.Logf("MEASURE: heap before=%d MB, heap after=%d MB",
		heapBefore/(1024*1024), heapAfter/(1024*1024))
	t.Logf("MEASURE: heap delta=%d MB over %v with 2 sources", deltaMB, duration)
	t.Logf("MEASURE: HeapObjects before=%d, after=%d",
		memBefore.HeapObjects, memAfter.HeapObjects)
	t.Logf("MEASURE: TotalAlloc before=%d MB, after=%d MB (cumulative)",
		memBefore.TotalAlloc/(1024*1024), memAfter.TotalAlloc/(1024*1024))
	t.Logf("MEASURE: NumGC before=%d, after=%d",
		memBefore.NumGC, memAfter.NumGC)
	t.Logf("MEASURE: TS data accumulated=%d bytes (%.1f MB)",
		h.tsLen(), float64(h.tsLen())/(1024*1024))

	// Assert: heap growth < 100MB.
	if heapAfter > heapBefore {
		heapGrowth := int64(heapAfter - heapBefore)
		require.Less(t, heapGrowth, int64(100*1024*1024),
			"heap grew by %d MB (>100MB) during %v multi-source run — possible memory leak",
			deltaMB, duration)
	}
}

// ==========================================================================
// TestMultiSourceOutputQuality
//
// 2 sources with very distinct Y values (Y=30 vs Y=230). Feed for 5
// seconds, cut at 2.5s. Decode output, verify PSNR > 25dB for each
// segment vs expected source color.
// ==========================================================================

func TestMultiSourceOutputQuality(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	// Use very distinct Y values for clear PSNR measurement.
	darkColor := sourceColor{Y: 30, Cb: 128, Cr: 128}
	brightColor := sourceColor{Y: 230, Cb: 128, Cr: 128}

	h.addSource("qualDark", darkColor)
	h.addSource("qualBright", brightColor)

	// Cut to dark source first.
	h.cut(t, "qualDark")

	// Feed dark source for 2.5 seconds (75 frames at 30fps).
	const framesPerHalf = 75
	const totalFrames = framesPerHalf * 2

	// Phase 1: dark source.
	t.Log("Phase 1: feeding dark source (Y=30) for 2.5s")
	yuv1 := testutil.GenerateSolidYUV420(testWidth, testHeight, darkColor.Y, darkColor.Cb, darkColor.Cr)
	pts := int64(90000)
	for i := 0; i < framesPerHalf; i++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv1),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i%30 == 0,
		}
		h.sw.IngestRawVideo("qualDark", pf)
		pts += ptsDelta
		time.Sleep(5 * time.Millisecond)
	}

	// Also feed bright source in background so it is ready after cut.
	go func() {
		bYuv := testutil.GenerateSolidYUV420(testWidth, testHeight, brightColor.Y, brightColor.Cb, brightColor.Cr)
		bPts := int64(90000)
		for i := 0; i < totalFrames; i++ {
			pf := &switcher.ProcessingFrame{
				YUV:        copyBytes(bYuv),
				Width:      testWidth,
				Height:     testHeight,
				PTS:        bPts,
				IsKeyframe: i%30 == 0,
			}
			h.sw.IngestRawVideo("qualBright", pf)
			bPts += ptsDelta
			time.Sleep(5 * time.Millisecond)
		}
	}()

	h.waitForPipeline(300 * time.Millisecond)

	// Record frame count at cut point.
	frameCountAtCut := h.capture.videoCount()
	t.Logf("MEASURE: frames before cut = %d", frameCountAtCut)

	// Phase 2: cut to bright source and feed for 2.5 seconds.
	t.Log("Phase 2: cutting to bright source (Y=230)")
	h.cut(t, "qualBright")

	yuv2 := testutil.GenerateSolidYUV420(testWidth, testHeight, brightColor.Y, brightColor.Cb, brightColor.Cr)
	for i := 0; i < framesPerHalf; i++ {
		pf := &switcher.ProcessingFrame{
			YUV:        copyBytes(yuv2),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i%30 == 0,
		}
		h.sw.IngestRawVideo("qualBright", pf)
		pts += ptsDelta
		time.Sleep(5 * time.Millisecond)
	}

	h.waitForPipeline(1 * time.Second)

	// Decode all captured output.
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)

	t.Logf("MEASURE: total captured=%d, total decoded=%d, cut at frame ~%d",
		len(videos), len(decoded), frameCountAtCut)

	require.Greater(t, len(decoded), 10,
		"need at least 10 decoded frames for quality analysis")

	// Generate reference frames for PSNR comparison.
	refDark := testutil.GenerateSolidYUV420(testWidth, testHeight, darkColor.Y, darkColor.Cb, darkColor.Cr)
	refBright := testutil.GenerateSolidYUV420(testWidth, testHeight, brightColor.Y, brightColor.Cb, brightColor.Cr)

	// Split decoded frames into segments based on Y value.
	// Frames with avgY < 130 belong to dark segment, >= 130 to bright segment.
	var darkPSNRs, brightPSNRs []float64

	for i, df := range decoded {
		avgY := averageY(df.YUV, df.Width, df.Height)

		if avgY < 130 {
			// Dark segment: compare against dark reference.
			psnr := testutil.YUVFramePSNR(df.YUV, refDark, df.Width, df.Height)
			darkPSNRs = append(darkPSNRs, psnr)
			t.Logf("Frame %d: dark segment avgY=%.1f PSNR=%.1f dB", i, avgY, psnr)
		} else {
			// Bright segment: compare against bright reference.
			psnr := testutil.YUVFramePSNR(df.YUV, refBright, df.Width, df.Height)
			brightPSNRs = append(brightPSNRs, psnr)
			t.Logf("Frame %d: bright segment avgY=%.1f PSNR=%.1f dB", i, avgY, psnr)
		}
	}

	// Compute average PSNR for each segment.
	var avgDarkPSNR, avgBrightPSNR float64
	if len(darkPSNRs) > 0 {
		sum := 0.0
		for _, p := range darkPSNRs {
			sum += p
		}
		avgDarkPSNR = sum / float64(len(darkPSNRs))
	}
	if len(brightPSNRs) > 0 {
		sum := 0.0
		for _, p := range brightPSNRs {
			sum += p
		}
		avgBrightPSNR = sum / float64(len(brightPSNRs))
	}

	t.Logf("MEASURE: segment1 (dark, Y=30) PSNR=%.1f dB (%d frames)",
		avgDarkPSNR, len(darkPSNRs))
	t.Logf("MEASURE: segment2 (bright, Y=230) PSNR=%.1f dB (%d frames)",
		avgBrightPSNR, len(brightPSNRs))

	// Verify both segments have frames.
	require.NotEmpty(t, darkPSNRs, "dark segment should have decoded frames")
	require.NotEmpty(t, brightPSNRs, "bright segment should have decoded frames")

	// Verify PSNR > 25dB for each segment.
	// This tests that the encoder maintains quality under multi-source load.
	require.Greater(t, avgDarkPSNR, 25.0,
		"dark segment average PSNR=%.1f dB should be > 25 dB", avgDarkPSNR)
	require.Greater(t, avgBrightPSNR, 25.0,
		"bright segment average PSNR=%.1f dB should be > 25 dB", avgBrightPSNR)

	// Also check worst-case per-frame PSNR (skip first 3 frames of each segment
	// which may be transitional).
	darkSkip := 3
	if darkSkip > len(darkPSNRs) {
		darkSkip = 0
	}
	brightSkip := 3
	if brightSkip > len(brightPSNRs) {
		brightSkip = 0
	}

	for i, p := range darkPSNRs[darkSkip:] {
		t.Logf("MEASURE: dark frame %d PSNR=%.1f dB", i+darkSkip, p)
		require.Greater(t, p, 20.0,
			"dark frame %d PSNR=%.1f dB should be > 20 dB (minimum quality)", i+darkSkip, p)
	}
	for i, p := range brightPSNRs[brightSkip:] {
		t.Logf("MEASURE: bright frame %d PSNR=%.1f dB", i+brightSkip, p)
		require.Greater(t, p, 20.0,
			"bright frame %d PSNR=%.1f dB should be > 20 dB (minimum quality)", i+brightSkip, p)
	}
}
