package video

import "testing"

func TestIsNominalPTSDelta(t *testing.T) {
	// Integer frame rates: exact 90_000 / fps.
	// Rational frame rates (NTSC): ±1 tick tolerance for Bresenham remainder.
	cases := []struct {
		delta int64
		want  bool
		label string
	}{
		{3000, true, "30fps"},
		{3003, true, "29.97fps (30000/1001)"},
		{3004, true, "29.97fps +1 (Bresenham)"},
		{3600, true, "25fps"},
		{3750, true, "24fps"},
		{3753, true, "23.976fps (24000/1001)"},
		{3754, true, "23.976fps +1 (Bresenham)"},
		{1800, true, "50fps"},
		{1500, true, "60fps"},
		{1501, true, "59.94fps (60000/1001)"},
		{1502, true, "59.94fps +1 (Bresenham)"},

		// Non-nominal — should return false so a real anomaly still warns.
		{0, false, "zero"},
		{1, false, "one tick"},
		{3500, false, "between 24 and 25 fps"},
		{7500, false, "2x 24fps (dropped frame)"},
		{6000, false, "2x 30fps (dropped frame)"},
		{-3750, false, "negative delta"},
	}
	for _, c := range cases {
		got := IsNominalPTSDelta(c.delta)
		if got != c.want {
			t.Errorf("IsNominalPTSDelta(%d) [%s] = %v, want %v", c.delta, c.label, got, c.want)
		}
	}
}
