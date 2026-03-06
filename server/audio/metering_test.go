package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestPeakLevel(t *testing.T) {
	pcm := []float32{0.5, -0.3, 0.8, -0.9, 0.1, 0.2}
	peakL, peakR := PeakLevel(pcm, 2)
	require.InDelta(t, 0.8, peakL, 0.001)
	require.InDelta(t, 0.9, peakR, 0.001)
}

func TestPeakLevelDBFS(t *testing.T) {
	pcm := []float32{1.0, 1.0}
	peakL, peakR := PeakLevel(pcm, 2)
	dbL := LinearToDBFS(peakL)
	dbR := LinearToDBFS(peakR)
	require.InDelta(t, 0.0, dbL, 0.001, "full scale = 0 dBFS")
	require.InDelta(t, 0.0, dbR, 0.001)
}

func TestPeakLevelSilence(t *testing.T) {
	pcm := make([]float32, 2048)
	peakL, peakR := PeakLevel(pcm, 2)
	dbL := LinearToDBFS(peakL)
	require.Equal(t, float64(0), peakL)
	require.Equal(t, float64(0), peakR)
	require.Equal(t, float64(-96), dbL, "silence = -96 dBFS")
}

func TestPeakLevelMono(t *testing.T) {
	pcm := []float32{0.5, -0.7, 0.3, 0.1}
	peakL, peakR := PeakLevel(pcm, 1)
	require.InDelta(t, 0.7, peakL, 0.001)
	require.Equal(t, float64(0), peakR, "mono has no right channel")
}

func TestPeakLevelEmpty(t *testing.T) {
	peakL, peakR := PeakLevel(nil, 2)
	require.Equal(t, float64(0), peakL)
	require.Equal(t, float64(0), peakR)
}

func TestLinearToDBFS(t *testing.T) {
	tests := []struct {
		name     string
		linear   float64
		expected float64
	}{
		{"full scale", 1.0, 0.0},
		{"half", 0.5, -6.0206},
		{"quarter", 0.25, -12.0412},
		{"silence", 0.0, -96},
		{"negative", -0.1, -96},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LinearToDBFS(tt.linear)
			require.InDelta(t, tt.expected, result, 0.001)
		})
	}
}

func TestMixerProgramPeak(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Before any metering, peaks should be -96 dBFS (silence floor)
	peak := m.ProgramPeak()
	require.Equal(t, float64(-96), peak[0], "initial left peak should be -96 dBFS")
	require.Equal(t, float64(-96), peak[1], "initial right peak should be -96 dBFS")
}

func TestMixerChannelStates(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	_ = m.SetLevel("cam1", -6.0)
	_ = m.SetMuted("cam2", true)

	states := m.ChannelStates()
	require.Len(t, states, 2)
	require.InDelta(t, -6.0, states["cam1"].Level, 0.001)
	require.False(t, states["cam1"].Muted)
	require.True(t, states["cam2"].Muted)
}

func TestMixer_DebugSnapshot(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	snap := m.DebugSnapshot()
	if snap["mode"] != "passthrough" {
		t.Errorf("expected passthrough, got %v", snap["mode"])
	}
	if snap["frames_passthrough"] != int64(0) {
		t.Errorf("expected 0, got %v", snap["frames_passthrough"])
	}
	if snap["decode_errors"] != int64(0) {
		t.Errorf("expected 0, got %v", snap["decode_errors"])
	}
}

func TestMixerMasterLevelGetter(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	require.Equal(t, 0.0, m.MasterLevel())

	m.SetMasterLevel(-3.0)
	require.InDelta(t, -3.0, m.MasterLevel(), 0.001)
}
