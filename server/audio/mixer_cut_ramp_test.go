package audio

import (
	"fmt"
	"hash/fnv"
	"math"
	"slices"
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

// rampFormat is a program frame rate and a mixer sample rate.
type rampFormat struct {
	fps clock.Rate
	sr  int
}

// rampFormats returns the formats the ramp tests run at: every frame rate at
// every sample rate, and under the race detector, where CI's coverage
// counting makes each sample many times dearer and the race detector
// finds nothing in tests on one goroutine, the slowest frame rate at the
// lowest sample rate and the fastest at the highest. The CI job without
// the race detector runs the full matrix.
func rampFormats() []rampFormat {
	if raceDetector {
		return []rampFormat{
			{rampFrameRates[0], rampSampleRates[0]},
			{rampFrameRates[len(rampFrameRates)-1], rampSampleRates[len(rampSampleRates)-1]},
		}
	}
	var fs []rampFormat
	for _, fps := range rampFrameRates {
		for _, sr := range rampSampleRates {
			fs = append(fs, rampFormat{fps, sr})
		}
	}
	return fs
}

// afvModes returns whether the sources follow video, for each run: both
// ways, and under the race detector following video only, as MXL, SRT and
// camera sources do by default. The CI job without the race detector runs
// both.
func afvModes() []bool {
	if raceDetector {
		return []bool{true}
	}
	return []bool{false, true}
}

// raceChoice returns a number fixed by a scenario's name, so a choice
// made by it does not move when another scenario is added.
func raceChoice(name string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return h.Sum32()
}

// interleaveModes returns, for a scenario, whether a period is produced
// between the calls of one action: both ways, and under the race detector
// one way, chosen by the scenario's name. The CI job without the race
// detector runs both.
func interleaveModes(name string) []bool {
	if raceDetector {
		return []bool{raceChoice(name)%2 == 1}
	}
	return []bool{false, true}
}

// scenarioFormats returns the formats a scenario runs at: rampFormats, and
// under the race detector one of its two, chosen by the scenario's name
// apart from its interleave mode. The CI job without the race detector
// runs every format.
func scenarioFormats(name string) []rampFormat {
	fs := rampFormats()
	if raceDetector {
		k := raceChoice(name) / 2 % uint32(len(fs))
		return fs[k : k+1]
	}
	return fs
}

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

// periodOf returns the period sample i falls in.
func periodOf(starts []int, i int) int {
	for k := len(starts) - 1; k > 0; k-- {
		if i >= starts[k] {
			return k
		}
	}
	return 0
}

// gainLevel is the level the source a gainRig measures delivers.
const gainLevel = 0.5

// gainRig drives a mixer one period at a time, as the sync loop does, and
// records the gain one source airs at: only that source delivers a level,
// so each sample aired over the level is its gain. Every source delivers
// two periods ahead of the read point, as a live source does, in blocks
// whose 90 kHz PTS and length in nanoseconds both fall on a sample, so
// the mixer places them to the sample.
type gainRig struct {
	t        *testing.T
	m        *Mixer
	ra       clock.Rate
	starts   []int
	base     uint64
	block    int // samples in the shortest span a whole number of 100 us long
	sent     int // samples delivered from base on
	sources  []string
	measured string
	// interleave produces a period between the calls a switcher action
	// makes in a row, as the sync loop can.
	interleave bool
	// steep is the fewest periods any gain change of the run takes from
	// silence to full gain: cutRampPeriods unless a shorter mix runs.
	steep int
	k     int       // the next period
	gain  []float64 // the measured source's gain, sample by sample
}

func newGainRig(t *testing.T, fps clock.Rate, sr int, sources []string, measured string, afv, interleave bool) *gainRig {
	t.Helper()
	// The mixer has PCM channels only, so it holds no decoder to close. A
	// t.Cleanup would keep it, and its rings, alive until the whole test
	// ends: thousands of mixers across the matrix.
	m := NewMixer(MixerConfig{SampleRate: sr, Channels: 2})
	ra := clock.Rate{Num: int64(sr), Den: 1}
	block := sr / gcd(sr, 10000)
	base := clock.TimestampToIndex(ra, clock.TAINow())
	r := &gainRig{
		t: t, m: m, ra: ra,
		starts: periodStarts(fps, sr, 64),
		base:   base - base%uint64(block), block: block,
		sources: sources, measured: measured, interleave: interleave,
		steep: cutRampPeriods,
	}
	for _, key := range sources {
		m.AddChannel(key)
		if afv {
			if err := m.SetAFV(key, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.deliver(2)
	return r
}

// deliver ingests every source's audio up to the end of period k.
func (r *gainRig) deliver(k int) {
	to := (r.starts[k] + r.block - 1) / r.block * r.block
	if to <= r.sent {
		return
	}
	for _, key := range r.sources {
		level := float32(0)
		if key == r.measured {
			level = gainLevel
		}
		at := r.base + uint64(r.sent)
		r.m.IngestPCM(key, constPCM(to-r.sent, level), clock.IndexPTS(r.ra, at), 2)
	}
	r.sent = to
}

// period produces the next period and advances the cut ramp, as a tick
// does.
func (r *gainRig) period() {
	r.t.Helper()
	k := r.k
	if k+3 >= len(r.starts) {
		r.t.Fatal("the rig ran out of periods")
	}
	r.deliver(k + 3)
	n := r.starts[k+1] - r.starts[k]
	at := r.base + uint64(r.starts[k])
	out := r.m.ProduceSamples(at, n, clock.IndexPTS(r.ra, at))
	r.m.AdvanceCrossfade()
	if len(out) != 2*n {
		r.t.Fatalf("period %d: %d values, want %d", k, len(out), 2*n)
	}
	for i := 0; i < n; i++ {
		r.gain = append(r.gain, float64(out[2*i])/gainLevel)
	}
	r.k++
}

// periods produces n periods.
func (r *gainRig) periods(n int) {
	for range n {
		r.period()
	}
}

// calls makes the calls of one switcher action in a row, with a period
// between them when the rig interleaves.
func (r *gainRig) calls(fns ...func()) {
	for i, fn := range fns {
		if i > 0 && r.interleave {
			r.period()
		}
		fn()
	}
}

// cut makes the calls the switcher's Cut makes.
func (r *gainRig) cut(from, to string) {
	r.calls(func() { r.m.OnCut(from, to) }, func() { r.m.OnProgramChange(to) })
}

// mix starts a mix from to to and runs it over n periods, each tick
// handing the position to the mixer before its audio, without completing
// it. It returns the transition's token.
func (r *gainRig) mix(from, to string, n, run int) uint64 {
	return r.transition(Crossfade, from, to, n, run)
}

// transition is mix with the transition's mode.
func (r *gainRig) transition(mode TransitionMode, from, to string, n, run int) uint64 {
	token := r.m.OnTransitionStart(from, to, mode, 0)
	for k := 1; k <= run; k++ {
		r.m.OnTransitionPosition(float64(k) / float64(n))
		r.period()
	}
	return token
}

// stepBound returns the largest step from one sample to the next a gain
// ramping by one ramp increment per period takes: the steepest period of
// the equal-power curve over steep periods, over the shortest period.
func (r *gainRig) stepBound() float64 {
	minN := r.starts[1]
	for k := 1; k < r.k; k++ {
		minN = min(minN, r.starts[k+1]-r.starts[k])
	}
	return math.Sin(math.Pi/2/float64(r.steep))/float64(minN-1) + 1e-6
}

// checkContinuous fails when the measured gain steps by more than a ramp
// increment anywhere, at a period boundary above all.
func (r *gainRig) checkContinuous() {
	r.t.Helper()
	bound := r.stepBound()
	for i := 1; i < len(r.gain); i++ {
		if step := math.Abs(r.gain[i] - r.gain[i-1]); step > bound {
			r.t.Fatalf("%s: sample %d (period %d, starting at sample %d): gain steps from %.6f to %.6f, a ramp steps at most %.6f",
				r.measured, i, periodOf(r.starts, i), r.starts[periodOf(r.starts, i)], r.gain[i-1], r.gain[i], bound)
		}
	}
}

// checkEnd fails unless the measured source airs at gain want at the end.
func (r *gainRig) checkEnd(want float64) {
	r.t.Helper()
	if got := r.gain[len(r.gain)-1]; math.Abs(got-want) > 1e-6 {
		r.t.Fatalf("%s airs at gain %.6f at the end, want %v", r.measured, got, want)
	}
}

// formatName names a subtest after its frame and sample rate.
func formatName(fps clock.Rate, sr int) string {
	return fmt.Sprintf("%dHz_%d_%d", sr, fps.Num, fps.Den)
}

// A cut airs the incoming source from the period it lands on: the ramp
// rises over that period and the three after it, from silence to full
// gain, with no step between periods and none where the ramp ends. A
// ramp starting one period late airs a silent period first and steps at
// its end. The outgoing source fades out alongside when it follows video
// and goes off; without AFV it stays on, and a cut does not dip it.
func TestCutRampStartsOnTheCutPeriod(t *testing.T) {
	t.Parallel()
	for _, f := range rampFormats() {
		fps, sr := f.fps, f.sr
		for _, afv := range afvModes() {
			name := fmt.Sprintf("%s/afv=%v", formatName(fps, sr), afv)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				gains := map[string][]float64{}
				var cut, end int
				var starts []int
				for _, measured := range []string{"program", "clip"} {
					r := newGainRig(t, fps, sr, []string{"program", "clip"}, measured, afv, false)
					if afv {
						r.m.OnProgramChange("program")
					} else {
						r.m.SetActive("program", true)
					}
					r.periods(2)
					cutAt := r.k
					r.cut("program", "clip")
					r.periods(cutRampPeriods + 2)
					r.checkContinuous()
					gains[measured] = r.gain
					starts = r.starts[cutAt : cutAt+cutRampPeriods+1]
					cut, end = starts[0], starts[cutRampPeriods]

					if measured == "clip" {
						if r.gain[cut+1] <= 0 {
							t.Fatalf("cut period: sample 1 airs at %v, the clip does not air on the cut tick", r.gain[cut+1])
						}
						for i := cut + 1; i < end; i++ {
							if r.gain[i] < r.gain[i-1]-1e-6 {
								t.Fatalf("sample %d: the incoming gain falls", i)
							}
						}
					}
					want := 1.0
					if measured == "program" && afv {
						want = 0
					}
					for i := end; i < len(r.gain); i++ {
						if math.Abs(r.gain[i]-want) > 1e-6 {
							t.Fatalf("sample %d after the ramp airs at %v, want %v", i, r.gain[i], want)
						}
					}
				}
				if !afv {
					return
				}
				// Where the program fades out, the ramp is equal-power: at
				// every period boundary the two gains' squares sum to 1, so
				// the program's loudness does not dip across the cut.
				for k, at := range starts {
					for _, i := range []int{at - 1, at} {
						a, b := gains["program"][i], gains["clip"][i]
						if p := a*a + b*b; math.Abs(p-1) > 1e-5 {
							t.Fatalf("period boundary %d, sample %d: program %.6f and clip %.6f, power %.6f, want 1", k, i, a, b, p)
						}
					}
				}
			})
		}
	}
}

// gainScenario is a sequence of switcher actions over sources a, b and c,
// a on program to start with, and the gain each source ends at.
type gainScenario struct {
	name string
	// afvOnly marks a scenario whose sources all follow video.
	afvOnly bool
	// also is a source on besides a before the actions start.
	also string
	// steep is the fewest periods the scenario's mix takes, when shorter
	// than a cut ramp.
	steep int
	// steady is a source that, when no source follows video, airs at full
	// gain throughout.
	steady string
	// dips marks a scenario in which, when every source follows video,
	// every source airs silence at some point.
	dips bool
	run  func(r *gainRig, afv bool)
	end  func(afv bool) map[string]float64
}

// afvEnd returns the end gains of a scenario that takes program from a to
// on: on alone with AFV, everything that was on without it.
func afvEnd(on string, without map[string]float64) func(bool) map[string]float64 {
	return func(afv bool) map[string]float64 {
		if !afv {
			return without
		}
		end := map[string]float64{"a": 0, "b": 0, "c": 0}
		end[on] = 1
		return end
	}
}

var gainScenarios = []gainScenario{
	{
		name: "cut",
		run:  func(r *gainRig, _ bool) { r.cut("a", "b") },
		end:  afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A second cut during the first one's ramp: every channel carries on
	// from the gain it aired, the first outgoing source fading out on its
	// own ramp instead of jumping back to full gain.
	{
		name: "cut_during_a_cut_ramp_1",
		run:  func(r *gainRig, _ bool) { r.cut("a", "b"); r.periods(1); r.cut("b", "c") },
		end:  afvEnd("c", map[string]float64{"a": 1, "b": 1, "c": 1}),
	},
	{
		name: "cut_during_a_cut_ramp_2",
		run:  func(r *gainRig, _ bool) { r.cut("a", "b"); r.periods(2); r.cut("b", "c") },
		end:  afvEnd("c", map[string]float64{"a": 1, "b": 1, "c": 1}),
	},
	{
		name: "cut_during_a_cut_ramp_3",
		run:  func(r *gainRig, _ bool) { r.cut("a", "b"); r.periods(3); r.cut("b", "c") },
		end:  afvEnd("c", map[string]float64{"a": 1, "b": 1, "c": 1}),
	},
	// A cut back to the outgoing source during the ramp.
	{
		name: "cut_back_during_a_cut_ramp",
		run:  func(r *gainRig, _ bool) { r.cut("a", "b"); r.periods(2); r.cut("b", "a") },
		end:  afvEnd("a", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A completed mix hands program to its new source: the program change
	// that follows starts no second crossfade from the old one.
	{
		name: "mix_completes",
		run: func(r *gainRig, _ bool) {
			r.mix("a", "b", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("b") })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A cut to the preview during a mix carries on from the mix's gains,
	// and the aborted transition's release after it changes nothing.
	{
		name: "cut_to_the_preview_during_a_mix",
		run: func(r *gainRig, _ bool) {
			token := r.mix("a", "b", 8, 4)
			r.cut("a", "b")
			r.calls(func() { r.m.OnTransitionAbort(token) })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A cut to a third source during a mix fades the mix's incoming source
	// out as well.
	{
		name: "cut_elsewhere_during_a_mix",
		run: func(r *gainRig, _ bool) {
			token := r.mix("a", "b", 8, 4)
			r.cut("a", "c")
			r.calls(func() { r.m.OnTransitionAbort(token) })
		},
		end: afvEnd("c", map[string]float64{"a": 1, "b": 0, "c": 1}),
	},
	// A source that follows video and is on without being on program goes
	// off with a cut, faded out.
	{
		name:    "cut_with_a_second_source_on",
		afvOnly: true,
		also:    "c",
		run:     func(r *gainRig, _ bool) { r.cut("a", "b") },
		end:     afvEnd("b", nil),
	},
	// A mix started during a cut's ramp, or as the cut lands, takes every
	// source on from the gain it aired: the ramp's end does not cut the
	// mix short, and the source the ramp was taking off, the mix's target
	// here, rises instead of going off.
	cutThenMix(0),
	cutThenMix(1),
	cutThenMix(2),
	cutThenMix(3),
	// A mix of two periods, a period after the cut.
	{
		name:  "cut_then_short_mix",
		steep: 2,
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(1)
			r.mix("b", "a", 2, 2)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("a") })
		},
		end: afvEnd("a", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A mix during a cut's ramp to a third source: the source the ramp was
	// taking off keeps fading under the mix and goes off when it ends.
	{
		name: "cut_then_mix_elsewhere",
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(1)
			r.mix("b", "c", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("c") })
		},
		end: afvEnd("c", map[string]float64{"a": 1, "b": 1, "c": 1}),
	},
	// A mix back to a source a transition had put on and a cut elsewhere
	// is taking off again: it rises from where it is and stays on.
	{
		name: "mix_back_to_a_source_a_cut_takes_off",
		run: func(r *gainRig, _ bool) {
			token := r.mix("a", "b", 8, 4)
			r.cut("a", "c")
			r.calls(func() { r.m.OnTransitionAbort(token) })
			r.periods(1)
			r.mix("c", "b", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("b") })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 1}),
	},
	// A dip to a source already on dips it only when it follows video.
	{
		name:   "dip_to_a_source_already_on",
		also:   "b",
		steady: "b",
		run: func(r *gainRig, _ bool) {
			r.transition(DipToSilence, "a", "b", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("b") })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A mix to a source already on does not dip it.
	{
		name: "mix_to_a_source_already_on",
		also: "b",
		run: func(r *gainRig, _ bool) {
			r.mix("a", "b", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("b") })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A fade to black as a cut lands or during its ramp takes every source
	// down from the gain it aired, and the program is muted at its end.
	cutThenFTB(0),
	cutThenFTB(1),
	cutThenFTB(2),
	cutThenFTB(3),
	// A fade from black during a cut made while the program is black: the
	// source the cut takes off stays silent and goes off, the rest rise.
	{
		name: "cut_while_black_then_ftb_in",
		also: "c",
		// The 5 ms unmute ramp the switcher starts with a fade from black
		// steepens its first period.
		steep: 2,
		run: func(r *gainRig, _ bool) {
			r.transition(FadeOut, "a", "", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.SetProgramMute(true) })
			r.periods(1)
			r.cut("a", "b")
			r.periods(1)
			r.m.SetProgramMute(false)
			r.transition(FadeIn, "b", "", 8, 8)
			r.calls(r.m.OnTransitionComplete)
		},
		end: func(afv bool) map[string]float64 {
			if afv {
				return map[string]float64{"a": 0, "b": 1, "c": 0}
			}
			return map[string]float64{"a": 1, "b": 1, "c": 1}
		},
	},
	// A dip back to a source a cut is taking off, a period after the cut,
	// takes it through silence.
	{
		name: "cut_then_dip_1",
		dips: true,
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(1)
			r.transition(DipToSilence, "b", "a", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("a") })
		},
		end: afvEnd("a", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A mix back to the source a cut is taking off, aborted midway, at
	// once, or with the T-bar pulled back first: every source goes on from
	// the gain it aired to where the abort leaves it.
	{
		name: "cut_then_mix_aborted_midway",
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(1)
			token := r.mix("b", "a", 8, 4)
			r.calls(func() { r.m.OnTransitionAbort(token) })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	{
		name: "cut_then_mix_aborted_at_once",
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(1)
			token := r.mix("b", "a", 8, 0)
			r.calls(func() { r.m.OnTransitionAbort(token) })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	{
		name: "cut_then_mix_tbar_back",
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(1)
			token := r.mix("b", "a", 8, 3)
			for k := 2; k >= 1; k-- {
				r.m.OnTransitionPosition(float64(k) / 8)
				r.period()
			}
			r.calls(func() { r.m.OnTransitionAbort(token) })
		},
		end: afvEnd("b", map[string]float64{"a": 1, "b": 1, "c": 0}),
	},
	// A mix aborted before any of it aired leaves the program as it was.
	{
		name: "mix_aborted_at_once",
		run: func(r *gainRig, _ bool) {
			token := r.mix("a", "b", 8, 0)
			r.calls(func() { r.m.OnTransitionAbort(token) })
		},
		end: func(bool) map[string]float64 { return map[string]float64{"a": 1, "b": 0, "c": 0} },
	},
}

// cutThenFTB is a cut from a to b and, periods later, a fade to black
// over 8 periods, run to completion and muted.
func cutThenFTB(periods int) gainScenario {
	return gainScenario{
		name: fmt.Sprintf("cut_then_ftb_%d", periods),
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(periods)
			r.transition(FadeOut, "b", "", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.SetProgramMute(true) })
		},
		end: func(bool) map[string]float64 { return map[string]float64{"a": 0, "b": 0, "c": 0} },
	}
}

// cutThenMix is a cut from a to b and, periods later, a mix back to a
// over 8 periods, run to completion.
func cutThenMix(periods int) gainScenario {
	return gainScenario{
		name: fmt.Sprintf("cut_then_mix_%d", periods),
		run: func(r *gainRig, _ bool) {
			r.cut("a", "b")
			r.periods(periods)
			r.mix("b", "a", 8, 8)
			r.calls(r.m.OnTransitionComplete, func() { r.m.OnProgramChange("a") })
		},
		end: afvEnd("a", map[string]float64{"a": 1, "b": 1, "c": 0}),
	}
}

// No switcher action makes a source's gain step: a cut, a second cut
// during a cut's ramp, a completed mix and the program change after it, a
// cut during a mix, with or without a period produced between the calls
// one action makes. Every source's gain moves at most one ramp increment
// a period and ends where the action puts it.
func TestCutGainsNeverStep(t *testing.T) {
	t.Parallel()
	for _, sc := range gainScenarios {
		for _, f := range scenarioFormats(sc.name) {
			fps, sr := f.fps, f.sr
			for _, afv := range afvModes() {
				if sc.afvOnly && !afv {
					continue
				}
				for _, interleave := range interleaveModes(sc.name) {
					name := fmt.Sprintf("%s/%s/afv=%v/interleave=%v", sc.name, formatName(fps, sr), afv, interleave)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						for _, measured := range []string{"a", "b", "c"} {
							r := newGainRig(t, fps, sr, []string{"a", "b", "c"}, measured, afv, interleave)
							if afv {
								r.m.OnProgramChange("a")
							} else {
								r.m.SetActive("a", true)
							}
							if sc.also != "" {
								r.m.SetActive(sc.also, true)
							}
							if sc.steep != 0 {
								r.steep = sc.steep
							}
							r.periods(2)
							sc.run(r, afv)
							r.periods(cutRampPeriods + 2)
							r.checkContinuous()
							r.checkEnd(sc.end(afv)[measured])
							if afv && sc.dips && slices.Min(r.gain) > 1e-6 {
								t.Fatalf("%s never airs silence, a dip through silence does", measured)
							}
							if !afv && measured == sc.steady {
								for i, g := range r.gain {
									if math.Abs(g-1) > 1e-6 {
										t.Fatalf("%s: sample %d airs at %.6f, a source on that does not follow video stays at full gain", measured, i, g)
									}
								}
							}
						}
					})
				}
			}
		}
	}
}

// A cut ramp reaches full gain at the end of the fourth period from the
// cut's own, also when a period airs between the cut and the program
// change the switcher sends after it: the program change leaves the ramp
// to run instead of starting it again.
func TestProgramChangeLeavesTheCutRampToRun(t *testing.T) {
	t.Parallel()
	for _, f := range rampFormats() {
		fps, sr := f.fps, f.sr
		t.Run(formatName(fps, sr), func(t *testing.T) {
			t.Parallel()
			r := newGainRig(t, fps, sr, []string{"a", "b"}, "b", true, true)
			r.m.OnProgramChange("a")
			r.periods(2)
			cutAt := r.k
			r.cut("a", "b")
			r.periods(cutRampPeriods)
			full := r.starts[cutAt+cutRampPeriods] - 1
			if got := r.gain[full]; math.Abs(got-1) > 1e-6 {
				t.Fatalf("the last sample of the cut's fourth period airs at %.6f, want full gain", got)
			}
		})
	}
}
