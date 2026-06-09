package playout

import (
	"math"
	"testing"
)

// TestPTSStepAdvance_NoLongRunDrift verifies that carrying the sub-tick PTS
// remainder keeps accumulated PTS within a fraction of a tick of true media
// time over a long run, instead of drifting linearly the way plain integer
// truncation (int64(90000/fps)) does.
func TestPTSStepAdvance_NoLongRunDrift(t *testing.T) {
	cases := []struct {
		name    string
		incTick float64 // exact per-frame increment in 90 kHz ticks
		frames  int
	}{
		{"video_29.97", 90000.0 / (30000.0 / 1001.0), 90000},  // 3003.003.., ~50 min
		{"video_59.94", 90000.0 / (60000.0 / 1001.0), 180000}, // 1501.5, ~50 min
		{"video_23.976", 90000.0 / (24000.0 / 1001.0), 72000},
		{"audio_44100", 1024.0 * 90000.0 / 44100.0, 130000}, // 2089.795..
		{"audio_48000", 1024.0 * 90000.0 / 48000.0, 140000}, // 1920 exact
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stepInt, stepFrac := ptsStep(tc.incTick)

			var pts, frac int64
			for i := 0; i < tc.frames; i++ {
				pts, frac = advancePTS(pts, frac, stepInt, stepFrac)
			}

			// True accumulated PTS in real (fractional) ticks.
			exact := tc.incTick * float64(tc.frames)
			drift := math.Abs(float64(pts) - exact)

			// The carry should keep us within ~1 tick of truth regardless of run
			// length. Plain truncation would drift by frames*(incTick-floor) — e.g.
			// hundreds of ticks here — so 1.0 is a tight, meaningful bound.
			if drift > 1.0 {
				t.Errorf("%s: pts=%d exact=%.3f drift=%.3f ticks (carry not holding)",
					tc.name, pts, exact, drift)
			}

			// Sanity: the carried remainder stays bounded in [0, scale).
			if frac < 0 || frac >= ptsFracScale {
				t.Errorf("%s: carried frac %d out of range [0,%d)", tc.name, frac, ptsFracScale)
			}
		})
	}
}

// TestPTSStep_HalfTickRateExact pins the 59.94 case: the exact step is 1501.5
// ticks, so two consecutive frames must advance by exactly 3003 ticks (1501 +
// 1502). Plain truncation to 1501/frame would yield 3002 and lose a tick every
// other frame.
func TestPTSStep_HalfTickRateExact(t *testing.T) {
	stepInt, stepFrac := ptsStep(90000.0 / (60000.0 / 1001.0)) // 1501.5

	var pts, frac int64
	pts, frac = advancePTS(pts, frac, stepInt, stepFrac)
	pts, frac = advancePTS(pts, frac, stepInt, stepFrac)

	if pts != 3003 {
		t.Fatalf("59.94 two-frame PTS = %d, want 3003 (truncation bug yields 3002)", pts)
	}
	_ = frac
}

// TestPTSStep_IntegerRateNoFraction confirms exact integer rates (e.g. 30 fps =
// 3000 ticks) carry no fractional remainder and never drift.
func TestPTSStep_IntegerRateNoFraction(t *testing.T) {
	stepInt, stepFrac := ptsStep(90000.0 / 30.0) // exactly 3000
	if stepInt != 3000 || stepFrac != 0 {
		t.Fatalf("30 fps step = (%d, %d), want (3000, 0)", stepInt, stepFrac)
	}

	var pts, frac int64
	for i := 0; i < 10000; i++ {
		pts, frac = advancePTS(pts, frac, stepInt, stepFrac)
	}
	if pts != 3000*10000 || frac != 0 {
		t.Fatalf("30 fps after 10000 frames = (%d, %d), want (%d, 0)", pts, frac, 3000*10000)
	}
}

// TestPTSStep_ZeroAndNegative guards the degenerate inputs.
func TestPTSStep_ZeroAndNegative(t *testing.T) {
	if si, sf := ptsStep(0); si != 0 || sf != 0 {
		t.Errorf("ptsStep(0) = (%d,%d), want (0,0)", si, sf)
	}
	if si, sf := ptsStep(-5); si != 0 || sf != 0 {
		t.Errorf("ptsStep(-5) = (%d,%d), want (0,0)", si, sf)
	}
}
