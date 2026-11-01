package audio

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetRawAudioSink_ReceivesMixedPCM(t *testing.T) {
	// Verify the raw audio sink receives mixed PCM and the PTS ProduceSamples
	// was given.
	t.Parallel()

	var mu sync.Mutex
	var sinkPCM []float32
	var sinkPTS int64
	var sinkSR int
	var sinkCh int
	var sinkCalled bool

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
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

	tl := newTimeline()
	tl.push(m, "cam1", pcm1, 0)
	tl.push(m, "cam2", pcm2, 0)

	_ = tl.produce(m, 1024)

	// Verify sink was called with correct metadata
	mu.Lock()
	defer mu.Unlock()
	require.True(t, sinkCalled, "sink should have been called")
	require.Equal(t, 2048, len(sinkPCM), "sink PCM should be 1024 samples * 2 channels")
	require.Equal(t, 48000, sinkSR, "sink should receive correct sample rate")
	require.Equal(t, 2, sinkCh, "sink should receive correct channel count")
	require.Equal(t, tl.pts(0), sinkPTS, "sink should receive the PTS ProduceSamples was given")

	// Verify the mixed PCM values: 0.25 + 0.15 = 0.40
	for i, s := range sinkPCM {
		require.InDelta(t, 0.40, s, 0.01, "sample %d should be sum of channels", i)
	}
}

func TestSetRawAudioSink_AfterLimiter(t *testing.T) {
	// Verify the sink receives PCM that has been limited (peak <= -1 dBFS).
	t.Parallel()

	var mu sync.Mutex
	var sinkPCM []float32

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
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
	tl := newTimeline()
	tl.push(m, "cam1", hotPCM, 0)

	_ = tl.produce(m, 1024)

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
	// Set a sink, then set to nil. Produce again. Verify no panic.
	t.Parallel()

	var callCount int
	var mu sync.Mutex

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push data for two periods
	pcm := make([]float32, 2*1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	tl := newTimeline()
	tl.push(m, "cam1", pcm, 0)

	m.SetRawAudioSink(func(p []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	// First period: sink should be called
	_ = tl.produce(m, 1024)

	mu.Lock()
	require.Equal(t, 1, callCount, "sink should have been called once")
	mu.Unlock()

	// Disable the sink
	m.SetRawAudioSink(nil)

	// Second period: sink should NOT be called (and no panic)
	_ = tl.produce(m, 1024)

	mu.Lock()
	require.Equal(t, 1, callCount, "sink should not have been called after being set to nil")
	mu.Unlock()
}
