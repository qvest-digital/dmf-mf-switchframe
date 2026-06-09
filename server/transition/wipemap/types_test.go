package wipemap

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWipeConfigMultiplier verifies Multiplier() clamps the JSON-decoded
// Multiply field into the safe [1, 64] range. This guards the blend path
// against unvalidated input that would otherwise overflow int(g)*Multiply or
// (when a multiple of 256) collapse the gradient to a degenerate hard cut.
func TestWipeConfigMultiplier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		multiply int
		want     int
	}{
		{"negative", -5, 1},
		{"zero", 0, 1},
		{"one", 1, 1},
		{"in-range", 3, 3},
		{"at-max", maxMultiply, maxMultiply},
		{"above-max", maxMultiply + 1, maxMultiply},
		{"multiple-of-256", 256, maxMultiply},        // would collapse gradient to 0
		{"max-int", math.MaxInt, maxMultiply},        // would overflow int(g)*Multiply
		{"large-negative", math.MinInt, 1},           // negative -> no repeat
		{"big-multiple-of-256", 256 * 1000, maxMultiply},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := WipeConfig{Multiply: tc.multiply}.Multiplier()
			require.Equal(t, tc.want, got)
			// Invariant: result is always a safe repeat count.
			require.GreaterOrEqual(t, got, 1)
			require.LessOrEqual(t, got, maxMultiply)
			// Invariant: with got in [1,64], the per-byte transform
			// byte((255*got)%256) can never collapse to 0 (the degenerate
			// hard-cut failure mode), since 255*got % 256 == 0 only when got
			// is a multiple of 256.
			require.NotEqual(t, 0, (255*got)%256, "max gradient byte must not collapse to 0")
		})
	}
}
