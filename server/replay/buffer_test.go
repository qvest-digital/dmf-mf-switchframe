package replay

import (
	"testing"
	"time"

	"github.com/zsiec/prism/media"
)

func makeVideoFrame(pts int64, keyframe bool, dataSize int) *media.VideoFrame {
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	f := &media.VideoFrame{
		PTS:        pts,
		IsKeyframe: keyframe,
		WireData:   data,
		Codec:      "avc1.42C01E",
	}
	if keyframe {
		f.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
		f.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
	}
	return f
}

func TestNewReplayBuffer(t *testing.T) {
	buf := newReplayBuffer(60)
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	if buf.maxDuration != 60*time.Second {
		t.Errorf("expected maxDuration 60s, got %v", buf.maxDuration)
	}
}

func TestReplayBuffer_RecordFrame_KeyframeStartsGOP(t *testing.T) {
	buf := newReplayBuffer(60)
	frame := makeVideoFrame(0, true, 1000)
	buf.RecordFrame(frame)

	info := buf.Status()
	if info.FrameCount != 1 {
		t.Errorf("expected 1 frame, got %d", info.FrameCount)
	}
	if info.GOPCount != 1 {
		t.Errorf("expected 1 GOP, got %d", info.GOPCount)
	}
}

func TestReplayBuffer_RecordFrame_DeltaAppendsToCurrentGOP(t *testing.T) {
	buf := newReplayBuffer(60)
	buf.RecordFrame(makeVideoFrame(0, true, 1000))
	buf.RecordFrame(makeVideoFrame(3003, false, 500))
	buf.RecordFrame(makeVideoFrame(6006, false, 500))

	info := buf.Status()
	if info.FrameCount != 3 {
		t.Errorf("expected 3 frames, got %d", info.FrameCount)
	}
	if info.GOPCount != 1 {
		t.Errorf("expected 1 GOP, got %d", info.GOPCount)
	}
}

func TestReplayBuffer_RecordFrame_MultipleGOPs(t *testing.T) {
	buf := newReplayBuffer(60)
	// GOP 1: keyframe + 2 deltas
	buf.RecordFrame(makeVideoFrame(0, true, 1000))
	buf.RecordFrame(makeVideoFrame(3003, false, 500))
	buf.RecordFrame(makeVideoFrame(6006, false, 500))
	// GOP 2: keyframe + 1 delta
	buf.RecordFrame(makeVideoFrame(9009, true, 1000))
	buf.RecordFrame(makeVideoFrame(12012, false, 500))

	info := buf.Status()
	if info.FrameCount != 5 {
		t.Errorf("expected 5 frames, got %d", info.FrameCount)
	}
	if info.GOPCount != 2 {
		t.Errorf("expected 2 GOPs, got %d", info.GOPCount)
	}
}

func TestReplayBuffer_RecordFrame_DeepCopiesData(t *testing.T) {
	buf := newReplayBuffer(60)
	frame := makeVideoFrame(0, true, 100)
	original := make([]byte, len(frame.WireData))
	copy(original, frame.WireData)

	buf.RecordFrame(frame)

	// Mutate the original frame's data
	frame.WireData[0] = 0xFF

	// Buffer's copy should be unaffected
	buf.mu.RLock()
	if buf.frames[0].wireData[0] == 0xFF {
		t.Error("buffer should hold a deep copy, not a reference")
	}
	buf.mu.RUnlock()
}

func TestReplayBuffer_RecordFrame_DeltaBeforeKeyframeDropped(t *testing.T) {
	buf := newReplayBuffer(60)
	// Delta without a preceding keyframe should be dropped
	buf.RecordFrame(makeVideoFrame(3003, false, 500))

	info := buf.Status()
	if info.FrameCount != 0 {
		t.Errorf("expected 0 frames (delta before keyframe), got %d", info.FrameCount)
	}
}

func TestReplayBuffer_GOPAlignedTrimming(t *testing.T) {
	buf := newReplayBuffer(1) // 1 second buffer — very small

	now := time.Now()
	// Fill with GOPs spaced 500ms apart (2 GOPs should exceed 1s)
	for i := 0; i < 3; i++ {
		kf := makeVideoFrame(int64(i)*90000, true, 1000)
		buf.recordFrameAt(kf, now.Add(time.Duration(i)*500*time.Millisecond))
		for j := 1; j <= 3; j++ {
			df := makeVideoFrame(int64(i)*90000+int64(j)*3003, false, 500)
			buf.recordFrameAt(df, now.Add(time.Duration(i)*500*time.Millisecond+time.Duration(j)*33*time.Millisecond))
		}
	}

	info := buf.Status()
	// Oldest GOP(s) should have been trimmed
	if info.GOPCount >= 3 {
		t.Errorf("expected fewer than 3 GOPs after trimming, got %d", info.GOPCount)
	}
	// First frame should be a keyframe (GOP-aligned)
	buf.mu.RLock()
	if len(buf.frames) > 0 && !buf.frames[0].isKeyframe {
		t.Error("first frame after trim should be a keyframe")
	}
	buf.mu.RUnlock()
}

func TestReplayBuffer_ExtractClip(t *testing.T) {
	buf := newReplayBuffer(60)
	now := time.Now()

	// Record frames with known wall times
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(6006, false, 500), now.Add(66*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(9009, true, 1000), now.Add(100*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(12012, false, 500), now.Add(133*time.Millisecond))

	// Extract clip spanning the first GOP
	clip, err := buf.ExtractClip(now.Add(-1*time.Millisecond), now.Add(99*time.Millisecond))
	if err != nil {
		t.Fatalf("ExtractClip: %v", err)
	}

	// Should include GOP starting before or at the mark-in time
	if len(clip) == 0 {
		t.Fatal("expected non-empty clip")
	}
	// First frame should be a keyframe
	if !clip[0].isKeyframe {
		t.Error("first frame of clip should be a keyframe")
	}
}

func TestReplayBuffer_ExtractClip_EmptyBuffer(t *testing.T) {
	buf := newReplayBuffer(60)
	now := time.Now()
	_, err := buf.ExtractClip(now.Add(-1*time.Second), now)
	if err != ErrEmptyClip {
		t.Errorf("expected ErrEmptyClip, got %v", err)
	}
}

func TestReplayBuffer_ExtractClip_NoFramesInRange(t *testing.T) {
	buf := newReplayBuffer(60)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)

	// Query a time range that doesn't overlap
	_, err := buf.ExtractClip(now.Add(10*time.Second), now.Add(20*time.Second))
	if err != ErrEmptyClip {
		t.Errorf("expected ErrEmptyClip, got %v", err)
	}
}

func TestReplayBuffer_ExtractClip_DeepCopies(t *testing.T) {
	buf := newReplayBuffer(60)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 100), now)

	clip, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	if err != nil {
		t.Fatalf("ExtractClip: %v", err)
	}

	// Mutate clip data — buffer should be unaffected
	clip[0].wireData[0] = 0xFF
	buf.mu.RLock()
	if buf.frames[0].wireData[0] == 0xFF {
		t.Error("ExtractClip should return deep copies")
	}
	buf.mu.RUnlock()
}

func TestReplayBuffer_BytesUsed(t *testing.T) {
	buf := newReplayBuffer(60)
	buf.RecordFrame(makeVideoFrame(0, true, 1000))
	buf.RecordFrame(makeVideoFrame(3003, false, 500))

	info := buf.Status()
	if info.BytesUsed <= 0 {
		t.Error("expected positive BytesUsed")
	}
}

func TestReplayBuffer_DurationSecs(t *testing.T) {
	buf := newReplayBuffer(60)
	now := time.Now()

	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(90000, true, 1000), now.Add(1*time.Second))

	info := buf.Status()
	if info.DurationSecs < 0.9 || info.DurationSecs > 1.1 {
		t.Errorf("expected ~1.0s duration, got %f", info.DurationSecs)
	}
}

func TestReplayBuffer_ConcurrentAccess(t *testing.T) {
	buf := newReplayBuffer(60)
	done := make(chan struct{})

	// Writer goroutine
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			keyframe := i%30 == 0
			buf.RecordFrame(makeVideoFrame(int64(i)*3003, keyframe, 500))
		}
	}()

	// Reader goroutine
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = buf.Status()
			}
		}
	}()

	<-done
}
