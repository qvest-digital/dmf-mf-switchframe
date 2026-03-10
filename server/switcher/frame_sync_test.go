package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// syncTestHandler captures frames released by the FrameSynchronizer.
type syncTestHandler struct {
	mu     sync.Mutex
	videos []syncTaggedVideo
	audios []syncTaggedAudio
}

type syncTaggedVideo struct {
	sourceKey string
	frame     media.VideoFrame
	recvTime  time.Time
}

type syncTaggedAudio struct {
	sourceKey string
	frame     media.AudioFrame
	recvTime  time.Time
}

func (h *syncTestHandler) onVideo(sourceKey string, frame media.VideoFrame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.videos = append(h.videos, syncTaggedVideo{sourceKey: sourceKey, frame: frame, recvTime: time.Now()})
}

func (h *syncTestHandler) onAudio(sourceKey string, frame media.AudioFrame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.audios = append(h.audios, syncTaggedAudio{sourceKey: sourceKey, frame: frame, recvTime: time.Now()})
}

func (h *syncTestHandler) videoCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.videos)
}

func (h *syncTestHandler) audioCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.audios)
}

func (h *syncTestHandler) getVideos() []syncTaggedVideo {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]syncTaggedVideo, len(h.videos))
	copy(cp, h.videos)
	return cp
}

func (h *syncTestHandler) getAudios() []syncTaggedAudio {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]syncTaggedAudio, len(h.audios))
	copy(cp, h.audios)
	return cp
}

// --- Buffer tests ---

func TestFrameSync_IngestBuffersFrame(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Ingest a video frame — should NOT be delivered immediately (buffered).
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	// Without starting the ticker, nothing should be released.
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 0, handler.videoCount(), "video count before Start")
}

func TestFrameSync_IngestUnknownSourceIgnored(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)

	// Ingesting to an unregistered source should not panic.
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("unknown", vf)

	af := &media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	fs.IngestAudio("unknown", af)

	// Nothing released.
	require.Equal(t, 0, handler.videoCount(), "video count")
	require.Equal(t, 0, handler.audioCount(), "audio count")
}

func TestFrameSync_RingBufferOverwrite(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Push 3 frames into a 2-slot ring buffer — first should be overwritten.
	vf1 := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	vf3 := &media.VideoFrame{PTS: 3000, WireData: []byte{0x03}}
	fs.IngestVideo("cam1", vf1)
	fs.IngestVideo("cam1", vf2)
	fs.IngestVideo("cam1", vf3)

	// Start and wait for one tick.
	fs.Start()
	defer fs.Stop()
	time.Sleep(50 * time.Millisecond)

	// Should release the most recent frame (vf3).
	videos := handler.getVideos()
	require.NotEmpty(t, videos, "expected at least 1 released video frame")
	// The latest frame should be vf3 (PTS 3000) — oldest vf1 was overwritten.
	last := videos[len(videos)-1]
	require.Equal(t, byte(0x03), last.frame.WireData[0], "expected latest frame data 0x03")
}

// --- Tick tests ---

func TestFrameSync_TickReleasesFrames(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// Wait for at least one tick cycle.
	time.Sleep(50 * time.Millisecond)
	require.GreaterOrEqual(t, handler.videoCount(), 1, "video count after tick")
}

func TestFrameSync_TickReleasesAudio(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	af := &media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)
	require.GreaterOrEqual(t, handler.audioCount(), 1, "audio count after tick")
}

func TestFrameSync_PTSPreservedForFreshFrames(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 99999, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)
	videos := handler.getVideos()
	require.NotEmpty(t, videos, "no video frames released")
	// Fresh frame PTS should be preserved (same timeline as audio for A/V sync).
	require.Equal(t, int64(99999), videos[0].frame.PTS, "fresh frame PTS should be preserved")
	// Repeated frames (freeze) should advance PTS monotonically.
	if len(videos) >= 3 {
		require.Greater(t, videos[2].frame.PTS, videos[1].frame.PTS,
			"repeated frame PTS should advance monotonically")
	}
}

func TestFrameSync_FreezeRepeatsLastFrame(t *testing.T) {
	handler := &syncTestHandler{}
	// Use a fast tick for reliable testing.
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Push one frame, then let the ticker run without pushing more.
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0xAA}, IsKeyframe: true}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// Wait for several ticks — frame should be repeated (freeze behavior).
	time.Sleep(80 * time.Millisecond)
	count := handler.videoCount()
	require.GreaterOrEqual(t, count, 3, "video count after multiple ticks (freeze repeat)")

	// All repeated frames should have the same WireData.
	videos := handler.getVideos()
	for i, v := range videos {
		require.Equal(t, byte(0xAA), v.frame.WireData[0], "frame[%d] data", i)
	}
}

func TestFrameSync_NoFrameNoRelease(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Start ticker without ingesting any frame — no release.
	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, handler.videoCount(), "video count (no frame ingested)")
}

// --- Multi-source alignment tests ---

func TestFrameSync_MultiSourceAlignment(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	vf1 := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	fs.IngestVideo("cam1", vf1)
	fs.IngestVideo("cam2", vf2)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)

	videos := handler.getVideos()
	// Both sources should have released at least one frame.
	cam1Count, cam2Count := 0, 0
	for _, v := range videos {
		switch v.sourceKey {
		case "cam1":
			cam1Count++
		case "cam2":
			cam2Count++
		}
	}
	require.GreaterOrEqual(t, cam1Count, 1, "cam1 video count")
	require.GreaterOrEqual(t, cam2Count, 1, "cam2 video count")

	// Frames from the same tick should have the same PTS (aligned).
	// Find first cam1 and first cam2 frame.
	var firstCam1, firstCam2 *syncTaggedVideo
	for i, v := range videos {
		if v.sourceKey == "cam1" && firstCam1 == nil {
			firstCam1 = &videos[i]
		}
		if v.sourceKey == "cam2" && firstCam2 == nil {
			firstCam2 = &videos[i]
		}
	}
	if firstCam1 != nil && firstCam2 != nil {
		ptsDiff := firstCam1.frame.PTS - firstCam2.frame.PTS
		if ptsDiff < 0 {
			ptsDiff = -ptsDiff
		}
		// Same tick should produce same PTS or very close (within one tick in 90 kHz units).
		// 20ms tick at 90 kHz = 20_000_000 ns * 90000 / 1_000_000_000 = 1800 ticks.
		const oneTick90kHz = int64(20*time.Millisecond) * 90000 / int64(time.Second)
		require.LessOrEqual(t, ptsDiff, oneTick90kHz,
			"PTS difference between sources = %d, want <= %d (one tick at 90 kHz)", ptsDiff, oneTick90kHz)
	}
}

func TestFrameSync_AddSourceDynamic(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	fs.Start()
	defer fs.Stop()

	vf1 := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf1)
	time.Sleep(40 * time.Millisecond)

	// Add a second source dynamically while running.
	fs.AddSource("cam2")
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	fs.IngestVideo("cam2", vf2)

	time.Sleep(40 * time.Millisecond)

	videos := handler.getVideos()
	cam2Found := false
	for _, v := range videos {
		if v.sourceKey == "cam2" {
			cam2Found = true
			break
		}
	}
	require.True(t, cam2Found, "cam2 frame not released after dynamic add")
}

func TestFrameSync_RemoveSource(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	vf1 := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	fs.IngestVideo("cam1", vf1)
	fs.IngestVideo("cam2", vf2)

	fs.Start()
	defer fs.Stop()

	time.Sleep(30 * time.Millisecond)
	// Remove cam2.
	fs.RemoveSource("cam2")

	// Clear handler state.
	handler.mu.Lock()
	handler.videos = nil
	handler.mu.Unlock()

	// Push new frame to cam2 — should be ignored.
	vf3 := &media.VideoFrame{PTS: 3000, WireData: []byte{0x03}}
	fs.IngestVideo("cam2", vf3)

	time.Sleep(40 * time.Millisecond)

	videos := handler.getVideos()
	for _, v := range videos {
		require.NotEqual(t, "cam2", v.sourceKey, "cam2 frame released after RemoveSource")
	}
}

// --- SetTickRate tests ---

func TestFrameSync_SetTickRate(t *testing.T) {
	handler := &syncTestHandler{}
	// Start with a slow tick rate.
	fs := NewFrameSynchronizer(100*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// At 100ms tick, after 80ms we should have 0 releases.
	time.Sleep(80 * time.Millisecond)
	countBefore := handler.videoCount()

	// Speed up to 15ms tick.
	fs.SetTickRate(15 * time.Millisecond)

	// Push another frame so freeze has content.
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	fs.IngestVideo("cam1", vf2)

	// Wait for several fast ticks.
	time.Sleep(80 * time.Millisecond)
	countAfter := handler.videoCount()

	// Should have more frames after speeding up.
	gained := countAfter - countBefore
	require.GreaterOrEqual(t, gained, 3, "after SetTickRate(15ms), gained only %d frames in 80ms", gained)
}

// --- Stop tests ---

func TestFrameSync_StopCeasesTicking(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	time.Sleep(50 * time.Millisecond)
	fs.Stop()

	countAtStop := handler.videoCount()
	time.Sleep(50 * time.Millisecond)
	countAfter := handler.videoCount()

	require.Equal(t, countAtStop, countAfter, "frames released after Stop")
}

func TestFrameSync_StopWaitsForGoroutine(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(10*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()

	// Let the goroutine run a few ticks.
	time.Sleep(50 * time.Millisecond)

	// Stop must block until the tickLoop goroutine has exited.
	fs.Stop()

	// After Stop returns, we can verify the goroutine exited by checking
	// that no more frames are released. Record the count, wait, and verify
	// it hasn't changed.
	countAtStop := handler.videoCount()
	time.Sleep(50 * time.Millisecond)
	countAfter := handler.videoCount()
	require.Equal(t, countAtStop, countAfter,
		"frames released after Stop returned — goroutine still running")
}

func TestFrameSync_StopWithoutStart(t *testing.T) {
	// Stop without Start must not block or panic.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.Stop()
}

func TestFrameSync_StopIdempotent(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.Start()

	// Multiple Stop calls should not panic.
	fs.Stop()
	fs.Stop()
}

func TestFrameSync_StartWithoutStop(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	// Start twice should not panic (second call is a no-op).
	fs.Start()
	fs.Start()
	fs.Stop()
}

// --- Concurrent safety tests ---

func TestFrameSync_ConcurrentIngest(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(10*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")
	fs.Start()
	defer fs.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(pts int64) {
			defer wg.Done()
			fs.IngestVideo("cam1", &media.VideoFrame{PTS: pts, WireData: []byte{0x01}})
		}(int64(i * 1000))
		go func(pts int64) {
			defer wg.Done()
			fs.IngestVideo("cam2", &media.VideoFrame{PTS: pts, WireData: []byte{0x02}})
		}(int64(i * 1000))
	}
	wg.Wait()

	time.Sleep(30 * time.Millisecond)
	// Should not panic; just verify we got some frames.
	require.Greater(t, handler.videoCount(), 0, "no frames released during concurrent ingest")
}

func TestFrameSync_ConcurrentAddRemove(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(10*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.Start()
	defer fs.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			key := "cam" + string(rune('A'+idx%5))
			fs.AddSource(key)
			fs.IngestVideo(key, &media.VideoFrame{PTS: int64(idx), WireData: []byte{0x01}})
		}(i)
		go func(idx int) {
			defer wg.Done()
			key := "cam" + string(rune('A'+idx%5))
			fs.RemoveSource(key)
		}(i)
	}
	wg.Wait()
	// Must not panic.
}

// --- Paired audio/video release ---

func TestFrameSync_PairedAudioVideoRelease(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	af := &media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	fs.IngestVideo("cam1", vf)
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)

	require.GreaterOrEqual(t, handler.videoCount(), 1, "no video frames released")
	require.GreaterOrEqual(t, handler.audioCount(), 1, "no audio frames released")
}

func TestFrameSync_AudioFreezeRepeatsLast(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	af := &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}}
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	time.Sleep(60 * time.Millisecond)
	count := handler.audioCount()
	require.GreaterOrEqual(t, count, 2, "audio count (freeze repeat)")
	audios := handler.getAudios()
	for i, a := range audios {
		require.Equal(t, byte(0xBB), a.frame.Data[0], "audio[%d] data", i)
	}
}

func TestFrameSync_AudioFreezeLimit(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(10*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.Start()
	defer fs.Stop()

	// Send one audio frame, then never send another.
	fs.IngestAudio("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0x01}})

	// Wait for several ticks (more than 3).
	// With 10ms ticks over 80ms we'd get ~8 ticks, but audio should stop after 3
	// (1 original emission + 2 repeats = 3 total).
	time.Sleep(80 * time.Millisecond)

	count := handler.audioCount()
	// Must have received the original + at most 2 repeats = 3 audio frames.
	require.LessOrEqual(t, count, 4, "audio should stop repeating after 2 misses, got %d frames", count)
	require.GreaterOrEqual(t, count, 2, "should have received at least 2 audio frames")
}

// --- Frame preservation tests ---

func TestFrameSync_PreservesKeyframeFlag(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}, IsKeyframe: true}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	time.Sleep(40 * time.Millisecond)
	videos := handler.getVideos()
	require.NotEmpty(t, videos, "no video frames released")
	require.True(t, videos[0].frame.IsKeyframe, "IsKeyframe flag was not preserved")
}

func TestFrameSync_PreservesWireData(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	vf := &media.VideoFrame{PTS: 1000, WireData: data, IsKeyframe: false}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	time.Sleep(40 * time.Millisecond)
	videos := handler.getVideos()
	require.NotEmpty(t, videos, "no video frames released")
	require.Equal(t, data, videos[0].frame.WireData, "WireData not preserved")
}

func TestFrameSync_ReleaseSliceReuse(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(10*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	fs.IngestVideo("cam1", &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}})
	fs.IngestVideo("cam2", &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}})

	fs.Start()
	defer fs.Stop()

	// Let several ticks run so the releases slice is reused.
	time.Sleep(60 * time.Millisecond)

	// The releases field should exist on the struct and have capacity
	// from previous ticks (reused, not re-allocated each tick).
	fs.mu.Lock()
	require.NotNil(t, fs.releases, "releases slice should be initialized after ticks")
	require.GreaterOrEqual(t, cap(fs.releases), 2,
		"releases slice cap should reflect reuse across ticks")
	fs.mu.Unlock()
}

func TestMonotonicTickAccuracy(t *testing.T) {
	handler := &syncTestHandler{}
	tickRate := 10 * time.Millisecond
	numTicks := 100

	fs := NewFrameSynchronizer(tickRate, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	start := time.Now()
	fs.Start()

	// Wait for 100 ticks worth of frames. With freeze repeat, each tick
	// releases the same frame, so we need >= numTicks frames.
	deadline := time.After(3 * time.Second)
	for handler.videoCount() < numTicks {
		select {
		case <-deadline:
			t.Fatalf("timed out: only got %d frames, wanted %d", handler.videoCount(), numTicks)
		case <-time.After(5 * time.Millisecond):
		}
	}
	fs.Stop()
	elapsed := time.Since(start)

	expected := time.Duration(numTicks) * tickRate
	drift := elapsed - expected
	if drift < 0 {
		drift = -drift
	}

	// Drift should be less than one tick interval. Allow some slack for
	// goroutine scheduling but the monotonic approach should keep it tight.
	require.Less(t, drift, tickRate,
		"total drift %v over %d ticks exceeds one tick interval (%v); elapsed=%v expected=%v",
		drift, numTicks, tickRate, elapsed, expected)
}

func TestFrameSync_PTSClampAfterFreeze(t *testing.T) {
	// When a source freezes for several ticks, lastReleasedPTS accumulates
	// forward. When the source resumes with a PTS behind the accumulated
	// value, the output PTS must be clamped forward to prevent backward
	// PTS in the MPEG-TS output (which confuses downstream decoders).
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Push one frame, let it freeze for several ticks.
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// Wait for freeze to accumulate PTS well past 2000.
	time.Sleep(80 * time.Millisecond)

	videos := handler.getVideos()
	require.GreaterOrEqual(t, len(videos), 4, "need enough freeze frames")

	// Record the last freeze PTS.
	lastFreezePTS := videos[len(videos)-1].frame.PTS
	require.Greater(t, lastFreezePTS, int64(1000), "freeze PTS should have advanced")

	// Now push a fresh frame with PTS behind the accumulated freeze PTS.
	// This simulates a source resuming after a stall.
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	fs.IngestVideo("cam1", vf2)

	time.Sleep(30 * time.Millisecond)

	videos = handler.getVideos()
	// Find the frame with WireData 0x02.
	var resumePTS int64
	for _, v := range videos {
		if len(v.frame.WireData) > 0 && v.frame.WireData[0] == 0x02 {
			resumePTS = v.frame.PTS
			break
		}
	}
	require.Greater(t, resumePTS, int64(0), "should have found resume frame")
	require.Greater(t, resumePTS, lastFreezePTS,
		"resume PTS %d should be > last freeze PTS %d (clamped forward)", resumePTS, lastFreezePTS)
}

func TestFrameSync_AudioPTSMonotonic(t *testing.T) {
	// Audio repeat frames should have advancing PTS, not duplicate PTS.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	af := &media.AudioFrame{PTS: 5000, Data: []byte{0xAA}}
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	// Wait for the original + 2 repeats.
	time.Sleep(60 * time.Millisecond)
	audios := handler.getAudios()
	require.GreaterOrEqual(t, len(audios), 2, "need at least 2 audio frames")

	// All PTS values should be monotonically non-decreasing, and repeats
	// should advance (no duplicate PTS values).
	for i := 1; i < len(audios); i++ {
		require.Greater(t, audios[i].frame.PTS, audios[i-1].frame.PTS,
			"audio PTS[%d]=%d should be > PTS[%d]=%d",
			i, audios[i].frame.PTS, i-1, audios[i-1].frame.PTS)
	}
}

func TestFrameSync_RemoveSourceReleasesPoolBuffers(t *testing.T) {
	// RemoveSource should release raw video pool buffers held in ring
	// buffers and lastRawVideo to prevent FramePool starvation.
	// Test without Stop() to isolate RemoveSource behavior.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.onRawVideo = func(string, *ProcessingFrame) {} // enable raw video delivery
	fs.AddSource("cam1")

	pool := NewFramePool(2, 4, 4) // tiny 4x4 pool with 2 buffers
	buf := pool.Acquire()
	pf := &ProcessingFrame{
		PTS:  1000,
		YUV:  buf,
		pool: pool,
	}
	fs.IngestRawVideo("cam1", pf)

	// Run one tick manually so the frame becomes lastRawVideo (no Start/Stop).
	fs.releaseTick()

	// Pool should have 1 buffer (the other is held by lastRawVideo).
	pool.mu.Lock()
	freeBefore := len(pool.free)
	pool.mu.Unlock()
	require.Equal(t, 1, freeBefore, "pool should have 1 free buffer before RemoveSource")

	// Remove the source — should release the pool buffer back.
	fs.RemoveSource("cam1")

	pool.mu.Lock()
	freeAfter := len(pool.free)
	pool.mu.Unlock()
	require.Equal(t, 2, freeAfter, "pool should have 2 free buffers after RemoveSource")
}

func TestFrameSync_LastRawVideoReleasedOnReplacement(t *testing.T) {
	// When a fresh raw frame replaces lastRawVideo, the old frame's pool
	// buffer must be released to prevent FramePool starvation.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.onRawVideo = func(string, *ProcessingFrame) {} // enable raw video delivery
	fs.AddSource("cam1")

	pool := NewFramePool(3, 4, 4) // tiny 4x4 pool with 3 buffers

	// Push first frame and let it become lastRawVideo.
	buf1 := pool.Acquire()
	pf1 := &ProcessingFrame{PTS: 1000, YUV: buf1, pool: pool}
	fs.IngestRawVideo("cam1", pf1)

	// Run one tick so pf1 is popped and becomes lastRawVideo.
	fs.releaseTick()

	pool.mu.Lock()
	freeAfterFirst := len(pool.free)
	pool.mu.Unlock()
	require.Equal(t, 2, freeAfterFirst, "pool should have 2 free after first frame held as lastRawVideo")

	// Push a second frame — on next tick, it should replace lastRawVideo
	// and the old buffer (buf1) should be released back to the pool.
	buf2 := pool.Acquire()
	pf2 := &ProcessingFrame{PTS: 2000, YUV: buf2, pool: pool}
	fs.IngestRawVideo("cam1", pf2)

	fs.releaseTick()

	pool.mu.Lock()
	freeAfterReplace := len(pool.free)
	pool.mu.Unlock()
	// buf1 released + buf2 held as new lastRawVideo = 2 free
	require.Equal(t, 2, freeAfterReplace,
		"old lastRawVideo buffer should be released when replaced by fresh frame")
}

func TestFrameSync_StopReleasesPoolBuffers(t *testing.T) {
	// Bug 8: Stop() doesn't release pool buffers held in sources' pendingRawVideo,
	// lastRawVideo, and FRC state. After Stop(), all pool buffers should be returned.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.onRawVideo = func(string, *ProcessingFrame) {} // enable raw video delivery
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	pool := NewFramePool(6, 4, 4) // tiny 4x4 pool with 6 buffers

	// Acquire 4 buffers, put them into the frame sync as raw video frames.
	for i := 0; i < 2; i++ {
		for _, key := range []string{"cam1", "cam2"} {
			buf := pool.Acquire()
			pf := &ProcessingFrame{
				PTS:  int64(i * 3000),
				YUV:  buf,
				pool: pool,
			}
			fs.IngestRawVideo(key, pf)
		}
	}

	// Run one tick so frames become lastRawVideo.
	fs.Start()
	time.Sleep(50 * time.Millisecond)

	// Some buffers are held by the frame sync (lastRawVideo, pending ring slots).
	pool.mu.Lock()
	freeBefore := len(pool.free)
	pool.mu.Unlock()
	require.Less(t, freeBefore, 6, "pool should have fewer than 6 free buffers before Stop")

	// Stop should release all held pool buffers.
	fs.Stop()

	pool.mu.Lock()
	freeAfter := len(pool.free)
	pool.mu.Unlock()
	// All buffers should be back in the pool (some may have been consumed by
	// the ring buffer overwrite release, so we check that at least the
	// lastRawVideo buffers are released).
	require.Greater(t, freeAfter, freeBefore,
		"Stop() should release pool buffers: before=%d after=%d", freeBefore, freeAfter)
}

func TestFrameSync_StopReleasesPoolBuffersWithFRC(t *testing.T) {
	// Bug 8 + Bug 16: Stop() should also release FRC state buffers.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.frcQuality = FRCBlend
	fs.onRawVideo = func(string, *ProcessingFrame) {} // enable raw video delivery
	fs.AddSource("cam1")

	pool := NewFramePool(4, 64, 64)

	// Ingest frames so FRC has prevFrame and currFrame populated.
	for i := 0; i < 3; i++ {
		buf := pool.Acquire()
		yuvSize := 64 * 64 * 3 / 2
		for j := range buf[:yuvSize] {
			buf[j] = byte(100 + i)
		}
		pf := &ProcessingFrame{
			PTS:    int64(i * 3000),
			YUV:    buf[:yuvSize],
			Width:  64,
			Height: 64,
			pool:   pool,
		}
		fs.IngestRawVideo("cam1", pf)
	}

	fs.Start()
	time.Sleep(50 * time.Millisecond)

	pool.mu.Lock()
	freeBefore := len(pool.free)
	pool.mu.Unlock()

	fs.Stop()

	pool.mu.Lock()
	freeAfter := len(pool.free)
	pool.mu.Unlock()

	// FRC holds prevFrame and currFrame (2 buffers), plus lastRawVideo (1 buffer).
	// Stop should release all of them.
	require.Greater(t, freeAfter, freeBefore,
		"Stop() should release FRC pool buffers: before=%d after=%d", freeBefore, freeAfter)
}

func TestFrameSync_AudioPTSUsesAudioFrameDuration(t *testing.T) {
	// Bug 4: Repeated audio frames advance PTS by tickIntervalPTS (video tick,
	// e.g. 3003 for 29.97fps) instead of the correct audio frame duration
	// (~1920 ticks for 1024 AAC samples at 48kHz). This test verifies that
	// repeated audio PTS increments are ~1920, not ~3003.
	handler := &syncTestHandler{}
	// Use 29.97fps (tickInterval = 3003 at 90kHz) to make the wrong value
	// distinct from the correct audio value (1920).
	fs := NewFrameSynchronizer(33366666*time.Nanosecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Push one audio frame, then let it repeat via freeze behavior.
	af := &media.AudioFrame{PTS: 10000, Data: []byte{0xAA}}
	fs.IngestAudio("cam1", af)

	// Manually run enough ticks for original + 2 repeats (max allowed).
	fs.releaseTick() // tick 1: fresh frame, PTS=10000
	fs.releaseTick() // tick 2: repeat #1
	fs.releaseTick() // tick 3: repeat #2

	audios := handler.getAudios()
	require.GreaterOrEqual(t, len(audios), 3, "need at least 3 audio frames")

	// First frame: original PTS preserved.
	require.Equal(t, int64(10000), audios[0].frame.PTS, "first frame PTS")

	// Repeated frames should advance by audioFramePTS (~1920), NOT tickIntervalPTS (3003).
	const expectedAudioIncrement int64 = 1920 // 1024 * 90000 / 48000
	for i := 1; i < len(audios); i++ {
		delta := audios[i].frame.PTS - audios[i-1].frame.PTS
		require.Equal(t, expectedAudioIncrement, delta,
			"audio PTS delta[%d] = %d, want %d (not video tick interval 3003)", i, delta, expectedAudioIncrement)
	}
}

func BenchmarkReleaseTick(b *testing.B) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	for _, src := range []string{"cam1", "cam2", "cam3", "cam4"} {
		fs.AddSource(src)
	}

	frame := &media.VideoFrame{PTS: 1000, WireData: []byte{0x65, 0x01}}
	audioFrame := &media.AudioFrame{PTS: 1000, Data: []byte{0x01, 0x02}}

	for _, src := range []string{"cam1", "cam2", "cam3", "cam4"} {
		fs.IngestVideo(src, frame)
		fs.IngestAudio(src, audioFrame)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fs.releaseTick()
	}
}

func BenchmarkFrameSyncIngest(b *testing.B) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	frame := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01, 0x02, 0x03, 0x04}}
	aframe := &media.AudioFrame{PTS: 1000, Data: []byte{0x01, 0x02}}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fs.IngestVideo("cam1", frame)
		fs.IngestAudio("cam1", aframe)
	}
}

func TestTickPTSWithRemainder_NTSCDrift(t *testing.T) {
	// At 59.94fps (60000/1001), tickPTSInterval truncates:
	// 90000 * 1001 / 60000 = 1501.5 → 1501 (loses 0.5 ticks/frame).
	// Over 1 hour at 59.94fps (~215827 frames), the truncation drift is
	// 215827 * 0.5 = ~107913 ticks = ~1.2 seconds.
	// The Bresenham accumulator must eliminate this drift.
	fpsNum := int64(60000)
	fpsDen := int64(1001)
	baseInterval := int64(mpegtsClock) * fpsDen / fpsNum // 1501
	remNum := (int64(mpegtsClock) * fpsDen) % fpsNum     // 90090000 % 60000 = 30000
	remDen := fpsNum                                      // 60000

	require.Equal(t, int64(1501), baseInterval, "base interval for 59.94fps")
	require.Equal(t, int64(30000), remNum, "remainder numerator")

	ss := &syncSource{}

	// Simulate 1 hour of frames at 59.94fps
	oneHourFrames := int(60000 * 3600 / 1001) // ~215784 frames
	var totalPTS int64
	for i := 0; i < oneHourFrames; i++ {
		totalPTS += tickPTSWithRemainder(ss, baseInterval, remNum, remDen)
	}

	// Expected PTS for exactly oneHourFrames at exact 59.94fps:
	// oneHourFrames * 90000 * 1001 / 60000
	expectedPTS := int64(oneHourFrames) * int64(mpegtsClock) * fpsDen / fpsNum
	// The Bresenham remainder handles the sub-tick portion, so we also
	// need to account for the remainder that would have accumulated:
	expectedRem := (int64(oneHourFrames) * int64(mpegtsClock) * fpsDen) % fpsNum

	// The accumulated PTS should match the exact integer division
	require.Equal(t, expectedPTS, totalPTS,
		"Bresenham PTS must match exact computation over 1 hour")

	// Verify the accumulator state is consistent
	require.Equal(t, expectedRem, ss.ptsRemAccum,
		"accumulator remainder should match expected")
}

func TestTickPTSWithRemainder_IntegerFPS(t *testing.T) {
	// At integer FPS (e.g., 30fps), remainder is 0 — no correction needed.
	fpsNum := int64(30)
	fpsDen := int64(1)
	baseInterval := int64(mpegtsClock) * fpsDen / fpsNum // 3000
	remNum := (int64(mpegtsClock) * fpsDen) % fpsNum     // 0

	require.Equal(t, int64(3000), baseInterval)
	require.Equal(t, int64(0), remNum, "integer FPS should have zero remainder")

	ss := &syncSource{}

	// 1000 frames should produce exactly 3000*1000 = 3_000_000 PTS
	var totalPTS int64
	for i := 0; i < 1000; i++ {
		totalPTS += tickPTSWithRemainder(ss, baseInterval, remNum, fpsNum)
	}
	require.Equal(t, int64(3_000_000), totalPTS)
	require.Equal(t, int64(0), ss.ptsRemAccum, "no remainder for integer FPS")
}

func TestTickPTSWithRemainder_2997(t *testing.T) {
	// 29.97fps (30000/1001): 90000*1001/30000 = 3003.0 exactly.
	// Remainder is 0, so no Bresenham correction needed.
	fpsNum := int64(30000)
	fpsDen := int64(1001)
	baseInterval := int64(mpegtsClock) * fpsDen / fpsNum
	remNum := (int64(mpegtsClock) * fpsDen) % fpsNum

	require.Equal(t, int64(3003), baseInterval, "29.97fps interval")
	require.Equal(t, int64(0), remNum, "29.97fps has exact integer interval")
}

func TestTickPTSWithRemainder_23976(t *testing.T) {
	// 23.976fps (24000/1001): 90000*1001/24000 = 3753.75 → truncates to 3753.
	fpsNum := int64(24000)
	fpsDen := int64(1001)
	baseInterval := int64(mpegtsClock) * fpsDen / fpsNum // 3753
	remNum := (int64(mpegtsClock) * fpsDen) % fpsNum     // should be non-zero

	require.Equal(t, int64(3753), baseInterval, "23.976fps base interval")
	require.Greater(t, remNum, int64(0), "23.976fps should have non-zero remainder")

	ss := &syncSource{}

	// Over 24000 frames (exactly 1001 seconds at 23.976fps), PTS should be exact.
	// Expected: 24000 * 90000 * 1001 / 24000 = 90000 * 1001 = 90_090_000
	var totalPTS int64
	for i := 0; i < 24000; i++ {
		totalPTS += tickPTSWithRemainder(ss, baseInterval, remNum, fpsNum)
	}
	require.Equal(t, int64(90_090_000), totalPTS,
		"23.976fps Bresenham must be exact over 1001 seconds")
	require.Equal(t, int64(0), ss.ptsRemAccum,
		"accumulator should be zero after exact period")
}

func TestFrameSync_SetTickRateUpdatesFRCTickIntervalPTS(t *testing.T) {
	// Bug: SetTickRate() updates the global tick interval but does not
	// propagate the new value to existing frcSource.tickIntervalPTS fields.
	// FRC interpolation computes wrong alpha positions until the source is
	// removed and re-added.
	handler := &syncTestHandler{}

	// 30fps tick rate: tickPTSInterval = 90000 / 30 = 3000
	fs := NewFrameSynchronizer(33333333*time.Nanosecond, handler.onVideo, handler.onAudio)
	fs.frcQuality = FRCBlend
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	// Verify initial tickIntervalPTS = 3000 (30fps)
	fs.mu.Lock()
	initialInterval := fs.tickPTSInterval()
	for key, ss := range fs.sources {
		ss.mu.Lock()
		require.NotNil(t, ss.frc, "source %s should have FRC", key)
		require.Equal(t, initialInterval, ss.frc.tickIntervalPTS,
			"source %s initial tickIntervalPTS", key)
		ss.mu.Unlock()
	}
	fs.mu.Unlock()
	require.Equal(t, int64(3000), initialInterval, "30fps should be 3000 PTS ticks")

	// Change to 60fps: tickPTSInterval = 90000 / 60 = 1500
	fs.SetTickRate(16666666 * time.Nanosecond)

	// Verify all sources' FRC tickIntervalPTS updated to the new value
	fs.mu.Lock()
	newInterval := fs.tickPTSInterval()
	require.Equal(t, int64(1500), newInterval, "60fps should be 1500 PTS ticks")
	for key, ss := range fs.sources {
		ss.mu.Lock()
		require.NotNil(t, ss.frc, "source %s should still have FRC after SetTickRate", key)
		require.Equal(t, newInterval, ss.frc.tickIntervalPTS,
			"source %s tickIntervalPTS should be updated to %d after SetTickRate, got %d",
			key, newInterval, ss.frc.tickIntervalPTS)
		ss.mu.Unlock()
	}
	fs.mu.Unlock()
}

func TestFrameSync_AudioFIFODrainsAllOnTick(t *testing.T) {
	// Audio frames must never be dropped. Between ticks, multiple audio frames
	// may arrive (~47 AAC frames/sec vs 30 video frames/sec). All of them must
	// be released on the next tick in FIFO order — not just the newest.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Ingest 3 audio frames between ticks (more than the 2-slot ring buffer).
	af1 := &media.AudioFrame{PTS: 1000, Data: []byte{0x01}}
	af2 := &media.AudioFrame{PTS: 2000, Data: []byte{0x02}}
	af3 := &media.AudioFrame{PTS: 3000, Data: []byte{0x03}}
	fs.IngestAudio("cam1", af1)
	fs.IngestAudio("cam1", af2)
	fs.IngestAudio("cam1", af3)

	// No frames should be released yet (no tick has fired).
	require.Equal(t, 0, handler.audioCount(), "audio should not be released before tick")

	// Fire one tick manually.
	fs.releaseTick()

	// ALL 3 audio frames must be released, in FIFO order.
	audios := handler.getAudios()
	require.Equal(t, 3, len(audios), "all 3 audio frames must be released on tick (FIFO, no drop)")
	require.Equal(t, byte(0x01), audios[0].frame.Data[0], "first audio frame")
	require.Equal(t, byte(0x02), audios[1].frame.Data[0], "second audio frame")
	require.Equal(t, byte(0x03), audios[2].frame.Data[0], "third audio frame")
}

func TestFrameSync_AudioFIFOPreservesPTS(t *testing.T) {
	// FIFO-drained audio frames should preserve their original PTS values
	// (they are fresh frames, not repeats).
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	fs.IngestAudio("cam1", &media.AudioFrame{PTS: 10000, Data: []byte{0x01}})
	fs.IngestAudio("cam1", &media.AudioFrame{PTS: 11920, Data: []byte{0x02}})

	fs.releaseTick()

	audios := handler.getAudios()
	require.Equal(t, 2, len(audios), "both audio frames should be released")
	require.Equal(t, int64(10000), audios[0].frame.PTS, "first frame PTS preserved")
	require.Equal(t, int64(11920), audios[1].frame.PTS, "second frame PTS preserved")
}

func TestFrameSync_AudioFIFOEmptyOnTick(t *testing.T) {
	// When no audio frames are queued and no lastAudio exists, tick should
	// not release any audio.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Ingest only video, no audio.
	fs.IngestVideo("cam1", &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}})
	fs.releaseTick()

	require.Equal(t, 0, handler.audioCount(), "no audio should be released when queue is empty")
}

func TestFrameSync_SetTickRateNoFRC(t *testing.T) {
	// SetTickRate when FRC is disabled should not panic.
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33333333*time.Nanosecond, handler.onVideo, handler.onAudio)
	// frcQuality defaults to FRCNone, so sources won't have frc
	fs.AddSource("cam1")

	// Should not panic when frc is nil
	fs.SetTickRate(16666666 * time.Nanosecond)

	// Verify source exists but has no FRC
	fs.mu.Lock()
	ss := fs.sources["cam1"]
	ss.mu.Lock()
	require.Nil(t, ss.frc, "source should not have FRC when quality is FRCNone")
	ss.mu.Unlock()
	fs.mu.Unlock()
}
