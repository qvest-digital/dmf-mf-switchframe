package clock

import (
	"math"
	"slices"
)

// RateEstimate is a frame rate estimated from PTS.
type RateEstimate struct {
	FPS float64
	// Precision is the relative error FPS can carry: one timestamp
	// quantum over the span measured. The quantum is how far the
	// frames' spacing varies, never under a 90 kHz tick, which is what
	// rounding each PTS to a coarser timebase, such as 1 ms, leaves.
	Precision float64
}

// EstimateRate estimates the frame rate a run of frames' 90 kHz PTS stand
// for, in decode order or not, from the span of their PTS: each gap
// between neighbours in PTS order counts as the whole number of median
// spacings nearest to it, so a gap a dropped frame leaves counts as two
// periods, and the rate is those periods over the span. Its precision is
// the spread of the gaps around their periods, at least one 90 kHz tick,
// over the span. It is 30 fps without two distinct PTS, and never below
// 10.
func EstimateRate(pts []int64) RateEstimate {
	sorted := slices.Sorted(slices.Values(pts))
	gaps := make([]int64, 0, len(sorted))
	for i := 1; i < len(sorted); i++ {
		if g := sorted[i] - sorted[i-1]; g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return RateEstimate{FPS: 30}
	}
	median := slices.Sorted(slices.Values(gaps))[len(gaps)/2]
	var periods, span int64
	for _, g := range gaps {
		periods += max(1, (2*g+median)/(2*median))
		span += g
	}
	fps := float64(periods) * 90000 / float64(span)
	if fps < 10 {
		return RateEstimate{FPS: 10}
	}
	// How far a gap lies off its periods at the rate estimated.
	period := float64(span) / float64(periods)
	var spread float64
	for _, g := range gaps {
		k := max(1, (2*g+median)/(2*median))
		spread = max(spread, math.Abs(float64(g)-float64(k)*period))
	}
	return RateEstimate{FPS: fps, Precision: max(2*spread, 1) / float64(span)}
}

// Rate returns the rate the estimate stands for: n*1000/1001 when the
// estimate is nearer to it than to n and within a quarter of the distance
// between the two, widened by the estimate's precision; otherwise the
// nearest whole number of frames per second. 239.76 fps is 240000/1001,
// and 24.9 fps, measured precisely, is 25. Telling n*1000/1001 from n
// needs a span of about a thousand timestamp quanta: 30 frames at 1 ms
// timestamps are about the least for 29.97 fps, and over a shorter span
// an estimate between the two reads as the one it is nearer to. It is the
// zero Rate for a rate not above 0.
func (e RateEstimate) Rate() Rate {
	if e.FPS <= 0 {
		return Rate{}
	}
	n := math.Round(e.FPS * 1.001)
	d := n * 1000 / 1001
	if off := math.Abs(e.FPS - d); n > 0 && off < math.Abs(e.FPS-n) && off <= (n-d)/4+e.FPS*e.Precision {
		return Rate{Num: int64(n) * 1000, Den: 1001}
	}
	return Rate{Num: int64(math.Round(e.FPS)), Den: 1}
}

// RateOf returns the rate an exact or precisely measured frame rate stands
// for (RateEstimate.Rate).
func RateOf(fps float64) Rate {
	return RateEstimate{FPS: fps}.Rate()
}
