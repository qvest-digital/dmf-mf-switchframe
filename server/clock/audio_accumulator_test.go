package clock

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAccumulatorExactOverCycle verifies zero cumulative error over a full
// Bresenham cycle. At 48000Hz / 30000:1001 (29.97fps), one complete cycle
// is 30000 ticks, producing exactly 48000*1001 = 48,048,000 samples.
func TestAccumulatorExactOverCycle(t *testing.T) {
	acc := NewAudioAccumulator(48000, 30000, 1001)

	for i := 0; i < 30000; i++ {
		acc.SamplesForTick()
	}

	require.Equal(t, int64(48000*1001), acc.TotalSamples(),
		"after one full cycle (30000 ticks), total must be exactly 48,048,000")
}

// TestAccumulatorPerTickRange verifies that at 48000Hz/29.97fps each tick
// produces exactly 1601 or 1602 samples, never anything else.
func TestAccumulatorPerTickRange(t *testing.T) {
	acc := NewAudioAccumulator(48000, 30000, 1001)

	for i := 0; i < 100000; i++ {
		s := acc.SamplesForTick()
		if s != 1601 && s != 1602 {
			t.Fatalf("tick %d: got %d samples, want 1601 or 1602", i, s)
		}
	}
}

// TestAccumulatorExactIntegerRatio verifies that integer-ratio frame rates
// produce a constant sample count per tick with no jitter.
func TestAccumulatorExactIntegerRatio(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		fpsNum     int
		fpsDen     int
		want       int
	}{
		{"48000/25", 48000, 25, 1, 1920},
		{"48000/30", 48000, 30, 1, 1600},
		{"48000/60", 48000, 60, 1, 800},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := NewAudioAccumulator(tt.sampleRate, tt.fpsNum, tt.fpsDen)

			for i := 0; i < 10000; i++ {
				s := acc.SamplesForTick()
				require.Equal(t, tt.want, s,
					"tick %d: integer ratio should always produce exactly %d", i, tt.want)
			}
		})
	}
}

// TestAccumulatorExact5994 verifies 59.94fps (60000/1001). After 60000 ticks
// the total must be exactly 48000*1001 = 48,048,000 samples.
func TestAccumulatorExact5994(t *testing.T) {
	acc := NewAudioAccumulator(48000, 60000, 1001)

	for i := 0; i < 60000; i++ {
		s := acc.SamplesForTick()
		// 48000*1001/60000 = 800.8, so each tick is 800 or 801
		if s != 800 && s != 801 {
			t.Fatalf("tick %d: got %d samples, want 800 or 801", i, s)
		}
	}

	require.Equal(t, int64(48000*1001), acc.TotalSamples(),
		"after one full cycle (60000 ticks), total must be exactly 48,048,000")
}

// TestAccumulatorReset verifies that Reset clears state and reconfigures
// for a new sample rate / frame rate.
func TestAccumulatorReset(t *testing.T) {
	acc := NewAudioAccumulator(48000, 30000, 1001)

	// Run some ticks to build up state
	for i := 0; i < 500; i++ {
		acc.SamplesForTick()
	}
	require.NotZero(t, acc.TotalSamples())

	// Reset to 48000/25fps
	acc.Reset(48000, 25, 1)

	require.Equal(t, int64(0), acc.TotalSamples(), "total should be zero after reset")

	// Every tick should now produce exactly 1920
	for i := 0; i < 1000; i++ {
		s := acc.SamplesForTick()
		require.Equal(t, 1920, s, "tick %d after reset: want 1920", i)
	}

	require.Equal(t, int64(1920*1000), acc.TotalSamples())
}

// TestAccumulatorLongRun verifies exact count over 1,000,000 ticks at 29.97fps.
// Total = 1,000,000 * 48000 * 1001 / 30000 = 1,601,600,000.
func TestAccumulatorLongRun(t *testing.T) {
	acc := NewAudioAccumulator(48000, 30000, 1001)

	const ticks = 1_000_000
	for i := 0; i < ticks; i++ {
		acc.SamplesForTick()
	}

	// 1,000,000 * 48000 * 1001 / 30000 = 1,000,000 * 1601.6 = 1,601,600,000
	// More precisely: (1_000_000 * 48000 * 1001) / 30000
	// = 1_000_000 * 48_048_000 / 30000
	// = 1_000_000 * 1601.6
	// = 1_601_600_000
	expected := int64(1_000_000) * int64(48000) * int64(1001) / int64(30000)
	require.Equal(t, expected, acc.TotalSamples(),
		"after 1M ticks, total must be exactly 1,601,600,000")
}

// TestAccumulatorTotalSamples verifies that TotalSamples tracks correctly
// across individual calls and matches the sum of per-tick values.
func TestAccumulatorTotalSamples(t *testing.T) {
	acc := NewAudioAccumulator(48000, 30000, 1001)

	require.Equal(t, int64(0), acc.TotalSamples(), "initial total should be zero")

	var manualSum int64
	for i := 0; i < 5000; i++ {
		s := acc.SamplesForTick()
		manualSum += int64(s)
		require.Equal(t, manualSum, acc.TotalSamples(),
			"TotalSamples must match manual sum at tick %d", i)
	}
}
