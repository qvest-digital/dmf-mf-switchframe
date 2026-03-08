package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

// delayTestHandler captures frames forwarded by the DelayBuffer with
// receive timestamps for asserting delay behavior.
type delayTestHandler struct {
	mu       sync.Mutex
	videos   []delayTaggedVideo
	audios   []delayTaggedAudio
	captions []delayTaggedCaption
}

type delayTaggedVideo struct {
	sourceKey string
	frame     *media.VideoFrame
	recvTime  time.Time
}

type delayTaggedAudio struct {
	sourceKey string
	frame     *media.AudioFrame
	recvTime  time.Time
}

type delayTaggedCaption struct {
	sourceKey string
	frame     *ccx.CaptionFrame
	recvTime  time.Time
}

func (m *delayTestHandler) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videos = append(m.videos, delayTaggedVideo{sourceKey: sourceKey, frame: frame, recvTime: time.Now()})
}

func (m *delayTestHandler) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audios = append(m.audios, delayTaggedAudio{sourceKey: sourceKey, frame: frame, recvTime: time.Now()})
}

func (m *delayTestHandler) handleRawVideoFrame(sourceKey string, pf *ProcessingFrame) {
	// No-op for delay buffer tests
}

func (m *delayTestHandler) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.captions = append(m.captions, delayTaggedCaption{sourceKey: sourceKey, frame: frame, recvTime: time.Now()})
}

func (m *delayTestHandler) videoCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.videos)
}

func (m *delayTestHandler) audioCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.audios)
}

func (m *delayTestHandler) captionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.captions)
}

func (m *delayTestHandler) getVideos() []delayTaggedVideo {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]delayTaggedVideo, len(m.videos))
	copy(cp, m.videos)
	return cp
}

func (m *delayTestHandler) getAudios() []delayTaggedAudio {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]delayTaggedAudio, len(m.audios))
	copy(cp, m.audios)
	return cp
}

func TestDelayBuffer_ZeroDelayPassthrough(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	// With zero delay (default), frames should be forwarded immediately.
	before := time.Now()
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	af := &media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	cf := &ccx.CaptionFrame{}

	db.handleVideoFrame("cam1", vf)
	db.handleAudioFrame("cam1", af)
	db.handleCaptionFrame("cam1", cf)

	// All frames should be delivered immediately (no ticker needed).
	require.Equal(t, 1, handler.videoCount(), "video count")
	require.Equal(t, 1, handler.audioCount(), "audio count")
	require.Equal(t, 1, handler.captionCount(), "caption count")

	videos := handler.getVideos()
	require.Equal(t, "cam1", videos[0].sourceKey)
	require.Equal(t, vf, videos[0].frame, "video frame pointer mismatch")
	require.False(t, videos[0].recvTime.Before(before), "video received before push time")
}

func TestDelayBuffer_100msDelay(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	db.SetDelay("cam1", 100*time.Millisecond)

	pushTime := time.Now()
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	db.handleVideoFrame("cam1", vf)

	// Frame should NOT be delivered immediately.
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 0, handler.videoCount(), "video count after 10ms")

	// Wait for the delay to elapse (100ms + some margin for ticker).
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, 1, handler.videoCount(), "video count after 160ms")

	videos := handler.getVideos()
	elapsed := videos[0].recvTime.Sub(pushTime)
	require.GreaterOrEqual(t, elapsed, 90*time.Millisecond, "frame delivered too early: %v", elapsed)
	require.LessOrEqual(t, elapsed, 200*time.Millisecond, "frame delivered too late: %v", elapsed)
}

func TestDelayBuffer_PairedVideoAndAudio(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	db.SetDelay("cam1", 80*time.Millisecond)

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	af := &media.AudioFrame{PTS: 1000, Data: []byte{0x02}}

	pushTime := time.Now()
	db.handleVideoFrame("cam1", vf)
	db.handleAudioFrame("cam1", af)

	// Neither should be delivered immediately.
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 0, handler.videoCount(), "video delivered before delay elapsed")
	require.Equal(t, 0, handler.audioCount(), "audio delivered before delay elapsed")

	// Wait for delay to elapse.
	time.Sleep(130 * time.Millisecond)
	require.Equal(t, 1, handler.videoCount(), "video count")
	require.Equal(t, 1, handler.audioCount(), "audio count")

	// Both should arrive at approximately the same time.
	videos := handler.getVideos()
	audios := handler.getAudios()
	diff := videos[0].recvTime.Sub(audios[0].recvTime)
	if diff < 0 {
		diff = -diff
	}
	require.Less(t, diff, 10*time.Millisecond, "video/audio delivery difference = %v, want < 10ms", diff)

	// Both should have correct delay from push time.
	vElapsed := videos[0].recvTime.Sub(pushTime)
	aElapsed := audios[0].recvTime.Sub(pushTime)
	require.GreaterOrEqual(t, vElapsed, 70*time.Millisecond, "video delivered too early: %v", vElapsed)
	require.GreaterOrEqual(t, aElapsed, 70*time.Millisecond, "audio delivered too early: %v", aElapsed)
}

func TestDelayBuffer_IndependentSources(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	// cam1 has 50ms delay, cam2 has 150ms delay.
	db.SetDelay("cam1", 50*time.Millisecond)
	db.SetDelay("cam2", 150*time.Millisecond)

	vf1 := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}

	db.handleVideoFrame("cam1", vf1)
	db.handleVideoFrame("cam2", vf2)

	// After 100ms, cam1's frame should be delivered but not cam2's.
	time.Sleep(100 * time.Millisecond)
	videos := handler.getVideos()
	cam1Count := 0
	cam2Count := 0
	for _, v := range videos {
		if v.sourceKey == "cam1" {
			cam1Count++
		}
		if v.sourceKey == "cam2" {
			cam2Count++
		}
	}
	require.Equal(t, 1, cam1Count, "cam1 video count at 100ms")
	require.Equal(t, 0, cam2Count, "cam2 video count at 100ms")

	// After another 100ms (total ~200ms), cam2 should also be delivered.
	time.Sleep(100 * time.Millisecond)
	videos = handler.getVideos()
	cam2Count = 0
	for _, v := range videos {
		if v.sourceKey == "cam2" {
			cam2Count++
		}
	}
	require.Equal(t, 1, cam2Count, "cam2 video count at 200ms")
}

func TestDelayBuffer_MidStreamDelayChange(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	// Start with 200ms delay.
	db.SetDelay("cam1", 200*time.Millisecond)

	vf1 := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	db.handleVideoFrame("cam1", vf1)

	// Change delay to 50ms after queuing the first frame.
	time.Sleep(10 * time.Millisecond)
	db.SetDelay("cam1", 50*time.Millisecond)

	// Push a second frame at the new delay.
	vf2 := &media.VideoFrame{PTS: 2000, WireData: []byte{0x02}}
	db.handleVideoFrame("cam1", vf2)

	// After ~80ms, vf2 (50ms delay) should be delivered, but vf1 (200ms delay) not yet.
	time.Sleep(80 * time.Millisecond)
	videos := handler.getVideos()
	require.Equal(t, 1, len(videos), "video count at 90ms")
	require.Equal(t, vf2, videos[0].frame, "expected vf2 to be delivered first (shorter delay)")

	// After the original 200ms, vf1 should also be delivered.
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, 2, handler.videoCount(), "video count at 240ms")
}

func TestDelayBuffer_RemoveSource(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	db.SetDelay("cam1", 200*time.Millisecond)

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	db.handleVideoFrame("cam1", vf)

	// Remove the source before the delay elapses.
	time.Sleep(10 * time.Millisecond)
	db.RemoveSource("cam1")

	// The queued frame should be discarded (not delivered).
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, 0, handler.videoCount(), "video count after remove (frame should be discarded)")

	// Verify the source delay is gone.
	require.Equal(t, time.Duration(0), db.GetDelay("cam1"), "delay after remove")
}

func TestDelayBuffer_Close(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)

	db.SetDelay("cam1", 500*time.Millisecond)

	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	db.handleVideoFrame("cam1", vf)

	// Close should stop cleanly and not panic.
	db.Close()

	// After close, queued frames are discarded.
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 0, handler.videoCount(), "video count after close")

	// Calling Close again should not panic.
	db.Close()
}

func TestDelayBuffer_CloseWaitsForGoroutine(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)

	// After Close returns, the done channel must be closed.
	db.Close()

	// After Close, the done channel must be closed.
	select {
	case <-db.done:
		// success — done channel is closed
	default:
		t.Fatal("Close returned without closing done channel")
	}
}

func TestDelayBuffer_AtomicFastPath_NoDelay(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	// hasAnyDelay should be false initially.
	require.False(t, db.hasAnyDelay.Load(), "hasAnyDelay should be false with no delays set")

	// All frame types should pass through without acquiring the mutex.
	vf := &media.VideoFrame{PTS: 1000, WireData: []byte{0x01}}
	af := &media.AudioFrame{PTS: 1000, Data: []byte{0x02}}
	cf := &ccx.CaptionFrame{}

	db.handleVideoFrame("cam1", vf)
	db.handleAudioFrame("cam1", af)
	db.handleCaptionFrame("cam1", cf)

	require.Equal(t, 1, handler.videoCount(), "video fast path")
	require.Equal(t, 1, handler.audioCount(), "audio fast path")
	require.Equal(t, 1, handler.captionCount(), "caption fast path")
}

func TestDelayBuffer_AtomicFastPath_SetAndClear(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	// Setting a non-zero delay should flip hasAnyDelay to true.
	db.SetDelay("cam1", 100*time.Millisecond)
	require.True(t, db.hasAnyDelay.Load(), "hasAnyDelay should be true after SetDelay")

	// Setting it back to zero should flip hasAnyDelay to false.
	db.SetDelay("cam1", 0)
	require.False(t, db.hasAnyDelay.Load(), "hasAnyDelay should be false after clearing delay")
}

func TestDelayBuffer_AtomicFastPath_RemoveSource(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	db.SetDelay("cam1", 100*time.Millisecond)
	require.True(t, db.hasAnyDelay.Load())

	db.RemoveSource("cam1")
	require.False(t, db.hasAnyDelay.Load(),
		"hasAnyDelay should be false after removing the only delayed source")
}

func BenchmarkDelayBufferZeroDelay(b *testing.B) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()
	frame := &media.VideoFrame{PTS: 1000, WireData: []byte{0x65, 0x01}}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		db.handleVideoFrame("cam1", frame)
	}
}

func TestDelayBuffer_AtomicFastPath_MultipleDelays(t *testing.T) {
	handler := &delayTestHandler{}
	db := NewDelayBuffer(handler)
	defer db.Close()

	db.SetDelay("cam1", 50*time.Millisecond)
	db.SetDelay("cam2", 100*time.Millisecond)
	require.True(t, db.hasAnyDelay.Load())

	// Removing one still leaves another with delay.
	db.RemoveSource("cam1")
	require.True(t, db.hasAnyDelay.Load(),
		"hasAnyDelay should stay true when another source still has delay")

	db.SetDelay("cam2", 0)
	require.False(t, db.hasAnyDelay.Load(),
		"hasAnyDelay should be false when all delays are zero")
}
