package clock

import (
	"fmt"
	"testing"
)

var estimateRates = []Rate{
	{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {50, 1},
	{60000, 1001}, {60, 1}, {100, 1}, {120000, 1001}, {120, 1},
	{240000, 1001}, {240, 1},
}

// framePTS returns the PTS of frames 0 to n-1 at rate r, rounded to 90 kHz
// ticks, leaving out the frames in drop.
func framePTS(r Rate, n int64, drop ...int64) []int64 {
	var pts []int64
next:
	for i := int64(0); i < n; i++ {
		for _, d := range drop {
			if i == d {
				continue next
			}
		}
		pts = append(pts, (i*90000*r.Den+r.Num/2)/r.Num)
	}
	return pts
}

// A run of frames at any rate is estimated at that rate, and read back as
// it, with no frame missing or with one or two dropped: a 25 fps clip with
// one frame dropped in 250 is not 24.975 fps, which stamps and paces it
// slower than its audio.
func TestEstimateRateAtEveryRateWithDroppedFrames(t *testing.T) {
	for _, r := range estimateRates {
		for _, c := range []struct {
			name string
			drop []int64
		}{
			{"whole", nil},
			{"one dropped", []int64{120}},
			{"two dropped", []int64{60, 61}},
			{"two apart", []int64{30, 200}},
		} {
			t.Run(fmt.Sprintf("%d/%d %s", r.Num, r.Den, c.name), func(t *testing.T) {
				e := EstimateRate(framePTS(r, 250, c.drop...))
				want := float64(r.Num) / float64(r.Den)
				if d := e.FPS/want - 1; d > 1e-4 || d < -1e-4 {
					t.Fatalf("estimate %v fps, want %v", e.FPS, want)
				}
				if got := e.Rate(); got != r {
					t.Fatalf("estimate %v fps reads as %d/%d", e.FPS, got.Num, got.Den)
				}
			})
		}
	}
}

// Frames in decode order, as B-frames leave them, give the rate of their
// PTS order.
func TestEstimateRateInDecodeOrder(t *testing.T) {
	pts := []int64{0, 9000, 3000, 6000, 18000, 12000, 15000}
	if got := EstimateRate(pts).FPS; got != 30 {
		t.Fatalf("estimate %v fps, want 30", got)
	}
}

// Without two distinct PTS there is no spacing to measure: 30 fps. Below
// 10 fps the estimate is 10.
func TestEstimateRateDefaults(t *testing.T) {
	for _, c := range []struct {
		name string
		pts  []int64
		want float64
	}{
		{"none", nil, 30},
		{"one", []int64{90000}, 30},
		{"one PTS", []int64{90000, 90000, 90000}, 30},
		{"0.1 fps", []int64{0, 900000}, 10},
	} {
		if got := EstimateRate(c.pts).FPS; got != c.want {
			t.Errorf("%s: %v fps, want %v", c.name, got, c.want)
		}
	}
}

// A precise rate reads as n*1000/1001 nearer to it than to n and within a
// quarter of its distance from n, as the rounding of PTS to 90 kHz ticks
// leaves it; a rate further from it, as a few dropped or late frames
// leave, is n.
func TestRateOf(t *testing.T) {
	for _, c := range []struct {
		fps  float64
		want Rate
	}{
		{15, Rate{15, 1}},
		{23.976, Rate{24000, 1001}},
		{24.5, Rate{25, 1}},
		{24.9, Rate{25, 1}},
		{24.99, Rate{25, 1}},
		{24.985, Rate{25, 1}},
		{24.95, Rate{25, 1}},
		{29.9, Rate{30, 1}},
		{29.97, Rate{30000, 1001}},
		{49.9, Rate{50, 1}},
		{59.94, Rate{60000, 1001}},
		{59.945, Rate{60000, 1001}},
		{100, Rate{100, 1}},
		{119.88, Rate{120000, 1001}},
		{120, Rate{120, 1}},
		{239.76, Rate{240000, 1001}},
		{239.78, Rate{240000, 1001}},
		{240, Rate{240, 1}},
		{1000000.0 / 1001, Rate{1000000, 1001}},
	} {
		if got := RateOf(c.fps); got != c.want {
			t.Errorf("%v fps: %d/%d, want %d/%d", c.fps, got.Num, got.Den, c.want.Num, c.want.Den)
		}
	}
	for _, r := range estimateRates {
		if got := RateOf(float64(r.Num) / float64(r.Den)); got != r {
			t.Errorf("%d/%d reads as %d/%d", r.Num, r.Den, got.Num, got.Den)
		}
	}
}

// A clip whose timestamps count milliseconds, as Matroska, WebM and FLV
// do, places each frame up to half a millisecond off: over 30 frames at
// 29.97 fps the estimate is 29.959, further from 30000/1001 than a quarter
// of its distance from 30. Its precision, a millisecond over the span,
// widens that, so 30 and 60 frames of a 1001 rate read as it and the
// whole rates as themselves. Over 30 or 60 frames 120 fps and 119.88 fps,
// and 240 and 239.76 fps, round to the same milliseconds, so those whole
// rates are checked over 600 frames only. With frames dropped, a gap
// counts as the whole periods it spans, so 240 fps reads as 240, not 239
// or 241.
func TestEstimateRateOnAMillisecondTimebase(t *testing.T) {
	for _, c := range []struct {
		r      Rate
		frames []int
	}{
		{Rate{24000, 1001}, []int{30, 60, 600}}, {Rate{24, 1}, []int{30, 60, 600}},
		{Rate{30000, 1001}, []int{30, 60, 600}}, {Rate{30, 1}, []int{30, 60, 600}},
		{Rate{60000, 1001}, []int{30, 60, 600}}, {Rate{60, 1}, []int{30, 60, 600}},
		{Rate{25, 1}, []int{30, 60, 600}}, {Rate{50, 1}, []int{30, 60, 600}},
		{Rate{100, 1}, []int{30, 60, 600}},
		{Rate{120000, 1001}, []int{30, 60, 600}}, {Rate{120, 1}, []int{600}},
		{Rate{240000, 1001}, []int{30, 60, 600}}, {Rate{240, 1}, []int{600}},
	} {
		for _, n := range c.frames {
			for _, drop := range [][]int{nil, {n / 2}, {n / 3, n/3 + 1}, {n / 4, 3 * n / 4}} {
				var pts []int64
			next:
				for k := range n {
					for _, d := range drop {
						if k == d {
							continue next
						}
					}
					ms := (int64(k)*1000*c.r.Den*2 + c.r.Num) / (2 * c.r.Num)
					pts = append(pts, ms*90)
				}
				e := EstimateRate(pts)
				if got := e.Rate(); got != c.r {
					t.Errorf("%d frames at %d/%d on 1 ms, %v dropped: %v fps (precision %.5f) reads as %d/%d",
						n, c.r.Num, c.r.Den, drop, e.FPS, e.Precision, got.Num, got.Den)
				}
			}
		}
	}
}

// RateOf takes any float: not above 0, there is no rate.
func TestRateOfNoRate(t *testing.T) {
	for _, fps := range []float64{0, -25} {
		if got := RateOf(fps); got != (Rate{}) {
			t.Errorf("RateOf(%v) = %d/%d, want the zero Rate", fps, got.Num, got.Den)
		}
	}
}

// A frame placed off its grid, as a jittery source or a late frame leaves
// it, does not change the rate: the gap before it and the gap after it
// count as one period each, as the median spacing has them.
func TestEstimateRateWithAFrameOffItsGrid(t *testing.T) {
	for _, r := range estimateRates {
		pts := framePTS(r, 250)
		period := pts[1] - pts[0]
		pts[100] -= period * 4 / 10
		if got := EstimateRate(pts).Rate(); got != r {
			t.Errorf("%d/%d with frame 100 early reads as %d/%d", r.Num, r.Den, got.Num, got.Den)
		}
	}
}
