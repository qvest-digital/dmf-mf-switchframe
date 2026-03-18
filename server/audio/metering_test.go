package audio_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
)

func TestPeakLevel(t *testing.T) {
	t.Parallel()
	pcm := []float32{0.5, -0.3, 0.8, -0.9, 0.1, 0.2}
	peakL, peakR := audio.PeakLevel(pcm, 2)
	require.InDelta(t, 0.8, peakL, 0.001)
	require.InDelta(t, 0.9, peakR, 0.001)
}

func TestPeakLevelDBFS(t *testing.T) {
	t.Parallel()
	pcm := []float32{1.0, 1.0}
	peakL, peakR := audio.PeakLevel(pcm, 2)
	dbL := audio.LinearToDBFS(peakL)
	dbR := audio.LinearToDBFS(peakR)
	require.InDelta(t, 0.0, dbL, 0.001, "full scale = 0 dBFS")
	require.InDelta(t, 0.0, dbR, 0.001)
}

func TestPeakLevelSilence(t *testing.T) {
	t.Parallel()
	pcm := make([]float32, 2048)
	peakL, peakR := audio.PeakLevel(pcm, 2)
	dbL := audio.LinearToDBFS(peakL)
	require.Equal(t, float64(0), peakL)
	require.Equal(t, float64(0), peakR)
	require.Equal(t, float64(-96), dbL, "silence = -96 dBFS")
}

func TestPeakLevelMono(t *testing.T) {
	t.Parallel()
	pcm := []float32{0.5, -0.7, 0.3, 0.1}
	peakL, peakR := audio.PeakLevel(pcm, 1)
	require.InDelta(t, 0.7, peakL, 0.001)
	require.Equal(t, float64(0), peakR, "mono has no right channel")
}

func TestPeakLevelEmpty(t *testing.T) {
	t.Parallel()
	peakL, peakR := audio.PeakLevel(nil, 2)
	require.Equal(t, float64(0), peakL)
	require.Equal(t, float64(0), peakR)
}

func TestLinearToDBFS(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			result := audio.LinearToDBFS(tt.linear)
			require.InDelta(t, tt.expected, result, 0.001)
		})
	}
}

func TestMixerProgramPeak(t *testing.T) {
	t.Parallel()
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })

	// Before any metering, peaks should be -96 dBFS (silence floor)
	peak := m.ProgramPeak()
	require.Equal(t, float64(-96), peak[0], "initial left peak should be -96 dBFS")
	require.Equal(t, float64(-96), peak[1], "initial right peak should be -96 dBFS")
}

func TestMixerChannelStates(t *testing.T) {
	t.Parallel()
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })

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
	t.Parallel()
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })

	snap := m.DebugSnapshot()
	require.Equal(t, "mixing", snap["mode"])
	require.Equal(t, int64(0), snap["decode_errors"])
}

func TestMixer_DebugSnapshot_PerChannelDetails(t *testing.T) {
	t.Parallel()
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Set non-default EQ on cam1 (enable band 0 with +6dB gain)
	require.NoError(t, m.SetEQ("cam1", 0, 250, 6.0, 1.0, true))

	// Set non-default compressor on cam2 (threshold -20, ratio 4:1)
	require.NoError(t, m.SetCompressor("cam2", -20, 4.0, 5.0, 100.0, 0))

	// Set audio delay on cam1
	require.NoError(t, m.SetAudioDelay("cam1", 50))

	snap := m.DebugSnapshot()
	channels, ok := snap["channels"].(map[string]any)
	require.True(t, ok, "channels should be a map")

	// Verify cam1 channel details
	cam1, ok := channels["cam1"].(map[string]any)
	require.True(t, ok, "cam1 should be a map")
	require.Equal(t, false, cam1["eq_bypassed"], "cam1 EQ has +6dB band enabled, should not be bypassed")
	require.Equal(t, true, cam1["compressor_bypassed"], "cam1 compressor is default (ratio 1:1), should be bypassed")
	require.Equal(t, 50, cam1["delay_ms"], "cam1 delay should be 50ms")
	_, hasPeakL := cam1["peak_l_dbfs"]
	require.True(t, hasPeakL, "cam1 should have peak_l_dbfs field")
	_, hasPeakR := cam1["peak_r_dbfs"]
	require.True(t, hasPeakR, "cam1 should have peak_r_dbfs field")

	// Verify cam2 channel details
	cam2, ok := channels["cam2"].(map[string]any)
	require.True(t, ok, "cam2 should be a map")
	require.Equal(t, true, cam2["eq_bypassed"], "cam2 EQ is default (all 0dB), should be bypassed")
	require.Equal(t, false, cam2["compressor_bypassed"], "cam2 compressor has ratio 4:1, should not be bypassed")
	require.Equal(t, 0, cam2["delay_ms"], "cam2 delay should be 0ms")
}

func TestMixer_DebugSnapshot_Loudness(t *testing.T) {
	t.Parallel()
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })

	snap := m.DebugSnapshot()

	// Loudness meter is always initialized by NewMixer, so loudness section should exist
	loudness, ok := snap["loudness"].(map[string]any)
	require.True(t, ok, "loudness section should exist in snapshot")

	_, hasMomentary := loudness["momentary_lufs"]
	require.True(t, hasMomentary, "loudness should have momentary_lufs")

	_, hasShortTerm := loudness["short_term_lufs"]
	require.True(t, hasShortTerm, "loudness should have short_term_lufs")

	_, hasIntegrated := loudness["integrated_lufs"]
	require.True(t, hasIntegrated, "loudness should have integrated_lufs")
}

func TestMixerMasterLevelGetter(t *testing.T) {
	t.Parallel()
	m := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })

	require.Equal(t, 0.0, m.MasterLevel())

	require.NoError(t, m.SetMasterLevel(-3.0))
	require.InDelta(t, -3.0, m.MasterLevel(), 0.001)
}
