package audio

import (
	"fmt"
	"math"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// rampFrameRates and rampSampleRates are the formats the cut ramp is
// checked at.
var (
	rampFrameRates = []clock.Rate{
		{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
		{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
		{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
		{Num: 120, Den: 1}, {Num: 240, Den: 1},
	}
	rampSampleRates = []int{44100, 48000, 96000}
)

// periodStarts returns the sample offsets of the first n+1 period starts
// at frame rate fps on sample rate sr, as the reference clock tiles the
// sample timeline.
func periodStarts(fps clock.Rate, sr, n int) []int {
	s := make([]int, n+1)
	for k := range s {
		s[k] = int(int64(k) * int64(sr) * fps.Den / fps.Num)
	}
	return s
}

// A cut airs the incoming source from the period it lands on: the ramp
// rises over that period and the three after it, from silence to full
// gain, with no step between periods and none where the ramp ends. A
// ramp starting one period late airs a silent period first and steps at
// its end.
func TestCutRampStartsOnTheCutPeriod(t *testing.T) {
	t.Parallel()
	for _, fps := range rampFrameRates {
		for _, sr := range rampSampleRates {
			t.Run(fmt.Sprintf("%dHz_%d_%d", sr, fps.Num, fps.Den), func(t *testing.T) {
				t.Parallel()
				const level = 0.5
				m := NewMixer(MixerConfig{SampleRate: sr, Channels: 2})
				t.Cleanup(func() { _ = m.Close() })
				ra := clock.Rate{Num: int64(sr), Den: 1}
				starts := periodStarts(fps, sr, cutRampPeriods+2)
				lead := starts[2]                                // the clip delivers ahead of the cut
				span := lead + starts[len(starts)-1] + starts[1] // past the last read
				cut := clock.TimestampToIndex(ra, clock.TAINow())
				from := clock.IndexPTS(ra, cut-uint64(lead))

				m.AddChannel("program")
				m.AddChannel("clip")
				m.SetActive("program", true)
				m.OnCut("program", "clip")
				m.IngestPCM("program", constPCM(span, 0), from, 2)
				m.IngestPCM("clip", constPCM(span, level), from, 2)

				var aired []float32 // left channel
				for k := 0; k+1 < len(starts); k++ {
					n := starts[k+1] - starts[k]
					at := cut + uint64(starts[k])
					out := m.ProduceSamples(at, n, clock.IndexPTS(ra, at))
					m.AdvanceCrossfade()
					if len(out) != 2*n {
						t.Fatalf("period %d: %d values, want %d", k, len(out), 2*n)
					}
					for i := 0; i < n; i++ {
						aired = append(aired, out[2*i])
					}
				}

				if aired[1] <= 0 {
					t.Fatalf("cut period: sample 1 is %v, the clip does not air on the cut tick", aired[1])
				}
				// Within a period the gain is a straight line, so the
				// largest step a continuous ramp takes is the steepest
				// period's rise over its shortest length.
				minN := starts[1]
				for k := 1; k+1 < len(starts); k++ {
					minN = min(minN, starts[k+1]-starts[k])
				}
				maxStep := level*transitionToGain(Crossfade, 1.0/cutRampPeriods)/float64(minN-1) + 1e-6
				end := starts[cutRampPeriods]
				for i := 1; i <= end; i++ {
					step := float64(aired[i] - aired[i-1])
					if step < -1e-6 {
						t.Fatalf("sample %d (period %d): gain falls by %g", i, periodOf(starts, i), -step)
					}
					if step > maxStep {
						t.Fatalf("sample %d (period %d): step %g, a continuous ramp steps at most %g",
							i, periodOf(starts, i), step, maxStep)
					}
				}
				for i := end; i < len(aired); i++ {
					if math.Abs(float64(aired[i])-level) > 1e-6 {
						t.Fatalf("sample %d after the ramp is %v, want %v", i, aired[i], level)
					}
				}
			})
		}
	}
}

// periodOf returns the period sample i falls in.
func periodOf(starts []int, i int) int {
	for k := len(starts) - 1; k > 0; k-- {
		if i >= starts[k] {
			return k
		}
	}
	return 0
}
