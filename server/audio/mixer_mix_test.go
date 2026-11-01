package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// feedMasterChain runs sine-wave PCM through the master output chain for
// roughly the given duration, in 1024-sample blocks.
func feedMasterChain(m *Mixer, seconds float64, amplitude float64) {
	const freq = 997.0 // standard measurement tone, avoids bin alignment
	blocks := int(seconds * float64(m.sampleRate) / 1024.0)
	phase := 0.0
	inc := 2 * math.Pi * freq / float64(m.sampleRate)
	buf := make([]float32, 1024*m.numChannels)
	for b := 0; b < blocks; b++ {
		for i := 0; i < 1024; i++ {
			s := float32(amplitude * math.Sin(phase))
			phase += inc
			for c := 0; c < m.numChannels; c++ {
				buf[i*m.numChannels+c] = s
			}
		}
		m.mu.Lock()
		m.applyMasterChain(buf, 0)
		m.mu.Unlock()
	}
}

// TestApplyMasterChain_ProgramMute_LoudnessMetersSilence verifies the
// SetProgramMute contract ("the mixer produces silent output (FTB held).
// Metering reflects silence."): while FTB is held, the BS.1770-4 loudness
// meter must integrate the silence that is actually broadcast, not the live
// pre-mute program mix. Before the fix, loudness.Process ran before the
// program-mute zeroing, so momentary/short-term/integrated LUFS reported a
// live signal that never went to air.
func TestApplyMasterChain_ProgramMute_LoudnessMetersSilence(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	t.Cleanup(func() { _ = m.Close() })

	m.SetProgramMute(true)

	// Feed 1s of a loud (-6 dBFS) tone through the master chain while muted.
	feedMasterChain(m, 1.0, 0.5)

	momentary := m.MomentaryLUFS()
	require.Less(t, momentary, -60.0,
		"momentary LUFS must reflect the muted (silent) program output, got %.1f", momentary)
	shortTerm := m.ShortTermLUFS()
	require.Less(t, shortTerm, -60.0,
		"short-term LUFS must reflect the muted (silent) program output, got %.1f", shortTerm)
	integrated := m.IntegratedLUFS()
	require.Less(t, integrated, -60.0,
		"integrated LUFS must not accumulate audio that was never broadcast, got %.1f", integrated)
}

// TestApplyMasterChain_ProgramMute_RawSinkReceivesSilence verifies the MXL
// raw audio tap — a program output — receives the muted (silent) signal while
// FTB is held, matching the AAC output.
func TestApplyMasterChain_ProgramMute_RawSinkReceivesSilence(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	t.Cleanup(func() { _ = m.Close() })

	var sunk []float32
	m.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		sunk = append(sunk[:0], pcm...)
	})

	m.SetProgramMute(true)
	feedMasterChain(m, 0.1, 0.5)

	require.NotEmpty(t, sunk, "raw sink should have received PCM")
	for i, s := range sunk {
		require.Equal(t, float32(0), s,
			"sample %d: raw audio sink must receive silence while FTB is held", i)
	}
}

// TestApplyMasterChain_Unmuted_LoudnessMetersSignal is the control case: with
// program mute off, the loudness meter must report the live signal level.
func TestApplyMasterChain_Unmuted_LoudnessMetersSignal(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	t.Cleanup(func() { _ = m.Close() })

	feedMasterChain(m, 1.0, 0.5)

	momentary := m.MomentaryLUFS()
	require.Greater(t, momentary, -30.0,
		"momentary LUFS should report the live -6 dBFS tone, got %.1f", momentary)
}
