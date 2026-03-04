package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEqualPowerCrossfade(t *testing.T) {
	n := 1024
	old := make([]float32, n)
	new := make([]float32, n)
	for i := range old {
		old[i] = 1.0
		new[i] = 0.0
	}

	result := EqualPowerCrossfade(old, new)
	require.Equal(t, n, len(result))

	// At t=0: result ≈ old (cos(0)=1, sin(0)=0)
	require.InDelta(t, 1.0, result[0], 0.01, "start should be ~old")

	// At t=1 (last sample): result ≈ new (cos(π/2)≈0, sin(π/2)≈1)
	require.InDelta(t, 0.0, result[n-1], 0.01, "end should be ~new")

	// Midpoint: both contribute equally
	mid := n / 2
	// cos(0.5·π/2) = cos(π/4) ≈ 0.707
	// sin(0.5·π/2) = sin(π/4) ≈ 0.707
	// old=1.0, new=0.0 → result ≈ 0.707
	require.InDelta(t, 0.707, result[mid], 0.02, "midpoint should be ~cos(π/4)")
}

func TestEqualPowerCrossfadeBothSignals(t *testing.T) {
	n := 1024
	old := make([]float32, n)
	new := make([]float32, n)
	for i := range old {
		old[i] = 0.8
		new[i] = 0.6
	}

	result := EqualPowerCrossfade(old, new)

	// At start: mostly old
	require.InDelta(t, 0.8, result[0], 0.01)

	// At end: mostly new
	require.InDelta(t, 0.6, result[n-1], 0.01)
}

func TestCrossfadeEqualPowerProperty(t *testing.T) {
	// Equal power property: cos²(t·π/2) + sin²(t·π/2) = 1 for all t
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
	old := []float32{1.0, 1.0, 1.0, 1.0}
	new := []float32{0.5, 0.5} // shorter

	result := EqualPowerCrossfade(old, new)
	// Should use min length
	require.Equal(t, 2, len(result))
}

func TestCrossfadeEmptySlices(t *testing.T) {
	result := EqualPowerCrossfade(nil, nil)
	require.Equal(t, 0, len(result))

	result = EqualPowerCrossfade([]float32{1.0}, nil)
	require.Equal(t, 0, len(result))
}
