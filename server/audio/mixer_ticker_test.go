package audio

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// mockTickEncoder is a simple encoder for tick tests that captures calls.
type mockTickEncoder struct {
	mu        sync.Mutex
	calls     int
	lastInput []float32
}

func (e *mockTickEncoder) Encode(pcm []float32) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.lastInput = make([]float32, len(pcm))
	copy(e.lastInput, pcm)
	// Return non-empty bytes to simulate valid AAC output
	return []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, nil
}

func (e *mockTickEncoder) Close() error { return nil }

func (e *mockTickEncoder) getCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *mockTickEncoder) getLastInput() []float32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastInput == nil {
		return nil
	}
	cp := make([]float32, len(e.lastInput))
	copy(cp, e.lastInput)
	return cp
}

// newTickTestMixer creates a Mixer configured for tick() testing with a mock encoder.
// Returns the mixer and a reference to the mock encoder for inspection.
func newTickTestMixer(t *testing.T) (*Mixer, *mockTickEncoder) {
	t.Helper()

	enc := &mockTickEncoder{}
	var mu sync.Mutex
	var frames []*media.AudioFrame
	_ = frames

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			frames = append(frames, frame)
			mu.Unlock()
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	t.Cleanup(func() { _ = m.Close() })
	return m, enc
}

func TestTick_SingleChannel_ProducesOutput(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push a PCM frame into the ring buffer (1024 samples * 2 channels = 2048 values)
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.25 // known non-zero value
	}

	m.mu.Lock()
	ch := m.channels["cam1"]
	ch.ringBuf.Push(pcm)
	m.mu.Unlock()

	// Call tick() — should produce a non-nil frame
	frame := m.tick()
	require.NotNil(t, frame, "tick should produce output when channel has data")
	require.NotEmpty(t, frame.Data, "output frame should have AAC data")
	require.Equal(t, 48000, frame.SampleRate)
	require.Equal(t, 2, frame.Channels)

	// Encoder should have been called exactly once (once for priming, once for real)
	// The priming call happens in ensureEncoder, then tick calls Encode once more
	assert.Equal(t, 2, enc.getCalls(), "encoder should be called: 1 priming + 1 tick")

	// The PCM fed to the encoder should be non-zero (gain=1.0 means passthrough of 0.25 values)
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	require.Len(t, lastPCM, 1024*2, "encoder should receive exactly 1024*2 samples")
	// Check a sample value — with fader at 0dB (levelLinear=1.0) and master at 0dB,
	// after limiter the value should be close to 0.25 (well below -1dBFS threshold)
	assert.InDelta(t, 0.25, lastPCM[0], 0.01, "PCM should preserve input through unity gain")
}

func TestTick_NoChannels_ProducesSilence(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	// No channels added — tick should still produce output (encoded silence)
	frame := m.tick()
	require.NotNil(t, frame, "tick should produce output even with no channels")
	require.NotEmpty(t, frame.Data, "output frame should have AAC data")

	// Encoder should have been called (priming + 1 tick encode)
	assert.Equal(t, 2, enc.getCalls())

	// The PCM fed to the encoder should be all zeros (silence)
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	for i, v := range lastPCM {
		assert.Equal(t, float32(0), v, "sample %d should be zero (silence)", i)
	}
}

func TestTick_TwoChannels_SumsOutput(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Create PCM with known values
	pcm1 := make([]float32, 1024*2)
	pcm2 := make([]float32, 1024*2)
	for i := range pcm1 {
		pcm1[i] = 0.3
		pcm2[i] = 0.2
	}

	// Push into both ring buffers
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm1)
	m.channels["cam2"].ringBuf.Push(pcm2)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame, "tick should produce output with two channels")
	require.NotEmpty(t, frame.Data)

	// The encoded PCM should be the sum of both channels: 0.3 + 0.2 = 0.5
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	require.Len(t, lastPCM, 1024*2)
	// After summing and master chain (unity gain, limiter at -1dBFS = ~0.891),
	// 0.5 is well below the limiter threshold so it should pass through.
	assert.InDelta(t, 0.5, lastPCM[0], 0.01,
		"output should be sum of both channels (0.3 + 0.2 = 0.5)")
}

func TestTick_EmptyRingBuf_FreezeRepeat(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push one PCM frame
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.4
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	// First tick consumes the frame
	frame1 := m.tick()
	require.NotNil(t, frame1, "first tick should produce output")

	// Second tick: ring buffer is empty, should freeze-repeat the last frame
	frame2 := m.tick()
	require.NotNil(t, frame2, "second tick should produce output via freeze-repeat")
	require.NotEmpty(t, frame2.Data)

	// The freeze-repeated PCM should also contain our 0.4 values
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	assert.InDelta(t, 0.4, lastPCM[0], 0.01,
		"freeze-repeat should replay the last popped frame")
}

func TestTick_MutedChannel_Excluded(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetMuted("cam1", true)

	// Push data into the muted channel's ring buffer
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.9
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame, "tick should produce output even when channel is muted")

	// The PCM should be silence since the muted channel is skipped
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	for i, v := range lastPCM {
		assert.Equal(t, float32(0), v, "sample %d should be zero (muted channel skipped)", i)
	}
}

func TestTick_InactiveChannel_Excluded(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	// cam1 is NOT activated — should be excluded from mix

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.8
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	// Should be silence — inactive channel excluded
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	for i, v := range lastPCM {
		assert.Equal(t, float32(0), v, "sample %d should be zero (inactive channel)", i)
	}
}

func TestTick_FaderGainApplied(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	// Set fader to -6dB (approximately 0.5012 linear)
	_ = m.SetLevel("cam1", -6.0)

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	// Expected: 0.5 * 0.5012 ~= 0.2506
	assert.InDelta(t, 0.5*0.5012, lastPCM[0], 0.02,
		"fader gain should be applied to PCM")
}

func TestTick_NoEncoderFactory_ReturnsNil(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		// No encoder factory — tick should return nil
	})
	defer func() { _ = m.Close() }()

	frame := m.tick()
	assert.Nil(t, frame, "tick should return nil when no encoder factory is available")
}

func TestTick_PTS_Monotonic(t *testing.T) {
	t.Parallel()
	m, _ := newTickTestMixer(t)

	// Seed PTS from video so we have a known starting point
	m.SeedPTSFromVideo(90000)

	var ptsList []int64
	for i := 0; i < 5; i++ {
		frame := m.tick()
		require.NotNil(t, frame)
		ptsList = append(ptsList, frame.PTS)
	}

	// All PTS values should be monotonically increasing
	for i := 1; i < len(ptsList); i++ {
		assert.Greater(t, ptsList[i], ptsList[i-1],
			"PTS should be monotonically increasing: pts[%d]=%d <= pts[%d]=%d",
			i, ptsList[i], i-1, ptsList[i-1])
	}
}

func TestOutputTicker_ProducesFramesAtCadence(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputCount int

	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputCount++
			mu.Unlock()
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	// NewMixer already starts the outputTicker — no need to start a second one.

	// Let it run for ~100ms — should produce ~4-5 frames at ~21.3ms cadence
	time.Sleep(100 * time.Millisecond)

	// Stop the ticker
	_ = m.Close()

	mu.Lock()
	count := outputCount
	mu.Unlock()

	// At 48kHz with 1024-sample frames, interval is ~21.3ms.
	// In 100ms we expect roughly 4-5 frames. Allow some slack for scheduling.
	assert.GreaterOrEqual(t, count, 2, "should produce at least 2 frames in 100ms")
	assert.LessOrEqual(t, count, 10, "should not produce more than 10 frames in 100ms")
}

func TestTick_TransitionGainInterpolation(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Simulate a transition crossfade at 50%
	m.mu.Lock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = "cam1"
	m.transCrossfadeTo = "cam2"
	m.transCrossfadePosition = 0.5
	m.transCrossfadeMode = Crossfade
	m.transCrossfadeAudioPos = 0.5

	// Push known PCM into both ring buffers
	pcm1 := make([]float32, 1024*2)
	pcm2 := make([]float32, 1024*2)
	for i := range pcm1 {
		pcm1[i] = 1.0
		pcm2[i] = 1.0
	}
	m.channels["cam1"].ringBuf.Push(pcm1)
	m.channels["cam2"].ringBuf.Push(pcm2)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	// At 50% crossfade with equal-power: cos(pi/4) + sin(pi/4) ~= 1.414
	// Both channels contribute 1.0 * gain, so sum ~= 1.414
	// After limiter at -1dBFS (~0.891), 1.414 would be limited.
	// Verify the output is non-zero and the encoder was called.
	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	// At equal-power crossfade midpoint, total gain = cos(pi/4) + sin(pi/4) = sqrt(2) ~= 1.414
	// The limiter will clamp this, but the pre-limiter sum should be ~1.414.
	// Since the limiter is brickwall at -1dBFS, output will be <= 0.891.
	// We just check it's non-zero (positive).
	assert.Greater(t, lastPCM[0], float32(0), "output should be positive with crossfade")
}
