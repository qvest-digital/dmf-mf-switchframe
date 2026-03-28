//go:build correctness

package srt

import (
	"math"
	"testing"
	"time"
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

	// Assert: frame count should be approximately 90 (3s * 30fps), with tolerance
	// for encoder warmup/flush loss.
	if len(vFrames) < 70 {
		t.Errorf("too few video frames: got %d, expected ~90 (3s @ 30fps), min 70", len(vFrames))
	}
	if len(vFrames) > 110 {
		t.Errorf("too many video frames: got %d, expected ~90 (3s @ 30fps), max 110", len(vFrames))
	}

	// Assert: PTS monotonically increasing.
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
	if nonMonotonicCount > 0 {
		t.Errorf("PTS not monotonically increasing: %d violations out of %d frames",
			nonMonotonicCount, len(vFrames))
	}

	// Assert: all frames have the expected dimensions.
	wrongDimCount := 0
	for i, f := range vFrames {
		if f.Width != 320 || f.Height != 240 {
			wrongDimCount++
			if wrongDimCount <= 3 {
				t.Logf("MEASURE: wrong dimensions at frame %d: %dx%d (expected 320x240)",
					i, f.Width, f.Height)
			}
		}
	}
	if wrongDimCount > 0 {
		t.Errorf("frames with wrong dimensions: %d out of %d", wrongDimCount, len(vFrames))
	}

	// Assert: YUV420 frame size should be width*height*3/2.
	expectedSize := 320 * 240 * 3 / 2
	wrongSizeCount := 0
	for i, f := range vFrames {
		if f.Size != expectedSize {
			wrongSizeCount++
			if wrongSizeCount <= 3 {
				t.Logf("MEASURE: wrong YUV size at frame %d: %d (expected %d)",
					i, f.Size, expectedSize)
			}
		}
	}
	if wrongSizeCount > 0 {
		t.Errorf("frames with wrong YUV buffer size: %d out of %d (expected %d bytes each)",
			wrongSizeCount, len(vFrames), expectedSize)
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

	// Assert: frame count should be > 100. At 48kHz with AAC 1024-sample frames,
	// 3s produces ~140 frames. Allow for some loss.
	if len(aFrames) < 100 {
		t.Errorf("too few audio frames: got %d, expected >100 (3s at 48kHz/1024 samples per frame ≈ 140)",
			len(aFrames))
	}

	// Assert: sample rate is consistent across all frames.
	sampleRateMismatch := 0
	for i, f := range aFrames {
		if f.SampleRate != firstSR {
			sampleRateMismatch++
			if sampleRateMismatch <= 3 {
				t.Logf("MEASURE: sample rate mismatch at frame %d: %d vs first=%d",
					i, f.SampleRate, firstSR)
			}
		}
	}
	if sampleRateMismatch > 0 {
		t.Errorf("inconsistent sample rate across frames: %d mismatches out of %d",
			sampleRateMismatch, len(aFrames))
	}

	// Assert: sample rate is 48000 or 44100 (common values).
	if firstSR != 48000 && firstSR != 44100 {
		t.Errorf("unexpected sample rate: %d (expected 48000 or 44100)", firstSR)
	}

	// Assert: audio PTS monotonically increasing.
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
	if nonMonotonicCount > 0 {
		t.Errorf("audio PTS not monotonically increasing: %d violations out of %d frames",
			nonMonotonicCount, len(aFrames))
	}

	// Assert: channels are consistent.
	channelMismatch := 0
	for i, f := range aFrames {
		if f.Channels != firstCh {
			channelMismatch++
			if channelMismatch <= 3 {
				t.Logf("MEASURE: channel count mismatch at frame %d: %d vs first=%d",
					i, f.Channels, firstCh)
			}
		}
	}
	if channelMismatch > 0 {
		t.Errorf("inconsistent channel count: %d mismatches out of %d",
			channelMismatch, len(aFrames))
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
	if session1Frames < 5 {
		t.Errorf("session 1 delivered too few frames: %d (expected >= 5)", session1Frames)
	}
	if session2Frames < 5 {
		t.Errorf("session 2 delivered too few frames: %d (expected >= 5)", session2Frames)
	}

	// Assert: total frames reasonable (> 30 across both sessions).
	if totalFrames < 30 {
		t.Errorf("total frames across both sessions too low: %d (expected > 30)", totalFrames)
	}

	// Assert: no panic occurred (test reaching this point is sufficient).
	t.Log("MEASURE: no panic during reconnection test")
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
	// With 500ms latency on localhost, ARQ should recover everything.
	if len(vFrames) < 80 {
		t.Errorf("too few video frames with high latency: got %d, expected ~90 (3s @ 30fps), min 80", len(vFrames))
	}
	if len(vFrames) > 100 {
		t.Errorf("too many video frames with high latency: got %d, expected ~90 (3s @ 30fps), max 100", len(vFrames))
	}

	// Assert: all frames have valid dimensions (no decode artifacts).
	invalidDimCount := 0
	for i, f := range vFrames {
		if f.Width <= 0 || f.Height <= 0 {
			invalidDimCount++
			if invalidDimCount <= 3 {
				t.Logf("MEASURE: invalid dimensions at frame %d: %dx%d", i, f.Width, f.Height)
			}
		}
	}
	t.Logf("MEASURE: invalid dimension frames=%d out of %d total", invalidDimCount, len(vFrames))
	if invalidDimCount > 0 {
		t.Errorf("frames with invalid dimensions (decode artifacts): %d out of %d", invalidDimCount, len(vFrames))
	}

	// Assert: all frames match expected 320x240.
	wrongDimCount := 0
	for i, f := range vFrames {
		if f.Width != 320 || f.Height != 240 {
			wrongDimCount++
			if wrongDimCount <= 3 {
				t.Logf("MEASURE: wrong dimensions at frame %d: %dx%d (expected 320x240)", i, f.Width, f.Height)
			}
		}
	}
	if wrongDimCount > 0 {
		t.Errorf("frames with wrong dimensions: %d out of %d", wrongDimCount, len(vFrames))
	}

	// Assert: PTS monotonically increasing.
	nonMonotonicCount := 0
	for i := 1; i < len(vFrames); i++ {
		if vFrames[i].PTS <= vFrames[i-1].PTS {
			nonMonotonicCount++
		}
	}
	t.Logf("MEASURE: non-monotonic PTS violations=%d with 500ms latency", nonMonotonicCount)
	if nonMonotonicCount > 0 {
		t.Errorf("PTS not monotonically increasing with high latency: %d violations out of %d frames",
			nonMonotonicCount, len(vFrames))
	}
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

	// Assert: at minimum some frames delivered (no crash, no hang).
	if lowFrames < 20 {
		t.Errorf("too few video frames with 20ms latency: got %d, expected at least 20", lowFrames)
	}

	// Assert: low latency shouldn't produce drastically fewer frames than
	// default on localhost (within 50% of baseline).
	minExpected := defaultFrames / 2
	t.Logf("MEASURE: low-latency delivery ratio=%.1f%% of baseline (%d/%d)",
		float64(lowFrames)*100/float64(defaultFrames), lowFrames, defaultFrames)
	if lowFrames < minExpected {
		t.Errorf("low latency produced significantly fewer frames: %d vs baseline %d (threshold %d)",
			lowFrames, defaultFrames, minExpected)
	}
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

	// Assert: both sources produced a reasonable number of frames independently.
	if len(frames1) < 30 {
		t.Errorf("cam1 produced too few frames: got %d, expected >= 30 (out of ~90)", len(frames1))
	}
	if len(frames2) < 30 {
		t.Errorf("cam2 produced too few frames: got %d, expected >= 30 (out of ~90)", len(frames2))
	}

	// Assert: frames are correctly attributed to their source keys.
	// No frames should have unexpected keys.
	unexpectedKeys := 0
	for _, f := range allFrames {
		if f.Key != cam1Key && f.Key != cam2Key {
			unexpectedKeys++
			if unexpectedKeys <= 3 {
				t.Logf("MEASURE: unexpected key=%q at PTS=%d", f.Key, f.PTS)
			}
		}
	}
	if unexpectedKeys > 0 {
		t.Errorf("found %d frames with unexpected keys (not %q or %q)", unexpectedKeys, cam1Key, cam2Key)
	}

	// Assert: both sources have monotonically increasing PTS independently.
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
		if nonMono > 0 {
			t.Errorf("%s: PTS not monotonically increasing: %d violations out of %d frames",
				tc.name, nonMono, len(tc.frames))
		}
	}

	// Assert: total frame count is approximately the sum of both streams.
	totalExpected := len(frames1) + len(frames2)
	if len(allFrames) != totalExpected {
		t.Errorf("total frame count mismatch: got %d, expected %d (cam1=%d + cam2=%d)",
			len(allFrames), totalExpected, len(frames1), len(frames2))
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
