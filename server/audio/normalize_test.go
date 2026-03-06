package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeInt16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input int16
		want  float32
	}{
		{"min int16 maps to -1.0", math.MinInt16, -1.0},
		{"max int16 maps to ~0.99997", math.MaxInt16, float32(math.MaxInt16) / 32768.0},
		{"zero maps to 0.0", 0, 0.0},
		{"positive 1", 1, 1.0 / 32768.0},
		{"negative 1", -1, -1.0 / 32768.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeInt16(tt.input)
			require.InDelta(t, float64(tt.want), float64(got), 1e-7)
		})
	}

	// Verify that -32768 maps to exactly -1.0 (the bug was that it mapped to -1.000031)
	result := normalizeInt16(math.MinInt16)
	require.Equal(t, float32(-1.0), result, "MinInt16 must map to exactly -1.0")

	// Verify all values are within [-1.0, 1.0]
	require.GreaterOrEqual(t, normalizeInt16(math.MinInt16), float32(-1.0))
	require.LessOrEqual(t, normalizeInt16(math.MaxInt16), float32(1.0))
}
