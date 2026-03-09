package replay

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWSOLA_Passthrough_AtFullSpeed(t *testing.T) {
	input := make([]float32, 4096)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := WSOLATimeStretch(input, 1, 48000, 1.0)
	require.Len(t, output, len(input))
	for i := range input {
		assert.InDelta(t, input[i], output[i], 1e-6)
	}
}

func TestWSOLA_HalfSpeed_DoublesLength(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := WSOLATimeStretch(input, 1, 48000, 0.5)
	expectedLen := len(input) * 2
	assert.InDelta(t, expectedLen, len(output), float64(expectedLen)*0.05)
}

func TestWSOLA_QuarterSpeed_QuadruplesLength(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := WSOLATimeStretch(input, 1, 48000, 0.25)
	expectedLen := len(input) * 4
	assert.InDelta(t, expectedLen, len(output), float64(expectedLen)*0.05)
}

func TestWSOLA_Stereo(t *testing.T) {
	input := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
		input[i*2] = v
		input[i*2+1] = v * 0.5
	}
	output := WSOLATimeStretch(input, 2, 48000, 0.5)
	expectedLen := len(input) * 2
	assert.InDelta(t, expectedLen, len(output), float64(expectedLen)*0.05)
	assert.Equal(t, 0, len(output)%2)
}

func TestWSOLA_MinimumSpeedClamped(t *testing.T) {
	input := make([]float32, 4096)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	// Speed < 0.1 should be clamped to 0.1
	output := WSOLATimeStretch(input, 1, 48000, 0.01)
	require.Greater(t, len(output), len(input))
}

func TestWSOLA_EmptyInput(t *testing.T) {
	output := WSOLATimeStretch(nil, 1, 48000, 0.5)
	assert.Empty(t, output)

	output = WSOLATimeStretch([]float32{}, 1, 48000, 0.5)
	assert.Empty(t, output)
}

func TestWSOLA_VeryShortInput(t *testing.T) {
	input := []float32{0.1, 0.2, 0.3, 0.4}
	output := WSOLATimeStretch(input, 1, 48000, 0.5)
	// Should produce something without panicking.
	require.NotNil(t, output)
}

func TestMakePeriodicHannWindow(t *testing.T) {
	w := makePeriodicHannWindow(4)
	require.Len(t, w, 4)
	// First element should be 0 (periodic: only first is zero).
	assert.InDelta(t, 0.0, w[0], 1e-10)
	// Middle elements should be positive.
	assert.Greater(t, w[1], 0.0)
	assert.Greater(t, w[2], 0.0)
	// Last element should be positive (periodic, unlike symmetric).
	assert.Greater(t, w[3], 0.0)
}

func TestFindBestOverlap(t *testing.T) {
	// Simple case: identical signals should find offset 0.
	input := make([]float32, 2048)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := make([]float32, 2048)
	copy(output, input)

	offset := findBestOverlap(input, output, 512, 512, 512, 1, 256)
	// Best overlap should be near 0 for identical signals.
	assert.InDelta(t, 0, offset, 10)
}
