package audio

import (
	"math"
	"sync"
	"testing"

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Ingest a frame — should passthrough (single active source, 0dB, not muted)
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA, 0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	mu.Lock()
	require.Equal(t, 1, len(output))
	require.Equal(t, []byte{0xAA, 0xBB}, output[0].Data, "passthrough should forward raw AAC")
	require.Equal(t, int64(1000), output[0].PTS)
	mu.Unlock()
}

func TestMixerIgnoresInactiveChannel(t *testing.T) {
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { output = append(output, frame) },
	})
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetMuted("cam1", true)

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
	defer m.Close()

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
	m.SetLevel("cam1", -6.0)
	require.False(t, m.IsPassthrough())

	// Reset level
	m.SetLevel("cam1", 0.0)
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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			// Each call returns a new decoder; we'll set samples per-channel below
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer m.Close()

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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetMasterLevel(-6.0)
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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.True(t, m.IsPassthrough())

	m.SetMasterLevel(-3.0)
	require.False(t, m.IsPassthrough(), "non-zero master level should disable passthrough")

	m.SetMasterLevel(0.0)
	require.True(t, m.IsPassthrough(), "zero master level should enable passthrough")
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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetLevel("cam1", -6.0)
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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	m.SetMuted("cam2", true) // cam2 muted — still two active channels, but muted frames are dropped

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Only cam1 contributes (cam2 muted so not expected), mixer should output
	require.Equal(t, 1, len(outputFrames))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.5, s, 0.001, "sample %d: only cam1 should contribute", i)
	}
}
