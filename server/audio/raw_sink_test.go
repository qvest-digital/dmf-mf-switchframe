package audio

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestSetRawAudioSink_ReceivesMixedPCM(t *testing.T) {
	// Verify the raw audio sink receives mixed PCM from the clock-driven ticker.
	t.Parallel()

	enc := &mockTickEncoder{}
	var mu sync.Mutex
	var sinkPCM []float32
	var sinkPTS int64
	var sinkSR int
	var sinkCh int
	var sinkCalled bool

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Attach the sink
	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		defer mu.Unlock()
		sinkPCM = append([]float32{}, pcm...)
		sinkPTS = pts
		sinkSR = sampleRate
		sinkCh = channels
		sinkCalled = true
	})

	// Push known PCM into ring buffers
	pcm1 := make([]float32, 1024*2)
	pcm2 := make([]float32, 1024*2)
	for i := range pcm1 {
		pcm1[i] = 0.25
		pcm2[i] = 0.15
	}

	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm1)
	m.channels["cam2"].ringBuf.Push(pcm2)
	m.mu.Unlock()

	// Call tick directly
	frame := m.tick()
	require.NotNil(t, frame, "tick should produce output")

	// Verify sink was called with correct metadata
	mu.Lock()
	defer mu.Unlock()
	require.True(t, sinkCalled, "sink should have been called")
	require.Equal(t, 2048, len(sinkPCM), "sink PCM should be 1024 samples * 2 channels")
	require.Equal(t, 48000, sinkSR, "sink should receive correct sample rate")
	require.Equal(t, 2, sinkCh, "sink should receive correct channel count")
	require.NotZero(t, sinkPTS, "sink should receive a non-zero PTS")

	// Verify the mixed PCM values: 0.25 + 0.15 = 0.40
	for i, s := range sinkPCM {
		require.InDelta(t, 0.40, s, 0.01, "sample %d should be sum of channels", i)
	}
}

func TestSetRawAudioSink_AfterLimiter(t *testing.T) {
	// Verify the sink receives PCM that has been limited (peak <= -1 dBFS).
	t.Parallel()

	enc := &mockTickEncoder{}
	var mu sync.Mutex
	var sinkPCM []float32

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		defer mu.Unlock()
		sinkPCM = append([]float32{}, pcm...)
	})

	// Push hot signal (amplitude > 1.0)
	hotPCM := make([]float32, 1024*2)
	for i := range hotPCM {
		hotPCM[i] = 2.0
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(hotPCM)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, sinkPCM, "sink should have received PCM")

	// Limiter threshold is -1 dBFS = 10^(-1/20) ~= 0.891
	limiterThreshold := math.Pow(10, -1.0/20.0)
	for i, s := range sinkPCM {
		require.LessOrEqual(t, float64(math.Abs(float64(s))), limiterThreshold+0.001,
			"sample %d (%.4f) should be limited to <= %.4f", i, s, limiterThreshold)
	}
}

func TestSetRawAudioSink_NilDisables(t *testing.T) {
	// Set a sink, then set to nil. Run tick. Verify no panic.
	t.Parallel()

	enc := &mockTickEncoder{}
	var callCount int
	var mu sync.Mutex

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push data for two ticks
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	m.SetRawAudioSink(func(p []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	// First tick: sink should be called
	frame := m.tick()
	require.NotNil(t, frame)

	mu.Lock()
	require.Equal(t, 1, callCount, "sink should have been called once")
	mu.Unlock()

	// Disable the sink
	m.SetRawAudioSink(nil)

	// Wait briefly to avoid data races with ticker goroutine
	time.Sleep(30 * time.Millisecond)

	// Second tick: sink should NOT be called (and no panic)
	frame = m.tick()
	require.NotNil(t, frame)

	mu.Lock()
	require.Equal(t, 1, callCount, "sink should not have been called after being set to nil")
	mu.Unlock()
}
