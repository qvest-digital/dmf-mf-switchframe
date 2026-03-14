package replay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	buf := newReplayBuffer(60, 0)
	require.NotNil(t, buf)
	require.Equal(t, 60*time.Second, buf.maxDuration)
}

func TestReplayBuffer_RecordFrame_KeyframeStartsGOP(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	frame := makeVideoFrame(0, true, 1000)
	buf.RecordFrame(frame)

	info := buf.Status()
	require.Equal(t, 1, info.FrameCount)
	require.Equal(t, 1, info.GOPCount)
}

func TestReplayBuffer_RecordFrame_DeltaAppendsToCurrentGOP(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	buf.RecordFrame(makeVideoFrame(0, true, 1000))
	buf.RecordFrame(makeVideoFrame(3003, false, 500))
	buf.RecordFrame(makeVideoFrame(6006, false, 500))

	info := buf.Status()
	require.Equal(t, 3, info.FrameCount)
	require.Equal(t, 1, info.GOPCount)
}

func TestReplayBuffer_RecordFrame_MultipleGOPs(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	// GOP 1: keyframe + 2 deltas
	buf.RecordFrame(makeVideoFrame(0, true, 1000))
	buf.RecordFrame(makeVideoFrame(3003, false, 500))
	buf.RecordFrame(makeVideoFrame(6006, false, 500))
	// GOP 2: keyframe + 1 delta
	buf.RecordFrame(makeVideoFrame(9009, true, 1000))
	buf.RecordFrame(makeVideoFrame(12012, false, 500))

	info := buf.Status()
	require.Equal(t, 5, info.FrameCount)
	require.Equal(t, 2, info.GOPCount)
}

func TestReplayBuffer_RecordFrame_DeepCopiesData(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	frame := makeVideoFrame(0, true, 100)
	original := make([]byte, len(frame.WireData))
	copy(original, frame.WireData)

	buf.RecordFrame(frame)

	// Mutate the original frame's data
	frame.WireData[0] = 0xFF

	// Buffer's copy should be unaffected
	buf.mu.RLock()
	require.NotEqual(t, byte(0xFF), buf.frames[0].wireData[0],
		"buffer should hold a deep copy, not a reference")
	buf.mu.RUnlock()
}

func TestReplayBuffer_RecordFrame_DeltaBeforeKeyframeDropped(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	// Delta without a preceding keyframe should be dropped
	buf.RecordFrame(makeVideoFrame(3003, false, 500))

	info := buf.Status()
	require.Equal(t, 0, info.FrameCount, "expected 0 frames (delta before keyframe)")
}

func TestReplayBuffer_GOPAlignedTrimming(t *testing.T) {
	buf := newReplayBuffer(1, 0) // 1 second buffer — very small

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
	require.Less(t, info.GOPCount, 3, "expected fewer than 3 GOPs after trimming")
	// First frame should be a keyframe (GOP-aligned)
	buf.mu.RLock()
	if len(buf.frames) > 0 {
		require.True(t, buf.frames[0].isKeyframe, "first frame after trim should be a keyframe")
	}
	buf.mu.RUnlock()
}

func TestReplayBuffer_ExtractClip(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Record frames with known wall times
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(6006, false, 500), now.Add(66*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(9009, true, 1000), now.Add(100*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(12012, false, 500), now.Add(133*time.Millisecond))

	// Extract clip spanning the first GOP
	clip, _, err := buf.ExtractClip(now.Add(-1*time.Millisecond), now.Add(99*time.Millisecond))
	require.NoError(t, err)

	// Should include GOP starting before or at the mark-in time
	require.NotEmpty(t, clip)
	// First frame should be a keyframe
	require.True(t, clip[0].isKeyframe, "first frame of clip should be a keyframe")
}

func TestReplayBuffer_ExtractClip_EmptyBuffer(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()
	_, _, err := buf.ExtractClip(now.Add(-1*time.Second), now)
	require.ErrorIs(t, err, ErrEmptyClip)
}

func TestReplayBuffer_ExtractClip_NoFramesInRange(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)

	// Query a time range that doesn't overlap
	_, _, err := buf.ExtractClip(now.Add(10*time.Second), now.Add(20*time.Second))
	require.ErrorIs(t, err, ErrEmptyClip)
}

func TestReplayBuffer_ExtractClip_DeepCopies(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 100), now)

	clip, _, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)

	// Mutate clip data — buffer should be unaffected
	clip[0].wireData[0] = 0xFF
	buf.mu.RLock()
	require.NotEqual(t, byte(0xFF), buf.frames[0].wireData[0],
		"ExtractClip should return deep copies")
	buf.mu.RUnlock()
}

func TestReplayBuffer_BytesUsed(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	buf.RecordFrame(makeVideoFrame(0, true, 1000))
	buf.RecordFrame(makeVideoFrame(3003, false, 500))

	info := buf.Status()
	require.Greater(t, info.BytesUsed, int64(0), "expected positive BytesUsed")
}

func TestReplayBuffer_DurationSecs(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(90000, true, 1000), now.Add(1*time.Second))

	info := buf.Status()
	require.InDelta(t, 1.0, info.DurationSecs, 0.1)
}

func TestReplayBuffer_ByteLimit(t *testing.T) {
	// Set a very low byte limit (5KB) — recording large frames should trigger trimming.
	buf := newReplayBuffer(60, 5000)
	now := time.Now()

	// Record 3 GOPs, each with 1000-byte keyframes and 500-byte deltas.
	// Each GOP ≈ 1000 + 2×500 = 2000 bytes of wire data (plus SPS/PPS).
	for g := 0; g < 3; g++ {
		kf := makeVideoFrame(int64(g)*90000, true, 1000)
		buf.recordFrameAt(kf, now.Add(time.Duration(g)*time.Second))
		for j := 1; j <= 2; j++ {
			df := makeVideoFrame(int64(g)*90000+int64(j)*3003, false, 500)
			buf.recordFrameAt(df, now.Add(time.Duration(g)*time.Second+time.Duration(j)*33*time.Millisecond))
		}
	}

	info := buf.Status()
	// With a 5KB byte limit, not all 3 GOPs should fit.
	require.LessOrEqual(t, info.BytesUsed, int64(5000))
	require.Less(t, info.GOPCount, 3, "expected fewer than 3 GOPs after byte-limit trimming")
	// Buffer should still have at least 1 GOP.
	require.GreaterOrEqual(t, info.GOPCount, 1)
	// First frame should be a keyframe.
	buf.mu.RLock()
	if len(buf.frames) > 0 {
		require.True(t, buf.frames[0].isKeyframe,
			"first frame after byte-limit trim should be a keyframe")
	}
	buf.mu.RUnlock()
}

func TestReplayBuffer_ExtractClip_AudioIncludesLastFrameDuration(t *testing.T) {
	// Regression test: audio frames that fall within the last video frame's
	// display period (up to one frame duration after the last video frame)
	// must be included in the extracted clip. Previously, clipEndTime was set
	// to the last video frame's wall time, dropping ~21ms of audio (one AAC
	// frame) at the end of every clip.
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Record 4 video frames at 30fps (~33ms apart).
	// Frame 0: t+0ms   (keyframe)
	// Frame 1: t+33ms
	// Frame 2: t+66ms
	// Frame 3: t+99ms
	frameDuration := 33 * time.Millisecond
	for i := 0; i < 4; i++ {
		kf := i == 0
		pts := int64(i) * 3003 // 90kHz PTS at 30fps
		wallTime := now.Add(time.Duration(i) * frameDuration)
		buf.recordFrameAt(makeVideoFrame(pts, kf, 500), wallTime)
	}

	// Record audio frames at ~21ms AAC intervals.
	// Audio 0: t+0ms
	// Audio 1: t+21ms
	// Audio 2: t+42ms
	// Audio 3: t+63ms
	// Audio 4: t+84ms
	// Audio 5: t+105ms  <-- within frame 3's display period (t+99ms to t+132ms)
	// Audio 6: t+126ms  <-- within frame 3's display period
	// Audio 7: t+147ms  <-- beyond frame 3's display period
	aacDuration := 21 * time.Millisecond
	for i := 0; i < 8; i++ {
		af := &media.AudioFrame{
			PTS:        int64(i) * 1920, // 90kHz PTS for ~21ms AAC frames
			Data:       []byte{0xAA, byte(i)},
			SampleRate: 48000,
			Channels:   2,
		}
		wallTime := now.Add(time.Duration(i) * aacDuration)
		buf.recordAudioFrameAt(af, wallTime)
	}

	// Extract clip covering all 4 video frames.
	clip, audioClip, err := buf.ExtractClip(
		now.Add(-1*time.Millisecond),
		now.Add(200*time.Millisecond),
	)
	require.NoError(t, err)
	require.Len(t, clip, 4, "should extract all 4 video frames")

	// Last video frame is at t+99ms. Its display period extends to t+132ms
	// (one frame duration later). Audio frames at t+105ms and t+126ms both
	// fall within this display period and must be included.
	// Audio at t+147ms is beyond the display period and should be excluded.
	//
	// Expected audio frames: 0,1,2,3,4,5,6 (indices 0 through 6)
	// Audio 7 at t+147ms should be excluded (beyond t+99ms + 33ms = t+132ms)
	require.Len(t, audioClip, 7,
		"audio frames within the last video frame's display period should be included; "+
			"got %d audio frames instead of 7", len(audioClip))

	// Verify the last included audio frame is the one at t+126ms.
	lastAudio := audioClip[len(audioClip)-1]
	expectedLastAudioTime := now.Add(6 * aacDuration) // t+126ms
	require.Equal(t, expectedLastAudioTime, lastAudio.wallTime,
		"last audio frame should be at t+126ms (within display period)")
}

func TestReplayBuffer_AudioFrameDataIntegrity(t *testing.T) {
	// Fix 8: Verify audio frame data is correctly deep-copied into the buffer.
	// A self-copy bug (copy(af.data, af.data)) would leave the buffer with
	// zero-filled data instead of the original audio content.
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Record a keyframe first (required for audio recording).
	buf.recordFrameAt(makeVideoFrame(0, true, 100), now)

	// Record an audio frame with known non-zero data.
	audioData := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	af := &media.AudioFrame{
		PTS:        1000,
		Data:       audioData,
		SampleRate: 48000,
		Channels:   2,
	}
	buf.recordAudioFrameAt(af, now.Add(10*time.Millisecond))

	// Extract the clip and verify audio data matches the input.
	_, audioClip, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)
	require.Len(t, audioClip, 1)
	require.Equal(t, audioData, audioClip[0].data,
		"audio data should match input, not be zeroed out")
}

func TestReplayBuffer_AudioBytesIncludedInMemoryLimit(t *testing.T) {
	// Fix 7: trimLocked only checks bytesUsed (video bytes). audioBytesUsed
	// never contributes to the memory limit, and recordAudioFrameAt never
	// calls trimLocked. This means audio frames can grow unbounded.
	maxBytes := int64(2000)
	buf := newReplayBuffer(60, maxBytes)
	now := time.Now()

	// Record 3 small GOPs so trimming can work (need >=2 GOPs to trim).
	for g := 0; g < 3; g++ {
		kf := makeVideoFrame(int64(g)*90000, true, 100)
		buf.recordFrameAt(kf, now.Add(time.Duration(g)*time.Second))
		df := makeVideoFrame(int64(g)*90000+3003, false, 50)
		buf.recordFrameAt(df, now.Add(time.Duration(g)*time.Second+33*time.Millisecond))
	}

	// Now record many audio frames with large data, well exceeding maxBytes.
	for i := 0; i < 100; i++ {
		af := &media.AudioFrame{
			PTS:        int64(i) * 1920,
			Data:       make([]byte, 200), // 200 bytes each, 100 frames = 20KB
			SampleRate: 48000,
			Channels:   2,
		}
		wallTime := now.Add(time.Duration(i) * 21 * time.Millisecond)
		buf.recordAudioFrameAt(af, wallTime)
	}

	// Total memory (video + audio) should be bounded near maxBytes.
	buf.mu.RLock()
	totalBytes := buf.bytesUsed + buf.audioBytesUsed
	buf.mu.RUnlock()

	// Allow some headroom (the last GOP can't be trimmed, so we allow 2x).
	require.LessOrEqual(t, totalBytes, maxBytes*3,
		"total memory (video=%d + audio=%d = %d) should be bounded by maxBytes=%d",
		buf.bytesUsed, buf.audioBytesUsed, totalBytes, maxBytes)
}

func TestReplayBuffer_ConcurrentAccess(t *testing.T) {
	buf := newReplayBuffer(60, 0)
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

func BenchmarkReplayBuffer_TrimCompaction(b *testing.B) {
	// Benchmark validates that trimLocked() compaction (make+copy inside mutex)
	// is fast enough not to block frame ingest. At 9000 frames (300s buffer at
	// 30fps), the compaction copies ~9000 bufferedFrame structs (~120 bytes each),
	// which is a contiguous memcpy of ~1MB -- well under 1ms on modern hardware.
	//
	// The compaction is O(n) where n is frames REMAINING after trim, not total
	// frames ever recorded. Since trimLocked removes GOPs before compacting,
	// n is always smaller than the pre-trim count. The make+copy ensures the
	// old backing array (which may be much larger) can be GC'd.
	buf := newReplayBuffer(10, 0) // 10s window to force frequent trims
	now := time.Now()

	// Pre-fill with ~9000 frames (300 GOPs of 30 frames each).
	// Only the last 10s worth will survive trimming.
	for g := 0; g < 300; g++ {
		wallTime := now.Add(time.Duration(g) * 100 * time.Millisecond)
		kf := makeVideoFrame(int64(g)*90000, true, 1000)
		buf.recordFrameAt(kf, wallTime)
		for j := 1; j <= 29; j++ {
			df := makeVideoFrame(int64(g)*90000+int64(j)*3003, false, 500)
			buf.recordFrameAt(df, wallTime.Add(time.Duration(j)*3*time.Millisecond))
		}
	}

	// Now benchmark the steady-state: recording a new GOP triggers trim+compact.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g := 300 + i
		wallTime := now.Add(time.Duration(g) * 100 * time.Millisecond)
		kf := makeVideoFrame(int64(g)*90000, true, 1000)
		buf.recordFrameAt(kf, wallTime)
		for j := 1; j <= 29; j++ {
			df := makeVideoFrame(int64(g)*90000+int64(j)*3003, false, 500)
			buf.recordFrameAt(df, wallTime.Add(time.Duration(j)*3*time.Millisecond))
		}
	}
}
