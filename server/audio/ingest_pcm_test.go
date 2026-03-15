package audio

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestIngestPCM_ProcessesThroughPipeline(t *testing.T) {
	t.Parallel()

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
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// Silence PCM: 1024 samples * 2 channels = 2048 float32 values
	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	mu.Lock()
	require.Equal(t, 1, len(outputFrames), "should produce one output frame from PCM ingest")
	require.Equal(t, []byte{0xFF}, outputFrames[0].Data, "output frame should have encoded data")
	require.Equal(t, 48000, outputFrames[0].SampleRate)
	require.Equal(t, 2, outputFrames[0].Channels)
	mu.Unlock()
}

func TestIngestPCM_AppliesTrim(t *testing.T) {
	t.Parallel()

	var capturedPCM []float32

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 4)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// Set trim to +6dB (~2x gain)
	err := m.SetTrim("mxl1", 6.0)
	require.NoError(t, err)

	// PCM with known amplitude (0.25 on all samples)
	pcm := []float32{0.25, 0.25, 0.25, 0.25}
	m.IngestPCM("mxl1", pcm, 1000, 2)

	// Trim of +6dB ≈ 1.995x gain, so 0.25 * ~2.0 ≈ 0.5
	expectedGain := DBToLinear(6.0)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.25*expectedGain, float64(s), 0.01,
			"sample %d should have trim gain applied", i)
	}
}

func TestIngestPCM_PeakMetering(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 4)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// PCM with known peak values: L=0.75, R=0.5 (interleaved stereo)
	pcm := []float32{0.75, 0.5, 0.3, 0.4}
	m.IngestPCM("mxl1", pcm, 1000, 2)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	peakL := ch.peakL
	peakR := ch.peakR
	m.mu.RUnlock()

	require.InDelta(t, 0.75, peakL, 0.001, "peakL should reflect left channel peak")
	require.InDelta(t, 0.5, peakR, 0.001, "peakR should reflect right channel peak")
}

func TestIngestPCM_StoresForCrossfade(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 4)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	pcm := []float32{0.1, 0.2, 0.3, 0.4}
	m.IngestPCM("mxl1", pcm, 1000, 2)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	storedBuf := ch.storedBuf
	lastDecoded := m.lastDecodedPCM["mxl1"]
	m.mu.RUnlock()

	require.Equal(t, 4, len(storedBuf), "storedBuf should have PCM data")
	require.InDelta(t, 0.1, storedBuf[0], 0.001)
	require.InDelta(t, 0.2, storedBuf[1], 0.001)
	require.InDelta(t, 0.3, storedBuf[2], 0.001)
	require.InDelta(t, 0.4, storedBuf[3], 0.001)

	require.Equal(t, 4, len(lastDecoded), "lastDecodedPCM should have PCM data")
	// storedBuf and lastDecodedPCM should reference the same backing data
	require.Equal(t, storedBuf, lastDecoded)
}

func TestIngestPCM_MutedChannelSkipped(t *testing.T) {
	t.Parallel()

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
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)
	_ = m.SetMuted("mxl1", true)

	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	mu.Lock()
	require.Equal(t, 0, len(outputFrames), "muted channel should produce no output")
	mu.Unlock()
}

func TestIngestPCM_PassthroughRecalculatedAfterDisable(t *testing.T) {
	t.Parallel()

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

	// Set up one AAC source in passthrough-eligible state.
	m.AddChannel("aac1")
	m.SetActive("aac1", true)

	// Also add a PCM source.
	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// Passthrough should be false (two active sources).
	m.mu.RLock()
	require.False(t, m.passthrough, "two active sources should not be passthrough")
	m.mu.RUnlock()

	// IngestPCM forces passthrough=false.
	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	m.mu.RLock()
	require.False(t, m.passthrough, "passthrough should be false during PCM ingest")
	m.mu.RUnlock()

	// Now deactivate the PCM source, leaving only the AAC source.
	// This triggers recalcPassthrough, which should re-enable passthrough.
	m.SetActive("mxl1", false)

	m.mu.RLock()
	require.True(t, m.passthrough, "passthrough should be re-enabled with single AAC source at 0dB")
	m.mu.RUnlock()

	// Now send another PCM frame — this should disable passthrough again.
	// After IngestPCM, the passthrough flag should be properly computed,
	// not just hard-coded to false.
	m.SetActive("mxl1", true)

	// With 2 active sources, passthrough should be false anyway.
	m.mu.RLock()
	require.False(t, m.passthrough, "two active sources should not be passthrough")
	m.mu.RUnlock()

	// Deactivate PCM source again — passthrough should recover.
	m.SetActive("mxl1", false)
	m.mu.RLock()
	require.True(t, m.passthrough, "passthrough should recover after PCM source deactivated")
	m.mu.RUnlock()
}

// TestIngestPCM_PassthroughRecalcOnSinglePCMSource verifies that when a single
// PCM source is the only active source, passthrough remains false (PCM can't use
// passthrough which forwards raw AAC bytes), and mode transitions are logged.
func TestIngestPCM_PassthroughRecalcOnSinglePCMSource(t *testing.T) {
	t.Parallel()

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

	// Only one PCM source, passthrough starts true.
	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	m.mu.RLock()
	wasPassthrough := m.passthrough
	m.mu.RUnlock()
	require.True(t, wasPassthrough, "passthrough should start true for single active source at 0dB")

	// IngestPCM should disable passthrough via recalcPassthrough() and log
	// the mode transition.
	initialTransitions := m.modeTransitions.Load()

	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	m.mu.RLock()
	require.False(t, m.passthrough, "passthrough must be false when PCM source is active")
	m.mu.RUnlock()

	// Verify that a mode transition was logged (recalcPassthrough logs transitions).
	require.Greater(t, m.modeTransitions.Load(), initialTransitions,
		"recalcPassthrough should have logged a mode transition")
}

func TestIngestPCM_InactiveChannelSkipped(t *testing.T) {
	t.Parallel()

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
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	// Do NOT activate — channel is inactive

	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	mu.Lock()
	require.Equal(t, 0, len(outputFrames), "inactive channel should produce no output")
	mu.Unlock()
}
