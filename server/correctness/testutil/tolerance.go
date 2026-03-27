package testutil

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// AssertLUFS asserts that the measured LUFS value is within the specified
// tolerance of the expected value.
func AssertLUFS(t *testing.T, got, want, toleranceLU float64) {
	t.Helper()
	diff := math.Abs(got - want)
	require.LessOrEqual(t, diff, toleranceLU,
		"LUFS out of tolerance: got %.2f, want %.2f +/- %.2f LU (diff=%.2f)",
		got, want, toleranceLU, diff)
}

// AssertDBFS asserts that the measured dBFS value is within the specified
// tolerance of the expected value.
func AssertDBFS(t *testing.T, got, want, toleranceDB float64) {
	t.Helper()
	diff := math.Abs(got - want)
	require.LessOrEqual(t, diff, toleranceDB,
		"dBFS out of tolerance: got %.2f, want %.2f +/- %.2f dB (diff=%.2f)",
		got, want, toleranceDB, diff)
}

// AssertPTSDelta checks that consecutive frame PTS values differ by the expected
// delta, within the specified tolerance (in 90kHz ticks).
func AssertPTSDelta(t *testing.T, frames []FrameInfo, expectedDelta, toleranceTicks int64) {
	t.Helper()
	require.GreaterOrEqual(t, len(frames), 2, "need at least 2 frames to check PTS delta")

	for i := 1; i < len(frames); i++ {
		delta := frames[i].PTS - frames[i-1].PTS
		diff := int64(math.Abs(float64(delta - expectedDelta)))
		require.LessOrEqual(t, diff, toleranceTicks,
			"PTS delta out of tolerance at frame %d: got %d, want %d +/- %d (actual delta=%d)",
			i, delta, expectedDelta, toleranceTicks, delta)
	}
}

// AssertMonotonic asserts that the given values are strictly increasing.
func AssertMonotonic(t *testing.T, values []int64, fieldName string) {
	t.Helper()
	for i := 1; i < len(values); i++ {
		require.Greater(t, values[i], values[i-1],
			"%s not strictly increasing at index %d: %d <= %d",
			fieldName, i, values[i], values[i-1])
	}
}

// AssertInRange asserts that value is within [min, max] inclusive.
func AssertInRange(t *testing.T, value, min, max float64, fieldName string) {
	t.Helper()
	require.GreaterOrEqual(t, value, min,
		"%s below minimum: got %f, min %f", fieldName, value, min)
	require.LessOrEqual(t, value, max,
		"%s above maximum: got %f, max %f", fieldName, value, max)
}

// PeakDBFS finds the peak sample in the PCM buffer and returns its level in dBFS.
func PeakDBFS(pcm []float32) float64 {
	if len(pcm) == 0 {
		return math.Inf(-1)
	}

	peak := float64(0)
	for _, s := range pcm {
		abs := math.Abs(float64(s))
		if abs > peak {
			peak = abs
		}
	}

	return LinearToDBFS(peak)
}

// LinearToDBFS converts a linear amplitude value to dBFS.
// Returns -Inf for zero input. Uses absolute value for negative inputs.
func LinearToDBFS(linear float64) float64 {
	abs := math.Abs(linear)
	if abs == 0 {
		return math.Inf(-1)
	}
	return 20.0 * math.Log10(abs)
}
