//go:build correctness

package srt

import (
	"context"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	srtgo "github.com/zsiec/srtgo"
	"github.com/zsiec/switchframe/server/srt"
)

// TestSRTLoopbackFrameDelivery pushes a 3s TS file via SRT loopback and verifies
// that the expected number of video frames arrive with correct dimensions and
// monotonically increasing PTS.
func TestSRTLoopbackFrameDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT loopback test in short mode")
	}

	tsPath := generateTestTS(t, 3, 320, 240, 30)

	h := newSRTHarness(t)
	defer h.close()

	conn := h.pushTSFile("live/testcam", tsPath)
	defer func() { _ = conn.Close() }()

	// Wait for video frames with generous timeout for decode pipeline warmup.
	if !h.waitForVideoFrames(30, 30*time.Second) {
		vf := h.videoFrames()
		t.Fatalf("timeout waiting for video frames: only got %d", len(vf))
	}

	// Give extra time for remaining frames to arrive after file finishes pushing.
	time.Sleep(3 * time.Second)

	vFrames := h.videoFrames()

	// Log measured values before assertions.
	var firstPTS, lastPTS int64
	if len(vFrames) > 0 {
		firstPTS = vFrames[0].PTS
		lastPTS = vFrames[len(vFrames)-1].PTS
	}
	firstW, firstH := 0, 0
	if len(vFrames) > 0 {
		firstW = vFrames[0].Width
		firstH = vFrames[0].Height
	}
	t.Logf("MEASURE: video frames=%d, first PTS=%d, last PTS=%d, dimensions=%dx%d",
		len(vFrames), firstPTS, lastPTS, firstW, firstH)

	// Assert: frame count should be approximately 90 (3s * 30fps).
	// Spec: 3s * 30fps = 90 frames. Tolerance ±10 for encoder warmup/flush.
	require.InDelta(t, 90, len(vFrames), 10,
		"video frame count: 3s @ 30fps = 90 frames, got %d", len(vFrames))

	// Assert: PTS must be strictly monotonically increasing.
	nonMonotonicCount := 0
	for i := 1; i < len(vFrames); i++ {
		if vFrames[i].PTS <= vFrames[i-1].PTS {
			nonMonotonicCount++
			if nonMonotonicCount <= 5 {
				t.Logf("MEASURE: non-monotonic PTS at frame %d: %d <= %d (delta=%d)",
					i, vFrames[i].PTS, vFrames[i-1].PTS, vFrames[i].PTS-vFrames[i-1].PTS)
			}
		}
	}
	require.Zero(t, nonMonotonicCount, "PTS must be strictly monotonic: %d violations out of %d frames",
		nonMonotonicCount, len(vFrames))

	// Assert: all frames have the expected dimensions.
	for i, f := range vFrames {
		require.Equal(t, 320, f.Width, "frame %d: width mismatch", i)
		require.Equal(t, 240, f.Height, "frame %d: height mismatch", i)
	}

	// Assert: YUV420 frame size matches expected dimensions.
	expectedSize := 320 * 240 * 3 / 2
	for i, f := range vFrames {
		require.Equal(t, expectedSize, f.Size,
			"frame %d: YUV420 buffer size should be %d (320*240*3/2), got %d", i, expectedSize, f.Size)
	}
}

// TestSRTLoopbackAudioDelivery pushes a 3s TS file via SRT loopback and verifies
// that audio frames arrive with consistent sample rate and monotonic PTS.
func TestSRTLoopbackAudioDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT loopback test in short mode")
	}

	tsPath := generateTestTS(t, 3, 320, 240, 30)

	h := newSRTHarness(t)
	defer h.close()

	conn := h.pushTSFile("live/testcam", tsPath)
	defer func() { _ = conn.Close() }()

	// Wait for audio frames.
	if !h.waitForAudioFrames(50, 30*time.Second) {
		af := h.audioFrames()
		vf := h.videoFrames()
		t.Fatalf("timeout waiting for audio frames: only got %d audio, %d video", len(af), len(vf))
	}

	// Give extra time for remaining frames.
	time.Sleep(3 * time.Second)

	aFrames := h.audioFrames()

	// Log measured values.
	firstSR, firstCh := 0, 0
	var firstPTS, lastPTS int64
	if len(aFrames) > 0 {
		firstSR = aFrames[0].SampleRate
		firstCh = aFrames[0].Channels
		firstPTS = aFrames[0].PTS
		lastPTS = aFrames[len(aFrames)-1].PTS
	}
	t.Logf("MEASURE: audio frames=%d, sample_rate=%d, channels=%d, first PTS=%d, last PTS=%d",
		len(aFrames), firstSR, firstCh, firstPTS, lastPTS)

	// Assert: frame count should be ~140 (3s * 48000Hz / 1024 samples per frame = 140.6).
	// Spec: 3s @ 48kHz with AAC 1024-sample frames = 140 frames. Tolerance ±15.
	require.InDelta(t, 140, len(aFrames), 15,
		"audio frame count: 3s @ 48kHz/1024 = 140 frames, got %d", len(aFrames))

	// Assert: sample rate is consistent across all frames.
	for i, f := range aFrames {
		require.Equal(t, firstSR, f.SampleRate,
			"frame %d: sample rate mismatch: got %d, expected %d", i, f.SampleRate, firstSR)
	}

	// Assert: sample rate is 48000 (we generate test source at 48kHz).
	require.Equal(t, 48000, firstSR,
		"sample rate should be 48000 Hz (test source), got %d", firstSR)

	// Assert: audio PTS must be strictly monotonically increasing.
	nonMonotonicCount := 0
	for i := 1; i < len(aFrames); i++ {
		if aFrames[i].PTS <= aFrames[i-1].PTS {
			nonMonotonicCount++
			if nonMonotonicCount <= 5 {
				t.Logf("MEASURE: non-monotonic audio PTS at frame %d: %d <= %d (delta=%d)",
					i, aFrames[i].PTS, aFrames[i-1].PTS, aFrames[i].PTS-aFrames[i-1].PTS)
			}
		}
	}
	require.Zero(t, nonMonotonicCount, "audio PTS must be strictly monotonic: %d violations out of %d frames",
		nonMonotonicCount, len(aFrames))

	// Assert: channels are consistent across all frames.
	for i, f := range aFrames {
		require.Equal(t, firstCh, f.Channels,
			"frame %d: channel count mismatch: got %d, expected %d", i, f.Channels, firstCh)
	}
}

// TestSRTLoopbackAVSync pushes a 3s TS file and checks that the first and last
// video/audio PTS values are within 100ms of each other (A/V sync).
func TestSRTLoopbackAVSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT loopback test in short mode")
	}

	tsPath := generateTestTS(t, 3, 320, 240, 30)

	h := newSRTHarness(t)
	defer h.close()

	conn := h.pushTSFile("live/testcam", tsPath)
	defer func() { _ = conn.Close() }()

	// Wait for both video and audio frames.
	if !h.waitForVideoFrames(30, 30*time.Second) {
		t.Fatalf("timeout waiting for video frames: got %d video, %d audio",
			len(h.videoFrames()), len(h.audioFrames()))
	}
	if !h.waitForAudioFrames(50, 30*time.Second) {
		t.Fatalf("timeout waiting for audio frames: got %d video, %d audio",
			len(h.videoFrames()), len(h.audioFrames()))
	}

	// Extra time for remaining frames.
	time.Sleep(3 * time.Second)

	vFrames := h.videoFrames()
	aFrames := h.audioFrames()

	if len(vFrames) == 0 || len(aFrames) == 0 {
		t.Fatalf("no frames captured: video=%d, audio=%d", len(vFrames), len(aFrames))
	}

	firstVideoPTS := vFrames[0].PTS
	lastVideoPTS := vFrames[len(vFrames)-1].PTS
	firstAudioPTS := aFrames[0].PTS
	lastAudioPTS := aFrames[len(aFrames)-1].PTS

	startDelta := absDiff(firstVideoPTS, firstAudioPTS)
	endDelta := absDiff(lastVideoPTS, lastAudioPTS)

	startDeltaMs := float64(startDelta) * 1000.0 / 90000.0
	endDeltaMs := float64(endDelta) * 1000.0 / 90000.0

	t.Logf("MEASURE: first video PTS=%d, first audio PTS=%d", firstVideoPTS, firstAudioPTS)
	t.Logf("MEASURE: last video PTS=%d, last audio PTS=%d", lastVideoPTS, lastAudioPTS)
	t.Logf("MEASURE: start delta=%d ticks (%.1f ms), end delta=%d ticks (%.1f ms)",
		startDelta, startDeltaMs, endDelta, endDeltaMs)

	// Assert: A/V sync at start within 100ms (9000 ticks at 90kHz).
	const maxDeltaTicks = 9000 // 100ms
	if startDelta > maxDeltaTicks {
		t.Errorf("A/V sync at start too far apart: delta=%d ticks (%.1f ms), max=%d ticks (100ms)",
			startDelta, startDeltaMs, maxDeltaTicks)
	}

	// Assert: A/V sync at end within 100ms.
	if endDelta > maxDeltaTicks {
		t.Errorf("A/V sync at end too far apart: delta=%d ticks (%.1f ms), max=%d ticks (100ms)",
			endDelta, endDeltaMs, maxDeltaTicks)
	}
}

// TestSRTLoopbackReconnection verifies that the SRT listener can accept a second
// connection after the first one closes, and that frames are received from both sessions.
func TestSRTLoopbackReconnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT loopback test in short mode")
	}

	// Generate a short 1s clip for faster test execution.
	tsPath := generateTestTS(t, 1, 320, 240, 30)

	h := newSRTHarness(t)
	defer h.close()

	// Session 1: push data and wait for some frames.
	conn1 := h.pushTSFileSync("live/testcam", tsPath)

	if !h.waitForVideoFrames(5, 20*time.Second) {
		vf := h.videoFrames()
		t.Fatalf("session 1: timeout waiting for video frames, got %d", len(vf))
	}

	session1Frames := len(h.videoFrames())
	t.Logf("MEASURE: session 1 delivered %d video frames before close", session1Frames)

	// Close the first connection.
	_ = conn1.Close()

	// Wait for the decoder to notice the disconnect and clean up.
	time.Sleep(2 * time.Second)

	framesBeforeSession2 := len(h.videoFrames())
	t.Logf("MEASURE: total frames before session 2 start: %d", framesBeforeSession2)

	// Session 2: push data again on a new connection.
	conn2 := h.pushTSFileSync("live/testcam2", tsPath)
	defer func() { _ = conn2.Close() }()

	// Wait for more frames from session 2.
	targetFrames := framesBeforeSession2 + 5
	if !h.waitForVideoFrames(targetFrames, 20*time.Second) {
		vf := h.videoFrames()
		t.Fatalf("session 2: timeout waiting for video frames, got %d total (need %d)",
			len(vf), targetFrames)
	}

	// Extra settle time.
	time.Sleep(2 * time.Second)

	totalFrames := len(h.videoFrames())
	session2Frames := totalFrames - framesBeforeSession2

	t.Logf("MEASURE: session1 frames=%d, session2 frames=%d, total=%d",
		session1Frames, session2Frames, totalFrames)

	// Assert: frames received from both sessions.
	// Each session pushes a 1s clip = 30 frames. Expect at least 15 per session
	// (encoder warmup + pushTSFileSync doesn't wait for decode to finish).
	require.GreaterOrEqual(t, session1Frames, 15,
		"session 1 delivered too few frames: %d (1s @ 30fps = 30 expected, min 15)", session1Frames)
	require.GreaterOrEqual(t, session2Frames, 15,
		"session 2 delivered too few frames: %d (1s @ 30fps = 30 expected, min 15)", session2Frames)

	// Assert: total frames ~60 (2 sessions * 1s * 30fps). Tolerance ±20.
	require.InDelta(t, 60, totalFrames, 20,
		"total frames across both sessions: 2 * 1s @ 30fps = 60 expected, got %d", totalFrames)
}

// absDiff returns the absolute difference between two int64 values.
func absDiff(a, b int64) int64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}

// TestSRTHighLatencyNoLoss configures SRT with 500ms latency (plenty for ARQ
// on localhost) and verifies near-perfect frame delivery.
func TestSRTHighLatencyNoLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT latency test in short mode")
	}

	tsPath := generateTestTS(t, 3, 320, 240, 30)

	// Configure listener with 500ms latency — ample for localhost ARQ.
	h := newSRTHarness(t, withLatency(500*time.Millisecond))
	defer h.close()

	// Push with caller-side latency also set to 500ms.
	conn := h.pushTSFileWithLatency("live/highlatcam", tsPath, 500*time.Millisecond)
	defer func() { _ = conn.Close() }()

	// Wait for video frames with generous timeout.
	if !h.waitForVideoFrames(30, 30*time.Second) {
		vf := h.videoFrames()
		t.Fatalf("timeout waiting for video frames: only got %d", len(vf))
	}

	// Give extra time for remaining frames to arrive.
	time.Sleep(4 * time.Second)

	vFrames := h.videoFrames()

	t.Logf("MEASURE: frames=%d with 500ms latency", len(vFrames))

	// Assert: frame count should be approximately 90 (3s * 30fps).
	// Spec: 3s * 30fps = 90 frames. With 500ms latency on localhost, ARQ
	// should recover everything. Tolerance ±10.
	require.InDelta(t, 90, len(vFrames), 10,
		"video frame count with high latency: 3s @ 30fps = 90 frames, got %d", len(vFrames))

	// Assert: all frames match expected 320x240.
	for i, f := range vFrames {
		require.Equal(t, 320, f.Width, "frame %d: width mismatch", i)
		require.Equal(t, 240, f.Height, "frame %d: height mismatch", i)
	}

	// Assert: PTS must be strictly monotonically increasing.
	nonMonotonicCount := 0
	for i := 1; i < len(vFrames); i++ {
		if vFrames[i].PTS <= vFrames[i-1].PTS {
			nonMonotonicCount++
		}
	}
	t.Logf("MEASURE: non-monotonic PTS violations=%d with 500ms latency", nonMonotonicCount)
	require.Zero(t, nonMonotonicCount,
		"PTS must be strictly monotonic with high latency: %d violations out of %d frames",
		nonMonotonicCount, len(vFrames))
}

// TestSRTLowLatencyUnderLoad configures SRT with 20ms latency (very low) and
// documents frame delivery behavior. On localhost there is no real network
// delay, so this primarily measures decoder startup and pipeline overhead.
func TestSRTLowLatencyUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT latency test in short mode")
	}

	tsPath := generateTestTS(t, 3, 320, 240, 30)

	// --- Run with default 120ms latency as baseline ---
	hDefault := newSRTHarness(t)
	connDefault := hDefault.pushTSFile("live/defaultcam", tsPath)

	if !hDefault.waitForVideoFrames(30, 30*time.Second) {
		vf := hDefault.videoFrames()
		hDefault.close()
		_ = connDefault.Close()
		t.Fatalf("timeout waiting for baseline video frames: only got %d", len(vf))
	}
	time.Sleep(4 * time.Second)
	defaultFrames := len(hDefault.videoFrames())
	_ = connDefault.Close()
	hDefault.close()

	t.Logf("MEASURE: baseline frames=%d with 120ms default latency", defaultFrames)

	// --- Run with 20ms latency ---
	hLow := newSRTHarness(t, withLatency(20*time.Millisecond))
	defer hLow.close()

	connLow := hLow.pushTSFileWithLatency("live/lowlatcam", tsPath, 20*time.Millisecond)
	defer func() { _ = connLow.Close() }()

	// Wait for at least some frames — with very low latency on localhost,
	// this should still work since there is no real network delay.
	if !hLow.waitForVideoFrames(10, 30*time.Second) {
		vf := hLow.videoFrames()
		t.Fatalf("timeout waiting for low-latency video frames: only got %d", len(vf))
	}

	time.Sleep(4 * time.Second)

	lowFrames := len(hLow.videoFrames())

	t.Logf("MEASURE: frames=%d with 20ms latency, vs %d with default latency", lowFrames, defaultFrames)

	// Assert: on localhost with no real network delay, low latency should still
	// deliver close to spec. Spec: 3s * 30fps = 90 frames.
	// Tolerance ±20 to account for 20ms latency reducing ARQ effectiveness.
	require.InDelta(t, 90, lowFrames, 20,
		"low-latency video frame count: 3s @ 30fps = 90 frames, got %d", lowFrames)

	// Assert: low latency shouldn't produce drastically fewer frames than
	// default on localhost (within 75% of baseline).
	minExpected := defaultFrames * 3 / 4
	t.Logf("MEASURE: low-latency delivery ratio=%.1f%% of baseline (%d/%d)",
		float64(lowFrames)*100/float64(defaultFrames), lowFrames, defaultFrames)
	require.GreaterOrEqual(t, lowFrames, minExpected,
		"low latency produced significantly fewer frames: %d vs baseline %d (threshold 75%% = %d)",
		lowFrames, defaultFrames, minExpected)
}

// TestSRTStreamKeyRouting verifies that two simultaneous SRT callers with
// different stream IDs are routed to separate sources, each producing frames
// independently with the correct source key.
func TestSRTStreamKeyRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SRT stream key routing test in short mode")
	}

	tsPath := generateTestTS(t, 3, 320, 240, 30)

	h := newSRTHarness(t)
	defer h.close()

	// Connect two callers with different stream IDs.
	conn1 := h.pushTSFile("live/cam1", tsPath)
	defer func() { _ = conn1.Close() }()

	// Small delay to avoid connection race conditions in the listener.
	time.Sleep(200 * time.Millisecond)

	conn2 := h.pushTSFile("live/cam2", tsPath)
	defer func() { _ = conn2.Close() }()

	// Wait for both sources to produce frames.
	// The key format is "srt:" + ExtractStreamKey(streamID).
	// ExtractStreamKey("live/cam1") strips "live/" prefix -> "cam1"
	// So the key becomes "srt:cam1" and "srt:cam2".
	cam1Key := "srt:cam1"
	cam2Key := "srt:cam2"

	if !h.waitForVideoFramesForKey(cam1Key, 10, 30*time.Second) {
		frames1 := h.videoFramesForKey(cam1Key)
		frames2 := h.videoFramesForKey(cam2Key)
		allFrames := h.videoFrames()
		t.Logf("MEASURE: cam1 frames=%d, cam2 frames=%d, total undifferentiated=%d", len(frames1), len(frames2), len(allFrames))
		// Log all unique keys seen
		keySet := make(map[string]int)
		for _, f := range allFrames {
			keySet[f.Key]++
		}
		for k, c := range keySet {
			t.Logf("MEASURE: key=%q count=%d", k, c)
		}
		t.Fatalf("timeout waiting for cam1 video frames: got %d for key %q", len(frames1), cam1Key)
	}

	if !h.waitForVideoFramesForKey(cam2Key, 10, 30*time.Second) {
		frames1 := h.videoFramesForKey(cam1Key)
		frames2 := h.videoFramesForKey(cam2Key)
		allFrames := h.videoFrames()
		t.Logf("MEASURE: cam1 frames=%d, cam2 frames=%d, total undifferentiated=%d", len(frames1), len(frames2), len(allFrames))
		keySet := make(map[string]int)
		for _, f := range allFrames {
			keySet[f.Key]++
		}
		for k, c := range keySet {
			t.Logf("MEASURE: key=%q count=%d", k, c)
		}
		t.Fatalf("timeout waiting for cam2 video frames: got %d for key %q", len(frames2), cam2Key)
	}

	// Extra settle time.
	time.Sleep(4 * time.Second)

	frames1 := h.videoFramesForKey(cam1Key)
	frames2 := h.videoFramesForKey(cam2Key)
	allFrames := h.videoFrames()

	t.Logf("MEASURE: cam1 frames=%d, cam2 frames=%d", len(frames1), len(frames2))

	// Log all unique keys observed.
	keySet := make(map[string]int)
	for _, f := range allFrames {
		keySet[f.Key]++
	}
	for k, c := range keySet {
		t.Logf("MEASURE: key=%q frame_count=%d", k, c)
	}

	// Assert: both sources produced ~90 frames each (3s @ 30fps).
	// Each camera gets the same 3s file, so each should produce ~90 frames.
	// Tolerance ±15 since concurrent sources may cause slightly more loss.
	require.InDelta(t, 90, len(frames1), 15,
		"cam1 frame count: 3s @ 30fps = 90 frames, got %d", len(frames1))
	require.InDelta(t, 90, len(frames2), 15,
		"cam2 frame count: 3s @ 30fps = 90 frames, got %d", len(frames2))

	// Assert: frames are correctly attributed to their source keys.
	// No frames should have unexpected keys.
	for i, f := range allFrames {
		require.True(t, f.Key == cam1Key || f.Key == cam2Key,
			"frame %d: unexpected key %q (expected %q or %q)", i, f.Key, cam1Key, cam2Key)
	}

	// Assert: both sources have strictly monotonically increasing PTS.
	for _, tc := range []struct {
		name   string
		frames []capturedVideoFrame
	}{
		{"cam1", frames1},
		{"cam2", frames2},
	} {
		nonMono := 0
		for i := 1; i < len(tc.frames); i++ {
			if tc.frames[i].PTS <= tc.frames[i-1].PTS {
				nonMono++
			}
		}
		t.Logf("MEASURE: %s PTS non-monotonic violations=%d out of %d frames", tc.name, nonMono, len(tc.frames))
		require.Zero(t, nonMono,
			"%s: PTS must be strictly monotonic: %d violations out of %d frames",
			tc.name, nonMono, len(tc.frames))
	}

	// Assert: total frame count is approximately the sum of both streams.
	totalExpected := len(frames1) + len(frames2)
	if len(allFrames) != totalExpected {
		t.Errorf("total frame count mismatch: got %d, expected %d (cam1=%d + cam2=%d)",
			len(allFrames), totalExpected, len(frames1), len(frames2))
	}
}

// roundTripVideoFrame captures a decoded video frame with raw YUV data.
type roundTripVideoFrame struct {
	Width  int
	Height int
	PTS    int64
	AvgY   float64 // average luma computed at capture time
}

// roundTripAudioFrame captures a decoded audio frame with PCM statistics.
type roundTripAudioFrame struct {
	PTS        int64
	SampleRate int
	Channels   int
	Samples    int
	RMS        float64 // RMS computed at capture time
	HasSignal  bool    // at least one non-zero sample
}

// TestSRTFullRoundTrip is the ultimate SRT input path test: generate a known
// test pattern TS with ffmpeg, push it in via SRT, capture decoded video and
// audio frames, and verify the content matches expectations (correct resolution,
// monotonic PTS, non-black video, non-silent audio).
func TestSRTFullRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full round-trip correctness test in short mode")
	}

	// Step 1: Generate 3s test TS with ffmpeg (320x240, 30fps, testsrc pattern + 1kHz sine).
	tsPath := generateTestTS(t, 3, 320, 240, 30)

	// Step 2: Start SRT listener on random port with raw-data-capturing callbacks.
	addr := findFreeUDPAddr(t)
	dir := t.TempDir()
	store, err := srt.NewStore(filepath.Join(dir, "srt_sources.json"))
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	var vFrames []roundTripVideoFrame
	var aFrames []roundTripAudioFrame
	runDone := make(chan error, 1)

	l, err := srt.NewListener(srt.ListenerConfig{
		Addr:           addr,
		Store:          store,
		DefaultLatency: 120 * time.Millisecond,
		OnSource: func(srcCfg srt.SourceConfig, conn *srtgo.Conn) {
			stats := srt.NewConnStats(srcCfg.Mode, srcCfg.StreamID, srcCfg.LatencyMs)
			src := srt.NewSource(srcCfg, conn, stats, log)
			src.OnRawVideo = func(key string, yuv []byte, width, height int, pts int64) {
				// Compute average Y luma from the Y plane.
				yPlaneSize := width * height
				var sumY uint64
				if len(yuv) >= yPlaneSize && yPlaneSize > 0 {
					for i := 0; i < yPlaneSize; i++ {
						sumY += uint64(yuv[i])
					}
				}
				avgY := 0.0
				if yPlaneSize > 0 {
					avgY = float64(sumY) / float64(yPlaneSize)
				}
				mu.Lock()
				vFrames = append(vFrames, roundTripVideoFrame{
					Width:  width,
					Height: height,
					PTS:    pts,
					AvgY:   avgY,
				})
				mu.Unlock()
			}
			src.OnRawAudio = func(key string, pcm []float32, pts int64, sampleRate, channels int) {
				// Compute RMS and check for signal.
				var sumSq float64
				hasSignal := false
				for _, s := range pcm {
					sumSq += float64(s) * float64(s)
					if s != 0 {
						hasSignal = true
					}
				}
				rms := 0.0
				if len(pcm) > 0 {
					rms = math.Sqrt(sumSq / float64(len(pcm)))
				}
				mu.Lock()
				aFrames = append(aFrames, roundTripAudioFrame{
					PTS:        pts,
					SampleRate: sampleRate,
					Channels:   channels,
					Samples:    len(pcm),
					RMS:        rms,
					HasSignal:  hasSignal,
				})
				mu.Unlock()
			}
			if startErr := src.Start(ctx); startErr != nil {
				t.Errorf("source start failed: %v", startErr)
			}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		runDone <- l.Run(ctx)
	}()

	// Give listener time to start accepting.
	time.Sleep(100 * time.Millisecond)

	// Step 3: Push the TS via SRT.
	cfg := srtgo.DefaultConfig()
	cfg.StreamID = "live/roundtrip"

	var conn *srtgo.Conn
	for i := 0; i < 20; i++ {
		conn, err = srtgo.Dial(addr, cfg)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial SRT failed after retries: %v", err)
	}
	defer func() { _ = conn.Close() }()

	data, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("read TS file: %v", err)
	}

	// Pace the data at approximately real-time.
	nChunks := (len(data) + 1315) / 1316
	fileBits := int64(len(data)) * 8
	bitrateGuess := int64(400_000)
	durationUs := fileBits * 1_000_000 / bitrateGuess
	totalDuration := time.Duration(durationUs) * time.Microsecond
	pacingPerChunk := totalDuration / time.Duration(nChunks)
	if pacingPerChunk < time.Millisecond {
		pacingPerChunk = time.Millisecond
	}
	if pacingPerChunk > 100*time.Millisecond {
		pacingPerChunk = 100 * time.Millisecond
	}

	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		for i := 0; i < len(data); i += 1316 {
			select {
			case <-ctx.Done():
				return
			default:
			}
			end := i + 1316
			if end > len(data) {
				end = len(data)
			}
			_, werr := conn.Write(data[i:end])
			if werr != nil {
				return
			}
			time.Sleep(pacingPerChunk)
		}
	}()

	// Step 4: Wait for video frames with generous timeout.
	deadline := time.After(30 * time.Second)
	for {
		mu.Lock()
		vCount := len(vFrames)
		mu.Unlock()
		if vCount >= 30 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			vc := len(vFrames)
			ac := len(aFrames)
			mu.Unlock()
			t.Fatalf("timeout waiting for video frames: only got %d video, %d audio", vc, ac)
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Wait for push to complete plus extra drain time.
	select {
	case <-pushDone:
	case <-time.After(20 * time.Second):
	}
	time.Sleep(3 * time.Second)

	// Snapshot captured data.
	mu.Lock()
	videoSnap := make([]roundTripVideoFrame, len(vFrames))
	copy(videoSnap, vFrames)
	audioSnap := make([]roundTripAudioFrame, len(aFrames))
	copy(audioSnap, aFrames)
	mu.Unlock()

	// Compute aggregate metrics before assertions.
	var totalAvgY float64
	for _, f := range videoSnap {
		totalAvgY += f.AvgY
	}
	overallAvgY := 0.0
	if len(videoSnap) > 0 {
		overallAvgY = totalAvgY / float64(len(videoSnap))
	}

	var totalRMS float64
	audioWithSignal := 0
	for _, f := range audioSnap {
		totalRMS += f.RMS
		if f.HasSignal {
			audioWithSignal++
		}
	}
	overallAudioRMS := 0.0
	if len(audioSnap) > 0 {
		overallAudioRMS = totalRMS / float64(len(audioSnap))
	}

	t.Logf("MEASURE: video=%d, audio=%d, avg_Y=%.1f, audio_rms=%.4f",
		len(videoSnap), len(audioSnap), overallAvgY, overallAudioRMS)

	// --- Video assertions ---

	// 5a. Frame count ~90 (3s * 30fps). Spec-derived tolerance ±10.
	t.Logf("MEASURE: video frame count=%d (expected ~90)", len(videoSnap))
	require.InDelta(t, 90, len(videoSnap), 10,
		"round-trip video frame count: 3s @ 30fps = 90 frames, got %d", len(videoSnap))

	// 5b. All frames are 320x240.
	for i, f := range videoSnap {
		require.Equal(t, 320, f.Width, "frame %d: width mismatch", i)
		require.Equal(t, 240, f.Height, "frame %d: height mismatch", i)
	}

	// 5c. PTS must be strictly monotonically increasing.
	nonMonotonicCount := 0
	for i := 1; i < len(videoSnap); i++ {
		if videoSnap[i].PTS <= videoSnap[i-1].PTS {
			nonMonotonicCount++
			if nonMonotonicCount <= 5 {
				t.Logf("MEASURE: non-monotonic PTS at frame %d: %d <= %d (delta=%d)",
					i, videoSnap[i].PTS, videoSnap[i-1].PTS, videoSnap[i].PTS-videoSnap[i-1].PTS)
			}
		}
	}
	t.Logf("MEASURE: non-monotonic PTS violations=%d out of %d frames", nonMonotonicCount, len(videoSnap))
	require.Zero(t, nonMonotonicCount,
		"video PTS must be strictly monotonic: %d violations out of %d frames",
		nonMonotonicCount, len(videoSnap))

	// 5d. Frames are non-black (avg Y > 20 -- testsrc has bright content).
	// ffmpeg testsrc produces a pattern with avg luma typically 80-130.
	blackFrameCount := 0
	for i, f := range videoSnap {
		if f.AvgY <= 20 {
			blackFrameCount++
			if blackFrameCount <= 5 {
				t.Logf("MEASURE: dark/black frame at %d: avg_Y=%.1f (threshold 20)", i, f.AvgY)
			}
		}
	}
	t.Logf("MEASURE: black/dark frames=%d out of %d, overall avg_Y=%.1f", blackFrameCount, len(videoSnap), overallAvgY)
	// Overall average Y for testsrc should be well above black (typically 80+).
	require.Greater(t, overallAvgY, 40.0,
		"overall average Y too low: %.1f, testsrc pattern should have avg Y > 40", overallAvgY)
	// Allow at most 2 dark frames (codec warmup).
	require.LessOrEqual(t, blackFrameCount, 2,
		"too many black/dark frames: %d out of %d (max 2 allowed for codec warmup)", blackFrameCount, len(videoSnap))

	// --- Audio assertions ---

	// 7a. Audio frame count ~140 (3s * 48000 / 1024 = 140.6). Tolerance ±15.
	t.Logf("MEASURE: audio frame count=%d (expected ~140)", len(audioSnap))
	require.InDelta(t, 140, len(audioSnap), 15,
		"round-trip audio frame count: 3s @ 48kHz/1024 = 140 frames, got %d", len(audioSnap))

	// 7b. Samples are not all-zero (signal present).
	t.Logf("MEASURE: audio frames with signal=%d out of %d, overall_rms=%.4f",
		audioWithSignal, len(audioSnap), overallAudioRMS)
	// At least 90% of frames should have signal (1kHz sine tone).
	minSignalFrames := len(audioSnap) * 9 / 10
	require.GreaterOrEqual(t, audioWithSignal, minSignalFrames,
		"audio signal: %d of %d frames have signal (expected >= 90%%)", audioWithSignal, len(audioSnap))
	// The sine wave source should produce significant RMS (1kHz sine at full volume ~ 0.7 RMS).
	require.Greater(t, overallAudioRMS, 0.01,
		"audio RMS too low: %.6f, expected > 0.01 for 1kHz sine source", overallAudioRMS)

	// Audio PTS must be strictly monotonically increasing.
	audioNonMono := 0
	for i := 1; i < len(audioSnap); i++ {
		if audioSnap[i].PTS <= audioSnap[i-1].PTS {
			audioNonMono++
			if audioNonMono <= 5 {
				t.Logf("MEASURE: non-monotonic audio PTS at frame %d: %d <= %d",
					i, audioSnap[i].PTS, audioSnap[i-1].PTS)
			}
		}
	}
	t.Logf("MEASURE: audio non-monotonic PTS violations=%d", audioNonMono)
	require.Zero(t, audioNonMono,
		"audio PTS must be strictly monotonic: %d violations out of %d frames",
		audioNonMono, len(audioSnap))

	// Cleanup.
	cancel()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for listener Run to return")
	}
}

// Verify absDiff handles edge cases.
func init() {
	if absDiff(100, 50) != 50 {
		panic("absDiff broken")
	}
	if absDiff(50, 100) != 50 {
		panic("absDiff broken (negative)")
	}
	if absDiff(math.MinInt64+1, 0) != math.MaxInt64 {
		// This is fine for our use case — PTS values are always positive
		// and fit in int63.
	}
}
