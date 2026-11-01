package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestPhaseInvert_NegatesSamples(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Enable phase invert
	require.NoError(t, m.SetPhaseInvert("cam1", true))

	// Ingest known PCM values through IngestPCM
	pcm := []float32{0.5, -0.3, 1.0, -1.0, 0.0, 0.25}
	m.IngestPCM("cam1", pcm, 1000, 2)

	// Read back from the ring buffer. The samples should be negated
	// (phase inverted) after trim (trim is 1.0 by default).
	m.mu.RLock()
	ch := m.channels["cam1"]
	got := ch.ringBuf.Pop(6)
	m.mu.RUnlock()

	require.Len(t, got, 6, "should pop all 6 samples from ring buffer")
	require.InDelta(t, -0.5, float64(got[0]), 1e-6)
	require.InDelta(t, 0.3, float64(got[1]), 1e-6)
	require.InDelta(t, -1.0, float64(got[2]), 1e-6)
	require.InDelta(t, 1.0, float64(got[3]), 1e-6)
	require.InDelta(t, 0.0, float64(got[4]), 1e-6)
	require.InDelta(t, -0.25, float64(got[5]), 1e-6)
}

func TestPhaseInvert_ToggleOff_RestoresNormal(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Enable then disable phase invert
	require.NoError(t, m.SetPhaseInvert("cam1", true))
	require.NoError(t, m.SetPhaseInvert("cam1", false))

	// Ingest PCM -- should pass through normally (no negation)
	pcm := []float32{0.5, -0.3}
	m.IngestPCM("cam1", pcm, 2000, 2)

	m.mu.RLock()
	ch := m.channels["cam1"]
	got := ch.ringBuf.Pop(2)
	m.mu.RUnlock()

	require.Len(t, got, 2)
	require.InDelta(t, 0.5, float64(got[0]), 1e-6)
	require.InDelta(t, -0.3, float64(got[1]), 1e-6)
}

func TestPhaseInvert_UnknownSource_ReturnsError(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	err := m.SetPhaseInvert("nonexistent", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
}

func TestPhaseInvert_ChannelStates_IncludesField(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// cam1: phase invert on
	require.NoError(t, m.SetPhaseInvert("cam1", true))
	// cam2: phase invert off (default)

	states := m.ChannelStates()
	require.True(t, states["cam1"].PhaseInvert, "cam1 should have PhaseInvert=true")
	require.False(t, states["cam2"].PhaseInvert, "cam2 should have PhaseInvert=false by default")
}

func TestPhaseInvert_IngestFrame_NegatesSamples(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			// Return known PCM values from the decoder
			return &mockDecoder{samples: []float32{0.5, -0.3, 1.0, -1.0}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Enable phase invert
	require.NoError(t, m.SetPhaseInvert("cam1", true))

	// Ingest an AAC frame (decoder mock returns known PCM)
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Read from ring buffer -- samples should be negated
	m.mu.RLock()
	ch := m.channels["cam1"]
	got := ch.ringBuf.Pop(4)
	m.mu.RUnlock()

	require.Len(t, got, 4, "should pop 4 samples from ring buffer")
	require.InDelta(t, -0.5, float64(got[0]), 1e-6)
	require.InDelta(t, 0.3, float64(got[1]), 1e-6)
	require.InDelta(t, -1.0, float64(got[2]), 1e-6)
	require.InDelta(t, 1.0, float64(got[3]), 1e-6)
}

func TestPhaseInvert_DebugSnapshot_IncludesField(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	require.NoError(t, m.SetPhaseInvert("cam1", true))

	snap := m.DebugSnapshot()
	channels, ok := snap["channels"].(map[string]any)
	require.True(t, ok)
	cam1, ok := channels["cam1"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, cam1["phase_invert"])
}
