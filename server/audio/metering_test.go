package audio

import (
	"math"
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
	require.True(t, math.IsInf(dbL, -1), "silence = -inf dBFS")
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
		{"silence", 0.0, math.Inf(-1)},
		{"negative", -0.1, math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := LinearToDBFS(tt.linear)
			if math.IsInf(tt.expected, -1) {
				require.True(t, math.IsInf(result, -1))
			} else {
				require.InDelta(t, tt.expected, result, 0.001)
			}
		})
	}
}

func TestMixerProgramPeak(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer m.Close()

	// Before any metering, peaks should be -inf dBFS
	peak := m.ProgramPeak()
	require.True(t, math.IsInf(peak[0], -1), "initial left peak should be -inf")
	require.True(t, math.IsInf(peak[1], -1), "initial right peak should be -inf")

	// Set program peaks directly
	m.SetProgramPeak(0.5, 0.8)
	peak = m.ProgramPeak()
	require.InDelta(t, LinearToDBFS(0.5), peak[0], 0.001)
	require.InDelta(t, LinearToDBFS(0.8), peak[1], 0.001)
}

func TestMixerChannelStates(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetLevel("cam1", -6.0)
	m.SetMuted("cam2", true)

	states := m.ChannelStates()
	require.Len(t, states, 2)
	require.InDelta(t, -6.0, states["cam1"].Level, 0.001)
	require.False(t, states["cam1"].Muted)
	require.True(t, states["cam2"].Muted)
}

func TestMixerMasterLevelGetter(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer m.Close()

	require.Equal(t, 0.0, m.MasterLevel())

	m.SetMasterLevel(-3.0)
	require.InDelta(t, -3.0, m.MasterLevel(), 0.001)
}
