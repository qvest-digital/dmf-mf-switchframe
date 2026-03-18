package audio

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// mockEncoderCapture captures the PCM input passed to Encode.
type mockEncoderCapture struct {
	mu     sync.Mutex
	pcmRef *[]float32
}

func (m *mockEncoderCapture) Encode(pcm []float32) ([]byte, error) {
	m.mu.Lock()
	*m.pcmRef = append([]float32{}, pcm...)
	m.mu.Unlock()
	return []byte{0xFF}, nil
}
func (m *mockEncoderCapture) Close() error { return nil }

func TestMixerPassthrough(t *testing.T) {
	var mu sync.Mutex
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			output = append(output, frame)
			mu.Unlock()
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Ingest a frame — should passthrough (single active source, 0dB, not muted)
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA, 0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	mu.Lock()
	require.Equal(t, 1, len(output))
	require.Equal(t, []byte{0xAA, 0xBB}, output[0].Data, "passthrough should forward raw AAC")
	require.Equal(t, int64(1000), output[0].PTS, "first frame should seed from source PTS")
	mu.Unlock()

	// Second frame: PTS should advance based on wall clock (not source PTS).
	// In tests frames arrive almost instantly, so PTS advances minimally.
	frame2 := &media.AudioFrame{PTS: 2920, Data: []byte{0xCC}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame2)

	mu.Lock()
	require.Equal(t, 2, len(output))
	// Wall-clock PTS: should be >= first PTS and monotonically non-decreasing.
	require.GreaterOrEqual(t, output[1].PTS, output[0].PTS,
		"second frame PTS should be >= first frame PTS (wall-clock based)")
	mu.Unlock()
}

func TestMixerCloseIdempotent(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})
	require.NoError(t, m.Close())
	require.NoError(t, m.Close(), "second Close must not panic")
}

// TestMixerCloseConcurrentNoDoubleClose verifies that calling Close()
// concurrently from multiple goroutines closes each decoder and the
// encoder exactly once (not zero, not twice).
func TestMixerCloseConcurrentNoDoubleClose(t *testing.T) {
	t.Parallel()

	var decoderCloses atomic.Int64
	var encoderCloses atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &closeCountDecoder{closes: &decoderCloses, samples: make([]float32, 1024)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &closeCountEncoder{closes: &encoderCloses}, nil
		},
	})

	// Add two channels and ingest frames to trigger lazy decoder init.
	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	// Two active channels = mixing mode, which also initializes the encoder.
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Call Close() concurrently from 10 goroutines.
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = m.Close()
		}()
	}
	wg.Wait()

	require.Equal(t, int64(2), decoderCloses.Load(),
		"each decoder should be closed exactly once (2 channels)")
	require.Equal(t, int64(1), encoderCloses.Load(),
		"encoder should be closed exactly once")
}

// closeCountDecoder tracks Close() calls via an atomic counter.
type closeCountDecoder struct {
	closes  *atomic.Int64
	samples []float32
}

func (d *closeCountDecoder) Decode([]byte) ([]float32, error) { return d.samples, nil }
func (d *closeCountDecoder) Close() error                     { d.closes.Add(1); return nil }

// closeCountEncoder tracks Close() calls via an atomic counter.
type closeCountEncoder struct {
	closes *atomic.Int64
}

func (e *closeCountEncoder) Encode([]float32) ([]byte, error) { return []byte{0xFF}, nil }
func (e *closeCountEncoder) Close() error                     { e.closes.Add(1); return nil }

func TestMixerIgnoresInactiveChannel(t *testing.T) {
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { output = append(output, frame) },
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	// cam1 is inactive (not activated) — frames should be dropped
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	require.Equal(t, 0, len(output), "inactive channel should not output frames")
}

func TestMixerMutedChannelSilent(t *testing.T) {
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { output = append(output, frame) },
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetMuted("cam1", true)

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Muted channel: in passthrough mode, no output since single source is muted
	require.Equal(t, 0, len(output), "muted channel should produce no output")
}

func TestMixerPassthroughFlag(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Single active at 0dB → passthrough
	m.SetActive("cam1", true)
	require.True(t, m.IsPassthrough())

	// Two active → not passthrough
	m.SetActive("cam2", true)
	require.False(t, m.IsPassthrough())

	// Back to one
	m.SetActive("cam2", false)
	require.True(t, m.IsPassthrough())

	// One active but non-zero level → not passthrough
	_ = m.SetLevel("cam1", -6.0)
	require.False(t, m.IsPassthrough())

	// Reset level
	_ = m.SetLevel("cam1", 0.0)
	require.True(t, m.IsPassthrough())
}

func TestMixerGainApplication(t *testing.T) {
	require.InDelta(t, 1.0, DBToLinear(0), 0.001)
	require.InDelta(t, 0.5012, DBToLinear(-6.0), 0.001)
	require.InDelta(t, 0.1, DBToLinear(-20.0), 0.001)
	require.InDelta(t, 2.0, DBToLinear(6.02), 0.01)
	require.Equal(t, 0.0, DBToLinear(math.Inf(-1)))
}

func TestMixerMultiChannelMixing(t *testing.T) {
	// Two active channels → mixing mode → decode both, sum, encode
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	// PCM samples returned by each channel's decoder
	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
	cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			// Each call returns a new decoder; we'll set samples per-channel below
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	require.False(t, m.IsPassthrough())

	// Set the decoder samples for each channel directly
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Ingest frames from both channels
	frame1 := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	// After both channels contribute, output should appear
	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")
	require.Equal(t, []byte{0xFF}, outputFrames[0].Data, "output should be encoded AAC")

	// Verify summed PCM: 0.5 + 0.3 = 0.8 for each sample (both at 0dB gain)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.8, s, 0.001, "sample %d should be sum of channels", i)
	}
}

func TestMixerMasterLevel(t *testing.T) {
	// Single channel with non-zero master level → not passthrough → decode, apply gain, encode
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.NoError(t, m.SetMasterLevel(-6.0))
	require.False(t, m.IsPassthrough())

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Single active unmuted channel → output after 1 frame
	require.Equal(t, 1, len(outputFrames))

	// Verify master gain applied: 1.0 * DBToLinear(-6.0) ≈ 0.5012
	expectedGain := DBToLinear(-6.0)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedGain, s, 0.001, "sample %d should have master gain applied", i)
	}
}

func TestMixerSetMasterLevel(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.True(t, m.IsPassthrough())

	require.NoError(t, m.SetMasterLevel(-3.0))
	require.False(t, m.IsPassthrough(), "non-zero master level should disable passthrough")

	require.NoError(t, m.SetMasterLevel(0.0))
	require.True(t, m.IsPassthrough(), "zero master level should enable passthrough")
}

func TestSetMasterLevelRejectsNaN(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetMasterLevel(math.NaN())
	require.Error(t, err)
	require.Contains(t, err.Error(), "finite")
}

func TestSetMasterLevelRejectsInf(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetMasterLevel(math.Inf(1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "finite")

	err = m.SetMasterLevel(math.Inf(-1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "finite")
}

func TestSetMasterLevelRejectsOutOfRange(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetMasterLevel(-101)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")

	err = m.SetMasterLevel(21)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestSetMasterLevelAcceptsValidRange(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	for _, level := range []float64{-100, -60, 0, 10, 20} {
		require.NoError(t, m.SetMasterLevel(level), "level %f should be accepted", level)
	}
}

func TestMixerChannelGainApplied(t *testing.T) {
	// Channel with -6dB gain → mixing mode → gain applied to decoded PCM
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetLevel("cam1", -6.0)
	require.False(t, m.IsPassthrough())

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	require.Equal(t, 1, len(outputFrames))
	expectedGain := DBToLinear(-6.0) // ≈ 0.5012
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedGain, s, 0.001, "sample %d should have channel gain", i)
	}
}

func TestMixerMutedChannelInMixMode(t *testing.T) {
	// Two channels active, one muted → only unmuted contributes
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	_ = m.SetMuted("cam2", true) // cam2 muted — still two active channels, but muted frames are dropped

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Only cam1 contributes (cam2 muted so not expected), mixer should output
	require.Equal(t, 1, len(outputFrames))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.5, s, 0.001, "sample %d: only cam1 should contribute", i)
	}
}

// mockEncoderCallCounter counts calls to Encode for verifying priming behavior.
type mockEncoderCallCounter struct {
	mu        sync.Mutex
	callCount int
	lastPCM   []float32
}

func (m *mockEncoderCallCounter) Encode(pcm []float32) ([]byte, error) {
	m.mu.Lock()
	m.callCount++
	m.lastPCM = append([]float32{}, pcm...)
	m.mu.Unlock()
	return []byte{0xFF}, nil
}
func (m *mockEncoderCallCounter) Close() error { return nil }
func (m *mockEncoderCallCounter) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func TestMixerEnsureEncoderPrimesOnAllPaths(t *testing.T) {
	// Verify that the encoder is primed (silent frame + real frame = 2+ calls)
	// on the crossfade path.
	var enc *mockEncoderCallCounter
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			enc = &mockEncoderCallCounter{}
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade (cut path — no OnTransitionStart)
	m.OnCut("cam1", "cam2")

	// Ingest frames from both sources to complete the crossfade
	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: make([]float32, 2048)}
	m.channels["cam2"].decoder = &mockDecoder{samples: make([]float32, 2048)}
	m.mu.Unlock()

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.NotNil(t, enc, "encoder should have been created")
	// First call is priming (silent frame), second is the actual crossfade output.
	require.GreaterOrEqual(t, enc.CallCount(), 2, "encoder should be primed (silent frame) + real encode")
}

func TestMixerEnsureEncoderPrimesOnTransitionStart(t *testing.T) {
	// Verify that OnTransitionStart also primes the encoder.
	var enc *mockEncoderCallCounter

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			enc = &mockEncoderCallCounter{}
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	require.NotNil(t, enc, "encoder should have been created by OnTransitionStart")
	require.GreaterOrEqual(t, enc.CallCount(), 1, "encoder should be primed with silent frame")
}

func TestMixerOnCutCrossfade(t *testing.T) {
	// OnCut triggers a crossfade from old source to new source.
	// During crossfade, old source PCM fades out, new source fades in.
	var allCapturedPCM [][]float32
	var outputFrames []*media.AudioFrame

	oldPCM := []float32{1.0, 1.0, 1.0, 1.0}
	newPCM := []float32{0.0, 0.0, 0.0, 0.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			var captured []float32
			allCapturedPCM = append(allCapturedPCM, captured)
			idx := len(allCapturedPCM) - 1
			return &mockEncoderCapture{pcmRef: &allCapturedPCM[idx]}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Trigger crossfade: cam1 → cam2
	m.OnCut("cam1", "cam2")

	// During crossfade, both old and new source frames should be accepted
	// even though cam2 might not be "active" yet from the perspective of the channels
	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1) // outgoing source
	m.IngestFrame("cam2", frame2) // incoming source

	require.Equal(t, 1, len(outputFrames), "crossfade should produce one output frame")

	// The crossfaded PCM: at start old=1.0, new=0.0
	// EqualPowerCrossfade: result[0] = 1.0*cos(0) + 0.0*sin(0) = 1.0
	// The brickwall limiter at -1 dBFS (~0.891) clamps full-scale values.
	// result[3] = 1.0*cos(3/4·π/2) + 0.0*sin(3/4·π/2) ≈ cos(3π/8) ≈ 0.383 (below threshold)
	limiterThreshold := math.Pow(10, -1.0/20.0) // -1 dBFS ≈ 0.891
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]
	require.Equal(t, 4, len(lastPCM))
	require.InDelta(t, limiterThreshold, lastPCM[0], 0.02, "first sample should be clamped to limiter threshold")
	// Last sample should be faded from old toward new
	require.True(t, lastPCM[3] < lastPCM[0], "signal should be fading")
}

func TestMixerCrossfadeClears(t *testing.T) {
	// After crossfade completes, subsequent frames go through normal mixing
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Complete the 2-frame crossfade with two rounds of frames
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames), "first crossfade frame should be output")

	// Second round completes the crossfade
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 2, len(outputFrames), "second crossfade frame should be output")

	// After crossfade, verify it's cleared
	m.mu.RLock()
	active := m.crossfadeActive
	m.mu.RUnlock()
	require.False(t, active, "crossfade should be cleared after completion")
}

func TestMixerCrossfadeTimeout(t *testing.T) {
	// When the outgoing source disconnects, the crossfade should complete
	// after the timeout with only the incoming source's audio.
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade with immediate deadline (expired)
	m.OnCut("cam1", "cam2")
	m.mu.Lock()
	m.crossfadeDeadline = m.crossfadeDeadline.Add(-crossfadeTimeout * 2) // force expiry
	m.mu.Unlock()

	// Only the incoming source delivers frames — outgoing timed out.
	// 2-frame crossfade: each frame triggers on timeout since deadline is expired.
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames), "first crossfade frame should use incoming source after timeout")

	// Force deadline expiry again for the second frame
	m.mu.Lock()
	m.crossfadeDeadline = time.Now().Add(-crossfadeTimeout * 2)
	m.mu.Unlock()

	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 2, len(outputFrames), "second crossfade frame should complete")

	// Crossfade should be cleared
	m.mu.RLock()
	active := m.crossfadeActive
	m.mu.RUnlock()
	require.False(t, active, "crossfade should be cleared after timeout completion")
}

func TestMixerSetAFV(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	err := m.SetAFV("cam1", true)
	require.NoError(t, err)

	err = m.SetAFV("nonexistent", true)
	require.Error(t, err, "SetAFV on unknown channel should error")
}

func TestMixerAFVActivatesOnCut(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Both channels have AFV enabled
	_ = m.SetAFV("cam1", true)
	_ = m.SetAFV("cam2", true)

	// Initially neither is active (no program source set yet)
	require.False(t, m.IsChannelActive("cam1"))
	require.False(t, m.IsChannelActive("cam2"))

	// cam1 goes to program → cam1 activates, cam2 stays inactive
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"), "AFV channel on program should be active")
	require.False(t, m.IsChannelActive("cam2"), "AFV channel not on program should be inactive")

	// cam2 goes to program → cam2 activates, cam1 deactivates
	m.OnProgramChange("cam2")
	require.False(t, m.IsChannelActive("cam1"), "cam1 should deactivate when leaving program")
	require.True(t, m.IsChannelActive("cam2"), "cam2 should activate on program")
}

func TestMixerAFVDisabledChannelStaysActive(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("music")

	// cam1 has AFV, music does not
	_ = m.SetAFV("cam1", true)
	// music is manually activated and stays on regardless of program changes
	m.SetActive("music", true)

	require.True(t, m.IsChannelActive("music"), "non-AFV channel should be active")

	// Program changes should not affect non-AFV channels
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"), "AFV cam1 on program")
	require.True(t, m.IsChannelActive("music"), "non-AFV music stays active")

	m.OnProgramChange("cam1") // stay on cam1
	require.True(t, m.IsChannelActive("music"), "non-AFV music still active")
}

func TestMixerAFVToggledOff(t *testing.T) {
	// When AFV is toggled off, channel keeps its current active state
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	_ = m.SetAFV("cam1", true)

	// Activate via program change
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"))

	// Turn off AFV — channel stays active (was already active)
	_ = m.SetAFV("cam1", false)
	require.True(t, m.IsChannelActive("cam1"), "turning off AFV should not deactivate")

	// Now program changes should not affect cam1
	m.OnProgramChange("cam2") // cam2 doesn't exist, but that's fine
	require.True(t, m.IsChannelActive("cam1"), "non-AFV channel unaffected by program change")
}

func TestMixerIsChannelActive(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Unknown channel returns false
	require.False(t, m.IsChannelActive("unknown"))

	m.AddChannel("cam1")
	require.False(t, m.IsChannelActive("cam1"), "new channel starts inactive")

	m.SetActive("cam1", true)
	require.True(t, m.IsChannelActive("cam1"))
}

func TestMixerOnTransitionStart(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Before transition, no crossfade active
	require.False(t, m.IsInTransitionCrossfade())

	// Start transition
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	require.True(t, m.IsInTransitionCrossfade(), "transition crossfade should be active")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should start at 0")

	// Both channels should be considered active during transition
	// The new source channel must be activated so audio frames are accepted
	m.mu.RLock()
	ch2 := m.channels["cam2"]
	active := ch2.active
	m.mu.RUnlock()
	require.True(t, active, "new source channel should be activated on transition start")
}

func TestMixerOnTransitionPosition(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	// Update position
	m.OnTransitionPosition(0.25)
	require.InDelta(t, 0.25, m.TransitionPosition(), 0.001)

	m.OnTransitionPosition(0.75)
	require.InDelta(t, 0.75, m.TransitionPosition(), 0.001)
}

func TestMixerOnTransitionComplete(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	require.True(t, m.IsInTransitionCrossfade())

	m.OnTransitionComplete()

	require.False(t, m.IsInTransitionCrossfade(), "transition crossfade should be cleared")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should be reset")
}

func TestMixerOnTransitionAbortSnapsPosition(t *testing.T) {
	// OnTransitionAbort should snap position to 0.0 before clearing state,
	// preventing an audio discontinuity when the T-bar is pulled back.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Start transition and move to halfway
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	require.True(t, m.IsInTransitionCrossfade())

	// Ingest frames to start a mix cycle
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: make([]float32, 2048)}
	m.channels["cam2"].decoder = &mockDecoder{samples: make([]float32, 2048)}
	m.mu.Unlock()

	frame := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)
	m.IngestFrame("cam2", frame)

	// Now abort — should snap to position 0 and clear state
	m.OnTransitionAbort()

	require.False(t, m.IsInTransitionCrossfade(), "transition should be cleared after abort")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should be reset after abort")
}

func TestMixerTransitionCrossfadeGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	// At position 0.0: old=1.0, new=0.0
	m.OnTransitionPosition(0.0)
	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "old gain at 0.0")
	require.InDelta(t, 0.0, newGain, 0.001, "new gain at 0.0")

	// At position 0.5: old≈0.707, new≈0.707
	m.OnTransitionPosition(0.5)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "old gain at 0.5")
	require.InDelta(t, 0.707, newGain, 0.001, "new gain at 0.5")

	// At position 1.0: old=0.0, new=1.0
	m.OnTransitionPosition(1.0)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "old gain at 1.0")
	require.InDelta(t, 1.0, newGain, 0.001, "new gain at 1.0")
}

func TestMixerTransitionGainsNotActive(t *testing.T) {
	// When no transition is active, gains should return 1.0, 0.0
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "old gain when inactive")
	require.InDelta(t, 0.0, newGain, 0.001, "new gain when inactive")
}

func TestMixerTransitionCrossfadeIngestFrame(t *testing.T) {
	// During a transition crossfade, IngestFrame should apply position-based
	// gains to the from/to sources, multiplied with channel gain.
	// Use 0.5 amplitude to stay below the -1 dBFS limiter threshold.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{0.5, 0.5, 0.5, 0.5}
	toPCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start transition at 50% with audio position pre-set to 0.5 so both
	// audioPos and cyclePos are at 0.5, giving flat gain (no per-sample ramp).
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	m.mu.Lock()
	m.transCrossfadeAudioPos = 0.5 // simulate audio already advanced to 0.5
	m.mu.Unlock()

	// Ingest from both sources — the mixer is in mixing mode (2 active channels)
	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")

	// At position 0.5: oldGain = cos(0.5·π/2) ≈ 0.707, newGain = sin(0.5·π/2) ≈ 0.707
	// Both sources have 0.5 PCM at 0dB channel gain
	// Expected mixed sample: 0.5*0.707 + 0.5*0.707 ≈ 0.707
	expectedSum := 0.5 * (math.Cos(0.5*math.Pi/2) + math.Sin(0.5*math.Pi/2))
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedSum, s, 0.01, "sample %d should have transition gains applied", i)
	}
}

// Verify audio gain is continuous across mix cycles when multiple video position updates occur between audio frames.

func TestMixerTransitionGainContinuityAcrossFrames(t *testing.T) {
	// This test verifies that audio gain is continuous across mix cycles even
	// when multiple video position updates happen between audio frames.
	// The transition position is snapshotted per mix cycle, so intermediate
	// video position updates do not cause gain jumps between audio frames.
	//
	// Scenario: Audio frame 1 at position 0.1, then video updates to 0.2, 0.3,
	// then audio frame 2. Frame 2's start gain should match frame 1's end gain
	// (both at position 0.1), NOT jump to 0.2 from the video-driven prevPos.

	var capturedPCMs [][]float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.5, 0.5} // 2 stereo samples

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: new([]float32)}, nil
		},
	})
	defer func() { _ = m.Close() }()

	// Use a capturing encoder that records each call's PCM
	var encodeCalls [][]float32
	m.mu.Lock()
	m.encoder = &mockEncoderMultiCapture{calls: &encodeCalls}
	m.mu.Unlock()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.channels["cam2"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	// Start transition
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	// === Audio frame 1: position at 0.1 ===
	m.OnTransitionPosition(0.1)
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	require.Equal(t, 1, len(outputFrames), "should have 1 output after first mix cycle")

	// Record end gain of frame 1 — the last sample's gain for the "from" source
	require.True(t, len(encodeCalls) >= 1, "encoder should have been called")
	frame1PCM := encodeCalls[0]
	frame1LastSample := frame1PCM[len(frame1PCM)-1]

	// === Simulate video position updates WITHOUT audio frames (video is faster) ===
	m.OnTransitionPosition(0.15)
	m.OnTransitionPosition(0.2)
	m.OnTransitionPosition(0.3) // video jumped ahead

	// === Audio frame 2: position now at 0.3 ===
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	require.Equal(t, 2, len(outputFrames), "should have 2 outputs")

	frame2PCM := encodeCalls[1]
	frame2FirstSample := frame2PCM[0]

	// KEY ASSERTION: The first sample of frame 2 should be close to the last
	// sample of frame 1 (continuous gain). The difference should be small —
	// certainly less than 10% of the signal amplitude.
	discontinuity := math.Abs(float64(frame2FirstSample - frame1LastSample))
	maxAllowedDiscontinuity := 0.05 // 5% of 0.5 amplitude = 0.025, be generous
	require.Less(t, discontinuity, maxAllowedDiscontinuity,
		"gain discontinuity between frames: frame1 last=%.4f, frame2 first=%.4f, diff=%.4f",
		frame1LastSample, frame2FirstSample, discontinuity)

	_ = capturedPCMs
}

// TestMixerTransitionCompleteFlushes verifies that OnTransitionComplete flushes
// any pending mix cycle before re-enabling passthrough, preventing frame drops.
func TestMixerTransitionCompleteFlushes(t *testing.T) {
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
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

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.channels["cam2"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)

	// Ingest one frame from cam1 only — starts a mix cycle but doesn't flush
	// (needs both sources for eager flush)
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	mu.Lock()
	countBefore := len(outputFrames)
	mu.Unlock()

	// Complete the transition — this should flush the pending mix cycle
	m.OnTransitionComplete()

	mu.Lock()
	countAfter := len(outputFrames)
	mu.Unlock()

	require.Greater(t, countAfter, countBefore,
		"OnTransitionComplete should flush pending mix cycle (had %d frames before, %d after)",
		countBefore, countAfter)
}

// mockEncoderMultiCapture captures PCM from each Encode call separately.
type mockEncoderMultiCapture struct {
	calls *[][]float32
}

func (m *mockEncoderMultiCapture) Encode(pcm []float32) ([]byte, error) {
	cp := make([]float32, len(pcm))
	copy(cp, pcm)
	*m.calls = append(*m.calls, cp)
	return []byte{0xFF}, nil
}
func (m *mockEncoderMultiCapture) Close() error { return nil }

// FTB Reverse audio fades in (increasing gain as position advances).

func TestMixerTransitionFTBReverseGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// FTB reverse: fade the "from" source IN from silence
	m.OnTransitionStart("cam1", "", FadeIn, 1000)

	// At position 0.0: audio should be silent (starting from black)
	m.OnTransitionPosition(0.0)
	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "FTB reverse at 0.0: from source should be silent")
	require.InDelta(t, 0.0, newGain, 0.001, "FTB reverse: no 'to' source")

	// At position 0.5: audio should be at ~0.707 (fading in)
	m.OnTransitionPosition(0.5)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "FTB reverse at 0.5: from source fading in")
	require.InDelta(t, 0.0, newGain, 0.001, "FTB reverse: no 'to' source")

	// At position 1.0: audio should be at full volume
	m.OnTransitionPosition(1.0)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "FTB reverse at 1.0: from source fully in")
	require.InDelta(t, 0.0, newGain, 0.001, "FTB reverse: no 'to' source")
}

func TestMixerTransitionFTBForwardGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// FTB forward: fade the "from" source OUT to silence
	m.OnTransitionStart("cam1", "", FadeOut, 1000)

	// At position 0.0: audio should be full
	m.OnTransitionPosition(0.0)
	oldGain, _ := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "FTB forward at 0.0: full volume")

	// At position 0.5: fading out
	m.OnTransitionPosition(0.5)
	oldGain, _ = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "FTB forward at 0.5: fading out")

	// At position 1.0: silent
	m.OnTransitionPosition(1.0)
	oldGain, _ = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "FTB forward at 1.0: silent")
}

// Program mute silences output when FTB is held.

func TestMixerProgramMute(t *testing.T) {
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Mute program output (FTB held)
	m.SetProgramMute(true)
	require.True(t, m.IsProgramMuted())
	require.False(t, m.IsPassthrough(), "program mute disables passthrough")

	// Ingest a frame — output should be produced but with silent PCM
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	require.Equal(t, 1, len(outputFrames), "muted mixer should still produce output frames")
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.0, s, 0.001, "sample %d should be silent when program muted", i)
	}

	// Verify metering shows silence (LinearToDBFS clamps to -96)
	peak := m.ProgramPeak()
	require.InDelta(t, -96.0, peak[0], 0.001, "left peak should be -96 (silence)")
	require.InDelta(t, -96.0, peak[1], 0.001, "right peak should be -96 (silence)")

	// Unmute — next frame should have audio
	m.SetProgramMute(false)
	require.False(t, m.IsProgramMuted())

	outputFrames = nil
	capturedPCM = nil
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	for i, s := range capturedPCM {
		require.InDelta(t, 1.0, s, 0.001, "sample %d should have audio after unmute", i)
	}
}

// Dip transition reduces audio to silence at the midpoint.

func TestMixerTransitionDipGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", DipToSilence, 1000)

	// Position 0.0: fully source A
	m.OnTransitionPosition(0.0)
	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "dip at 0.0: source A full")
	require.InDelta(t, 0.0, newGain, 0.001, "dip at 0.0: source B silent")

	// Position 0.25: source A fading out, source B still silent
	m.OnTransitionPosition(0.25)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "dip at 0.25: source A fading")
	require.InDelta(t, 0.0, newGain, 0.001, "dip at 0.25: source B still silent")

	// Position 0.5: both sources SILENT (the dip midpoint)
	m.OnTransitionPosition(0.5)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "dip at 0.5: source A silent")
	require.InDelta(t, 0.0, newGain, 0.001, "dip at 0.5: source B silent")

	// Position 0.75: source A gone, source B fading in
	m.OnTransitionPosition(0.75)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "dip at 0.75: source A gone")
	require.InDelta(t, 0.707, newGain, 0.001, "dip at 0.75: source B fading in")

	// Position 1.0: fully source B
	m.OnTransitionPosition(1.0)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "dip at 1.0: source A gone")
	require.InDelta(t, 1.0, newGain, 0.001, "dip at 1.0: source B full")
}

func TestMixerDipIngestFrameMidpoint(t *testing.T) {
	// At the dip midpoint (0.5), both sources should produce silent output.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{1.0, 1.0, 1.0, 1.0}
	toPCM := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start dip at 0.5 (midpoint = silence), set audio position to match
	m.OnTransitionStart("cam1", "cam2", DipToSilence, 1000)
	m.OnTransitionPosition(0.5)
	m.mu.Lock()
	m.transCrossfadeAudioPos = 0.5 // simulate audio already advanced to 0.5
	m.mu.Unlock()

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.0, s, 0.001, "sample %d should be silent at dip midpoint", i)
	}
}

// Transition gain interpolation ramps smoothly per-sample (no zipper noise).

func TestMixerTransitionPerSampleInterpolation(t *testing.T) {
	// When position changes between frames, gain should ramp smoothly
	// across samples rather than being a flat block.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	// 8 samples of constant 0.5 — enough to see the ramp.
	// Use 0.5 to stay below the -1 dBFS limiter threshold.
	pcm := make([]float32, 8)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	// FTB forward from position 0.0 to 0.5 — gain should ramp from 1.0 to 0.707
	m.OnTransitionStart("cam1", "", FadeOut, 1000)
	m.OnTransitionPosition(0.5) // audioPos=0.0 (from start), cyclePos will snapshot 0.5

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	require.Equal(t, 8, len(capturedPCM))

	// First sample should be at audioPos gain: 0.5 * cos(0 * π/2) = 0.5
	// Last sample should approach cyclePos gain: 0.5 * cos(0.5 * π/2) ≈ 0.354
	// Intermediate samples should be between these values (monotonically decreasing)
	require.InDelta(t, 0.5, capturedPCM[0], 0.01, "first sample at prevPos gain")
	require.True(t, capturedPCM[7] < capturedPCM[0], "last sample should be less than first (fading out)")
	require.InDelta(t, 0.5*math.Cos(0.5*math.Pi/2), capturedPCM[7], 0.1, "last sample approaching currentPos gain")

	// Check monotonically decreasing (no staircase)
	for i := 1; i < len(capturedPCM); i++ {
		require.True(t, capturedPCM[i] <= capturedPCM[i-1]+0.001,
			"sample %d (%.4f) should be <= sample %d (%.4f) — smooth ramp", i, capturedPCM[i], i-1, capturedPCM[i-1])
	}
}

// --- Deadlock prevention: per-cycle deadline ---

func TestMixerDeadlockPrevention(t *testing.T) {
	// Two active channels, but only one sends frames.
	// The mixer produces output after the 50ms per-cycle deadline even when a channel is silent.
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: cam1PCM}, nil
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
	require.False(t, m.IsPassthrough())

	// Only send a frame to channel 1 — channel 2 is silent
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Wait longer than the 50ms deadline
	time.Sleep(100 * time.Millisecond)

	// Output should have been produced despite channel 2 being silent
	mu.Lock()
	count := len(outputFrames)
	mu.Unlock()
	require.GreaterOrEqual(t, count, 1, "mixer should produce output after deadline even if channel 2 is silent")
}

func TestChannelDecoderInitOnce(t *testing.T) {
	// Verify that the decoder factory is called exactly once per channel,
	// even when multiple goroutines call IngestFrame concurrently.
	const goroutines = 20

	var factoryCalls atomic.Int64

	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			factoryCalls.Add(1)
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough mode so the mixing path's decoder init is exercised.
	require.NoError(t, m.SetMasterLevel(-1.0))
	require.False(t, m.IsPassthrough())

	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			<-start // synchronize all goroutines
			frame := &media.AudioFrame{
				PTS:        int64(1000 + i),
				Data:       []byte{0xAA},
				SampleRate: 48000,
				Channels:   2,
			}
			m.IngestFrame("cam1", frame)
		}(i)
	}

	close(start) // release all goroutines at once
	wg.Wait()

	// The decoder factory must have been called exactly once for "cam1".
	require.Equal(t, int64(1), factoryCalls.Load(),
		"decoder factory should be called exactly once per channel, got %d", factoryCalls.Load())
}

func TestMixerCrossfadePreSeedAppliesGain(t *testing.T) {
	// When OnCut pre-seeds old source PCM, it should apply the channel's
	// gain (trim * fader) so the crossfade blends consistently with the
	// new source's gained PCM from ingestCrossfadeFrame.
	var allCapturedPCM [][]float32
	var outputFrames []*media.AudioFrame

	// Both sources produce 1.0 amplitude samples
	oldPCM := []float32{1.0, 1.0, 1.0, 1.0}
	newPCM := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			var captured []float32
			allCapturedPCM = append(allCapturedPCM, captured)
			idx := len(allCapturedPCM) - 1
			return &mockEncoderCapture{pcmRef: &allCapturedPCM[idx]}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set decoders
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Set cam1 trim to -6dB — this should apply to the pre-seeded PCM
	_ = m.SetTrim("cam1", -6.0)

	// Send frames from cam1 to populate lastDecodedPCM
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS: int64(i * 1024), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
		})
	}
	outputFrames = nil
	allCapturedPCM = nil

	// Trigger cut — cam1's pre-seeded PCM should have -6dB gain applied
	m.OnCut("cam1", "cam2")

	// Send one frame from cam2 (at 0dB trim, 0dB level → gain=1.0)
	_ = m.SetTrim("cam2", 0)
	m.IngestFrame("cam2", &media.AudioFrame{
		PTS: 3 * 1024, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2,
	})

	require.True(t, len(outputFrames) > 0, "crossfade should produce output")

	// The pre-seeded old source PCM (1.0 * DBToLinear(-6) ≈ 0.501) should be
	// blended with the new source PCM (1.0 * 1.0 = 1.0).
	// At t=0: result ≈ old*cos(0) + new*sin(0) ≈ 0.501*1.0 + 1.0*0.0 = 0.501
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]
	require.InDelta(t, DBToLinear(-6.0), float64(lastPCM[0]), 0.05,
		"first crossfade sample should reflect cam1's -6dB trim")
}

func TestMixerRemoveChannelCleansUpPCMBuffer(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.NoError(t, m.SetMasterLevel(-1.0)) // force mixing path

	// Ingest a frame to populate lastDecodedPCM
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 0, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	m.mu.RLock()
	_, hasPCM := m.lastDecodedPCM["cam1"]
	m.mu.RUnlock()
	require.True(t, hasPCM, "lastDecodedPCM should be populated after IngestFrame")

	// Remove channel — should clean up the PCM buffer
	m.RemoveChannel("cam1")

	m.mu.RLock()
	_, hasPCM = m.lastDecodedPCM["cam1"]
	m.mu.RUnlock()
	require.False(t, hasPCM, "lastDecodedPCM should be cleaned up after RemoveChannel")
}

func TestMixerTrimAppliedBeforeFader(t *testing.T) {
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(f *media.AudioFrame) {
			outputFrames = append(outputFrames, f)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Set trim to -6dB (fader stays at 0dB)
	err := m.SetTrim("cam1", -6.0)
	require.NoError(t, err)

	// Verify trim is stored in state
	states := m.ChannelStates()
	require.InDelta(t, -6.0, states["cam1"].Trim, 0.01)

	// Verify passthrough is broken (trim != 0)
	require.False(t, m.IsPassthrough())

	// Ingest a frame — output PCM should be attenuated by trim
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	// Trim at -6dB: gain ≈ 0.501, fader at 0dB: gain = 1.0
	// Combined: 1.0 * 0.501 * 1.0 ≈ 0.501
	expectedGain := DBToLinear(-6.0)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedGain, s, 0.001,
			"sample %d should have trim applied (expected %.3f, got %.3f)", i, expectedGain, s)
	}
}

func TestMixerTrimBreaksPassthrough(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Initially passthrough (single source at 0dB)
	require.True(t, m.IsPassthrough())

	// Set non-zero trim
	_ = m.SetTrim("cam1", -3.0)

	// Should break passthrough
	require.False(t, m.IsPassthrough())
}

func TestMixerTrimRangeValidation(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	require.Error(t, m.SetTrim("cam1", -25.0), "trim below -20 should error")
	require.Error(t, m.SetTrim("cam1", 25.0), "trim above +20 should error")
	require.NoError(t, m.SetTrim("cam1", -20.0), "trim at -20 should be OK")
	require.NoError(t, m.SetTrim("cam1", 20.0), "trim at +20 should be OK")
}

func TestMixerPerChannelPeaks(t *testing.T) {
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.3, 0.5, 0.3} // L=0.5, R=0.3 interleaved stereo

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough so the mixing path runs
	require.NoError(t, m.SetMasterLevel(-1.0))

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 0, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	// Channel peaks should be populated
	states := m.ChannelStates()
	ch, ok := states["cam1"]
	require.True(t, ok)
	require.True(t, ch.PeakL > -96 || ch.PeakR > -96,
		"per-channel peaks should be populated after frame ingestion")
}

func TestMixerTransitionCrossfadeWithTrim(t *testing.T) {
	// During a transition crossfade, trim should be applied to both sources
	// before the transition gain. Verifies that channelGain (trim * fader)
	// is multiplied with the transition position gain.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{1.0, 1.0, 1.0, 1.0}
	toPCM := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Apply -6dB trim to cam1 (from source)
	require.NoError(t, m.SetTrim("cam1", -6.0))

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start transition at 50% with stable position (no per-sample ramp)
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	m.OnTransitionPosition(0.5)

	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.Equal(t, 1, len(outputFrames))

	// At position 0.5: transitionFromGain = cos(0.5·π/2) ≈ 0.707
	//                   transitionToGain   = sin(0.5·π/2) ≈ 0.707
	// cam1: PCM=1.0, trim=-6dB (≈0.501), fader=0dB → channelGain ≈ 0.501
	//   contribution: 1.0 * 0.501 * 0.707 ≈ 0.354
	// cam2: PCM=1.0, trim=0dB, fader=0dB → channelGain = 1.0
	//   contribution: 1.0 * 1.0 * 0.707 ≈ 0.707
	// expected sum ≈ 0.354 + 0.707 ≈ 1.061 (clamped by limiter to ~0.891)
	trimGain := DBToLinear(-6.0)
	transGain := math.Cos(0.5 * math.Pi / 2)
	fromContribution := 1.0 * trimGain * transGain
	toContribution := 1.0 * 1.0 * transGain
	expectedSum := fromContribution + toContribution
	// Sum > 1.0, so limiter clamps. Verify samples are below 1.0 (limiter at -1dBFS ≈ 0.891)
	require.Equal(t, 4, len(capturedPCM))
	if expectedSum > 0.891 {
		// Limiter should have clamped
		for i, s := range capturedPCM {
			require.True(t, s <= 0.891+0.01,
				"sample %d (%.4f) should be limited to -1 dBFS", i, s)
			require.True(t, s > 0.5,
				"sample %d (%.4f) should be non-trivial (both sources contributing)", i, s)
		}
	} else {
		for i, s := range capturedPCM {
			require.InDelta(t, expectedSum, s, 0.02,
				"sample %d should have trim+transition gains applied", i)
		}
	}
}

func TestMixerCrossfadeUsesPreBufferedPCM(t *testing.T) {
	// After sending frames from cam1, triggering a cut to cam2, and sending
	// only ONE frame from cam2, the crossfade should complete immediately
	// because cam1's last PCM is pre-buffered — no waiting needed.
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.8, 0.8, 0.8, 0.8}
	cam2PCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
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

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Send several frames from cam1 to build up the pre-buffer
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS: int64(i * 1024), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
		})
	}

	// Clear output from passthrough/mixing frames
	outputFrames = nil

	// Trigger cut from cam1 -> cam2
	m.OnCut("cam1", "cam2")

	// Send ONE frame from cam2 — crossfade should complete immediately
	// because cam1's last PCM is already pre-buffered
	m.IngestFrame("cam2", &media.AudioFrame{
		PTS: 3 * 1024, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2,
	})

	// Should have output: the crossfade should have completed in one frame
	require.True(t, len(outputFrames) > 0,
		"crossfade should produce output immediately on first new-source frame")
}

func TestChannelDecoderInitOnceCrossfade(t *testing.T) {
	// Verify sync.Once works for the crossfade path too.
	var factoryCalls atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			factoryCalls.Add(1)
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Ingest frames from both sources — each channel's decoder should init once
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Two channels, each initialized once = 2 factory calls total
	require.Equal(t, int64(2), factoryCalls.Load(),
		"decoder factory should be called once per channel (2 channels), got %d", factoryCalls.Load())

	// Now do another crossfade — factories should NOT be called again
	m.OnCut("cam2", "cam1")
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xCC}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xDD}, SampleRate: 48000, Channels: 2})

	require.Equal(t, int64(2), factoryCalls.Load(),
		"decoder factory should not be called again on subsequent crossfades, got %d", factoryCalls.Load())
}

// TestMixerPassthroughRaceSafety exercises the RLock-to-Lock upgrade in
// IngestFrame. One goroutine ingests frames while another toggles passthrough
// by activating a second channel. With -race this detects the TOCTOU bug
// where passthrough is checked under RLock then re-acquired under Lock.
func TestMixerPassthroughRaceSafety(t *testing.T) {
	t.Parallel()

	var outputCount atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) { outputCount.Add(1) },
	})
	t.Cleanup(func() { _ = m.Close() })

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Ingest a few frames synchronously while passthrough is guaranteed
	// (only cam1 active). This ensures outputCount > 0 regardless of
	// goroutine scheduling in the concurrent phase below.
	for i := 0; i < 5; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS:        int64(i),
			Data:       []byte{0xAA, 0xBB},
			SampleRate: 48000,
			Channels:   2,
		})
	}

	const iterations = 1000
	var wg sync.WaitGroup

	// Goroutine 1: rapidly ingest frames on cam1
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 5; i < 5+iterations; i++ {
			m.IngestFrame("cam1", &media.AudioFrame{
				PTS:        int64(i),
				Data:       []byte{0xAA, 0xBB},
				SampleRate: 48000,
				Channels:   2,
			})
		}
	}()

	// Goroutine 2: toggle passthrough by activating/deactivating cam2
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.SetActive("cam2", true)  // passthrough → false
			m.SetActive("cam2", false) // passthrough → true
		}
	}()

	wg.Wait()

	// If the race detector didn't fire, we're good. The output count
	// doesn't matter — some frames are passthrough, some go through
	// mixing (which has no encoder, so they're dropped). The key is
	// no data race.
	require.Greater(t, outputCount.Load(), int64(0), "should have produced some output")
}

func TestMixer_SetProgramMute_ResetsEnvelopes(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Directly drive the limiter and compressor with loud signal
	// to build up envelope state.
	loudSamples := make([]float32, 2048)
	for i := range loudSamples {
		loudSamples[i] = 2.0
	}
	m.limiter.Process(loudSamples)
	require.Greater(t, m.limiter.GainReduction(), 0.0, "limiter should have engaged")

	// Also drive the per-channel compressor
	m.mu.RLock()
	ch := m.channels["cam1"]
	m.mu.RUnlock()
	require.NoError(t, ch.compressor.SetParams(-6, 4.0, 0.1, 100.0, 0))
	ch.compressor.Process(loudSamples)
	require.Greater(t, ch.compressor.GainReduction(), 0.0, "compressor should have engaged")

	// Mute (FTB) should reset limiter and all compressor envelopes
	m.SetProgramMute(true)

	require.InDelta(t, 0.0, m.limiter.GainReduction(), 0.001,
		"limiter GR should be 0 after mute/reset")
	require.InDelta(t, 0.0, ch.compressor.GainReduction(), 0.001,
		"compressor GR should be 0 after mute/reset")
}

func TestMixer_MonotonicOutputPTS(t *testing.T) {
	// Two channels with different PTS values. Verify consecutive output frames
	// have PTS incrementing by exactly frameDuration90k().
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
	cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
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

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Ingest 5 pairs with non-monotonic PTS values
	ptsValues := []int64{5000, 3000, 7000, 2000, 9000}
	for _, pts := range ptsValues {
		m.IngestFrame("cam1", &media.AudioFrame{PTS: pts, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
		m.IngestFrame("cam2", &media.AudioFrame{PTS: pts + 100, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	}

	mu.Lock()
	frames := append([]*media.AudioFrame{}, outputFrames...)
	mu.Unlock()

	require.GreaterOrEqual(t, len(frames), 2, "need at least 2 frames")

	// Output PTS must be strictly monotonically increasing.
	// With source-following PTS, the deltas track input PTS (not a fixed counter),
	// but backward inputs are corrected to advance by frameDuration90k.
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"frame %d PTS (%d) must be > frame %d PTS (%d)",
			i, frames[i].PTS, i-1, frames[i-1].PTS)
	}
}

func TestGrowBuf(t *testing.T) {
	// nil input — should allocate new slice
	buf := growBuf(nil, 10)
	require.Equal(t, 10, len(buf))
	require.GreaterOrEqual(t, cap(buf), 10)

	// Reuse when capacity sufficient
	original := buf
	buf = growBuf(buf, 5)
	require.Equal(t, 5, len(buf))
	// Should be the same underlying array (reused)
	require.Equal(t, cap(original), cap(buf), "should reuse existing capacity")

	// New alloc when capacity insufficient
	buf = growBuf(buf, 100)
	require.Equal(t, 100, len(buf))
	require.GreaterOrEqual(t, cap(buf), 100)

	// Zero length
	buf = growBuf(buf, 0)
	require.Equal(t, 0, len(buf))
}

func BenchmarkMixerMixingPath(b *testing.B) {
	// Measures allocations per frame in the mixing hot path.
	// Per-channel processing allocs (trim/gain/stored buffers) are 0 in steady state.
	// Remaining allocs are unavoidable: output media.AudioFrame + encoded AAC data.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetTrim("cam1", 1.0) // force mixing path

	// Warm up: run a few frames to initialize all buffers
	for i := 0; i < 10; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS: int64(i * 1920), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
		})
	}

	frame := &media.AudioFrame{PTS: 100000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		frame.PTS = int64(100000 + i*1920)
		m.IngestFrame("cam1", frame)
	}
}

func TestMixer_MonotonicPTS_ArrivalOrderIndependent(t *testing.T) {
	// Swapping which channel delivers first should not affect the output PTS sequence.
	for _, order := range []string{"cam1-first", "cam2-first"} {
		t.Run(order, func(t *testing.T) {
			var mu sync.Mutex
			var outputFrames []*media.AudioFrame

			cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
			cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

			m := NewMixer(MixerConfig{
				SampleRate: 48000,
				Channels:   2,
				Output: func(frame *media.AudioFrame) {
					mu.Lock()
					outputFrames = append(outputFrames, frame)
					mu.Unlock()
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

			m.mu.Lock()
			m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
			m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
			m.mu.Unlock()

			// Ingest 5 pairs, alternating which channel arrives first
			for i := 0; i < 5; i++ {
				f1 := &media.AudioFrame{PTS: int64(1000 + i*2000), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
				f2 := &media.AudioFrame{PTS: int64(1500 + i*2000), Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
				if order == "cam1-first" {
					m.IngestFrame("cam1", f1)
					m.IngestFrame("cam2", f2)
				} else {
					m.IngestFrame("cam2", f2)
					m.IngestFrame("cam1", f1)
				}
			}

			mu.Lock()
			frames := append([]*media.AudioFrame{}, outputFrames...)
			mu.Unlock()

			require.GreaterOrEqual(t, len(frames), 2, "need at least 2 frames")

			// Output PTS must be strictly monotonically increasing regardless
			// of ingestion order. Deltas follow source PTS, not a fixed counter.
			for i := 1; i < len(frames); i++ {
				require.Greater(t, frames[i].PTS, frames[i-1].PTS,
					"frame %d PTS (%d) must be > frame %d PTS (%d) (order=%s)",
					i, frames[i].PTS, i-1, frames[i-1].PTS, order)
			}
		})
	}
}

func TestMixer_MonotonicPTS_ResetOnGap(t *testing.T) {
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetTrim("cam1", 1.0) // force mixing mode

	// Ingest a few frames at PTS ~1000
	for i := range 3 {
		m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000 + int64(i)*1920, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	}

	mu.Lock()
	countBefore := len(outputFrames)
	mu.Unlock()

	// Now inject a PTS gap > 5 seconds
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 90000*6 + 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	// Then a normal follow-up
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 90000*6 + 2920, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	mu.Lock()
	frames := append([]*media.AudioFrame{}, outputFrames...)
	mu.Unlock()

	require.Greater(t, len(frames), countBefore, "should produce frames after gap")

	// With wall-clock PTS, frames should be monotonically non-decreasing
	// regardless of source PTS gaps.
	lastIdx := len(frames) - 1
	if lastIdx >= 1 {
		require.GreaterOrEqual(t, frames[lastIdx].PTS, frames[lastIdx-1].PTS,
			"PTS should be monotonically non-decreasing across source PTS gaps")
	}
}

// TestMixer_MonotonicPTSAcrossPassthroughMixingCycles verifies that output PTS
// is monotonically increasing across passthrough↔mixing mode transitions.
// Output PTS is monotonically increasing across passthrough/mixing mode transitions.
func TestMixer_MonotonicPTSAcrossPassthroughMixingCycles(t *testing.T) {
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	// 1024 samples / 48000 Hz = ~21.33ms; in 90 kHz ticks = 1920
	const frameDuration90k = int64(1024) * 90000 / 48000

	pcmSamples := make([]float32, 1024*2) // 1024 stereo samples
	for i := range pcmSamples {
		pcmSamples[i] = 0.1
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcmSamples}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Each source has its own PTS sequence — in a real system these come from
	// independent cameras and are NOT synchronized.
	cam1PTS := int64(100000)
	cam2PTS := int64(200000) // intentionally different from cam1

	// Run 4 transition cycles
	for cycle := 0; cycle < 4; cycle++ {
		fromCam, toCam := "cam1", "cam2"
		fromPTS, toPTS := &cam1PTS, &cam2PTS
		if cycle%2 == 1 {
			fromCam, toCam = "cam2", "cam1"
			fromPTS, toPTS = &cam2PTS, &cam1PTS
		}

		// Phase A: passthrough on fromCam (10 frames)
		m.SetActive(fromCam, true)
		m.SetActive(toCam, false)
		for i := 0; i < 10; i++ {
			m.IngestFrame(fromCam, &media.AudioFrame{
				PTS: *fromPTS, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
			})
			*fromPTS += frameDuration90k
		}

		// Phase B: transition crossfade (mixing mode)
		m.OnTransitionStart(fromCam, toCam, Crossfade, 500)
		m.OnTransitionPosition(0.5)
		for i := 0; i < 5; i++ {
			m.IngestFrame(fromCam, &media.AudioFrame{
				PTS: *fromPTS, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
			})
			*fromPTS += frameDuration90k
			m.IngestFrame(toCam, &media.AudioFrame{
				PTS: *toPTS, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2,
			})
			*toPTS += frameDuration90k
		}
		m.OnTransitionComplete()
	}

	// Collect all output frames
	mu.Lock()
	frames := append([]*media.AudioFrame{}, outputFrames...)
	mu.Unlock()

	require.Greater(t, len(frames), 40, "should have substantial output frames across 4 cycles")

	// Assert: output PTS is strictly monotonically increasing across ALL frames.
	// With source-following PTS, deltas may be larger than frameDuration90k
	// when the mixer switches between sources on different PTS timelines.
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"frame %d→%d: PTS must be strictly increasing (prev=%d, cur=%d)",
			i-1, i, frames[i-1].PTS, frames[i].PTS)
	}
}

func TestStereoGainInterpolation(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = "cam1"
	m.transCrossfadeTo = "cam2"
	m.transCrossfadeMode = Crossfade
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 1.0
	m.mixStarted = true

	// Create a stereo buffer: L=1.0, R=0.5 for all sample pairs
	numPairs := 512
	trimmedPCM := make([]float32, numPairs*2)
	for i := 0; i < numPairs; i++ {
		trimmedPCM[i*2] = 1.0   // L
		trimmedPCM[i*2+1] = 0.5 // R
	}

	ch := m.channels["cam1"]
	ch.gainBuf = make([]float32, len(trimmedPCM))
	gainedPCM := ch.gainBuf

	// Apply the transition gain interpolation (same code as IngestFrame)
	gainFn := func(p float64) float64 { return transitionFromGain(m.transCrossfadeMode, p) }
	gStart := float32(gainFn(m.transCrossfadeAudioPos))
	gEnd := float32(gainFn(m.mixCycleTransPos))
	channels := m.numChannels
	pairCount := len(trimmedPCM) / channels
	divisor := float32(pairCount)
	if pairCount > 1 {
		divisor = float32(pairCount - 1)
	}
	for i, s := range trimmedPCM {
		t := float32(i/channels) / divisor
		transGain := gStart + (gEnd-gStart)*t
		gainedPCM[i] = s * ch.levelLinear * transGain
	}
	m.mu.Unlock()

	// Verify that L and R at the same time position received identical gain
	for i := 0; i < numPairs; i++ {
		lSample := gainedPCM[i*2]
		rSample := gainedPCM[i*2+1]
		// L input was 1.0, R input was 0.5, so R should be exactly half of L
		// if the same gain was applied to both
		if lSample != 0 {
			ratio := rSample / lSample
			require.InDelta(t, 0.5, float64(ratio), 1e-5,
				"pair %d: L/R ratio should be 0.5 (identical gain), got %v", i, ratio)
		}
	}
}

func TestTransitionGainInterpolation_LastPairReachesTarget(t *testing.T) {
	// Verify that the transition gain interpolation ramp reaches gEnd on the
	// The interpolation ramp reaches gEnd exactly on the last sample pair using (pairCount-1) denominator.
	t.Parallel()

	// Simulate the gain interpolation logic from mixFrameLocked.
	channels := 2
	numPairs := 512
	buf := make([]float32, numPairs*channels)
	for i := range buf {
		buf[i] = 1.0 // uniform input
	}

	gStart := float32(1.0) // from gain at position 0
	gEnd := float32(0.0)   // from gain at position 1 (cos(pi/2) = 0)

	gained := make([]float32, len(buf))
	pairCount := len(buf) / channels
	for i, s := range buf {
		var tVal float32
		if pairCount > 1 {
			tVal = float32(i/channels) / float32(pairCount-1)
		}
		transGain := gStart + (gEnd-gStart)*tVal
		gained[i] = s * transGain
	}

	// First pair should have gain = gStart = 1.0
	require.InDelta(t, float64(gStart), float64(gained[0]), 1e-6,
		"first sample should have gain = gStart")
	require.InDelta(t, float64(gStart), float64(gained[1]), 1e-6,
		"first sample R should have gain = gStart")

	// Last pair should have gain = gEnd = 0.0 exactly
	lastL := gained[len(gained)-2]
	lastR := gained[len(gained)-1]
	require.InDelta(t, float64(gEnd), float64(lastL), 1e-6,
		"last sample L should have gain = gEnd exactly, got %f", lastL)
	require.InDelta(t, float64(gEnd), float64(lastR), 1e-6,
		"last sample R should have gain = gEnd exactly, got %f", lastR)
}

func TestChannelGainCached(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	// Default: 0dB level and 0dB trim should cache as 1.0 linear
	m.mu.RLock()
	ch := m.channels["cam1"]
	require.InDelta(t, 1.0, float64(ch.levelLinear), 1e-6, "default level should cache as 1.0")
	require.InDelta(t, 1.0, float64(ch.trimLinear), 1e-6, "default trim should cache as 1.0")
	m.mu.RUnlock()

	// Set level to -6 dB and verify cached value
	err := m.SetLevel("cam1", -6)
	require.NoError(t, err)
	m.mu.RLock()
	ch = m.channels["cam1"]
	expected := float32(DBToLinear(-6))
	require.InDelta(t, float64(expected), float64(ch.levelLinear), 1e-6, "levelLinear should match DBToLinear(-6)")
	m.mu.RUnlock()

	// Set trim to +3 dB and verify cached value
	err = m.SetTrim("cam1", 3)
	require.NoError(t, err)
	m.mu.RLock()
	ch = m.channels["cam1"]
	expectedTrim := float32(DBToLinear(3))
	require.InDelta(t, float64(expectedTrim), float64(ch.trimLinear), 1e-6, "trimLinear should match DBToLinear(3)")
	m.mu.RUnlock()

	// Set master level to -3 dB and verify cached value
	require.NoError(t, m.SetMasterLevel(-3))
	m.mu.RLock()
	expectedMaster := float32(DBToLinear(-3))
	require.InDelta(t, float64(expectedMaster), float64(m.masterLinear), 1e-6, "masterLinear should match DBToLinear(-3)")
	m.mu.RUnlock()
}

func TestMixerCrossfadeLimiterApplied(t *testing.T) {
	// Two full-scale signals crossfaded peak at ~1.414 (+3dB) at the midpoint.
	// The brickwall limiter must clamp output to ≤ -1 dBFS (~0.891).
	var allCapturedPCM [][]float32

	// Both sources at full scale — crossfade midpoint will exceed 1.0
	fullScale := make([]float32, 1024)
	for i := range fullScale {
		fullScale[i] = 1.0
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			var captured []float32
			allCapturedPCM = append(allCapturedPCM, captured)
			idx := len(allCapturedPCM) - 1
			return &mockEncoderCapture{pcmRef: &allCapturedPCM[idx]}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Both decoders return full-scale PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fullScale}
	m.channels["cam2"].decoder = &mockDecoder{samples: fullScale}
	m.mu.Unlock()

	// Trigger crossfade: cam1 → cam2
	m.OnCut("cam1", "cam2")

	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.GreaterOrEqual(t, len(allCapturedPCM), 1, "should have captured crossfade PCM")
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]

	// Limiter threshold is -1 dBFS ≈ 0.891
	threshold := float32(math.Pow(10, -1.0/20.0))

	for i, sample := range lastPCM {
		if sample > threshold+0.001 {
			t.Fatalf("sample %d = %.4f exceeds limiter threshold %.4f — limiter not applied during crossfade",
				i, sample, threshold)
		}
	}
}

// --- Stinger audio overlay tests ---

func TestMixer_SetStingerAudio(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	audio := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	m.SetStingerAudio(audio, 48000, 2)

	m.mu.Lock()
	require.Equal(t, audio, m.stingerAudio, "stingerAudio should be stored")
	require.Equal(t, 0, m.stingerOffset, "stingerOffset should be reset to 0")
	require.Equal(t, 2, m.stingerChannels, "stingerChannels should be stored")
	m.mu.Unlock()

	// Calling again should reset offset
	m.mu.Lock()
	m.stingerOffset = 3 // simulate partial consumption
	m.mu.Unlock()

	audio2 := []float32{0.7, 0.8}
	m.SetStingerAudio(audio2, 48000, 2)

	m.mu.Lock()
	require.Equal(t, audio2, m.stingerAudio, "stingerAudio should be replaced")
	require.Equal(t, 0, m.stingerOffset, "stingerOffset should be reset on new call")
	m.mu.Unlock()
}

func TestMixer_SetStingerAudio_SampleRateMismatch(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	audio := []float32{0.1, 0.2, 0.3}
	m.SetStingerAudio(audio, 44100, 2) // different sample rate → resampled, not rejected

	m.mu.RLock()
	stinger := m.stingerAudio
	m.mu.RUnlock()
	require.NotNil(t, stinger, "stingerAudio should be resampled, not rejected")
}

func TestMixer_SetStingerAudio_ChannelMismatch(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	audio := []float32{0.1, 0.2, 0.3}
	m.SetStingerAudio(audio, 48000, 1) // wrong channel count

	m.mu.Lock()
	require.Nil(t, m.stingerAudio, "stingerAudio should be nil on channel mismatch")
	m.mu.Unlock()
}

func TestMixer_AddStingerAudio_Basic(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Create stinger audio that's long enough to avoid fade regions for simple testing
	// With 48kHz stereo, fade-in is 10ms = 960 samples, fade-out is 50ms = 4800 samples
	// We need enough samples to have a "middle" region at full gain
	totalSamples := 960 + 100 + 4800 // fade-in + middle + fade-out
	stingerPCM := make([]float32, totalSamples)
	for i := range stingerPCM {
		stingerPCM[i] = 0.5
	}

	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 960 // skip past fade-in
	m.stingerChannels = 2

	// Mix buffer with some existing audio
	mixed := make([]float32, 100)
	for i := range mixed {
		mixed[i] = 0.3
	}

	m.addStingerAudio(mixed)

	// After addStingerAudio, mixed should have stinger added: 0.3 + 0.5 = 0.8
	for i := 0; i < 100; i++ {
		require.InDelta(t, 0.8, mixed[i], 0.01,
			"sample %d: should be source (0.3) + stinger (0.5)", i)
	}
	require.Equal(t, 960+100, m.stingerOffset, "offset should advance by mixed length")
	m.mu.Unlock()
}

func TestMixer_AddStingerAudio_FadeEnvelope(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// With 48kHz stereo: fadeIn = 48000 * 2 * 10 / 1000 = 960 samples
	// fadeOut = 48000 * 2 * 50 / 1000 = 4800 samples
	fadeInSamples := 960
	fadeOutSamples := 4800
	totalSamples := fadeInSamples + 1000 + fadeOutSamples // some middle section

	stingerPCM := make([]float32, totalSamples)
	for i := range stingerPCM {
		stingerPCM[i] = 1.0 // unity to make fade easy to verify
	}

	// Test fade-in: first samples should be attenuated
	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 0
	m.stingerChannels = 2

	mixed := make([]float32, fadeInSamples)
	m.addStingerAudio(mixed)

	// First sample should be near 0 (pos=0, gain=0/960=0)
	require.InDelta(t, 0.0, mixed[0], 0.01, "first sample should be near zero (fade-in start)")

	// Midpoint of fade-in should be ~0.5
	midIdx := fadeInSamples / 2
	require.InDelta(t, 0.5, mixed[midIdx], 0.05, "midpoint of fade-in should be ~0.5")

	// Last sample of fade-in should be near 1.0
	require.InDelta(t, 1.0, mixed[fadeInSamples-1], 0.01,
		"last sample of fade-in should be near 1.0")
	m.mu.Unlock()

	// Test fade-out: position near the end
	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = totalSamples - fadeOutSamples
	m.stingerChannels = 2

	mixed2 := make([]float32, fadeOutSamples)
	m.addStingerAudio(mixed2)

	// First sample of fade-out region should be near 1.0
	require.InDelta(t, 1.0, mixed2[0], 0.01,
		"first sample of fade-out should be near 1.0")

	// Last sample should be near 0
	require.InDelta(t, 0.0, mixed2[fadeOutSamples-1], 0.01,
		"last sample of fade-out should be near zero")

	// Midpoint should be ~0.5
	fadeMid := fadeOutSamples / 2
	require.InDelta(t, 0.5, mixed2[fadeMid], 0.05,
		"midpoint of fade-out should be ~0.5")
	m.mu.Unlock()
}

func TestMixer_AddStingerAudio_Exhaustion(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Small stinger audio
	stingerPCM := []float32{0.1, 0.2, 0.3, 0.4}

	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 0
	m.stingerChannels = 2

	// Mix buffer larger than remaining stinger
	mixed := make([]float32, 10)
	m.addStingerAudio(mixed)

	// Only first 4 samples should have stinger added (with fade envelope)
	// After exhaustion, stingerAudio is NOT set to nil here because we consumed
	// exactly 4 samples and offset is now 4 = len(stingerAudio)
	require.Equal(t, 4, m.stingerOffset, "offset should be at end")

	// Call again — should detect remaining <= 0 and set stingerAudio to nil
	mixed2 := make([]float32, 10)
	m.addStingerAudio(mixed2)
	require.Nil(t, m.stingerAudio, "stingerAudio should be nil after exhaustion")
	m.mu.Unlock()
}

func TestMixer_AddStingerAudio_StereoFadeSymmetry(t *testing.T) {
	// Bug: fade envelope advanced per-sample instead of per sample-frame,
	// causing L and R channels at the same time instant to get different gains.
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Create stereo stinger audio with all 1.0 values so gain is directly visible.
	// fade-in is 10ms = 480 sample-frames, fade-out is 50ms = 2400 sample-frames.
	// Use a long enough clip so the regions don't overlap.
	totalFrames := 480 + 2400 + 1000             // fade-in + fade-out + middle
	stingerPCM := make([]float32, totalFrames*2) // stereo
	for i := range stingerPCM {
		stingerPCM[i] = 1.0
	}

	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 0
	m.stingerChannels = 2

	// Process the fade-in region
	mixed := make([]float32, 960) // 480 sample-frames * 2 channels = fade-in region
	m.addStingerAudio(mixed)

	// Verify L and R channels at each time instant get the SAME gain.
	for i := 0; i < len(mixed)-1; i += 2 {
		frameIdx := i / 2
		require.InDelta(t, mixed[i], mixed[i+1], 1e-6,
			"frame %d: L (%f) and R (%f) should have identical fade gain",
			frameIdx, mixed[i], mixed[i+1])
	}

	// Also verify that gain is monotonically increasing during fade-in
	for i := 2; i < len(mixed)-1; i += 2 {
		require.GreaterOrEqual(t, mixed[i], mixed[i-2],
			"fade-in should be monotonically increasing at sample %d", i)
	}
	m.mu.Unlock()
}

func TestMixer_StingerAudioClearedOnComplete(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Set up stinger audio
	m.SetStingerAudio([]float32{0.1, 0.2, 0.3}, 48000, 2)

	m.mu.Lock()
	require.NotNil(t, m.stingerAudio)
	m.mu.Unlock()

	// Complete the transition
	m.OnTransitionComplete()

	m.mu.Lock()
	require.Nil(t, m.stingerAudio, "stingerAudio should be nil after OnTransitionComplete")
	require.Equal(t, 0, m.stingerOffset, "stingerOffset should be 0 after OnTransitionComplete")
	require.Equal(t, 0, m.stingerChannels, "stingerChannels should be 0 after OnTransitionComplete")
	m.mu.Unlock()
}

func TestMixer_StingerAudioInMixPath(t *testing.T) {
	// Verify stinger audio is additively mixed during the normal multi-source mix path.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	// Source PCM: silence (0.0) so we can isolate stinger contribution
	sourcePCM := make([]float32, 2048)

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: sourcePCM}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: sourcePCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: sourcePCM}
	m.mu.Unlock()

	// Set stinger audio with a value we can detect.
	// Make it large enough to avoid fade regions affecting our test area.
	fadeIn := 48000 * 2 * 10 / 1000  // 960 samples
	fadeOut := 48000 * 2 * 50 / 1000 // 4800
	stingerLen := fadeIn + len(sourcePCM)*10 + fadeOut
	stingerPCM := make([]float32, stingerLen)
	for i := range stingerPCM {
		stingerPCM[i] = 0.25
	}
	m.SetStingerAudio(stingerPCM, 48000, 2)

	// Advance past fade-in so we get full-gain stinger
	m.mu.Lock()
	m.stingerOffset = fadeIn
	m.mu.Unlock()

	// Ingest from both sources to trigger mix
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.GreaterOrEqual(t, len(outputFrames), 1, "should produce output")
	require.NotNil(t, capturedPCM, "should have captured PCM")

	// Source is silence (0.0), stinger is 0.25 at full gain.
	// After the limiter (which shouldn't affect 0.25), we should see stinger contribution.
	for i := 0; i < len(capturedPCM) && i < 100; i++ {
		require.InDelta(t, 0.25, capturedPCM[i], 0.05,
			"sample %d: with silent source, output should be stinger audio", i)
	}
}

func TestMixerCrossfadeUpdatesProgramPeakMetering(t *testing.T) {
	pcm := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Ingest frames
	f1 := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	f2 := &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", f1)
	m.IngestFrame("cam2", f2)

	// Program peak should have been updated (not stale/silence)
	peak := m.ProgramPeak()
	require.Greater(t, peak[0], -96.0, "left peak should reflect crossfade output, not be silent")
	require.Greater(t, peak[1], -96.0, "right peak should reflect crossfade output, not be silent")
}

func TestMixerCrossfadeDuringFTBProducesSilence(t *testing.T) {
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Activate FTB (program mute)
	m.SetProgramMute(true)

	// Trigger a crossfade (simulates a cut during FTB)
	m.OnCut("cam1", "cam2")

	// Ingest frames from both sources to trigger crossfade path
	frame1 := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.GreaterOrEqual(t, len(outputFrames), 1, "should produce output frame")
	require.NotNil(t, capturedPCM, "encoder should have been called")

	// All samples should be silent because FTB is held
	for i, s := range capturedPCM {
		require.InDelta(t, 0.0, s, 0.001,
			"sample %d should be silent during FTB crossfade, got %f", i, s)
	}
}

// IngestPCM applies crossfade during cuts to prevent audio clicks.

func TestMixerIngestPCM_CrossfadeOnCut(t *testing.T) {
	// When two MXL sources are active and a cut is triggered via OnCut(),
	// IngestPCM respects crossfadeActive, producing a crossfaded output frame.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		// No DecoderFactory needed — IngestPCM supplies raw PCM directly.
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.AddChannel("mxl2")
	m.SetActive("mxl1", true)
	m.SetActive("mxl2", true)

	// Pre-populate mxl1's lastDecodedPCM via an initial IngestPCM call.
	// This simulates normal operation where the source has been sending
	// audio before the cut.
	oldPCM := make([]float32, 2048) // 1024 samples * 2 channels
	for i := range oldPCM {
		oldPCM[i] = 0.8
	}
	m.IngestPCM("mxl1", oldPCM, 1000, 2)

	// Clear output from the initial frame
	outputFrames = nil
	capturedPCM = nil

	// Trigger crossfade: mxl1 → mxl2
	m.OnCut("mxl1", "mxl2")

	// Now send PCM from the new source (mxl2). The crossfade should
	// complete using mxl1's pre-buffered PCM + mxl2's new PCM.
	newPCM := make([]float32, 2048) // 1024 samples * 2 channels
	for i := range newPCM {
		newPCM[i] = 0.0 // silence on the new source
	}
	m.IngestPCM("mxl2", newPCM, 2000, 2)

	require.GreaterOrEqual(t, len(outputFrames), 1,
		"crossfade should produce at least one output frame (frame 1 of 2)")
	require.NotNil(t, capturedPCM,
		"encoder should have been called with crossfaded PCM")

	// The crossfaded PCM should NOT be the raw mix of old+new (which would
	// be 0.8+0.0=0.8 everywhere). Instead, it should show a fade envelope:
	// first samples should be mostly old source (close to 0.8), last samples
	// should be mostly new source (close to 0.0).
	require.True(t, len(capturedPCM) > 4,
		"captured PCM should have enough samples to verify crossfade")

	// Verify the fade shape: first sample should be higher than last sample,
	// because the old source (0.8) is fading out and the new source (0.0) is fading in.
	firstSample := capturedPCM[0]
	lastSample := capturedPCM[len(capturedPCM)-1]
	require.True(t, firstSample > lastSample,
		"crossfade should show fade from old (0.8) to new (0.0); first=%f, last=%f",
		firstSample, lastSample)

	// Complete the 2-frame crossfade with second round.
	// Force deadline expiry so the old source's absence triggers timeout.
	m.mu.Lock()
	m.crossfadeDeadline = time.Now().Add(-crossfadeTimeout * 2)
	m.mu.Unlock()

	m.IngestPCM("mxl2", newPCM, 3000, 2)

	require.GreaterOrEqual(t, len(outputFrames), 2,
		"crossfade should produce second output frame")

	// The crossfade should have cleared after completion.
	m.mu.RLock()
	active := m.crossfadeActive
	m.mu.RUnlock()
	require.False(t, active, "crossfade should be cleared after completion")
}

// IngestPCM upmixes mono to stereo when mixer is configured for stereo.

func TestMixerIngestPCM_MonoToStereoUpmix(t *testing.T) {
	// When an MXL source delivers mono PCM (1024 float32 samples) to a
	// stereo mixer (numChannels=2), mono samples are duplicated to L and R
	// channels, not interpreted as interleaved stereo.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mono_src")
	m.SetActive("mono_src", true)

	// Send mono PCM: 1024 samples (NOT 2048 for stereo). This is
	// half the expected stereo frame size.
	monoPCM := make([]float32, 1024) // mono: 1024 samples
	for i := range monoPCM {
		monoPCM[i] = 0.5
	}
	m.IngestPCM("mono_src", monoPCM, 1000, 1)

	require.GreaterOrEqual(t, len(outputFrames), 1,
		"should produce output frame from mono source")
	require.NotNil(t, capturedPCM,
		"encoder should have been called")

	// After upmix, the PCM should be 2048 samples (1024 * 2 channels),
	// with each mono sample duplicated to L and R.
	require.Equal(t, 2048, len(capturedPCM),
		"mono PCM should be upmixed to stereo (1024 * 2 channels)")

	// Verify all samples are 0.5 (the original mono value duplicated)
	for i, s := range capturedPCM {
		require.InDelta(t, 0.5, s, 0.01,
			"sample %d should be 0.5 after mono→stereo upmix, got %f", i, s)
	}
}

func TestMixerUnmuteFadeInRamp(t *testing.T) {
	// When SetProgramMute transitions from true→false, the mixer should apply
	// a 5ms linear fade-in ramp to prevent uncompressed burst after
	// compressor/limiter envelopes were reset to zero during mute.
	var allCapturedPCM [][]float32

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			enc := &mockEncoderCapture{pcmRef: new([]float32)}
			allCapturedPCM = append(allCapturedPCM, *enc.pcmRef)
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Mute, then unmute to schedule fade-in ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	// Verify unmuteFadeRemaining is set
	m.mu.RLock()
	fadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Greater(t, fadeRemaining, 0, "unmute should schedule fade-in ramp")

	// Expected ramp length: sampleRate * channels * 5 / 1000 = 48000 * 2 * 5 / 1000 = 480
	expectedRampLen := 48000 * 2 * 5 / 1000
	require.Equal(t, expectedRampLen, fadeRemaining, "ramp length should be 5ms of samples")
}

func TestMixerUnmuteFadeNotScheduledOnMute(t *testing.T) {
	// SetProgramMute(true) should clear any pending fade-in ramp.
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Mute then unmute to schedule ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	m.mu.RLock()
	require.Greater(t, m.unmuteFadeRemaining, 0, "ramp should be scheduled")
	m.mu.RUnlock()

	// Re-muting should clear the ramp
	m.SetProgramMute(true)

	m.mu.RLock()
	require.Equal(t, 0, m.unmuteFadeRemaining, "mute should clear pending fade-in ramp")
	m.mu.RUnlock()
}

func TestMixerOnCutPreSeedAppliesStatelessGainOnly(t *testing.T) {
	// OnCut pre-seeds the old source's crossfade PCM with only trim * fader gain,
	// NOT the full EQ/compressor pipeline. This prevents advancing EQ/compressor
	// internal state with potentially stale audio data.
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Set trim and level to known values
	_ = m.SetTrim("cam1", 6.0)   // +6 dB trim
	_ = m.SetLevel("cam1", -6.0) // -6 dB fader

	// Set EQ to a prominent boost — if EQ were applied, the PCM would differ
	_ = m.SetEQ("cam1", 1, 1000, 12.0, 1.0, true) // +12 dB mid band

	// Pre-buffer some PCM (simulate last decoded frame)
	inputPCM := make([]float32, 2048)
	for i := range inputPCM {
		inputPCM[i] = 0.5
	}
	m.mu.Lock()
	m.lastDecodedPCM["cam1"] = inputPCM
	trimLinear := m.channels["cam1"].trimLinear
	levelLinear := m.channels["cam1"].levelLinear
	m.mu.Unlock()

	// Trigger cut
	m.OnCut("cam1", "cam2")

	// Check the pre-seeded crossfade PCM
	m.mu.RLock()
	seeded := m.crossfadePCM["cam1"]
	m.mu.RUnlock()

	require.NotNil(t, seeded, "old source should be pre-seeded")
	require.Equal(t, len(inputPCM), len(seeded))

	// Verify: each sample should be inputPCM[i] * trimLinear * levelLinear
	// (stateless gain only, no EQ boost)
	expectedGain := trimLinear * levelLinear
	for i, s := range seeded {
		expected := inputPCM[i] * expectedGain
		require.InDelta(t, expected, s, 1e-5,
			"sample %d: pre-seed should apply only trim*fader (%.4f), not EQ", i, expectedGain)
	}
}

func TestMixer_MixCycleTimingRecorded(t *testing.T) {
	// Two active channels forces mixing mode, which goes through collectMixCycleLocked.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
	cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Ingest frames from both channels to trigger a mix cycle
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")

	lastCycle := m.lastMixCycleNs.Load()
	maxCycle := m.maxMixCycleNs.Load()
	require.Greater(t, lastCycle, int64(0), "lastMixCycleNs should be > 0 after a mix cycle")
	require.Greater(t, maxCycle, int64(0), "maxMixCycleNs should be > 0")
	require.GreaterOrEqual(t, maxCycle, lastCycle, "maxMixCycleNs should be >= lastMixCycleNs")
}

func TestMixerMixPTSUsesToSourceDuringTransition(t *testing.T) {
	// During a transition, the mixer output PTS should align with the TO
	// (incoming) source's PTS, not the FROM (outgoing) source. The video
	// transition engine outputs frames with the TO source's PTS, so audio
	// must match. When the FROM source ingests last, it should NOT overwrite
	// mixPTS — only the TO source should set it.
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{0.5, 0.5, 0.5, 0.5}
	toPCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start transition crossfade from cam1 → cam2
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	m.mu.Lock()
	m.transCrossfadeAudioPos = 0.5
	m.mu.Unlock()

	// Ingest TO source (cam2) FIRST with PTS=5000
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 5000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Ingest FROM source (cam1) LAST with PTS=3000 (lower PTS)
	// The FROM source's PTS must not overwrite the TO source's PTS.
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 3000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")

	// The output PTS should be based on the TO source's PTS (5000),
	// not the FROM source's PTS (3000). advanceOutputPTS(5000) for the
	// first output initializes outputPTS to 5000.
	require.Equal(t, int64(5000), outputFrames[0].PTS,
		"output PTS should use TO source's PTS, not FROM source's")
}

func TestCrossfadePipelineOrder_MXLTapReceivesPreMuteAudio(t *testing.T) {
	// MXL output tap must receive pre-mute audio during crossfade.
	// Before this fix, the crossfade path applied mute before the MXL tap,
	// causing the MXL output to go silent during FTB even though MXL is
	// a separate output that should not be affected by program mute.
	var mxlCaptured []float32
	var mxlMu sync.Mutex

	oldPCM := []float32{0.5, 0.5, 0.5, 0.5}
	newPCM := []float32{0.3, 0.3, 0.3, 0.3}

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

	// Set raw audio sink (MXL tap)
	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mxlMu.Lock()
		mxlCaptured = append([]float32{}, pcm...)
		mxlMu.Unlock()
	})

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Set up decoders
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Mute program (FTB) then trigger crossfade
	m.SetProgramMute(true)
	m.OnCut("cam1", "cam2")

	// Ingest frames to complete crossfade
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// MXL tap should have received non-zero audio (pre-mute)
	mxlMu.Lock()
	defer mxlMu.Unlock()
	require.NotNil(t, mxlCaptured, "MXL tap should have received audio during crossfade")

	hasNonZero := false
	for _, s := range mxlCaptured {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	require.True(t, hasNonZero, "MXL tap should receive pre-mute (non-zero) audio during crossfade")
}

func TestCrossfadePipelineOrder_PeakMeteringReflectsPostMuteState(t *testing.T) {
	// Peak metering must show silence when program is muted during crossfade.
	// Before this fix, peak metering ran before the limiter, so it didn't
	// reflect the full processing chain.
	oldPCM := []float32{0.5, 0.5, 0.5, 0.5}
	newPCM := []float32{0.3, 0.3, 0.3, 0.3}

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

	// Set up decoders
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Mute program then crossfade
	m.SetProgramMute(true)
	m.OnCut("cam1", "cam2")

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Peak metering should show silence (-96 dBFS floor) since program is muted.
	// LinearToDBFS returns -96 for zero values.
	peaks := m.ProgramPeak()
	require.Equal(t, -96.0, peaks[0], "left peak should be -96 dBFS (silence) when muted during crossfade")
	require.Equal(t, -96.0, peaks[1], "right peak should be -96 dBFS (silence) when muted during crossfade")
}

func TestCrossfadePipelineOrder_UnmuteFadeInRampDuringCrossfade(t *testing.T) {
	// The unmute fade-in ramp must apply during crossfade to prevent audio pops
	// after FTB release. Before this fix, the ramp was missing from the crossfade path.
	var capturedPCM []float32

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
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Set up decoders
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.channels["cam2"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	// Mute then unmute to schedule fade-in ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	// Verify ramp is scheduled
	m.mu.RLock()
	fadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Greater(t, fadeRemaining, 0, "unmute fade-in ramp should be scheduled")

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Ingest frames to trigger crossfade processing
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// The first sample should be ramped (near zero due to fade-in start)
	require.NotNil(t, capturedPCM, "encoder should have received PCM")
	require.Greater(t, len(capturedPCM), 0, "captured PCM should not be empty")
	// First sample should be attenuated by the fade-in ramp (progress near 0)
	require.Less(t, capturedPCM[0], float32(0.5),
		"first sample should be attenuated by unmute fade-in ramp during crossfade")

	// Ramp should have been consumed (at least partially)
	m.mu.RLock()
	newFadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Less(t, newFadeRemaining, fadeRemaining,
		"unmute fade-in ramp should be consumed during crossfade")
}

func TestCrossfadePCMPipelineOrder_MXLTapReceivesPreMuteAudio(t *testing.T) {
	// Same as TestCrossfadePipelineOrder_MXLTapReceivesPreMuteAudio but for
	// the PCM (MXL) crossfade path via IngestPCM.
	var mxlCaptured []float32
	var mxlMu sync.Mutex

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	// Set raw audio sink (MXL tap)
	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		mxlMu.Lock()
		mxlCaptured = append([]float32{}, pcm...)
		mxlMu.Unlock()
	})

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Mute program (FTB) then trigger crossfade
	m.SetProgramMute(true)
	m.OnCut("cam1", "cam2")

	// Pre-seed lastDecodedPCM for cam1 so OnCut has pre-buffer
	oldPCM := []float32{0.5, 0.5, 0.5, 0.5}
	newPCM := []float32{0.3, 0.3, 0.3, 0.3}

	// Ingest PCM frames to trigger crossfade
	m.IngestPCM("cam1", oldPCM, 1000, 2)
	m.IngestPCM("cam2", newPCM, 1000, 2)

	// MXL tap should have received non-zero audio (pre-mute)
	mxlMu.Lock()
	defer mxlMu.Unlock()
	require.NotNil(t, mxlCaptured, "MXL tap should have received audio during PCM crossfade")

	hasNonZero := false
	for _, s := range mxlCaptured {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	require.True(t, hasNonZero, "MXL tap should receive pre-mute (non-zero) audio during PCM crossfade")
}

func TestCrossfadePCMPipelineOrder_UnmuteFadeInRamp(t *testing.T) {
	// The unmute fade-in ramp must apply during PCM crossfade path.
	var capturedPCM []float32

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Mute then unmute to schedule fade-in ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	// Verify ramp is scheduled
	m.mu.RLock()
	fadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Greater(t, fadeRemaining, 0, "unmute fade-in ramp should be scheduled")

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Ingest PCM frames
	m.IngestPCM("cam1", pcm, 1000, 2)
	m.IngestPCM("cam2", pcm, 1000, 2)

	// The first sample should be attenuated by the fade-in ramp
	require.NotNil(t, capturedPCM, "encoder should have received PCM")
	require.Greater(t, len(capturedPCM), 0, "captured PCM should not be empty")
	require.Less(t, capturedPCM[0], float32(0.5),
		"first sample should be attenuated by unmute fade-in ramp during PCM crossfade")

	// Ramp should have been consumed
	m.mu.RLock()
	newFadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Less(t, newFadeRemaining, fadeRemaining,
		"unmute fade-in ramp should be consumed during PCM crossfade")
}

func TestCrossfadePipelineOrder_MasterGainBeforeMute(t *testing.T) {
	// Verify that master gain is applied before program mute in the crossfade path.
	// When program is NOT muted with master at -6dB, the output should reflect
	// master gain. When program IS muted, peak metering should show silence
	// regardless of master gain setting.
	var capturedPCM []float32

	oldPCM := []float32{0.8, 0.8, 0.8, 0.8}
	newPCM := []float32{0.8, 0.8, 0.8, 0.8}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	require.NoError(t, m.SetMasterLevel(-6.0)) // -6 dB

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Trigger crossfade without muting
	m.OnCut("cam1", "cam2")

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Output should have master gain applied (samples < 0.8)
	require.NotNil(t, capturedPCM, "should have captured PCM")
	masterLinear := float32(math.Pow(10, -6.0/20.0)) // ~0.501
	for i, s := range capturedPCM {
		require.Less(t, s, float32(0.8),
			"sample %d should have master gain applied (expected < 0.8, got %f)", i, s)
		// Should be approximately oldPCM * crossfade_weight * masterLinear
		// At the first frame of a 2-frame crossfade, old source dominates
		require.Greater(t, s, float32(0.0),
			"sample %d should be positive (master gain, not muted)", i, s)
		_ = masterLinear
	}

	// Peak metering should also reflect the gain-reduced values
	peaks := m.ProgramPeak()
	require.False(t, math.IsInf(peaks[0], -1), "peak should not be -Inf when not muted")
}

// TestMixerResamplesMismatchedSampleRate verifies that frames with a different
// sample rate than the mixer are resampled (not rejected) and mixed into output.
func TestMixerResamplesMismatchedSampleRate(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough by adding a second active channel
	m.AddChannel("cam2")
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: []float32{0.5, 0.5}}
	m.channels["cam2"].decoder = &mockDecoder{samples: []float32{0.3, 0.3}}
	m.mu.Unlock()

	// Ingest a frame with mismatched sample rate (44100 vs mixer's 48000)
	mismatchedFrame := &media.AudioFrame{
		PTS:        1000,
		Data:       []byte{0xAA},
		SampleRate: 44100,
		Channels:   2,
	}
	m.IngestFrame("cam1", mismatchedFrame)

	// The frame should be resampled and accepted into the mix buffer
	m.mu.RLock()
	_, cam1InBuffer := m.mixBuffer["cam1"]
	m.mu.RUnlock()
	require.True(t, cam1InBuffer, "mismatched sample rate frame should be resampled and mixed")

	// A resampler should have been created for this channel
	m.mu.RLock()
	hasResampler := m.channels["cam1"].resampler != nil
	m.mu.RUnlock()
	require.True(t, hasResampler, "resampler should be lazy-initialized on mismatch")
}

// TestMixerAcceptsZeroSampleRate verifies that frames with SampleRate==0
// (unknown) are accepted without creating a resampler — backward compatibility.
func TestMixerAcceptsZeroSampleRate(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough
	m.AddChannel("cam2")
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: []float32{0.5, 0.5}}
	m.channels["cam2"].decoder = &mockDecoder{samples: []float32{0.3, 0.3}}
	m.mu.Unlock()

	// Ingest a frame with SampleRate==0 (unknown — no resampler should be created)
	unknownRateFrame := &media.AudioFrame{
		PTS:      1000,
		Data:     []byte{0xAA},
		Channels: 2,
	}
	m.IngestFrame("cam1", unknownRateFrame)

	m.mu.RLock()
	_, cam1InBuffer := m.mixBuffer["cam1"]
	hasResampler := m.channels["cam1"].resampler != nil
	m.mu.RUnlock()
	require.True(t, cam1InBuffer, "frame with SampleRate==0 should be accepted")
	require.False(t, hasResampler, "no resampler should be created for SampleRate==0")
}

// TestMixerResamplerCreatedOncePerChannel verifies that the resampler is
// created once on first mismatch and reused for subsequent frames.
func TestMixerResamplerCreatedOncePerChannel(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.AddChannel("cam2")
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: []float32{0.5, 0.5}}
	m.channels["cam2"].decoder = &mockDecoder{samples: []float32{0.3, 0.3}}
	m.mu.Unlock()

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 44100, Channels: 2}

	m.IngestFrame("cam1", frame)

	m.mu.RLock()
	resampler1 := m.channels["cam1"].resampler
	m.mu.RUnlock()
	require.NotNil(t, resampler1, "resampler should be created on first mismatch")

	// Second frame should reuse the same resampler
	m.IngestFrame("cam1", frame)

	m.mu.RLock()
	resampler2 := m.channels["cam1"].resampler
	m.mu.RUnlock()
	require.True(t, resampler1 == resampler2, "resampler should be reused, not recreated")
}

// TestMixerResamplerDisablesPassthrough verifies that when a source needs
// resampling, passthrough mode is disabled (can't forward raw AAC at wrong rate).
func TestMixerResamplerDisablesPassthrough(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	// Single active source at 0dB — would normally enable passthrough
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	require.True(t, m.IsPassthrough(), "should be passthrough before mismatch")

	// Inject a resampler to simulate mismatch detection
	m.mu.Lock()
	m.channels["cam1"].resampler = NewResampler(44100, 48000, 2)
	m.recalcPassthrough()
	m.mu.Unlock()

	require.False(t, m.IsPassthrough(), "passthrough must be disabled when resampler is active")
}

// TestMixerCrossfadeWithMismatchedRate verifies that crossfade works correctly
// when the incoming source has a different sample rate.
func TestMixerCrossfadeWithMismatchedRate(t *testing.T) {
	t.Parallel()

	var outputCount atomic.Int64
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { outputCount.Add(1) },
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.AddChannel("cam2")
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: []float32{0.5, 0.5}}
	m.channels["cam2"].decoder = &mockDecoder{samples: []float32{0.3, 0.3}}
	m.mu.Unlock()

	// Pre-populate cam1's PCM for crossfade pre-buffer
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 500, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Trigger crossfade from cam1 to cam2
	m.OnCut("cam1", "cam2")

	// cam2 sends a frame with mismatched rate during crossfade
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 44100, Channels: 2})

	// The crossfade should complete (not silently drop the frame)
	require.Greater(t, outputCount.Load(), int64(0), "crossfade should produce output even with mismatched rate")
}

func TestUnmuteFadeLRSymmetry(t *testing.T) {
	// The unmute fade-in ramp must apply the same gain to L and R channels.
	// fadeSamples is decremented per sample-pair so L and R get identical gain.
	var capturedPCM []float32

	// Use a buffer large enough to span the full 5ms ramp (480 samples for 48kHz stereo)
	// plus some extra to verify post-ramp samples are unattenuated.
	const numSamples = 1024 // 512 stereo pairs
	pcm := make([]float32, numSamples)
	for i := range pcm {
		pcm[i] = 1.0
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Mute then unmute to schedule fade-in ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	// Verify ramp is scheduled: 48000 * 2 * 5 / 1000 = 480
	m.mu.RLock()
	fadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Equal(t, 480, fadeRemaining)

	// Set decoders: cam1 returns 1.0, cam2 returns 0.0 (so sum = 1.0)
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	zeroPCM := make([]float32, numSamples)
	m.channels["cam2"].decoder = &mockDecoder{samples: zeroPCM}
	m.mu.Unlock()

	// Ingest frames from both channels to trigger collectMixCycleLocked
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.NotNil(t, capturedPCM, "encoder should have received PCM")
	require.GreaterOrEqual(t, len(capturedPCM), 480, "captured PCM should span the ramp")

	// Verify L/R symmetry: every stereo pair must have identical gain values.
	// With the bug, L gets progress P and R gets progress P' != P because
	// fadeSamples is decremented between L and R.
	rampPairs := 480 / 2 // 240 stereo pairs in the ramp
	for i := 0; i < rampPairs && 2*i+1 < len(capturedPCM); i++ {
		left := capturedPCM[2*i]
		right := capturedPCM[2*i+1]
		require.InDelta(t, left, right, 1e-6,
			"stereo pair %d: L=%f R=%f must have identical fade gain", i, left, right)
	}

	// Verify the ramp is monotonically increasing (each pair's gain >= previous pair's)
	for i := 1; i < rampPairs && 2*i < len(capturedPCM); i++ {
		prev := capturedPCM[2*(i-1)]
		curr := capturedPCM[2*i]
		require.GreaterOrEqual(t, curr, prev,
			"fade ramp pair %d (%f) should be >= pair %d (%f)", i, curr, i-1, prev)
	}

	// Verify that samples beyond the 480-sample ramp have full gain (not ramped).
	// The ramp is 480 samples = 240 stereo pairs. Post-ramp samples should all
	// have the same value (the limiter may attenuate from 1.0, so we compare
	// against each other rather than against 1.0). With the bug the ramp would
	// extend to sample 960, so samples 480-959 would still be attenuated.
	if len(capturedPCM) > 482 {
		// All post-ramp samples should be equal to each other (same gain applied)
		postRampVal := capturedPCM[480]
		require.Greater(t, postRampVal, float32(0.5),
			"post-ramp sample should be significant (not still ramping)")
		for i := 481; i < len(capturedPCM); i++ {
			require.InDelta(t, postRampVal, capturedPCM[i], 1e-6,
				"sample %d should have same value as other post-ramp samples, got %f vs %f",
				i, capturedPCM[i], postRampVal)
		}
		// The last ramped pair (pair 239) should have lower gain than post-ramp
		lastRampedL := capturedPCM[478]
		require.Less(t, lastRampedL, postRampVal,
			"last ramped sample (%f) should be less than post-ramp (%f)", lastRampedL, postRampVal)
	}
}

func TestAdvanceOutputPTS_33BitWraparound(t *testing.T) {
	t.Parallel()

	const ptsMask33 = int64((1 << 33) - 1) // 0x1FFFFFFFF = 8589934591

	var mu sync.Mutex
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			output = append(output, frame)
			mu.Unlock()
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// frameDuration90k = 1024 * 90000 / 48000 = 1920 ticks
	// Set outputPTS near the 33-bit boundary so the next frame advance wraps.
	nearBoundary := ptsMask33 - 500 // 500 ticks below 2^33

	// First frame: seed the outputPTS to nearBoundary
	frame1 := &media.AudioFrame{PTS: nearBoundary, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame1)

	mu.Lock()
	require.Equal(t, 1, len(output))
	require.Equal(t, nearBoundary, output[0].PTS, "first frame should seed outputPTS")
	mu.Unlock()

	// Second frame: send a backward PTS to trigger the += frameDuration90k() branch.
	// This will push outputPTS past 2^33 if not masked.
	// nearBoundary + 1920 = ptsMask33 - 500 + 1920 = ptsMask33 + 1420
	// Without masking, the PTS would be > 2^33. With masking, it wraps to 1420.
	frame2 := &media.AudioFrame{PTS: 0, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame2)

	mu.Lock()
	require.Equal(t, 2, len(output))
	resultPTS := output[1].PTS
	mu.Unlock()

	// The PTS must be within the valid 33-bit range [0, 2^33 - 1].
	// With wall-clock PTS, the exact value depends on elapsed time, but
	// the 33-bit mask must always be applied.
	require.LessOrEqual(t, resultPTS, ptsMask33,
		"output PTS %d exceeds 33-bit max %d; advanceOutputPTS must mask to 33 bits", resultPTS, ptsMask33)
	require.GreaterOrEqual(t, resultPTS, int64(0),
		"output PTS must be non-negative")
}

func TestMixer_OutputPTSDoesNotFollowSourceJumps(t *testing.T) {
	// When cutting between sources with different PTS timelines, the mixer's
	// output PTS must not jump to the new source's PTS. It should continue
	// incrementing monotonically by frameDuration. This keeps audio PTS
	// aligned with video PTS (which the frame sync also advances monotonically).
	var mu sync.Mutex
	var frames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(f *media.AudioFrame) {
			mu.Lock()
			frames = append(frames, f)
			mu.Unlock()
		},
		EncoderFactory: func(sr, ch int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: new([]float32)}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Source 1 on program, PTS starting at 100,000,000.
	m.SetActive("cam1", true)
	pcm := make([]float32, 1024*2)
	m.IngestPCM("cam1", pcm, 100_000_000, 2)
	m.IngestPCM("cam1", pcm, 100_001_920, 2)
	m.IngestPCM("cam1", pcm, 100_003_840, 2)

	mu.Lock()
	lastPTSBeforeCut := frames[len(frames)-1].PTS
	mu.Unlock()

	// Cut to source 2 which has PTS at 500,000,000 (very different timeline).
	m.SetActive("cam1", false)
	m.SetActive("cam2", true)
	m.IngestPCM("cam2", pcm, 500_000_000, 2)
	m.IngestPCM("cam2", pcm, 500_001_920, 2)

	mu.Lock()
	lastPTSAfterCut := frames[len(frames)-1].PTS
	mu.Unlock()

	// The output PTS should NOT have jumped to 500M. It should have continued
	// monotonically from ~100M. The delta should be a few frameDurations,
	// not 400 million ticks.
	delta := lastPTSAfterCut - lastPTSBeforeCut
	require.Less(t, delta, int64(100_000),
		"output PTS should not follow source PTS jumps on cut; delta=%d (expected ~few frame durations)", delta)
	require.Greater(t, delta, int64(0),
		"output PTS should advance forward, not backward")
}

func TestMixer_SilenceFillWhenNoActiveAudio(t *testing.T) {
	// When no active channel produces audio (e.g., program on a video-only
	// source), the mixer should produce silence frames to keep the browser's
	// audio pipeline fed, instead of producing nothing for the entire duration.
	var mu sync.Mutex
	var frames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(f *media.AudioFrame) {
			mu.Lock()
			frames = append(frames, f)
			mu.Unlock()
		},
		EncoderFactory: func(sr, ch int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: new([]float32)}, nil
		},
	})
	defer func() { _ = m.Close() }()

	// Add a channel and activate it, but never send any audio.
	// This simulates a video-only source on program.
	m.AddChannel("video_only")
	m.SetActive("video_only", true)

	// Wait for the deadline ticker to produce silence frames.
	// Ticker runs every 10ms. Silence fill threshold is 500ms.
	// After 1200ms we should get at least a few silence frames.
	time.Sleep(1200 * time.Millisecond)

	mu.Lock()
	count := len(frames)
	mu.Unlock()

	require.GreaterOrEqual(t, count, 2,
		"mixer should produce silence frames when active channel has no audio")
}
