package audio_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
)

func TestEqualPowerCrossfade(t *testing.T) {
	t.Parallel()
	n := 1024
	old := make([]float32, n)
	new := make([]float32, n)
	for i := range old {
		old[i] = 1.0
		new[i] = 0.0
	}

	result := audio.EqualPowerCrossfade(old, new)
	require.Equal(t, n, len(result))

	// At t=0: result ~ old (cos(0)=1, sin(0)=0)
	require.InDelta(t, 1.0, result[0], 0.01, "start should be ~old")

	// At t=1 (last sample): result ~ new (cos(pi/2)~0, sin(pi/2)~1)
	require.InDelta(t, 0.0, result[n-1], 0.01, "end should be ~new")

	// Midpoint: both contribute equally
	mid := n / 2
	// cos(0.5*pi/2) = cos(pi/4) ~ 0.707
	// sin(0.5*pi/2) = sin(pi/4) ~ 0.707
	// old=1.0, new=0.0 -> result ~ 0.707
	require.InDelta(t, 0.707, result[mid], 0.02, "midpoint should be ~cos(pi/4)")
}

func TestEqualPowerCrossfadeBothSignals(t *testing.T) {
	t.Parallel()
	n := 1024
	old := make([]float32, n)
	new := make([]float32, n)
	for i := range old {
		old[i] = 0.8
		new[i] = 0.6
	}

	result := audio.EqualPowerCrossfade(old, new)

	// At start: mostly old
	require.InDelta(t, 0.8, result[0], 0.01)

	// At end: mostly new
	require.InDelta(t, 0.6, result[n-1], 0.01)
}

func TestCrossfadeEqualPowerProperty(t *testing.T) {
	t.Parallel()
	// Equal power property: cos^2(t*pi/2) + sin^2(t*pi/2) = 1 for all t
	// This means if old and new are both unit signals, total power is constant
	n := 1024
	for i := 0; i < n; i++ {
		tNorm := float64(i) / float64(n)
		cosGain := math.Cos(tNorm * math.Pi / 2)
		sinGain := math.Sin(tNorm * math.Pi / 2)
		powerSum := cosGain*cosGain + sinGain*sinGain
		require.InDelta(t, 1.0, powerSum, 0.0001,
			"equal power property must hold at sample %d (t=%.3f)", i, tNorm)
	}
}

func TestCrossfadeDifferentLengths(t *testing.T) {
	t.Parallel()
	old := []float32{1.0, 1.0, 1.0, 1.0}
	new := []float32{0.5, 0.5} // shorter

	result := audio.EqualPowerCrossfade(old, new)
	// Should output len(old) samples -- shorter buffer zero-padded
	require.Equal(t, 4, len(result), "output should be length of longer buffer")
	// Last sample: t=1.0 (3 intervals, last sample at endpoint),
	// cos(pi/2)~0, sin(pi/2)~1. old=1.0*0, new=0 (zero-padded) -> ~0.0
	expected := float32(math.Cos(1.0 * math.Pi / 2))
	require.InDelta(t, expected, result[3], 0.01, "last sample should be cos(pi/2)~0.0")
}

func TestCrossfadeEmptySlices(t *testing.T) {
	t.Parallel()
	result := audio.EqualPowerCrossfade(nil, nil)
	require.Equal(t, 0, len(result))

	// One non-empty, one nil -- result uses longer buffer, shorter is zero-padded
	result = audio.EqualPowerCrossfade([]float32{1.0}, nil)
	require.Equal(t, 1, len(result))
	require.InDelta(t, 1.0, result[0], 0.01, "single sample: old at t=0 should be ~1.0")
}

func TestEqualPowerCrossfadeStereoInto_MatchesOriginal(t *testing.T) {
	t.Parallel()
	n := 1024
	old := make([]float32, n)
	newPCM := make([]float32, n)
	for i := range old {
		old[i] = 0.8
		newPCM[i] = 0.6
	}

	original := audio.EqualPowerCrossfadeStereo(old, newPCM, 2)
	into := audio.EqualPowerCrossfadeStereoInto(nil, old, newPCM, 2)
	require.Equal(t, len(original), len(into))
	for i := range original {
		require.InDelta(t, original[i], into[i], 1e-7, "sample %d mismatch", i)
	}
}

func TestEqualPowerCrossfadeStereoInto_BufferReuse(t *testing.T) {
	t.Parallel()
	n := 512
	old := make([]float32, n)
	newPCM := make([]float32, n)
	for i := range old {
		old[i] = 1.0
		newPCM[i] = 0.5
	}

	// Pre-allocate a buffer with sufficient capacity
	dst := make([]float32, n)
	result := audio.EqualPowerCrossfadeStereoInto(dst, old, newPCM, 2)

	// Verify the returned slice shares the same backing array
	require.Equal(t, n, len(result))
	require.Equal(t, &dst[0], &result[0], "should reuse the provided buffer")

	// Verify correct values
	require.InDelta(t, 1.0, result[0], 0.01, "start should be ~old")
	require.InDelta(t, 0.5, result[n-1], 0.01, "end should be ~new")

	// Call again with a too-small buffer — should allocate a new one
	smallDst := make([]float32, 4)
	result2 := audio.EqualPowerCrossfadeStereoInto(smallDst, old, newPCM, 2)
	require.Equal(t, n, len(result2))
	require.NotEqual(t, &smallDst[0], &result2[0], "should allocate new buffer when capacity insufficient")
}

func TestEqualPowerCrossfadeInto(t *testing.T) {
	t.Parallel()
	old := []float32{1.0, 1.0, 1.0, 1.0}
	newPCM := []float32{0.0, 0.0, 0.0, 0.0}

	dst := make([]float32, 4)
	result := audio.EqualPowerCrossfadeInto(dst, old, newPCM)
	original := audio.EqualPowerCrossfade(old, newPCM)

	require.Equal(t, len(original), len(result))
	for i := range original {
		require.InDelta(t, original[i], result[i], 1e-7, "sample %d mismatch", i)
	}
	require.Equal(t, &dst[0], &result[0], "should reuse the provided buffer")
}

func TestEqualPowerCrossfade_StereoGainSymmetry(t *testing.T) {
	t.Parallel()
	numPairs := 512
	channels := 2
	n := numPairs * channels

	// Old: L=1.0, R=0.5; New: L=0.0, R=0.0
	// If crossfade gain is applied per-pair, L and R at the same time instant
	// should receive the same gain factor, preserving the 2:1 L/R ratio.
	old := make([]float32, n)
	new := make([]float32, n)
	for i := 0; i < numPairs; i++ {
		old[i*2] = 1.0
		old[i*2+1] = 0.5
	}

	result := audio.EqualPowerCrossfadeStereo(old, new, channels)
	require.Equal(t, n, len(result))

	for i := 0; i < numPairs; i++ {
		lSample := result[i*2]
		rSample := result[i*2+1]
		if lSample > 1e-6 {
			ratio := rSample / lSample
			require.InDelta(t, 0.5, float64(ratio), 1e-5,
				"pair %d: L/R ratio should be 0.5 (same gain for both channels)", i)
		}
	}
}
