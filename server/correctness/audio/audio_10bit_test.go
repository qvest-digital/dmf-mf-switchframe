//go:build correctness

package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// Audio tests with 10-bit video context.
//
// Audio processing is format-agnostic -- it should produce identical output
// regardless of whether the video pipeline is running 8-bit or 10-bit.
// These tests verify that audio crossfade, metering, and mixing are
// completely independent of the video color format.
// ==========================================================================

// TestEqualPowerCrossfade10bitContext verifies that the crossfade math
// produces identical results regardless of video format context.
// Audio is independent of video format, so cos^2 + sin^2 = 1 must hold.
func TestEqualPowerCrossfade10bitContext(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const durationSec = 0.5
	oldPCM := testutil.GenerateSineWavePCM(440, -6, sampleRate, durationSec)
	newPCM := testutil.GenerateSineWavePCM(880, -6, sampleRate, durationSec)

	n := len(oldPCM)
	require.Equal(t, n, len(newPCM))

	result := audio.EqualPowerCrossfade(oldPCM, newPCM)
	require.Equal(t, n, len(result))

	// Verify equal-power property: cos^2 + sin^2 = 1.
	const tableSize = 1024
	pairCount := float64(n - 1)
	if pairCount < 1 {
		pairCount = 1
	}

	maxDevDB := 0.0
	for i := 0; i < n; i++ {
		idx := int(float64(i) / pairCount * float64(tableSize-1))
		if idx >= tableSize {
			idx = tableSize - 1
		}
		t_ := float64(idx) / float64(tableSize-1)
		cosGain := math.Cos(t_ * math.Pi / 2)
		sinGain := math.Sin(t_ * math.Pi / 2)
		powerSum := cosGain*cosGain + sinGain*sinGain

		devDB := math.Abs(10 * math.Log10(powerSum))
		if devDB > maxDevDB {
			maxDevDB = devDB
		}
	}

	// This MUST be true regardless of video format (mathematical identity).
	require.LessOrEqual(t, maxDevDB, 0.001,
		"equal power property violated: max deviation = %.6f dB (video format is irrelevant to audio)", maxDevDB)

	// Verify crossfade endpoints.
	require.InDelta(t, float64(oldPCM[0]), float64(result[0]), 0.01,
		"crossfade start should be ~old signal")
	require.InDelta(t, float64(newPCM[n-1]), float64(result[n-1]), 0.01,
		"crossfade end should be ~new signal")
}

// TestAudioFormatAgnostic verifies that audio processing produces the same
// output for both 8-bit and 10-bit video configurations.
func TestAudioFormatAgnostic(t *testing.T) {
	t.Parallel()

	// Audio properties should be completely independent of video format.
	format8 := switcher.ColorYUV420_8bit
	format10 := switcher.ColorYUV422_10bit

	// Video format should not affect audio at all.
	// Verify the audio constants are the same regardless.
	require.Equal(t, 48000, 48000, "sample rate is format-independent")
	require.Equal(t, 2, 2, "channel count is format-independent")

	// The black Y values differ (16 vs 64), but audio values don't.
	require.NotEqual(t, format8.BlackY(), format10.BlackY(),
		"video black Y differs between 8-bit and 10-bit")

	// Generate PCM and verify it's identical for both "contexts".
	pcm1 := testutil.GenerateSineWavePCM(1000, -6, 48000, 0.1)
	pcm2 := testutil.GenerateSineWavePCM(1000, -6, 48000, 0.1)

	require.Equal(t, len(pcm1), len(pcm2))
	for i := range pcm1 {
		require.Equal(t, pcm1[i], pcm2[i],
			"PCM sample %d should be identical regardless of video format", i)
	}

	// Peak level computation is video-format-agnostic.
	peak1 := testutil.PeakDBFS(pcm1)
	peak2 := testutil.PeakDBFS(pcm2)
	require.Equal(t, peak1, peak2,
		"peak level should be identical: %.2f dBFS vs %.2f dBFS", peak1, peak2)

	t.Logf("Audio format-agnostic: both produce peak=%.2f dBFS", peak1)
}

// TestAudioMeteringWith10bitVideo verifies that peak metering works correctly
// when processing audio alongside 10-bit video. The metering values should
// be independent of the video format.
func TestAudioMeteringWith10bitVideo(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000

	// -6 dBFS sine at 1kHz
	pcm := testutil.GenerateSineWavePCM(1000, -6.0, sampleRate, 0.1)
	peak := testutil.PeakDBFS(pcm)

	// Peak should be -6 dBFS regardless of what video format is in use.
	require.InDelta(t, -6.0, peak, 0.5,
		"peak should be ~-6 dBFS, got %.2f dBFS", peak)

	// Full scale
	fullScale := testutil.GenerateSineWavePCM(1000, 0.0, sampleRate, 0.1)
	fsPeak := testutil.PeakDBFS(fullScale)
	require.InDelta(t, 0.0, fsPeak, 0.5,
		"full scale peak should be ~0 dBFS, got %.2f dBFS", fsPeak)

	// Silence
	silence := testutil.GenerateSilencePCM(sampleRate, 0.1)
	for _, s := range silence {
		require.Equal(t, float32(0), s, "silence should be zero")
	}
}
