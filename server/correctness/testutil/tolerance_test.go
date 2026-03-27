package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssertLUFS(t *testing.T) {
	// Should pass when within tolerance
	AssertLUFS(t, -23.0, -23.0, 0.5)
	AssertLUFS(t, -23.3, -23.0, 0.5)
	AssertLUFS(t, -22.7, -23.0, 0.5)
}

func TestAssertDBFS(t *testing.T) {
	// Should pass when within tolerance
	AssertDBFS(t, -14.0, -14.0, 1.0)
	AssertDBFS(t, -14.5, -14.0, 1.0)
}

func TestAssertPTSDelta(t *testing.T) {
	frames := make([]FrameInfo, 5)
	for i := 0; i < 5; i++ {
		frames[i] = FrameInfo{
			PTS: int64(i) * 3000,
		}
	}

	// Should pass with correct delta
	AssertPTSDelta(t, frames, 3000, 0)
	AssertPTSDelta(t, frames, 3000, 10)
}

func TestAssertMonotonic(t *testing.T) {
	// Strictly increasing
	values := []int64{1, 2, 3, 4, 5}
	AssertMonotonic(t, values, "test_values")

	// Large gaps are fine
	values2 := []int64{100, 200, 500, 1000}
	AssertMonotonic(t, values2, "test_values_2")
}

func TestAssertInRange(t *testing.T) {
	AssertInRange(t, 5.0, 0.0, 10.0, "test_value")
	AssertInRange(t, 0.0, 0.0, 10.0, "test_min_edge")
	AssertInRange(t, 10.0, 0.0, 10.0, "test_max_edge")
}

func TestPeakDBFS(t *testing.T) {
	// Full scale sine
	pcm := GenerateSineWavePCM(1000.0, 0.0, 48000, 0.1)
	peak := PeakDBFS(pcm)
	require.InDelta(t, 0.0, peak, 0.5)

	// -6 dBFS
	pcm2 := GenerateSineWavePCM(1000.0, -6.0, 48000, 0.1)
	peak2 := PeakDBFS(pcm2)
	require.InDelta(t, -6.0, peak2, 0.5)

	// Silence
	pcm3 := GenerateSilencePCM(48000, 0.05)
	peak3 := PeakDBFS(pcm3)
	require.True(t, peak3 < -90, "silence should be very low dBFS, got %f", peak3)
}
