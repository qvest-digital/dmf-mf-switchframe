package demo

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/stinger"
)

func TestGenerateWAV(t *testing.T) {
	// Create known PCM, generate WAV, round-trip through stinger.ParseWAV
	pcm := make([]float32, 960) // 10ms at 48kHz stereo
	for i := range pcm {
		pcm[i] = 0.5
	}
	wav := GenerateWAV(pcm, 48000, 2)
	require.True(t, len(wav) > 44)

	audio, sr, ch, err := stinger.ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sr)
	require.Equal(t, 2, ch)
	require.Len(t, audio, 960)
	// Values should be approximately 0.5 (int16 quantization)
	require.InDelta(t, 0.5, audio[0], 0.001)
}

func TestGenerateWAV_Empty(t *testing.T) {
	wav := GenerateWAV(nil, 48000, 2)
	require.Equal(t, 44, len(wav)) // header only, no data

	audio, sr, ch, err := stinger.ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sr)
	require.Equal(t, 2, ch)
	require.Len(t, audio, 0)
}

func TestGenerateWAV_Clamping(t *testing.T) {
	// Values outside [-1, 1] should be clamped
	pcm := []float32{2.0, -2.0, 0.0, 1.5}
	wav := GenerateWAV(pcm, 44100, 1)

	audio, _, _, err := stinger.ParseWAV(wav)
	require.NoError(t, err)
	require.Len(t, audio, 4)
	// Clamped values
	require.InDelta(t, 1.0, audio[0], 0.001)
	require.InDelta(t, -1.0, audio[1], 0.001)
	require.InDelta(t, 0.0, audio[2], 0.001)
	require.InDelta(t, 1.0, audio[3], 0.001)
}

func TestSynthesizeWhoosh(t *testing.T) {
	pcm := SynthesizeWhoosh(48000, 2, 1.0)
	require.Equal(t, 96000, len(pcm)) // 48000 * 2 channels
	hasNonZero := false
	for _, s := range pcm {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	require.True(t, hasNonZero)
	// No clipping
	for _, s := range pcm {
		require.LessOrEqual(t, s, float32(1.0))
		require.GreaterOrEqual(t, s, float32(-1.0))
	}
}

func TestSynthesizeWhoosh_Mono(t *testing.T) {
	pcm := SynthesizeWhoosh(48000, 1, 0.5)
	require.Equal(t, 24000, len(pcm)) // 48000 * 1 * 0.5
}

func TestSynthesizeSlam(t *testing.T) {
	pcm := SynthesizeSlam(48000, 2, 1.0)
	require.Equal(t, 96000, len(pcm))
	// Peak should be in first 20%
	var peakIdx int
	var peakVal float32
	for i, s := range pcm {
		abs := s
		if abs < 0 {
			abs = -abs
		}
		if abs > peakVal {
			peakVal = abs
			peakIdx = i
		}
	}
	require.Less(t, peakIdx, len(pcm)/5)
}

func TestSynthesizeSlam_NoClipping(t *testing.T) {
	pcm := SynthesizeSlam(48000, 2, 1.0)
	for _, s := range pcm {
		require.LessOrEqual(t, s, float32(1.0))
		require.GreaterOrEqual(t, s, float32(-1.0))
	}
}

func TestSynthesizeMusical(t *testing.T) {
	pcm := SynthesizeMusical(48000, 2, 1.0)
	require.Equal(t, 96000, len(pcm))
	hasNonZero := false
	for _, s := range pcm {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	require.True(t, hasNonZero)
}

func TestSynthesizeMusical_NoClipping(t *testing.T) {
	pcm := SynthesizeMusical(48000, 2, 1.0)
	for _, s := range pcm {
		require.LessOrEqual(t, s, float32(1.0))
		require.GreaterOrEqual(t, s, float32(-1.0))
	}
}

func TestSynthesizeWhoosh_RoundTrip(t *testing.T) {
	// Generate whoosh, convert to WAV, parse back
	pcm := SynthesizeWhoosh(48000, 2, 0.5)
	wav := GenerateWAV(pcm, 48000, 2)

	audio, sr, ch, err := stinger.ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sr)
	require.Equal(t, 2, ch)
	require.Equal(t, len(pcm), len(audio))
}

func TestSynthesizeSlam_RoundTrip(t *testing.T) {
	pcm := SynthesizeSlam(48000, 2, 0.5)
	wav := GenerateWAV(pcm, 48000, 2)

	audio, sr, ch, err := stinger.ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sr)
	require.Equal(t, 2, ch)
	require.Equal(t, len(pcm), len(audio))
}

func TestSynthesizeMusical_RoundTrip(t *testing.T) {
	pcm := SynthesizeMusical(48000, 2, 0.5)
	wav := GenerateWAV(pcm, 48000, 2)

	audio, sr, ch, err := stinger.ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sr)
	require.Equal(t, 2, ch)
	require.Equal(t, len(pcm), len(audio))
}

func TestSynthesizers_ChannelDuplication(t *testing.T) {
	// For stereo, left and right channels should be identical (mono content duplicated)
	for _, tc := range []struct {
		name string
		fn   func(int, int, float64) []float32
	}{
		{"whoosh", SynthesizeWhoosh},
		{"slam", SynthesizeSlam},
		{"musical", SynthesizeMusical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm := tc.fn(48000, 2, 0.1)
			require.True(t, len(pcm) > 0)
			// Check that interleaved L/R pairs are identical
			for i := 0; i < len(pcm)-1; i += 2 {
				require.Equal(t, pcm[i], pcm[i+1],
					"sample %d: left=%f right=%f", i/2, pcm[i], pcm[i+1])
			}
		})
	}
}
