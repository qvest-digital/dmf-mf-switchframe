package switcher

import (
	"sync"
	"testing"
	"time"

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
	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	// Without starting the ticker, nothing should be released.
	time.Sleep(10 * time.Millisecond)
	if got := handler.videoCount(); got != 0 {
		t.Fatalf("video count before Start = %d, want 0", got)
	}
}

func TestFrameSync_IngestUnknownSourceIgnored(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)

	// Ingesting to an unregistered source should not panic.
	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("unknown", vf)

	af := media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	fs.IngestAudio("unknown", af)

	// Nothing released.
	if got := handler.videoCount(); got != 0 {
		t.Fatalf("video count = %d, want 0", got)
	}
	if got := handler.audioCount(); got != 0 {
		t.Fatalf("audio count = %d, want 0", got)
	}
}

func TestFrameSync_RingBufferOverwrite(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Push 3 frames into a 2-slot ring buffer — first should be overwritten.
	vf1 := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	vf3 := media.VideoFrame{PTS: 3000, WireData: []byte{0x03}}
	fs.IngestVideo("cam1", vf1)
	fs.IngestVideo("cam1", vf2)
	fs.IngestVideo("cam1", vf3)

	// Start and wait for one tick.
	fs.Start()
	defer fs.Stop()
	time.Sleep(50 * time.Millisecond)

	// Should release the most recent frame (vf3).
	videos := handler.getVideos()
	if len(videos) == 0 {
		t.Fatal("expected at least 1 released video frame")
	}
	// The latest frame should be vf3 (PTS 3000) — oldest vf1 was overwritten.
	last := videos[len(videos)-1]
	if last.frame.WireData[0] != 0x03 {
		t.Errorf("expected latest frame data 0x03, got 0x%02x", last.frame.WireData[0])
	}
}

// --- Tick tests ---

func TestFrameSync_TickReleasesFrames(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// Wait for at least one tick cycle.
	time.Sleep(50 * time.Millisecond)
	if got := handler.videoCount(); got < 1 {
		t.Fatalf("video count after tick = %d, want >= 1", got)
	}
}

func TestFrameSync_TickReleasesAudio(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	af := media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)
	if got := handler.audioCount(); got < 1 {
		t.Fatalf("audio count after tick = %d, want >= 1", got)
	}
}

func TestFrameSync_PTSRewritten(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := media.VideoFrame{PTS: 99999, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)
	videos := handler.getVideos()
	if len(videos) == 0 {
		t.Fatal("no video frames released")
	}
	// PTS should be rewritten to a tick-based timestamp (not the original 99999).
	if videos[0].frame.PTS == 99999 {
		t.Error("PTS was not rewritten — still original value 99999")
	}
}

func TestFrameSync_FreezeRepeatsLastFrame(t *testing.T) {
	handler := &syncTestHandler{}
	// Use a fast tick for reliable testing.
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	// Push one frame, then let the ticker run without pushing more.
	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0xAA}, IsKeyframe: true}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// Wait for several ticks — frame should be repeated (freeze behavior).
	time.Sleep(80 * time.Millisecond)
	count := handler.videoCount()
	if count < 3 {
		t.Fatalf("video count after multiple ticks = %d, want >= 3 (freeze repeat)", count)
	}

	// All repeated frames should have the same WireData.
	videos := handler.getVideos()
	for i, v := range videos {
		if v.frame.WireData[0] != 0xAA {
			t.Errorf("frame[%d] data = 0x%02x, want 0xAA", i, v.frame.WireData[0])
		}
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
	if got := handler.videoCount(); got != 0 {
		t.Fatalf("video count = %d, want 0 (no frame ingested)", got)
	}
}

// --- Multi-source alignment tests ---

func TestFrameSync_MultiSourceAlignment(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	vf1 := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
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
	if cam1Count < 1 {
		t.Errorf("cam1 video count = %d, want >= 1", cam1Count)
	}
	if cam2Count < 1 {
		t.Errorf("cam2 video count = %d, want >= 1", cam2Count)
	}

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
		if ptsDiff > oneTick90kHz {
			t.Errorf("PTS difference between sources = %d, want <= %d (one tick at 90 kHz)", ptsDiff, oneTick90kHz)
		}
	}
}

func TestFrameSync_AddSourceDynamic(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	fs.Start()
	defer fs.Stop()

	vf1 := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf1)
	time.Sleep(40 * time.Millisecond)

	// Add a second source dynamically while running.
	fs.AddSource("cam2")
	vf2 := media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
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
	if !cam2Found {
		t.Error("cam2 frame not released after dynamic add")
	}
}

func TestFrameSync_RemoveSource(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.AddSource("cam2")

	vf1 := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
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
	vf3 := media.VideoFrame{PTS: 3000, WireData: []byte{0x03}}
	fs.IngestVideo("cam2", vf3)

	time.Sleep(40 * time.Millisecond)

	videos := handler.getVideos()
	for _, v := range videos {
		if v.sourceKey == "cam2" {
			t.Error("cam2 frame released after RemoveSource")
		}
	}
}

// --- SetTickRate tests ---

func TestFrameSync_SetTickRate(t *testing.T) {
	handler := &syncTestHandler{}
	// Start with a slow tick rate.
	fs := NewFrameSynchronizer(100*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	// At 100ms tick, after 80ms we should have 0 releases.
	time.Sleep(80 * time.Millisecond)
	countBefore := handler.videoCount()

	// Speed up to 15ms tick.
	fs.SetTickRate(15 * time.Millisecond)

	// Push another frame so freeze has content.
	vf2 := media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	fs.IngestVideo("cam1", vf2)

	// Wait for several fast ticks.
	time.Sleep(80 * time.Millisecond)
	countAfter := handler.videoCount()

	// Should have more frames after speeding up.
	gained := countAfter - countBefore
	if gained < 3 {
		t.Errorf("after SetTickRate(15ms), gained only %d frames in 80ms, want >= 3", gained)
	}
}

// --- Stop tests ---

func TestFrameSync_StopCeasesTicking(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	time.Sleep(50 * time.Millisecond)
	fs.Stop()

	countAtStop := handler.videoCount()
	time.Sleep(50 * time.Millisecond)
	countAfter := handler.videoCount()

	if countAfter != countAtStop {
		t.Errorf("frames released after Stop: before=%d, after=%d", countAtStop, countAfter)
	}
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
			fs.IngestVideo("cam1", media.VideoFrame{PTS: pts, WireData: []byte{0x01}})
		}(int64(i * 1000))
		go func(pts int64) {
			defer wg.Done()
			fs.IngestVideo("cam2", media.VideoFrame{PTS: pts, WireData: []byte{0x02}})
		}(int64(i * 1000))
	}
	wg.Wait()

	time.Sleep(30 * time.Millisecond)
	// Should not panic; just verify we got some frames.
	if got := handler.videoCount(); got == 0 {
		t.Error("no frames released during concurrent ingest")
	}
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
			fs.IngestVideo(key, media.VideoFrame{PTS: int64(idx), WireData: []byte{0x01}})
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

	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	af := media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	fs.IngestVideo("cam1", vf)
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	time.Sleep(50 * time.Millisecond)

	if handler.videoCount() < 1 {
		t.Error("no video frames released")
	}
	if handler.audioCount() < 1 {
		t.Error("no audio frames released")
	}
}

func TestFrameSync_AudioFreezeRepeatsLast(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(15*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	af := media.AudioFrame{PTS: 1000, Data: []byte{0xBB}}
	fs.IngestAudio("cam1", af)

	fs.Start()
	defer fs.Stop()

	time.Sleep(60 * time.Millisecond)
	count := handler.audioCount()
	if count < 2 {
		t.Fatalf("audio count = %d, want >= 2 (freeze repeat)", count)
	}
	audios := handler.getAudios()
	for i, a := range audios {
		if a.frame.Data[0] != 0xBB {
			t.Errorf("audio[%d] data = 0x%02x, want 0xBB", i, a.frame.Data[0])
		}
	}
}

func TestFrameSync_AudioFreezeLimit(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(10*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")
	fs.Start()
	defer fs.Stop()

	// Send one audio frame, then never send another.
	fs.IngestAudio("cam1", media.AudioFrame{PTS: 1000, Data: []byte{0x01}})

	// Wait for several ticks (more than 3).
	// With 10ms ticks over 80ms we'd get ~8 ticks, but audio should stop after 3
	// (1 original emission + 2 repeats = 3 total).
	time.Sleep(80 * time.Millisecond)

	count := handler.audioCount()
	// Must have received the original + at most 2 repeats = 3 audio frames.
	if count > 4 {
		t.Errorf("audio should stop repeating after 2 misses, got %d frames (want <= 4)", count)
	}
	if count < 2 {
		t.Errorf("should have received at least 2 audio frames, got %d", count)
	}
}

// --- Frame preservation tests ---

func TestFrameSync_PreservesKeyframeFlag(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	vf := media.VideoFrame{PTS: 1000, WireData: []byte{0x01}, IsKeyframe: true}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	time.Sleep(40 * time.Millisecond)
	videos := handler.getVideos()
	if len(videos) == 0 {
		t.Fatal("no video frames released")
	}
	if !videos[0].frame.IsKeyframe {
		t.Error("IsKeyframe flag was not preserved")
	}
}

func TestFrameSync_PreservesWireData(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(20*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	vf := media.VideoFrame{PTS: 1000, WireData: data, IsKeyframe: false}
	fs.IngestVideo("cam1", vf)

	fs.Start()
	defer fs.Stop()

	time.Sleep(40 * time.Millisecond)
	videos := handler.getVideos()
	if len(videos) == 0 {
		t.Fatal("no video frames released")
	}
	got := videos[0].frame.WireData
	if len(got) != len(data) {
		t.Fatalf("WireData length = %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Errorf("WireData[%d] = 0x%02x, want 0x%02x", i, got[i], data[i])
		}
	}
}
