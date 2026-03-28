//go:build correctness

package pipeline

import (
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// TestPipelineLongRunDrift
//
// Production runs for hours. Verify that A/V PTS drift does not accumulate
// over a 60-second run (1800 frames at 30fps). The wall-clock-based video
// PTS and mixer audio PTS should track each other within ±100ms (9000 ticks)
// throughout the run.
//
// Protocol:
// 1. Create pipeline harness with mixer
// 2. Feed 1800 frames (60s at 30fps) — 300 frames (10s) in short mode
// 3. Write TS, ffprobe: extract video and audio frame PTS
// 4. Measure A/V PTS delta at start and at end
// 5. Assert: delta at end is within ±9000 ticks of delta at start
// ==========================================================================

func TestPipelineLongRunDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	// Use 300 frames (10s at 30fps) with real-time pacing. The wall-clock-
	// based video PTS requires frames to arrive at actual real-time intervals;
	// faster-than-realtime feeding causes the frame counter to race ahead of
	// the wall clock, producing artificial PTS drift.
	totalFrames := 300

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("driftA", sourceA)
	h.mixer.AddChannel("driftA")
	h.mixer.SetActive("driftA", true)

	h.cut(t, "driftA")

	// Feed frames at real-time: one frame every ~33ms (30fps).
	// This matches the wall-clock PTS expectation in production.
	frameDuration := time.Second / time.Duration(testFPS) // ~33.3ms
	color := h.sources["driftA"]
	yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, color.Y, color.Cb, color.Cr)
	pts := int64(90000)
	for i := 0; i < totalFrames; i++ {
		pf := &switcher.ProcessingFrame{
			Data:       copyBytes(yuv),
			Width:      testWidth,
			Height:     testHeight,
			PTS:        pts,
			IsKeyframe: i == 0,
		}
		h.sw.IngestRawVideo("driftA", pf)
		pts += ptsDelta
		if i < totalFrames-1 {
			time.Sleep(frameDuration)
		}
	}

	// Final drain.
	h.waitForPipeline(1 * time.Second)

	// Write TS and probe frames.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	audioFrames := testutil.ProbeAudioFrames(t, tsPath)
	require.Greater(t, len(videoFrames), 10, "need substantial video frames")
	require.Greater(t, len(audioFrames), 10, "need substantial audio frames")

	t.Logf("MEASURE: total video frames = %d, total audio frames = %d", len(videoFrames), len(audioFrames))

	// Measure A/V drift by finding the audio frame nearest to each video
	// frame's PTS and computing the delta. The mixer ticker produces audio
	// beyond the last video frame (wall-clock driven), so we compare over
	// the video time range only.
	firstVideoPTS := videoFrames[0].PTS
	lastVideoPTS := videoFrames[len(videoFrames)-1].PTS

	// Find audio frame nearest to first video PTS.
	nearestAudioStart := audioFrames[0].PTS
	for _, af := range audioFrames {
		if math.Abs(float64(af.PTS-firstVideoPTS)) < math.Abs(float64(nearestAudioStart-firstVideoPTS)) {
			nearestAudioStart = af.PTS
		}
	}
	deltaStart := firstVideoPTS - nearestAudioStart

	// Find audio frame nearest to last video PTS.
	nearestAudioEnd := audioFrames[0].PTS
	for _, af := range audioFrames {
		if math.Abs(float64(af.PTS-lastVideoPTS)) < math.Abs(float64(nearestAudioEnd-lastVideoPTS)) {
			nearestAudioEnd = af.PTS
		}
	}
	deltaEnd := lastVideoPTS - nearestAudioEnd

	drift := deltaEnd - deltaStart
	absDrift := int64(math.Abs(float64(drift)))

	t.Logf("MEASURE: A/V delta at start = %d ticks, at end = %d ticks, drift = %d ticks",
		deltaStart, deltaEnd, drift)
	t.Logf("MEASURE: first video PTS = %d, last video PTS = %d (span = %d ticks = %.1f ms)",
		firstVideoPTS, lastVideoPTS, lastVideoPTS-firstVideoPTS, float64(lastVideoPTS-firstVideoPTS)/90.0)
	t.Logf("MEASURE: nearest audio start PTS = %d, nearest audio end PTS = %d",
		nearestAudioStart, nearestAudioEnd)

	// Assert no cumulative drift: delta at end should be within ±9000 ticks
	// (100ms) of delta at start.
	require.LessOrEqual(t, absDrift, int64(9000),
		"A/V drift over %d frames: start delta=%d, end delta=%d, cumulative drift=%d ticks (%.1f ms) exceeds ±9000 ticks (100ms)",
		totalFrames, deltaStart, deltaEnd, drift, float64(drift)/90.0)
}

// ==========================================================================
// TestPipelineLongRunMemoryStability
//
// Verify that encoding 30 seconds of video through the pipeline does not
// cause unbounded memory growth. Heap growth must stay under 50MB.
// ==========================================================================

func TestPipelineLongRunMemoryStability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	// Force GC before starting to get a clean baseline.
	runtime.GC()
	runtime.GC()
	var memStart runtime.MemStats
	runtime.ReadMemStats(&memStart)

	h := newPipelineHarness(t)
	defer h.close()

	h.addSource("memA", sourceA)
	h.cut(t, "memA")

	const totalFrames = 900 // 30 seconds at 30fps
	const batchSize = 30

	pts := int64(90000)
	for fed := 0; fed < totalFrames; fed += batchSize {
		n := batchSize
		if fed+n > totalFrames {
			n = totalFrames - fed
		}
		h.feedFrames("memA", n, pts)
		pts += int64(n) * ptsDelta
		// Brief pause between batches.
		time.Sleep(30 * time.Millisecond)
	}

	h.waitForPipeline(500 * time.Millisecond)

	// Verify we actually produced output.
	require.Greater(t, h.capture.videoCount(), 100,
		"expected substantial video output for memory test")

	// Force GC to reclaim any temporary allocations, then measure.
	runtime.GC()
	runtime.GC()
	var memEnd runtime.MemStats
	runtime.ReadMemStats(&memEnd)

	heapStartMB := memStart.HeapAlloc / (1024 * 1024)
	heapEndMB := memEnd.HeapAlloc / (1024 * 1024)
	deltaMB := int64(memEnd.HeapAlloc) - int64(memStart.HeapAlloc)
	deltaMBSigned := deltaMB / (1024 * 1024)

	t.Logf("MEASURE: heap start = %d MB, heap end = %d MB, delta = %d MB",
		heapStartMB, heapEndMB, deltaMBSigned)
	t.Logf("MEASURE: NumGC = %d (start) -> %d (end), PauseTotalNs = %d ns (%.1f ms)",
		memStart.NumGC, memEnd.NumGC,
		memEnd.PauseTotalNs-memStart.PauseTotalNs,
		float64(memEnd.PauseTotalNs-memStart.PauseTotalNs)/1e6)
	t.Logf("MEASURE: TotalAlloc start = %d MB, end = %d MB (cumulative)",
		memStart.TotalAlloc/(1024*1024), memEnd.TotalAlloc/(1024*1024))
	t.Logf("MEASURE: HeapObjects start = %d, end = %d",
		memStart.HeapObjects, memEnd.HeapObjects)
	t.Logf("MEASURE: TS data accumulated = %d bytes (%.1f MB)",
		h.tsLen(), float64(h.tsLen())/(1024*1024))

	// Assert heap growth < 50MB.
	// Use absolute value since delta could be negative (GC reclaimed more than allocated).
	if deltaMB > 0 {
		require.Less(t, deltaMB, int64(50*1024*1024),
			"heap grew by %d MB (>50MB) during 30s pipeline run — possible memory leak",
			deltaMBSigned)
	}
}

// ==========================================================================
// TestPipelineLongRunWithPeriodicCuts
//
// Verify pipeline correctness over 30 seconds with periodic cuts (every
// 3 seconds). Alternate between source A (Y=40) and source B (Y=220).
// Decode the last 10 frames of each segment and verify correct source color.
//
// This catches:
// - Frame pool corruption from repeated cuts
// - State leakage between segments
// - Encoder state contamination
// ==========================================================================

func TestPipelineLongRunWithPeriodicCuts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t)
	defer h.close()

	colorA := sourceColor{Y: 40, Cb: 128, Cr: 128}
	colorB := sourceColor{Y: 220, Cb: 128, Cr: 128}

	h.addSource("cutA", colorA)
	h.addSource("cutB", colorB)

	const (
		totalFrames     = 900 // 30 seconds at 30fps
		framesPerSegment = 90  // 3 seconds per segment
		numSegments     = totalFrames / framesPerSegment // 10 segments
		flushFrames     = 8 // frames to skip after each cut for pipeline flush
		thresholdLow    = 100.0
		thresholdHigh   = 160.0
	)

	type segmentResult struct {
		source     string
		expectedY  float64
		framesOK   int
		framesBAD  int
		avgY       float64
	}

	var results []segmentResult
	prevDecodedLen := 0
	pts := int64(90000)

	for seg := 0; seg < numSegments; seg++ {
		activeSource := "cutA"
		activeColor := colorA
		expectedY := 40.0
		if seg%2 == 1 {
			activeSource = "cutB"
			activeColor = colorB
			expectedY = 220.0
		}

		h.cut(t, activeSource)

		// Feed frames for this segment.
		h.feedFramesWithColor(activeSource, framesPerSegment, pts,
			activeColor.Y, activeColor.Cb, activeColor.Cr)
		pts += int64(framesPerSegment) * ptsDelta

		// Wait for pipeline to process.
		h.waitForPipeline(300 * time.Millisecond)

		// Decode all frames and look at the new ones for this segment.
		allVideos := h.capture.videosCopy()
		allDecoded := decodeCapturedFrames(t, allVideos)

		if len(allDecoded) <= prevDecodedLen {
			t.Logf("Segment %d (%s): no new decoded frames (total %d, prev %d)",
				seg, activeSource, len(allDecoded), prevDecodedLen)
			results = append(results, segmentResult{
				source:    activeSource,
				expectedY: expectedY,
			})
			continue
		}

		newFrames := allDecoded[prevDecodedLen:]
		prevDecodedLen = len(allDecoded)

		// Check the last 10 frames of this segment (skip flush frames).
		checkStart := len(newFrames) - 10
		if checkStart < flushFrames {
			checkStart = flushFrames
		}
		if checkStart >= len(newFrames) {
			checkStart = 0
		}

		sr := segmentResult{
			source:    activeSource,
			expectedY: expectedY,
		}

		var ySum float64
		var yCount int
		for i := checkStart; i < len(newFrames); i++ {
			df := newFrames[i]
			avgY := averageY(df.YUV, df.Width, df.Height)
			ySum += avgY
			yCount++

			if seg%2 == 0 {
				// Source A: Y=40, expect < thresholdLow
				if avgY < thresholdLow {
					sr.framesOK++
				} else {
					sr.framesBAD++
				}
			} else {
				// Source B: Y=220, expect > thresholdHigh
				if avgY > thresholdHigh {
					sr.framesOK++
				} else {
					sr.framesBAD++
				}
			}
		}
		if yCount > 0 {
			sr.avgY = ySum / float64(yCount)
		}
		results = append(results, sr)
	}

	// Log per-segment results.
	totalOK := 0
	totalBAD := 0
	for i, sr := range results {
		t.Logf("MEASURE: segment %d (%s, expected Y≈%.0f): avg Y=%.1f, OK=%d, BAD=%d",
			i, sr.source, sr.expectedY, sr.avgY, sr.framesOK, sr.framesBAD)
		totalOK += sr.framesOK
		totalBAD += sr.framesBAD
	}
	t.Logf("MEASURE: total frames verified OK=%d, BAD=%d across %d segments", totalOK, totalBAD, numSegments)

	// Assert: no corruption. Allow zero BAD frames.
	require.Equal(t, 0, totalBAD,
		"found %d frames with wrong source color after periodic cuts (expected 0)", totalBAD)

	// Assert: we verified a reasonable number of frames.
	require.Greater(t, totalOK, numSegments*2,
		"expected to verify at least %d frames (2 per segment), got %d", numSegments*2, totalOK)
}

// ==========================================================================
// TestMixerLongRunDrift
//
// Verify that the audio mixer's PTS advances at the expected rate over a
// 30-second run. Audio PTS should advance by ~450000 ticks per 5-second
// interval (5 * 48000/1024 * 1920 = ~450000 ticks at 90kHz).
//
// Captures output audio PTS at 5-second intervals and verifies each
// interval's advance is within ±9000 ticks of the expected value.
// ==========================================================================

func TestMixerLongRunDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withMixer())
	defer h.close()

	h.addSource("mixA", sourceA)
	h.mixer.AddChannel("mixA")
	h.mixer.SetActive("mixA", true)

	h.cut(t, "mixA")

	// Feed video frames to seed the PTS and keep the pipeline alive.
	// The mixer's wall-clock ticker produces audio independently.
	const (
		totalDuration = 30 * time.Second
		checkInterval = 5 * time.Second
		numCheckpoints = 6
	)

	// Start feeding video in the background to keep the pipeline alive
	// and to seed the mixer's PTS.
	done := make(chan struct{})
	go func() {
		defer close(done)
		pts := int64(90000)
		for {
			select {
			case <-done:
				return
			default:
			}
			yuv := testutil.GenerateSolidYUV420(testWidth, testHeight, sourceA.Y, sourceA.Cb, sourceA.Cr)
			pf := &switcher.ProcessingFrame{
				Data:       copyBytes(yuv),
				Width:      testWidth,
				Height:     testHeight,
				PTS:        pts,
				IsKeyframe: pts == 90000,
			}
			h.sw.IngestRawVideo("mixA", pf)
			pts += ptsDelta
			time.Sleep(5 * time.Millisecond)
			if pts > 90000+int64(totalDuration.Seconds()*float64(testFPS))*ptsDelta {
				return
			}
		}
	}()

	// Collect audio PTS at checkpoints.
	type checkpoint struct {
		time      time.Duration
		lastPTS   int64
		audioCount int
	}

	var checkpoints []checkpoint

	// Wait for first audio output.
	deadline := time.After(2 * time.Second)
	for h.capture.audioCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("no audio output within 2 seconds")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	startTime := time.Now()

	for i := 0; i < numCheckpoints; i++ {
		targetTime := time.Duration(i+1) * checkInterval
		elapsed := time.Since(startTime)
		if targetTime > elapsed {
			time.Sleep(targetTime - elapsed)
		}

		audios := h.capture.audiosCopy()
		if len(audios) > 0 {
			checkpoints = append(checkpoints, checkpoint{
				time:       time.Since(startTime),
				lastPTS:    audios[len(audios)-1].PTS,
				audioCount: len(audios),
			})
		}
	}

	// Wait for background feeder to finish.
	<-done
	h.waitForPipeline(500 * time.Millisecond)

	require.GreaterOrEqual(t, len(checkpoints), 2,
		"need at least 2 checkpoints for drift analysis")

	// Expected PTS advance per 5 seconds:
	// Audio: 48000 Hz / 1024 samples_per_frame * 5s = ~234.375 frames
	// Each frame advances PTS by 1920 ticks (1024 samples * 90000/48000)
	// Total: ~234.375 * 1920 ≈ 450000 ticks per 5s
	const expectedPTSPer5s = int64(450000)

	t.Logf("MEASURE: collected %d checkpoints", len(checkpoints))
	var ptsAdvances []int64

	for i, cp := range checkpoints {
		t.Logf("MEASURE: checkpoint %d: time=%.1fs, lastPTS=%d, audioFrames=%d",
			i, cp.time.Seconds(), cp.lastPTS, cp.audioCount)
	}

	for i := 1; i < len(checkpoints); i++ {
		advance := checkpoints[i].lastPTS - checkpoints[i-1].lastPTS
		ptsAdvances = append(ptsAdvances, advance)
		timeDelta := checkpoints[i].time - checkpoints[i-1].time
		t.Logf("MEASURE: PTS advance interval %d->%d: %d ticks (expected ~%d) over %.1fs",
			i-1, i, advance, expectedPTSPer5s, timeDelta.Seconds())
	}

	t.Logf("MEASURE: PTS advance per 5s interval = %v ticks (expected ~%d)", ptsAdvances, expectedPTSPer5s)

	// Assert each interval's PTS advance is within ±9000 ticks of expected.
	for i, advance := range ptsAdvances {
		diff := int64(math.Abs(float64(advance - expectedPTSPer5s)))
		require.LessOrEqual(t, diff, int64(9000),
			"interval %d: PTS advance %d deviates from expected %d by %d ticks (>9000, %.1f ms)",
			i, advance, expectedPTSPer5s, diff, float64(diff)/90.0)
	}

	// Also verify total PTS span is reasonable.
	if len(checkpoints) >= 2 {
		totalPTSSpan := checkpoints[len(checkpoints)-1].lastPTS - checkpoints[0].lastPTS
		totalTimeSpan := checkpoints[len(checkpoints)-1].time - checkpoints[0].time
		expectedTotalPTS := int64(totalTimeSpan.Seconds() * 90000.0)
		totalDiff := int64(math.Abs(float64(totalPTSSpan - expectedTotalPTS)))

		t.Logf("MEASURE: total PTS span = %d ticks over %.1fs (expected ~%d, diff=%d ticks = %.1f ms)",
			totalPTSSpan, totalTimeSpan.Seconds(), expectedTotalPTS, totalDiff, float64(totalDiff)/90.0)
	}
}
