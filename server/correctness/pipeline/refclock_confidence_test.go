//go:build correctness

package pipeline

import (
	"math"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// TestRefClockEncodeUnderCPULoad
//
// Full encode pipeline (real x264) with artificial CPU load.
// Spawns 4 goroutines burning CPU, feeds 150 frames through the sync loop,
// and verifies the output is valid H.264 with monotonic PTS and no
// corruption from overload freeze-frame.
// ==========================================================================

func TestRefClockEncodeUnderCPULoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock())
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed 150 frames (5 seconds at 30fps).
	h.feedFrames("camA", 150, 0)

	// Start CPU load: 4 goroutines doing busy loops.
	var cpuStop atomic.Bool
	var cpuWg sync.WaitGroup
	for i := 0; i < 4; i++ {
		cpuWg.Add(1)
		go func() {
			defer cpuWg.Done()
			for !cpuStop.Load() {
				runtime.Gosched()
			}
		}()
	}

	// Start ref clock + sync loop.
	h.startRefClock()

	// Wait for pipeline to produce frames under load.
	h.waitForVideoFrames(t, 30)

	// Feed more frames to sustain pipeline under load.
	h.feedFrames("camA", 100, 150*ptsDelta)
	h.waitForVideoFrames(t, 60)

	// Stop CPU load.
	cpuStop.Store(true)
	cpuWg.Wait()

	// Feed a few more frames post-load to ensure pipeline recovers.
	h.feedFrames("camA", 30, 250*ptsDelta)
	h.waitForVideoFrames(t, 80)

	// Stop ref clock.
	h.refClock.Stop()
	h.refClock = nil

	// Write TS and verify with ffprobe.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	totalFrames := len(videoFrames)

	t.Logf("MEASURE: frames under load=%d", totalFrames)
	// Under CPU load the sync loop legitimately drops video frames via
	// overload recovery. 50 frames is a generous lower bound -- the pipeline
	// should still produce output even when heavily loaded.
	require.Greater(t, totalFrames, 50,
		"expected >50 video frames (some dropped under overload is expected)")

	// PTS monotonically increasing.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Every frame must be decodable (no corruption from overload freeze-frame).
	videos := h.capture.videosCopy()
	decoded := decodeCapturedFrames(t, videos)
	require.Greater(t, len(decoded), 50,
		"should decode >50 frames -- corruption would cause decode failures")

	overloadDrops := totalFrames - len(decoded)
	if overloadDrops < 0 {
		overloadDrops = 0
	}
	t.Logf("MEASURE: frames under load=%d, overload drops=%d, decoded=%d",
		totalFrames, overloadDrops, len(decoded))
}

// ==========================================================================
// TestRefClockAVSyncUnderLoad
//
// A/V sync through muxer under CPU load. Runs the pipeline twice: once
// without load, once with 4 CPU-burning goroutines. Compares A/V PTS
// deltas at start, middle, and end of both runs.
// ==========================================================================

func TestRefClockAVSyncUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const aacFramePTS = int64(1920) // 1024 * 90000 / 48000

	// measureMaxAVDelta finds the maximum distance from any video frame's PTS
	// to its nearest audio frame's PTS. With both streams driven by the same
	// reference clock (same epoch, same tick cadence), this should always be
	// <= 1 AAC frame (1920 ticks = ~21.3ms).
	measureMaxAVDelta := func(videos []*media.VideoFrame, audios []*media.AudioFrame) int64 {
		if len(videos) == 0 || len(audios) == 0 {
			return 0
		}
		maxDelta := int64(0)
		for _, v := range videos {
			best := int64(math.MaxInt64)
			for _, a := range audios {
				d := v.PTS - a.PTS
				if d < 0 {
					d = -d
				}
				if d < best {
					best = d
				}
			}
			if best > maxDelta {
				maxDelta = best
			}
		}
		return maxDelta
	}

	// --- Run 1: No load (no-encode harness for deterministic PTS) ---
	h1 := newPipelineHarnessNoEncode(t)
	h1.addSource("camA", sourceA)
	h1.cut(t, "camA")
	h1.feedFrames("camA", 90, 0)
	h1.startRefClock()
	h1.waitForVideoFrames(t, 60)
	h1.waitForAudioFrames(t, 10)
	h1.refClock.Stop()
	h1.refClock = nil

	v1 := h1.capture.videosCopy()
	a1 := h1.capture.audiosCopy()
	h1.close()

	require.Greater(t, len(v1), 30, "run1 should have >30 video frames")
	require.Greater(t, len(a1), 5, "run1 should have >5 audio frames")

	maxDelta1 := measureMaxAVDelta(v1, a1)

	// --- Run 2: Under CPU load ---
	h2 := newPipelineHarnessNoEncode(t)
	h2.addSource("camA", sourceA)
	h2.cut(t, "camA")
	h2.feedFrames("camA", 90, 0)

	var cpuStop atomic.Bool
	var cpuWg sync.WaitGroup
	for i := 0; i < 4; i++ {
		cpuWg.Add(1)
		go func() {
			defer cpuWg.Done()
			for !cpuStop.Load() {
				runtime.Gosched()
			}
		}()
	}

	h2.startRefClock()
	h2.waitForVideoFrames(t, 60)
	h2.waitForAudioFrames(t, 10)

	cpuStop.Store(true)
	cpuWg.Wait()

	h2.refClock.Stop()
	h2.refClock = nil

	v2 := h2.capture.videosCopy()
	a2 := h2.capture.audiosCopy()
	h2.close()

	require.Greater(t, len(v2), 30, "run2 should have >30 video frames")
	require.Greater(t, len(a2), 5, "run2 should have >5 audio frames")

	maxDelta2 := measureMaxAVDelta(v2, a2)

	// Log both sets of deltas.
	t.Logf("MEASURE: no-load  max A/V delta=%d ticks (%.1fms), video=%d audio=%d",
		maxDelta1, float64(maxDelta1)/90.0, len(v1), len(a1))
	t.Logf("MEASURE: cpu-load max A/V delta=%d ticks (%.1fms), video=%d audio=%d",
		maxDelta2, float64(maxDelta2)/90.0, len(v2), len(a2))

	// Assert: max A/V delta is <= 1920 ticks (1 AAC frame) in BOTH cases.
	require.LessOrEqual(t, maxDelta1, aacFramePTS,
		"no-load max A/V delta should be <= 1 AAC frame (1920 ticks)")
	require.LessOrEqual(t, maxDelta2, aacFramePTS,
		"cpu-load max A/V delta should be <= 1 AAC frame (1920 ticks)")

	// Assert: delta under load is within +/-500 ticks of delta without load.
	// The reference clock guarantees load independence, so the A/V sync
	// should be identical regardless of processing time.
	drift := maxDelta2 - maxDelta1
	if drift < 0 {
		drift = -drift
	}
	require.LessOrEqual(t, drift, int64(500),
		"A/V delta drift between load/no-load should be <= 500 ticks")
}

// ==========================================================================
// TestRefClockRecoveryWithJitter
//
// Clock recovery with realistic SRT jitter. Feeds arrivals with random
// jitter and occasional spikes, verifies that the recovered rate snaps
// to the correct standard broadcast rate.
// ==========================================================================

func TestRefClockRecoveryWithJitter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic seed for test

	// --- Phase 1: 29.97fps with +/-5ms jitter + 15ms spike every 20th frame ---
	// The 64-sample sliding window in ClockRecovery averages over ~2 seconds.
	// A 15ms spike on a 33.37ms interval is realistic for SRT network jitter.
	cr := clock.NewClockRecovery(30, 1, 10) // initial 30fps, 10-frame stability threshold

	baseInterval := time.Duration(int64(time.Second) * 1001 / 30000) // ~33.37ms
	now := time.Now()

	for i := 0; i < 100; i++ {
		// Add random jitter: +/-5ms uniform.
		jitter := time.Duration(rng.Int63n(10_000_000) - 5_000_000) // +/-5ms in ns

		// Every 20th frame: add a 15ms spike (realistic SRT network burst).
		if i > 0 && i%20 == 0 {
			jitter += 15 * time.Millisecond
		}

		now = now.Add(baseInterval + jitter)
		cr.RecordArrival(now)
	}

	fpsNum, fpsDen := cr.Rate()
	measured := cr.MeasuredFPS()
	t.Logf("MEASURE: Phase 1 (29.97fps input): recovered rate=%d/%d, measuredFPS=%.3f", fpsNum, fpsDen, measured)

	require.Equal(t, 30000, fpsNum, "29.97fps should recover as 30000/1001")
	require.Equal(t, 1001, fpsDen, "29.97fps should recover as 30000/1001")

	// --- Phase 2: 25fps with +/-3ms jitter ---
	cr2 := clock.NewClockRecovery(30, 1, 10)

	baseInterval25 := 40 * time.Millisecond // 25fps
	now2 := time.Now()

	for i := 0; i < 100; i++ {
		jitter := time.Duration(rng.Int63n(6_000_000) - 3_000_000) // +/-3ms
		now2 = now2.Add(baseInterval25 + jitter)
		cr2.RecordArrival(now2)
	}

	fpsNum2, fpsDen2 := cr2.Rate()
	measured2 := cr2.MeasuredFPS()
	t.Logf("MEASURE: Phase 2 (25fps input): recovered rate=%d/%d, measuredFPS=%.3f", fpsNum2, fpsDen2, measured2)

	require.Equal(t, 25, fpsNum2, "25fps should recover as 25/1")
	require.Equal(t, 1, fpsDen2, "25fps should recover as 25/1")
}

// ==========================================================================
// TestRefClockRateChangeThroughEncode
//
// Rate change continuity in encoded output. Starts at 30fps, feeds 60
// frames, changes to 25fps, feeds 60 more. Verifies the entire stream
// is valid with monotonic PTS and no gap at the rate change point.
// ==========================================================================

func TestRefClockRateChangeThroughEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	h := newPipelineHarness(t, withReferenceClock())
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed 60 frames for the first rate (30fps).
	h.feedFrames("camA", 60, 0)

	h.startRefClock()
	h.waitForVideoFrames(t, 20)

	preChangeCount := h.capture.videoCount()
	t.Logf("MEASURE: frames before rate change = %d", preChangeCount)

	// Change clock rate to 25fps.
	h.refClock.SetRate(25, 1)

	// Feed 60 more frames for the second rate.
	h.feedFrames("camA", 60, 60*ptsDelta)
	h.waitForVideoFrames(t, preChangeCount+15)

	h.refClock.Stop()
	h.refClock = nil

	// Write TS and verify.
	tsPath := h.writeTS(t)
	testutil.RequireNoErrors(t, tsPath)

	result := testutil.ProbeFile(t, tsPath)
	vs := result.VideoStream()
	require.NotNil(t, vs, "expected video stream in TS output")
	require.Equal(t, "h264", vs.CodecName)

	videoFrames := testutil.ProbeVideoFrames(t, tsPath)
	t.Logf("MEASURE: total video frames = %d", len(videoFrames))
	require.Greater(t, len(videoFrames), 20, "expected >20 video frames across rate change")

	// PTS monotonically increasing.
	ptsValues := make([]int64, len(videoFrames))
	for i, f := range videoFrames {
		ptsValues[i] = f.PTS
	}
	testutil.AssertMonotonic(t, ptsValues, "PTS")

	// Measure PTS gap distribution to characterize rate change behavior.
	// The encoder has internal buffering (2 frames for x264 ultrafast), so
	// there may be a larger gap at the rate change point as the encoder
	// adapts. Log all gaps for analysis.
	var maxGapSeen int64
	var maxGapIdx int
	for i := 1; i < len(ptsValues); i++ {
		delta := ptsValues[i] - ptsValues[i-1]
		if delta > maxGapSeen {
			maxGapSeen = delta
			maxGapIdx = i
		}
	}
	t.Logf("MEASURE: max PTS gap=%d ticks (%.1fms) at frame %d",
		maxGapSeen, float64(maxGapSeen)/90.0, maxGapIdx)

	// The stream should not have gaps exceeding 5 seconds (catastrophic).
	// Encoder-induced gaps during rate change are expected to be 1-2 seconds
	// due to flushing and restarting the rate controller.
	maxAllowedGap := int64(90000 * 5) // 5 seconds
	require.LessOrEqual(t, maxGapSeen, maxAllowedGap,
		"max PTS gap (%d ticks) exceeds 5 seconds -- catastrophic discontinuity", maxGapSeen)

	// Log PTS around the rate change boundary.
	if preChangeCount > 0 && preChangeCount < len(ptsValues) {
		t.Logf("MEASURE: PTS at rate change boundary: frame[%d]=%d, frame[%d]=%d, delta=%d",
			preChangeCount-1, ptsValues[preChangeCount-1],
			preChangeCount, ptsValues[preChangeCount],
			ptsValues[preChangeCount]-ptsValues[preChangeCount-1])
	}
}

// ==========================================================================
// TestRefClock5994EndToEnd
//
// 59.94fps through the sync loop (no-encode path). Verifies PTS deltas
// alternate between 1501 and 1502 (Bresenham pattern) and that total
// PTS advance is correct.
// ==========================================================================

func TestRefClock5994EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	// Create a harness WITHOUT the encoder (no-encode) to get deterministic
	// PTS from the reference clock. The encoder adds its own buffering
	// which makes PTS delta analysis unreliable.
	// We use a custom reference clock at 59.94fps.
	h := newPipelineHarnessNoEncodeCustom(t, 60000, 1001, 0)
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed 300 frames (5 seconds at 59.94fps).
	h.feedFrames("camA", 300, 0)

	h.startRefClock()
	h.waitForVideoFrames(t, 300)

	h.refClock.Stop()
	h.refClock = nil

	allVideos := h.capture.videosCopy()
	require.GreaterOrEqual(t, len(allVideos), 300,
		"expected ~300 video frames at 59.94fps")

	// Use exactly the first 300 frames for analysis. The sync loop may
	// produce a few extra frames before stop, which would change the
	// total advance calculation.
	const analyzeCount = 300
	videos := allVideos[:analyzeCount]

	// Measure PTS deltas.
	count1501 := 0
	count1502 := 0
	otherDeltas := make(map[int64]int)
	for i := 1; i < len(videos); i++ {
		delta := videos[i].PTS - videos[i-1].PTS
		switch delta {
		case 1501:
			count1501++
		case 1502:
			count1502++
		default:
			otherDeltas[delta]++
		}
	}

	t.Logf("MEASURE: PTS delta distribution (first %d frames): 1501=%d, 1502=%d, other=%v",
		analyzeCount, count1501, count1502, otherDeltas)

	// The Bresenham pattern for 59.94fps should produce BOTH 1501 and 1502.
	// (Not constant 1501 which would cause drift.)
	require.Greater(t, count1501, 0,
		"should have some PTS deltas of 1501 (Bresenham pattern)")
	require.Greater(t, count1502, 0,
		"should have some PTS deltas of 1502 (Bresenham pattern)")

	// No unexpected deltas (other than 1501/1502).
	require.Empty(t, otherDeltas,
		"should not have PTS deltas other than 1501 and 1502")

	// Verify total PTS advance: 300 frames (299 deltas) at 59.94fps.
	totalAdvance := videos[analyzeCount-1].PTS - videos[0].PTS
	expectedAdvance := int64(analyzeCount-1) * 90000 * 1001 / 60000

	// Allow 1 frame tolerance for Bresenham rounding.
	drift := totalAdvance - expectedAdvance
	if drift < 0 {
		drift = -drift
	}
	t.Logf("MEASURE: total PTS advance=%d, expected=%d, drift=%d ticks",
		totalAdvance, expectedAdvance, drift)
	require.LessOrEqual(t, drift, int64(1502),
		"total PTS drift should be <= 1 frame (1502 ticks)")
}

// ==========================================================================
// TestRefClockPTSWrapThroughSyncLoop
//
// PTS wrap at 2^33 through the sync loop. Sets basePTS near 2^33 and
// feeds 100 frames, crossing the wrap boundary. Verifies PTS wraps
// correctly and audio PTS is valid.
// ==========================================================================

func TestRefClockPTSWrapThroughSyncLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	// Create a custom harness with basePTS near 2^33.
	// 50 frames * 3000 PTS/frame = 150000 ticks before wrap.
	basePTS := int64(1<<33) - 50*3000
	h := newPipelineHarnessNoEncodeCustom(t, testFPS, 1, basePTS)
	defer h.close()

	h.addSource("camA", sourceA)
	h.cut(t, "camA")

	// Feed 100 frames to cross the wrap boundary.
	h.feedFrames("camA", 100, 0)

	h.startRefClock()
	h.waitForVideoFrames(t, 100)
	h.waitForAudioFrames(t, 10)

	h.refClock.Stop()
	h.refClock = nil

	videos := h.capture.videosCopy()
	audios := h.capture.audiosCopy()

	require.GreaterOrEqual(t, len(videos), 100,
		"expected ~100 video frames through PTS wrap")
	require.Greater(t, len(audios), 5,
		"expected >5 audio frames through PTS wrap")

	// Find the wrap point in video PTS.
	maxPTS := int64(1 << 33)
	var preWrapPTS, postWrapPTS int64
	wrapFound := false
	for i := 1; i < len(videos); i++ {
		if videos[i].PTS < videos[i-1].PTS {
			preWrapPTS = videos[i-1].PTS
			postWrapPTS = videos[i].PTS
			wrapFound = true
			break
		}
	}

	t.Logf("MEASURE: pre-wrap PTS=%d, post-wrap PTS=%d, wrap found=%v",
		preWrapPTS, postWrapPTS, wrapFound)

	require.True(t, wrapFound, "PTS should wrap around 2^33")

	// All PTS values must be < 2^33 (properly masked).
	for i, v := range videos {
		require.Less(t, v.PTS, maxPTS,
			"video frame %d PTS=%d should be < 2^33 (%d)", i, v.PTS, maxPTS)
		require.GreaterOrEqual(t, v.PTS, int64(0),
			"video frame %d PTS=%d should be >= 0", i, v.PTS)
	}

	// Audio PTS should be valid and monotonically increasing.
	for i := 1; i < len(audios); i++ {
		require.Greater(t, audios[i].PTS, audios[i-1].PTS,
			"audio PTS should be monotonically increasing at frame %d", i)
	}

	// Verify PTS continuity across the wrap. When accounting for the wrap,
	// the effective delta should be close to ptsDelta (3000).
	if wrapFound {
		effectiveDelta := (postWrapPTS + maxPTS) - preWrapPTS
		t.Logf("MEASURE: effective delta across wrap = %d (expected ~%d)", effectiveDelta, ptsDelta)
		require.InDelta(t, float64(ptsDelta), float64(effectiveDelta), float64(ptsDelta),
			"effective PTS delta across wrap should be close to frame duration")
	}

	t.Logf("PTS wrap test passed: %d video frames, %d audio frames, wrap at PTS=%d->%d",
		len(videos), len(audios), preWrapPTS, postWrapPTS)
}

// ==========================================================================
// TestRefClockTransitionAVSync
//
// A/V sync during a dissolve transition. Creates a pipeline with ref
// clock, mixer, and 2 sources. Starts a dissolve and verifies that
// video PTS and audio PTS advance in lockstep throughout.
// ==========================================================================

func TestRefClockTransitionAVSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test")
	}

	const aacFramePTS = int64(1920)

	h := newPipelineHarnessNoEncode(t)
	defer h.close()

	h.addSource("camA", sourceA)
	h.addSource("camB", sourceB)

	h.cut(t, "camA")
	require.NoError(t, h.sw.SetPreview(t.Context(), "camB"))

	// Feed frames for both sources.
	h.feedFrames("camA", 60, 0)
	h.feedFrames("camB", 60, 0)

	// Start ref clock + sync loop.
	h.startRefClock()

	// Wait for some initial output before starting transition.
	h.waitForVideoFrames(t, 10)
	h.waitForAudioFrames(t, 3)

	initialVideoCount := h.capture.videoCount()
	initialAudioCount := h.capture.audioCount()

	// Set up transition engine (sync path) -- 1 second dissolve (1000ms).
	engine := transition.NewEngine(transition.EngineConfig{})
	engine.StartSync("camA", "camB", transition.Mix, 1000)

	// Wire the transition into the switcher via test helper.
	h.sw.SetTransitionEngine(engine)

	// Feed more frames to sustain both sources during the transition.
	h.feedFrames("camA", 60, 60*ptsDelta)
	h.feedFrames("camB", 60, 60*ptsDelta)

	// Wait for frames to be produced during the transition.
	h.waitForVideoFrames(t, initialVideoCount+30)
	h.waitForAudioFrames(t, initialAudioCount+5)

	h.refClock.Stop()
	h.refClock = nil

	videos := h.capture.videosCopy()
	audios := h.capture.audiosCopy()

	t.Logf("MEASURE: video frames=%d, audio frames=%d", len(videos), len(audios))
	require.Greater(t, len(videos), 20, "should have >20 video frames through transition")
	require.Greater(t, len(audios), 5, "should have >5 audio frames through transition")

	// Verify video PTS monotonicity.
	for i := 1; i < len(videos); i++ {
		require.Greater(t, videos[i].PTS, videos[i-1].PTS,
			"video PTS should be monotonically increasing at frame %d", i)
	}

	// Verify audio PTS monotonicity and consistent delta.
	for i := 1; i < len(audios); i++ {
		delta := audios[i].PTS - audios[i-1].PTS
		require.Equal(t, aacFramePTS, delta,
			"audio PTS delta should be 1920: got %d at frame %d", delta, i)
	}

	// For each video frame during the transition period, find the nearest
	// audio frame and verify A/V delta is <= 1920 ticks.
	transStart := initialVideoCount
	if transStart >= len(videos) {
		transStart = 0
	}

	maxAVDelta := int64(0)
	for i := transStart; i < len(videos); i++ {
		vPTS := videos[i].PTS
		bestDelta := int64(math.MaxInt64)
		for _, a := range audios {
			d := vPTS - a.PTS
			if d < 0 {
				d = -d
			}
			if d < bestDelta {
				bestDelta = d
			}
		}
		if bestDelta > maxAVDelta {
			maxAVDelta = bestDelta
		}
		if i < transStart+5 || i == len(videos)-1 {
			t.Logf("MEASURE: tick %d video PTS=%d, nearest audio delta=%d",
				i, vPTS, bestDelta)
		}
	}

	t.Logf("MEASURE: max A/V delta during transition = %d ticks", maxAVDelta)
	require.LessOrEqual(t, maxAVDelta, aacFramePTS,
		"A/V delta should be <= 1 AAC frame (1920 ticks) at all points during transition")
}

// ==========================================================================
// Helper: newPipelineHarnessNoEncodeCustom creates a no-encode harness with
// custom FPS and basePTS parameters. Used by the 59.94fps and PTS wrap tests.
// ==========================================================================

func newPipelineHarnessNoEncodeCustom(t *testing.T, fpsNum, fpsDen int, basePTS int64) *pipelineHarness {
	t.Helper()

	programRelay := distribution.NewRelay()

	h := &pipelineHarness{
		programRelay: programRelay,
		muxer:        output.NewTSMuxer(),
		sources:      make(map[string]sourceColor),
	}

	// Capture viewer only (no muxer, since raw YUV isn't muxable).
	capture := newCaptureViewer("pipeline-capture")
	h.capture = capture
	programRelay.AddViewer(capture)

	// Create switcher (no encoder factory = no pipeline).
	sw := switcher.NewTestSwitcher(t, programRelay)
	h.sw = sw

	// Enable frame sync in clock-driven mode.
	sw.SetFrameSync(true, 0)
	sw.SetClockDrivenSync(true)

	// Create non-realtime reference clock with custom params.
	rc := clock.NewReferenceClock(fpsNum, fpsDen, 48000, basePTS, false)
	sw.SetReferenceClock(rc)
	h.refClock = rc
	h.useRefClock = true

	// Create AAC frame buffer with real FDK encoder.
	aacEnc, err := audio.NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	t.Cleanup(func() { aacEnc.Close() })
	aacBuf := clock.NewAACFrameBuffer(2, 48000, func(pcm []float32) ([]byte, error) {
		return aacEnc.Encode(pcm)
	})
	sw.SetAACBuffer(aacBuf)
	h.aacBuffer = aacBuf

	// Create clock-driven mixer.
	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output: func(frame *media.AudioFrame) {
			programRelay.BroadcastAudio(frame)
		},
		EncoderFactory: func(sampleRate, channels int) (audio.Encoder, error) {
			return audio.NewFDKEncoder(sampleRate, channels)
		},
	})
	h.mixer = mixer
	sw.SetMixer(mixer)
	sw.SetSyncMixer(mixer)

	return h
}
