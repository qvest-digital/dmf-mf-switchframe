package replay

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func makeAudioFrame(pts int64, dataSize int) *media.AudioFrame {
	data := make([]byte, dataSize)
	for i := range data {
		data[i] = byte(i % 256)
	}
	return &media.AudioFrame{
		PTS:        pts,
		Data:       data,
		SampleRate: 48000,
		Channels:   2,
	}
}

func TestReplayBuffer_RecordAudioFrame(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Audio before any video GOP is dropped.
	buf.recordAudioFrameAt(makeAudioFrame(0, 100), now)
	buf.mu.RLock()
	require.Equal(t, 0, len(buf.audioFrames))
	buf.mu.RUnlock()

	// Record a video keyframe to establish a GOP.
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)

	// Now audio should be recorded.
	buf.recordAudioFrameAt(makeAudioFrame(1000, 200), now.Add(10*time.Millisecond))
	buf.mu.RLock()
	require.Equal(t, 1, len(buf.audioFrames))
	require.Equal(t, int64(1000), buf.audioFrames[0].pts)
	require.Equal(t, 48000, buf.audioFrames[0].sampleRate)
	require.Equal(t, 2, buf.audioFrames[0].channels)
	require.Equal(t, 200, len(buf.audioFrames[0].data))
	buf.mu.RUnlock()
}

func TestReplayBuffer_RecordAudioFrame_DeepCopies(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)

	frame := makeAudioFrame(1000, 100)
	original := make([]byte, len(frame.Data))
	copy(original, frame.Data)

	buf.recordAudioFrameAt(frame, now.Add(10*time.Millisecond))

	// Mutate the original frame's data.
	frame.Data[0] = 0xFF

	// Buffer's copy should be unaffected.
	buf.mu.RLock()
	require.NotEqual(t, byte(0xFF), buf.audioFrames[0].data[0],
		"buffer should hold a deep copy of audio data")
	buf.mu.RUnlock()
}

func TestReplayBuffer_AudioBytesUsed(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordAudioFrameAt(makeAudioFrame(1000, 200), now.Add(10*time.Millisecond))

	buf.mu.RLock()
	require.Equal(t, int64(200), buf.audioBytesUsed)
	buf.mu.RUnlock()
}

func TestReplayBuffer_AudioTrimmedWithVideo(t *testing.T) {
	buf := newReplayBuffer(1, 0) // 1 second buffer
	now := time.Now()

	// Record 3 GOPs spaced 500ms apart, with audio interleaved.
	for i := 0; i < 3; i++ {
		t0 := now.Add(time.Duration(i) * 500 * time.Millisecond)
		buf.recordFrameAt(makeVideoFrame(int64(i)*90000, true, 1000), t0)
		buf.recordAudioFrameAt(makeAudioFrame(int64(i)*90000+100, 100), t0.Add(5*time.Millisecond))
		for j := 1; j <= 3; j++ {
			buf.recordFrameAt(makeVideoFrame(int64(i)*90000+int64(j)*3003, false, 500),
				t0.Add(time.Duration(j)*33*time.Millisecond))
		}
	}

	// After trimming, audio frames before the oldest video frame should be removed.
	buf.mu.RLock()
	audioCount := len(buf.audioFrames)
	buf.mu.RUnlock()

	// Should have fewer than 3 audio frames (oldest trimmed with oldest GOP).
	require.Less(t, audioCount, 3, "expected audio frames to be trimmed with video")
}

func TestReplayBuffer_ExtractClip_IncludesAudio(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Record video + audio.
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordAudioFrameAt(makeAudioFrame(500, 100), now.Add(5*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))
	buf.recordAudioFrameAt(makeAudioFrame(3503, 100), now.Add(38*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(6006, false, 500), now.Add(66*time.Millisecond))

	clip, audioClip, err := buf.ExtractClip(now.Add(-1*time.Millisecond), now.Add(70*time.Millisecond))
	require.NoError(t, err)
	require.NotEmpty(t, clip)
	require.Len(t, audioClip, 2, "expected 2 audio frames in extracted clip")

	// Verify audio frame data is deep-copied.
	require.Equal(t, int64(500), audioClip[0].pts)
	require.Equal(t, int64(3503), audioClip[1].pts)
	require.Equal(t, 48000, audioClip[0].sampleRate)
	require.Equal(t, 2, audioClip[0].channels)
}

func TestReplayBuffer_ExtractClip_NoAudio(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Record video only.
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))

	clip, audioClip, err := buf.ExtractClip(now.Add(-1*time.Millisecond), now.Add(50*time.Millisecond))
	require.NoError(t, err)
	require.NotEmpty(t, clip)
	require.Empty(t, audioClip, "expected no audio frames when none recorded")
}

func TestReplayBuffer_ExtractClip_AudioDeepCopied(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	now := time.Now()

	// Need at least two video frames so the audio frame falls within the range.
	buf.recordFrameAt(makeVideoFrame(0, true, 1000), now)
	buf.recordAudioFrameAt(makeAudioFrame(500, 100), now.Add(5*time.Millisecond))
	buf.recordFrameAt(makeVideoFrame(3003, false, 500), now.Add(33*time.Millisecond))

	_, audioClip, err := buf.ExtractClip(now.Add(-1*time.Second), now.Add(1*time.Second))
	require.NoError(t, err)
	require.Len(t, audioClip, 1)

	// Mutate extracted audio data — buffer should be unaffected.
	audioClip[0].data[0] = 0xFF

	buf.mu.RLock()
	require.NotEqual(t, byte(0xFF), buf.audioFrames[0].data[0],
		"ExtractClip should return deep copies of audio frames")
	buf.mu.RUnlock()
}

func TestReplayPlayer_AudioOutput(t *testing.T) {
	clip := buildTestClip(1, 5) // 1 GOP, 5 frames

	// Build audio frames that match the video clip's wall times.
	var audioClip []bufferedAudioFrame
	for i := 0; i < 5; i++ {
		audioClip = append(audioClip, bufferedAudioFrame{
			data:       []byte{byte(i), 0xAA, 0xBB},
			pts:        clip[i].pts + 100,
			sampleRate: 48000,
			channels:   2,
			wallTime:   clip[i].wallTime.Add(1 * time.Millisecond),
		})
	}

	var videoFrames []*media.VideoFrame
	var audioFrames []*media.AudioFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      audioClip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output: func(frame *media.VideoFrame) {
			mu.Lock()
			defer mu.Unlock()
			videoFrames = append(videoFrames, &media.VideoFrame{PTS: frame.PTS})
		},
		AudioOutput: func(frame *media.AudioFrame) {
			mu.Lock()
			defer mu.Unlock()
			dataCopy := make([]byte, len(frame.Data))
			copy(dataCopy, frame.Data)
			audioFrames = append(audioFrames, &media.AudioFrame{
				PTS:        frame.PTS,
				Data:       dataCopy,
				SampleRate: frame.SampleRate,
				Channels:   frame.Channels,
			})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, videoFrames, 5, "expected 5 video output frames")
	require.Greater(t, len(audioFrames), 0, "expected some audio output frames")

	// Verify audio frames have correct metadata.
	for _, af := range audioFrames {
		assert.Equal(t, 48000, af.SampleRate)
		assert.Equal(t, 2, af.Channels)
		assert.Greater(t, len(af.Data), 0)
	}
}

func TestReplayPlayer_AudioOutput_NoAudioClip(t *testing.T) {
	clip := buildTestClip(1, 3)
	audioCallCount := 0
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      nil, // No audio
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		AudioOutput: func(frame *media.AudioFrame) {
			mu.Lock()
			audioCallCount++
			mu.Unlock()
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	assert.Equal(t, 0, audioCallCount, "expected no audio output when AudioClip is nil")
	mu.Unlock()
}

func TestReplayPlayer_AudioOutput_NilCallback(t *testing.T) {
	clip := buildTestClip(1, 3)
	audioClip := []bufferedAudioFrame{
		{data: []byte{0x01}, pts: 100, sampleRate: 48000, channels: 2,
			wallTime: clip[0].wallTime.Add(time.Millisecond)},
	}

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      audioClip,
		Speed:          1.0,
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		AudioOutput:    nil, // No callback
		OnDone:         func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Should not panic.
	p.Start(ctx)
	p.Wait()
}

func TestReplayPlayer_AudioOutput_SlowMotion(t *testing.T) {
	clip := buildTestClip(1, 4) // 1 GOP, 4 frames

	// Build audio frames.
	var audioClip []bufferedAudioFrame
	for i := 0; i < 4; i++ {
		audioClip = append(audioClip, bufferedAudioFrame{
			data:       []byte{byte(i)},
			pts:        clip[i].pts + 100,
			sampleRate: 48000,
			channels:   2,
			wallTime:   clip[i].wallTime.Add(1 * time.Millisecond),
		})
	}

	var audioFrames []*media.AudioFrame
	var mu sync.Mutex

	p := newReplayPlayer(PlayerConfig{
		Clip:           clip,
		AudioClip:      audioClip,
		Speed:          0.5, // Slow-motion
		Loop:           false,
		DecoderFactory: mockDecoderFactory,
		EncoderFactory: mockEncoderFactory,
		Output:         func(frame *media.VideoFrame) {},
		AudioOutput: func(frame *media.AudioFrame) {
			mu.Lock()
			defer mu.Unlock()
			audioFrames = append(audioFrames, &media.AudioFrame{
				PTS:  frame.PTS,
				Data: append([]byte{}, frame.Data...),
			})
		},
		OnDone: func() {},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Start(ctx)
	p.Wait()

	mu.Lock()
	defer mu.Unlock()

	// At 0.5x speed with frame duplication, audio should only be emitted once
	// per source frame (not duplicated). So we expect ~4 audio frames, not 8.
	require.Greater(t, len(audioFrames), 0, "expected audio output in slow motion")
	require.LessOrEqual(t, len(audioFrames), 4, "audio should not be duplicated in slow motion")
}
