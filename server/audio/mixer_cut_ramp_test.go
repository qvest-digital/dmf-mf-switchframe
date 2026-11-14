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

// rampFormat is a program frame rate and a mixer sample rate.
type rampFormat struct {
	fps clock.Rate
	sr  int
}

// rampFormats returns the formats the ramp tests run at: every frame rate at
// every sample rate, and under the race detector, where CI's coverage
// counting makes each sample many times dearer and the race detector
// finds nothing in tests on one goroutine, the slowest frame rate at the
// lowest sample rate and the fastest at the highest.
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
// camera sources do by default.
func afvModes() []bool {
	if raceDetector {
		return []bool{true}
	}
	return []bool{false, true}
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
	k          int       // the next period
	gain       []float64 // the measured source's gain, sample by sample
}

func newGainRig(t *testing.T, fps clock.Rate, sr int, sources []string, measured string, afv, interleave bool) *gainRig {
	t.Helper()
	m := NewMixer(MixerConfig{SampleRate: sr, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	ra := clock.Rate{Num: int64(sr), Den: 1}
	block := sr / gcd(sr, 10000)
	base := clock.TimestampToIndex(ra, clock.TAINow())
	r := &gainRig{
		t: t, m: m, ra: ra,
		starts: periodStarts(fps, sr, 64),
		base:   base - base%uint64(block), block: block,
		sources: sources, measured: measured, interleave: interleave,
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
	token := r.m.OnTransitionStart(from, to, Crossfade, 0)
	for k := 1; k <= run; k++ {
		r.m.OnTransitionPosition(float64(k) / float64(n))
		r.period()
	}
	return token
}

// stepBound returns the largest step from one sample to the next a gain
// ramping by one ramp increment per period takes: the steepest period of
// the equal-power curve over the shortest period.
func (r *gainRig) stepBound() float64 {
	minN := r.starts[1]
	for k := 1; k < r.k; k++ {
		minN = min(minN, r.starts[k+1]-r.starts[k])
	}
	return math.Sin(math.Pi/2/cutRampPeriods)/float64(minN-1) + 1e-6
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
			for _, measured := range []string{"program", "clip"} {
				name := fmt.Sprintf("%s/afv=%v/%s", formatName(fps, sr), afv, measured)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
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

					cut, end := r.starts[cutAt], r.starts[cutAt+cutRampPeriods]
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
				})
			}
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
}

// No switcher action makes a source's gain step: a cut, a second cut
// during a cut's ramp, a completed mix and the program change after it, a
// cut during a mix, with or without a period produced between the calls
// one action makes. Every source's gain moves at most one ramp increment
// a period and ends where the action puts it.
func TestCutGainsNeverStep(t *testing.T) {
	t.Parallel()
	for _, sc := range gainScenarios {
		for _, f := range rampFormats() {
			fps, sr := f.fps, f.sr
			for _, afv := range afvModes() {
				if sc.afvOnly && !afv {
					continue
				}
				for _, interleave := range []bool{false, true} {
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
							r.periods(2)
							sc.run(r, afv)
							r.periods(cutRampPeriods + 2)
							r.checkContinuous()
							r.checkEnd(sc.end(afv)[measured])
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
