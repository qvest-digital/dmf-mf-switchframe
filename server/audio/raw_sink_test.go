package audio

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestSetRawAudioSink_ReceivesMixedPCM(t *testing.T) {
	// Set up a mixer in mixing mode (two active channels forces decode→mix→encode path).
	// Attach a RawAudioSink and verify it receives the mixed PCM with correct metadata.
	cam1PCM := make([]float32, 2048) // 1024 samples * 2 channels
	for i := range cam1PCM {
		cam1PCM[i] = 0.25
	}
	cam2PCM := make([]float32, 2048)
	for i := range cam2PCM {
		cam2PCM[i] = 0.15
	}

	var outMu sync.Mutex
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outMu.Lock()
			outputFrames = append(outputFrames, frame)
			outMu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set per-channel decoders that return known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Attach the sink
	var mu sync.Mutex
	var sinkPCM []float32
	var sinkPTS int64
	var sinkSR int
	var sinkCh int
	var sinkCalled bool

	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		defer mu.Unlock()
		sinkPCM = append([]float32{}, pcm...)
		sinkPTS = pts
		sinkSR = sampleRate
		sinkCh = channels
		sinkCalled = true
	})

	// Ingest frames from both channels to trigger mix cycle
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Verify output was produced (sanity check).
	// The deadline ticker goroutine may produce an additional frame.
	outMu.Lock()
	outCount := len(outputFrames)
	outMu.Unlock()
	require.GreaterOrEqual(t, outCount, 1, "should produce at least one output frame")

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
	// Ingest audio with very hot signal (amplitude > 1.0).
	// Verify the sink receives PCM that has been limited (peak <= -1 dBFS ~= 0.891).
	hotPCM := make([]float32, 2048)
	for i := range hotPCM {
		hotPCM[i] = 2.0 // way over 0 dBFS
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: hotPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: hotPCM}
	m.mu.Unlock()

	var mu sync.Mutex
	var sinkPCM []float32

	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mu.Lock()
		defer mu.Unlock()
		sinkPCM = append([]float32{}, pcm...)
	})

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

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
	// Set a sink, then set to nil. Ingest audio. Verify no panic and
	// the sink was not called after being set to nil.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.channels["cam2"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	var callCount atomic.Int32
	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		callCount.Add(1)
	})

	// First ingest: sink should be called
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	require.GreaterOrEqual(t, callCount.Load(), int32(1), "sink should have been called at least once")

	// Disable the sink
	countBefore := callCount.Load()
	m.SetRawAudioSink(nil)

	// Second ingest: sink should NOT be called (and no panic)
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	require.Equal(t, countBefore, callCount.Load(), "sink should not have been called after being set to nil")
}
