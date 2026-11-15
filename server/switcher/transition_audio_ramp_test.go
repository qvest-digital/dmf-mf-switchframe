package switcher

import (
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// mixFrameRates and mixSampleRates are the formats a mix's audio is
// checked at.
var (
	mixFrameRates = []clock.Rate{
		{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
		{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
		{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
		{Num: 120, Den: 1}, {Num: 240, Den: 1},
	}
	mixSampleRates = []int{44100, 48000, 96000}
)

// audioTickAt builds the tick of program grain n with its audio period on
// sample rate sr, as the reference clock does.
func audioTickAt(s *Switcher, sr int, n uint64) clock.Tick {
	tk := tickAt(s, n)
	pf := s.pipelineFormat.Load()
	rv := clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}
	ra := clock.Rate{Num: int64(sr), Den: 1}
	tk.AudioStart = clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, n))
	tk.AudioSamples = int(clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, n+1)) - tk.AudioStart)
	tk.AudioPTS = clock.IndexPTS(ra, tk.AudioStart)
	tk.ReadSample = clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, n-clock.ReaderDelayGrains))
	return tk
}

// rampFormat is a program frame rate and a mixer sample rate.
type rampFormat struct {
	fps clock.Rate
	sr  int
}

// mixFormats returns the formats the mix tests run at: every frame rate at
// every sample rate, and under the race detector, where CI's coverage
// counting makes each sample many times dearer and the race detector
// finds nothing in tests on one goroutine, the slowest frame rate at the
// lowest sample rate and the fastest at the highest. The CI job without
// the race detector runs the full matrix.
func mixFormats() []rampFormat {
	if raceDetector {
		return []rampFormat{
			{mixFrameRates[0], mixSampleRates[0]},
			{mixFrameRates[len(mixFrameRates)-1], mixSampleRates[len(mixSampleRates)-1]},
		}
	}
	var fs []rampFormat
	for _, fps := range mixFrameRates {
		for _, sr := range mixSampleRates {
			fs = append(fs, rampFormat{fps, sr})
		}
	}
	return fs
}

// interleaveModes returns, for a scenario, whether the sync loop produces
// a period between the calls of one action: both ways, and under the race
// detector one way, chosen by the scenario's name. The CI job without the
// race detector runs both.
func interleaveModes(name string) []bool {
	if raceDetector {
		h := fnv.New32a()
		_, _ = h.Write([]byte(name))
		return []bool{h.Sum32()%2 == 1}
	}
	return []bool{false, true}
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

// rampPeriods is how many periods the mixer's cut ramp takes.
const rampPeriods = 4

// interleavedMixer is the mixer as a switcher action reaches it while the
// sync loop runs: before each call the action makes, between produces a
// tick's audio, as the sync loop can between two calls of one action.
type interleavedMixer struct {
	*audio.Mixer
	between func()
}

func (m *interleavedMixer) OnCut(from, to string) {
	m.between()
	m.Mixer.OnCut(from, to)
}

func (m *interleavedMixer) OnProgramChange(source string) {
	m.between()
	m.Mixer.OnProgramChange(source)
}

func (m *interleavedMixer) OnTransitionComplete() {
	m.between()
	m.Mixer.OnTransitionComplete()
}

func (m *interleavedMixer) SetProgramMute(muted bool) {
	m.between()
	m.Mixer.SetProgramMute(muted)
}

func (m *interleavedMixer) OnTransitionStart(from, to string, mode audio.TransitionMode, durationMs int) uint64 {
	m.between()
	return m.Mixer.OnTransitionStart(from, to, mode, durationMs)
}

func (m *interleavedMixer) OnTransitionAbort(token uint64) {
	m.between()
	m.Mixer.OnTransitionAbort(token)
}

func (m *interleavedMixer) AbortToBlack(token uint64) {
	m.between()
	m.Mixer.AbortToBlack(token)
}

// airedPeriod is one period of the gain a source aired at.
type airedPeriod struct {
	gain []float64
	tick bool // produced by a tick, not between the calls of an action
}

// mixRig runs a switcher tick by tick with a mixer over sources mxl:a,
// mxl:b and mxl:c, mxl:a on program, and records the gain one source airs
// at: only that source delivers a level, so each sample aired over the
// level is its gain. Every source delivers two periods ahead of the read
// point, in blocks whose 90 kHz PTS and length in nanoseconds fall on a
// sample, so the mixer places them to the sample.
type mixRig struct {
	t        *testing.T
	s        *Switcher
	m        *audio.Mixer
	sr       int
	measured string
	g        uint64 // the next tick's grain
	block    uint64 // samples in the shortest span a whole number of 100 us long
	sent     uint64 // the sample index delivered up to
	inTick   bool

	mu    sync.Mutex
	aired []airedPeriod
}

const mixLevel = 0.5

var mixSources = []string{"mxl:a", "mxl:b", "mxl:c"}

func newMixRig(t *testing.T, fps clock.Rate, sr int, measured string, afv, interleave bool) *mixRig {
	t.Helper()
	s := newTestSwitcherDirect(distribution.NewRelay())
	f := *s.pipelineFormat.Load()
	f.FPSNum, f.FPSDen = int(fps.Num), int(fps.Den)
	s.pipelineFormat.Store(&f)
	recordPipeline(t, s)

	m := audio.NewMixer(audio.MixerConfig{SampleRate: sr, Channels: 2})
	r := &mixRig{t: t, s: s, m: m, sr: sr, measured: measured}
	m.SetRawAudioSink(func(pcm []float32, _ int64, _, ch int) {
		gain := make([]float64, len(pcm)/ch)
		for i := range gain {
			gain[i] = float64(pcm[i*ch]) / mixLevel
		}
		r.mu.Lock()
		r.aired = append(r.aired, airedPeriod{gain: gain, tick: r.inTick})
		r.mu.Unlock()
	})
	// On program before the mixer is attached, so no cut ramp runs into
	// the actions.
	onProgram(t, s, "mxl:a")
	s.RegisterMXLSource("mxl:b")
	s.RegisterMXLSource("mxl:c")
	s.SetSyncMixer(m)
	s.SetAACBuffer(clock.NewAACFrameBuffer(2, sr, mockAACEncode))
	if interleave {
		s.SetMixer(&interleavedMixer{Mixer: m, between: r.between})
	} else {
		s.SetMixer(m)
	}
	s.SetTransitionConfig(TransitionConfig{})
	for _, key := range mixSources {
		m.AddChannel(key)
		if afv {
			if err := m.SetAFV(key, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if afv {
		m.OnProgramChange("mxl:a")
	} else {
		m.SetActive("mxl:a", true)
	}

	r.g = grainNow(s)
	r.block = 1
	for r.block*10000%uint64(sr) != 0 {
		r.block++
	}
	first := audioTickAt(s, sr, r.g)
	r.sent = (first.ReadSample - 2*uint64(first.AudioSamples)) / r.block * r.block
	// The rig is the sync loop: a cut reaches the mixer on the first tick
	// that shows it, as it does while the loop runs.
	s.syncLoopRunning.Store(true)
	// One tick airs the program before any action, as a running switcher
	// has: the mixer takes every channel on from the gain it aired.
	r.tick()
	r.aired = nil
	return r
}

// close releases the rig's switcher and mixer. A rig is closed as soon as
// its run ends rather than by t.Cleanup, which would keep thousands of
// them alive until the whole test ends.
func (r *mixRig) close() {
	r.s.Close()
	_ = r.m.Close()
}

// deliver ingests every source's audio up to two periods past the next
// tick's read.
func (r *mixRig) deliver() {
	ra := clock.Rate{Num: int64(r.sr), Den: 1}
	to := (audioTickAt(r.s, r.sr, r.g+2).ReadSample + r.block - 1) / r.block * r.block
	if to <= r.sent {
		return
	}
	for _, key := range mixSources {
		level := float32(0)
		if key == r.measured {
			level = mixLevel
		}
		r.m.IngestPCM(key, constPCM(int(to-r.sent), level), clock.IndexPTS(ra, r.sent), 2)
	}
	r.sent = to
}

// rigTick is the tick of grain n reading the samples up to the next
// tick's read, so the reads tile the sample timeline and a ring placed
// again by a cut lands on the read point to the sample.
func (r *mixRig) rigTick(n uint64) clock.Tick {
	tk := audioTickAt(r.s, r.sr, n)
	tk.AudioSamples = int(audioTickAt(r.s, r.sr, n+1).ReadSample - tk.ReadSample)
	return tk
}

// tick runs the next tick.
func (r *mixRig) tick() {
	r.deliver()
	tk := r.rigTick(r.g)
	r.g++
	r.inTick = true
	r.s.processTick(tk)
	r.inTick = false
}

// ticks runs n ticks.
func (r *mixRig) ticks(n int) {
	for range n {
		r.tick()
	}
}

// between produces the next tick's audio alone, as the sync loop does
// while a switcher action is under way.
func (r *mixRig) between() {
	// Within a tick the calls run on the sync loop, as a cut the tick
	// hands to the mixer does: no period comes between them.
	if r.inTick {
		return
	}
	wasTick := r.inTick
	r.inTick = false
	r.deliver()
	r.s.processAudioTick(r.rigTick(r.g))
	r.g++
	r.inTick = wasTick
}

// gains returns the measured gain sample by sample and the sample each
// period starts at.
func (r *mixRig) gains() (gain []float64, starts []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.aired {
		starts = append(starts, len(gain))
		gain = append(gain, p.gain...)
	}
	return gain, starts
}

// checkContinuous fails when the measured gain steps by more than a cut
// ramp's increment anywhere, at a period boundary above all, or does not
// end at want.
func (r *mixRig) checkContinuous(want float64) {
	r.t.Helper()
	gain, starts := r.gains()
	minN := len(gain)
	for k := range starts {
		end := len(gain)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		minN = min(minN, end-starts[k])
	}
	bound := math.Sin(math.Pi/2/rampPeriods)/float64(minN-1) + 1e-6
	k := 0
	for i := 1; i < len(gain); i++ {
		for k+1 < len(starts) && starts[k+1] <= i {
			k++
		}
		if step := math.Abs(gain[i] - gain[i-1]); step > bound {
			r.t.Fatalf("%s: sample %d (period %d, starting at sample %d): gain steps from %.6f to %.6f, a ramp steps at most %.6f",
				r.measured, i, k, starts[k], gain[i-1], gain[i], bound)
		}
	}
	if got := gain[len(gain)-1]; math.Abs(got-want) > 1e-6 {
		r.t.Fatalf("%s airs at gain %.6f at the end, want %v", r.measured, got, want)
	}
}

// startFade starts a fade to black, or back from black, lasting mixTicks
// ticks or the shortest fade allowed, and returns how many ticks it lasts.
func (r *mixRig) startFade() int {
	r.t.Helper()
	pf := r.s.pipelineFormat.Load()
	ms := max(int(math.Round(mixTicks*1000*float64(pf.FPSDen)/float64(pf.FPSNum))), minFTBDurationMs)
	if err := r.s.SetFTBDuration(ms); err != nil {
		r.t.Fatal(err)
	}
	if err := r.s.FadeToBlack(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	return int(int64(ms) * int64(pf.FPSNum) / (1000 * int64(pf.FPSDen)))
}

// fadeToBlack fades the program to black, or back from black, and runs
// ticks until the switcher reaches state.
func (r *mixRig) fadeToBlack(state State) {
	r.t.Helper()
	r.startFade()
	for range 1000 {
		r.tick()
		if r.s.hotState.Load().state == state {
			return
		}
	}
	r.t.Fatalf("the fade did not reach state %v", state)
}

// mixTicks is the length of the mixes the tests run, in ticks.
const mixTicks = 8

// mixMs is the length of mixTicks ticks in milliseconds.
func mixMs(r *mixRig) int {
	pf := r.s.pipelineFormat.Load()
	return int(math.Round(mixTicks * 1000 * float64(pf.FPSDen) / float64(pf.FPSNum)))
}

// startMix starts a mix to key lasting mixTicks ticks.
func (r *mixRig) startMix(key string) {
	r.t.Helper()
	if err := r.s.StartTransition(r.t.Context(), key, "mix", mixMs(r), ""); err != nil {
		r.t.Fatal(err)
	}
}

// A mix airs the incoming source from the tick it starts on, and each
// tick's audio ends at the position that tick's video shows: the audio
// does not trail the picture by a period. A source that follows video
// fades out alongside and goes off when the mix completes, and the program
// change after it starts no second crossfade; one that does not stays on.
// Periods join without a step, at completion too.
func TestMixAudioFollowsTheTicksVideoPosition(t *testing.T) {
	t.Parallel()
	for _, f := range mixFormats() {
		fps, sr := f.fps, f.sr
		for _, afv := range afvModes() {
			for _, measured := range []string{"mxl:a", "mxl:b"} {
				name := fmt.Sprintf("%dHz_%d_%d/afv=%v/%s", sr, fps.Num, fps.Den, afv, measured)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					r := newMixRig(t, fps, sr, measured, afv, false)
					t.Cleanup(r.close)
					r.startMix("mxl:b")
					var shown []float64 // the position each tick's video shows, while mixing
					for range mixTicks + 4 {
						r.tick()
						if r.m.IsInTransitionCrossfade() {
							shown = append(shown, r.m.TransitionPosition())
						}
					}
					if len(shown) < mixTicks-2 {
						t.Fatalf("the mix ran %d ticks, want about %d", len(shown), mixTicks)
					}

					want := 1.0
					if measured == "mxl:a" && afv {
						want = 0
					}
					r.checkContinuous(want)
					r.mu.Lock()
					defer r.mu.Unlock()
					if len(r.aired) != mixTicks+4 {
						t.Fatalf("%d periods aired, want %d", len(r.aired), mixTicks+4)
					}
					if p := r.aired[0].gain; measured == "mxl:b" && p[len(p)-1] <= 0 {
						t.Fatal("the tick the mix starts on airs none of the incoming source")
					}
					for k, pos := range shown {
						want := math.Sin(pos * math.Pi / 2)
						if measured == "mxl:a" {
							want = 1
							if afv {
								want = math.Cos(pos * math.Pi / 2)
							}
						}
						if got := r.aired[k].gain[len(r.aired[k].gain)-1]; math.Abs(got-want) > 1e-6 {
							t.Fatalf("tick %d: audio ends at gain %.6f, the video shows position %.4f (gain %.6f)",
								k, got, pos, want)
						}
					}
				})
			}
		}
	}
}

// No switcher action steps a source's audio: a mix run to completion, a
// cut to the mix's incoming source during it, a cut to a third source
// during it and a fade to or from black aborted part way each carry every
// source on from the gain it aired, also when the sync loop produces a
// period between the calls an action makes, and leave each source where
// the action puts it.
func TestSwitcherActionsNeverStepTheAudio(t *testing.T) {
	t.Parallel()
	cut := func(r *mixRig, key string) {
		if err := r.s.Cut(r.t.Context(), key); err != nil {
			r.t.Fatal(err)
		}
	}
	cutThenFTB := func(ticks int) func(r *mixRig) {
		return func(r *mixRig) {
			cut(r, "mxl:b")
			r.ticks(ticks)
			r.fadeToBlack(StateFTB)
			r.ticks(rampPeriods + 2)
		}
	}
	cutThenAbort := func(ticks int) func(r *mixRig) {
		return func(r *mixRig) {
			cut(r, "mxl:b")
			r.ticks(1)
			r.startMix("mxl:a")
			r.ticks(ticks)
			r.s.AbortTransition()
			r.ticks(rampPeriods + 2)
		}
	}
	// abortFade aborts the fade under way after the given share of its
	// ticks, by AbortTransition or, as a T-bar pulled back to 0 or the
	// frame watchdog does, from the transition engine, and checks that it
	// was still running. It returns how many samples aired before the
	// abort.
	abortFade := func(r *mixRig, ticks func(n int) int, engine bool) int {
		n := r.startFade()
		r.ticks(ticks(n))
		hs := r.s.hotState.Load()
		if !hs.state.isInTransition() || hs.transEngine == nil {
			r.t.Fatalf("the fade ended before its abort, after %d of %d ticks", ticks(n), n)
		}
		gain, _ := r.gains()
		if engine {
			hs.transEngine.Abort()
		} else {
			r.s.AbortTransition()
		}
		return len(gain)
	}
	first := func(int) int { return 1 }
	half := func(n int) int { return n / 2 }
	almostAll := func(n int) int { return n - 1 }
	fromBlackAborted := func(ticks func(n int) int, engine bool) func(r *mixRig) {
		return func(r *mixRig) {
			r.fadeToBlack(StateFTB)
			r.ticks(1)
			at := abortFade(r, ticks, engine)
			r.ticks(rampPeriods + 2)
			if !r.m.IsProgramMuted() {
				r.t.Fatal("the program is not muted after the aborted fade from black")
			}
			// The picture is black from the abort on, so no source rises
			// after it, also when the sync loop produces a period while
			// the switcher hands the abort to the mixer.
			gain, _ := r.gains()
			for i := at; i < len(gain); i++ {
				if gain[i] > gain[at-1]+1e-9 {
					r.t.Fatalf("%s: sample %d rises to %.6f under black, from %.6f at the abort",
						r.measured, i, gain[i], gain[at-1])
				}
			}
		}
	}
	cutThenMix := func(ticks int) func(r *mixRig) {
		return func(r *mixRig) {
			cut(r, "mxl:b")
			r.ticks(ticks)
			r.startMix("mxl:a")
			r.ticks(mixTicks + 4)
		}
	}
	scenarios := []struct {
		name string
		run  func(r *mixRig)
		on   string   // the source on program at the end, "" when black
		kept []string // the sources still on without AFV besides it
	}{
		{"mix", func(r *mixRig) { r.startMix("mxl:b"); r.ticks(mixTicks + 4) }, "mxl:b", []string{"mxl:a"}},
		{"cut_to_the_preview_during_a_mix", func(r *mixRig) {
			r.startMix("mxl:b")
			r.ticks(mixTicks / 2)
			cut(r, "mxl:b")
			r.ticks(rampPeriods + 2)
		}, "mxl:b", []string{"mxl:a"}},
		{"cut_elsewhere_during_a_mix", func(r *mixRig) {
			r.startMix("mxl:b")
			r.ticks(mixTicks / 2)
			cut(r, "mxl:c")
			r.ticks(rampPeriods + 2)
		}, "mxl:c", []string{"mxl:a"}},
		// A mix as a cut lands or during its ramp takes every source on
		// from the gain it aired.
		{"cut_then_mix_0", cutThenMix(0), "mxl:a", []string{"mxl:b"}},
		{"cut_then_mix_1", cutThenMix(1), "mxl:a", []string{"mxl:b"}},
		{"cut_then_mix_2", cutThenMix(2), "mxl:a", []string{"mxl:b"}},
		{"cut_then_mix_3", cutThenMix(3), "mxl:a", []string{"mxl:b"}},
		// A fade to black as a cut lands or during its ramp takes every
		// source down from the gain it aired.
		{"cut_then_ftb_0", cutThenFTB(0), "", nil},
		{"cut_then_ftb_1", cutThenFTB(1), "", nil},
		{"cut_then_ftb_2", cutThenFTB(2), "", nil},
		{"cut_then_ftb_3", cutThenFTB(3), "", nil},
		// A cut while black and the fade back from black.
		{"cut_while_black_then_ftb_in", func(r *mixRig) {
			r.fadeToBlack(StateFTB)
			cut(r, "mxl:b")
			r.ticks(1)
			r.fadeToBlack(StateIdle)
			r.ticks(rampPeriods + 2)
		}, "mxl:b", []string{"mxl:a"}},
		// A mix back to the source a cut is taking off, aborted at once or
		// midway: every source goes on from the gain it aired.
		{"cut_then_mix_aborted_at_once", cutThenAbort(0), "mxl:b", []string{"mxl:a"}},
		{"cut_then_mix_aborted_midway", cutThenAbort(mixTicks / 2), "mxl:b", []string{"mxl:a"}},
		// A fade from black aborted after its first tick, halfway or
		// almost at its end, by the operator or by the transition engine,
		// returns to black: every source fades back down from the gain it
		// aired, and the program is muted once it is silent.
		{"ftb_reverse_aborted_first", fromBlackAborted(first, false), "", nil},
		{"ftb_reverse_aborted_half", fromBlackAborted(half, false), "", nil},
		{"ftb_reverse_aborted_almost_all", fromBlackAborted(almostAll, false), "", nil},
		{"ftb_reverse_engine_aborted_first", fromBlackAborted(first, true), "", nil},
		{"ftb_reverse_engine_aborted_half", fromBlackAborted(half, true), "", nil},
		{"ftb_reverse_engine_aborted_almost_all", fromBlackAborted(almostAll, true), "", nil},
		// A cut while the aborted fade from black takes the program back
		// down leaves it black; a fade from black started then rises from
		// the gains aired.
		{"ftb_reverse_aborted_then_cut", func(r *mixRig) {
			r.fadeToBlack(StateFTB)
			r.ticks(1)
			abortFade(r, half, true)
			r.tick()
			cut(r, "mxl:b")
			r.ticks(rampPeriods + 2)
			if !r.m.IsProgramMuted() {
				r.t.Fatal("the program is not muted after the aborted fade from black")
			}
		}, "", nil},
		{"ftb_reverse_aborted_then_ftb_in", func(r *mixRig) {
			r.fadeToBlack(StateFTB)
			r.ticks(1)
			abortFade(r, half, false)
			r.tick()
			r.fadeToBlack(StateIdle)
			r.ticks(rampPeriods + 2)
		}, "mxl:a", nil},
		// A fade to black aborted halfway takes every source back up from
		// the gain it aired.
		{"ftb_aborted_half", func(r *mixRig) {
			abortFade(r, half, false)
			r.ticks(rampPeriods + 2)
		}, "mxl:a", nil},
		// A cut during a fade from black takes every source on from the
		// gain it aired.
		{"cut_during_ftb_reverse", func(r *mixRig) {
			r.fadeToBlack(StateFTB)
			r.ticks(1)
			n := r.startFade()
			r.ticks(n / 2)
			cut(r, "mxl:b")
			r.ticks(rampPeriods + 2)
		}, "mxl:b", []string{"mxl:a"}},
		// A mix to a source already on does not dip it.
		{"mix_to_a_source_already_on", func(r *mixRig) {
			r.m.SetActive("mxl:b", true)
			r.tick()
			r.startMix("mxl:b")
			r.ticks(mixTicks + 4)
		}, "mxl:b", []string{"mxl:a"}},
	}
	for _, sc := range scenarios {
		for _, f := range mixFormats() {
			fps, sr := f.fps, f.sr
			for _, afv := range afvModes() {
				for _, interleave := range interleaveModes(sc.name) {
					name := fmt.Sprintf("%s/%dHz_%d_%d/afv=%v/interleave=%v", sc.name, sr, fps.Num, fps.Den, afv, interleave)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						for _, measured := range mixSources {
							func() {
								r := newMixRig(t, fps, sr, measured, afv, interleave)
								defer r.close()
								sc.run(r)
								// With AFV only the program source is on;
								// without it the sources the scenario
								// keeps on stay on too.
								want := 0.0
								if sc.on != "" && (measured == sc.on || !afv && slices.Contains(sc.kept, measured)) {
									want = 1
								}
								r.checkContinuous(want)
							}()
						}
					})
				}
			}
		}
	}
}

// hookedFadeMixer is the mixer with hooks run once each: before and after
// OnTransitionStart, and before and after an abort, by OnTransitionAbort
// or AbortToBlack.
type hookedFadeMixer struct {
	*audio.Mixer
	beforeStart, afterStart, beforeAbort, afterAbort func()
}

// runOnce runs the hook f points to, if any, and clears it.
func runOnce(f *func()) {
	if g := *f; g != nil {
		*f = nil
		g()
	}
}

func (m *hookedFadeMixer) OnTransitionStart(from, to string, mode audio.TransitionMode, durationMs int) uint64 {
	runOnce(&m.beforeStart)
	token := m.Mixer.OnTransitionStart(from, to, mode, durationMs)
	runOnce(&m.afterStart)
	return token
}

func (m *hookedFadeMixer) OnTransitionAbort(token uint64) {
	runOnce(&m.beforeAbort)
	m.Mixer.OnTransitionAbort(token)
	runOnce(&m.afterAbort)
}

func (m *hookedFadeMixer) AbortToBlack(token uint64) {
	runOnce(&m.beforeAbort)
	m.Mixer.AbortToBlack(token)
	runOnce(&m.afterAbort)
}

// A fade from black can be cut short while it is set up, and its abort
// can reach the mixer after a cut has: the program audio follows the
// picture either way. A cut that pre-empts the setup puts the picture up,
// so the program is not muted and the new source airs. An abort that
// pre-empts the setup, landing before or after the mixer starts the fade,
// leaves black, so the program goes down and is muted. An aborted fade
// whose abort reaches the mixer after a cut made on black, its token stale
// then, leaves black too. A fade from black pressed again before the
// abort of the last one reaches the mixer, or after it, while the program
// goes down, brings the picture up, and the program with it. No source
// steps on the way.
func TestFadeFromBlackPreEmptedFollowsThePicture(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name  string
		hook  func(r *mixRig, m *hookedFadeMixer)
		abort bool  // abort the fade from the engine halfway
		state State // where the switcher ends
		on    string
		kept  []string
	}{
		{"cut_during_the_setup", func(r *mixRig, m *hookedFadeMixer) {
			m.afterStart = func() {
				if err := r.s.Cut(r.t.Context(), "mxl:b"); err != nil {
					r.t.Error(err)
				}
			}
		}, false, StateIdle, "mxl:b", []string{"mxl:a"}},
		{"abort_before_the_audio_starts", func(r *mixRig, m *hookedFadeMixer) {
			m.beforeStart = r.s.AbortTransition
		}, false, StateFTB, "", nil},
		{"abort_after_the_audio_starts", func(r *mixRig, m *hookedFadeMixer) {
			m.afterStart = r.s.AbortTransition
		}, false, StateFTB, "", nil},
		{"cut_ahead_of_the_abort", func(r *mixRig, m *hookedFadeMixer) {
			m.beforeAbort = func() {
				if err := r.s.Cut(r.t.Context(), "mxl:b"); err != nil {
					r.t.Error(err)
				}
				r.tick()
			}
		}, true, StateFTB, "", nil},
		{"ftb_in_ahead_of_the_abort", func(r *mixRig, m *hookedFadeMixer) {
			m.beforeAbort = func() {
				if err := r.s.FadeToBlack(r.t.Context()); err != nil {
					r.t.Error(err)
				}
			}
		}, true, StateIdle, "mxl:a", nil},
		{"ftb_in_after_the_abort", func(r *mixRig, m *hookedFadeMixer) {
			m.afterAbort = func() {
				if err := r.s.FadeToBlack(r.t.Context()); err != nil {
					r.t.Error(err)
				}
			}
		}, true, StateIdle, "mxl:a", nil},
	}
	for _, sc := range scenarios {
		for _, f := range mixFormats() {
			for _, afv := range afvModes() {
				name := fmt.Sprintf("%s/%dHz_%d_%d/afv=%v", sc.name, f.sr, f.fps.Num, f.fps.Den, afv)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					for _, measured := range mixSources {
						func() {
							r := newMixRig(t, f.fps, f.sr, measured, afv, false)
							defer r.close()
							r.fadeToBlack(StateFTB)
							r.ticks(1)
							m := &hookedFadeMixer{Mixer: r.m}
							sc.hook(r, m)
							r.s.SetMixer(m)
							// A fade pre-empted during its setup reports
							// so; the hooks check their own calls.
							if !sc.abort {
								_ = r.s.FadeToBlack(r.t.Context())
							} else {
								n := r.startFade()
								r.ticks(n / 2)
								r.s.hotState.Load().transEngine.Abort()
							}
							for i := 0; i < 1000 && r.s.hotState.Load().state != sc.state; i++ {
								r.tick()
							}
							r.ticks(rampPeriods + 2)
							if st := r.s.hotState.Load().state; st != sc.state {
								t.Fatalf("the switcher ends in state %v, want %v", st, sc.state)
							}
							if muted := r.m.IsProgramMuted(); muted != (sc.on == "") {
								t.Fatalf("the program is muted: %v, want %v", muted, sc.on == "")
							}
							want := 0.0
							if sc.on != "" && (measured == sc.on || !afv && slices.Contains(sc.kept, measured)) {
								want = 1
							}
							r.checkContinuous(want)
						}()
					}
				})
			}
		}
	}
}

// A fade from black pressed again before the abort of the last one
// reaches the mixer leaves that abort nothing to take down. Aborted in
// turn, by the operator or from the transition engine, the newer fade
// takes the program back to black and mutes it, with no step.
func TestFadeFromBlackPressedAgainThenAbortedEndsBlack(t *testing.T) {
	t.Parallel()
	for _, f := range mixFormats() {
		for _, afv := range afvModes() {
			for _, engine := range []bool{false, true} {
				name := fmt.Sprintf("%dHz_%d_%d/afv=%v/engine=%v", f.sr, f.fps.Num, f.fps.Den, afv, engine)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					for _, measured := range mixSources {
						func() {
							r := newMixRig(t, f.fps, f.sr, measured, afv, false)
							defer r.close()
							r.fadeToBlack(StateFTB)
							r.ticks(1)
							m := &hookedFadeMixer{Mixer: r.m}
							m.beforeAbort = func() {
								if err := r.s.FadeToBlack(r.t.Context()); err != nil {
									r.t.Error(err)
								}
							}
							r.s.SetMixer(m)
							n := r.startFade()
							r.ticks(n / 2)
							r.s.hotState.Load().transEngine.Abort()
							r.ticks(2)
							hs := r.s.hotState.Load()
							if hs.state != StateFTBReversing || hs.transEngine == nil {
								t.Fatalf("the newer fade from black is not running: state %v", hs.state)
							}
							if engine {
								hs.transEngine.Abort()
							} else {
								r.s.AbortTransition()
							}
							r.ticks(rampPeriods + 2)
							if st := r.s.hotState.Load().state; st != StateFTB {
								t.Fatalf("the switcher ends in state %v, want %v", st, StateFTB)
							}
							if !r.m.IsProgramMuted() {
								t.Fatal("the program is not muted, with the picture black")
							}
							r.checkContinuous(0)
						}()
					}
				})
			}
		}
	}
}

// A tick whose video is shed does not advance a transition, so neither
// its audio nor its video moves on from the position the tick before
// showed, and the next tick advances it again.
func TestShedTickDoesNotAdvanceTheTransition(t *testing.T) {
	t.Parallel()
	for _, f := range mixFormats() {
		t.Run(fmt.Sprintf("%dHz_%d_%d", f.sr, f.fps.Num, f.fps.Den), func(t *testing.T) {
			t.Parallel()
			r := newMixRig(t, f.fps, f.sr, "mxl:b", true, false)
			t.Cleanup(r.close)
			r.startMix("mxl:b")
			r.tick()
			pos := r.m.TransitionPosition()
			if pos <= 0 {
				t.Fatalf("the first tick of the mix shows position %v", pos)
			}
			r.s.ticksBehind = 1
			r.tick()
			if got := r.m.TransitionPosition(); got != pos {
				t.Fatalf("a shed tick moved the transition from %v to %v", pos, got)
			}
			r.tick()
			if got := r.m.TransitionPosition(); got <= pos {
				t.Fatalf("the tick after a shed one left the transition at %v", got)
			}
		})
	}
}

// constPCM returns n stereo samples of value v.
func constPCM(n int, v float32) []float32 {
	pcm := make([]float32, 2*n)
	for i := range pcm {
		pcm[i] = v
	}
	return pcm
}

// hookedSyncMixer is the mixer as the sync loop drives it, with a hook at
// each point a tick's audio passes: before the period is produced, after
// it is produced and before the ramp advances, and after the ramp
// advances, before the tick's video.
type hookedSyncMixer struct {
	*audio.Mixer
	at func(point int)
}

func (m *hookedSyncMixer) ProduceSamples(start uint64, n int, pts int64) []float32 {
	m.at(0)
	pcm := m.Mixer.ProduceSamples(start, n, pts)
	m.at(1)
	return pcm
}

func (m *hookedSyncMixer) AdvanceCrossfade() {
	m.Mixer.AdvanceCrossfade()
	m.at(2)
}

// A cut can land at any point of a tick while the sync loop runs: early
// in the period, before the tick produces its audio; amid it, between
// producing the audio and advancing the ramp; or late, after the audio,
// while the video runs. Wherever it lands, the
// first tick whose video shows the cut airs the ramp's first step, from
// silence to the first increment, and no tick before it airs the new
// source.
func TestCutRampStartsOnTheTickThatShowsTheCut(t *testing.T) {
	t.Parallel()
	for _, f := range mixFormats() {
		for point, at := range []string{"before_the_audio", "amid_the_audio", "after_the_audio"} {
			t.Run(fmt.Sprintf("%dHz_%d_%d/%s", f.sr, f.fps.Num, f.fps.Den, at), func(t *testing.T) {
				t.Parallel()
				r := newMixRig(t, f.fps, f.sr, "mxl:b", true, false)
				t.Cleanup(r.close)
				shown := -1
				r.s.SetOnProgramTick(func(key string, _ int64) {
					if key == "mxl:b" && shown < 0 {
						r.mu.Lock()
						shown = len(r.aired)
						r.mu.Unlock()
					}
				})
				armed := false
				r.s.SetSyncMixer(&hookedSyncMixer{Mixer: r.m, at: func(p int) {
					if armed && p == point {
						armed = false
						if err := r.s.Cut(r.t.Context(), "mxl:b"); err != nil {
							r.t.Error(err)
						}
					}
				}})
				r.ticks(2)
				armed = true
				r.ticks(rampPeriods + 3)

				r.mu.Lock()
				aired := r.aired
				r.mu.Unlock()
				if shown < 0 {
					t.Fatal("no tick showed the cut")
				}
				for k := 0; k < shown; k++ {
					if g := aired[k].gain; g[len(g)-1] != 0 {
						t.Fatalf("period %d airs the new source at %.6f, before the tick that shows the cut (%d)", k, g[len(g)-1], shown)
					}
				}
				first := aired[shown].gain
				if want := math.Sin(math.Pi / 2 / rampPeriods); first[0] > 1e-3 || math.Abs(first[len(first)-1]-want) > 1e-6 {
					t.Fatalf("the tick that shows the cut airs the new source from %.6f to %.6f, want from 0 to %.6f",
						first[0], first[len(first)-1], want)
				}
				r.checkContinuous(1)
			})
		}
	}
}

// handOverMixer is the mixer with a hook inside OnProgramChange, the last
// call a cut makes, and one that reports each OnTransitionStart.
type handOverMixer struct {
	*audio.Mixer
	inProgramChange func()
	started         chan string
}

func (m *handOverMixer) OnProgramChange(source string) {
	if f := m.inProgramChange; f != nil {
		m.inProgramChange = nil
		f()
	}
	m.Mixer.OnProgramChange(source)
}

func (m *handOverMixer) OnTransitionStart(from, to string, mode audio.TransitionMode, durationMs int) uint64 {
	m.started <- to
	return m.Mixer.OnTransitionStart(from, to, mode, durationMs)
}

// A mix started while the tick that takes a cut is handing it to the
// mixer waits until the mixer has all of it: the mix's audio starts after
// the cut's program change, which would otherwise start a cut ramp over
// the mix, and every source goes on from the gain it aired.
func TestTransitionWaitsForTheCutBeingHandedOver(t *testing.T) {
	t.Parallel()
	for _, f := range mixFormats() {
		t.Run(fmt.Sprintf("%dHz_%d_%d", f.sr, f.fps.Num, f.fps.Den), func(t *testing.T) {
			t.Parallel()
			for _, measured := range mixSources {
				func() {
					r := newMixRig(t, f.fps, f.sr, measured, true, false)
					defer r.close()
					hm := &handOverMixer{Mixer: r.m, started: make(chan string, 1)}
					r.s.SetMixer(hm)
					if err := r.s.Cut(r.t.Context(), "mxl:b"); err != nil {
						t.Fatal(err)
					}
					mixed := make(chan error, 1)
					early := false
					hm.inProgramChange = func() {
						go func() { mixed <- r.s.StartTransition(r.t.Context(), "mxl:c", "mix", mixMs(r), "") }()
						// Long enough for a mix that does not wait to
						// reach the mixer.
						select {
						case <-hm.started:
							early = true
							t.Error("the mix reached the mixer while the cut was being handed over")
						case <-time.After(50 * time.Millisecond):
						}
					}
					r.tick()
					if err := <-mixed; err != nil {
						t.Fatal(err)
					}
					if !early {
						<-hm.started
					}
					r.ticks(mixTicks + 4)
					want := 0.0
					if measured == "mxl:c" {
						want = 1
					}
					r.checkContinuous(want)
				}()
			}
		})
	}
}

// A cut made while the clock is stopping, its loop not yet ended, reaches
// the mixer at once: no tick will take it, and stopClock may have taken
// the cuts before it already.
func TestCutWhileTheClockStopsReachesTheMixer(t *testing.T) {
	t.Parallel()
	r := newMixRig(t, clock.Rate{Num: 50, Den: 1}, 48000, "mxl:b", true, false)
	t.Cleanup(r.close)
	r.s.configMu.Lock()
	r.s.clockStopped = true
	r.s.configMu.Unlock()
	if err := r.s.Cut(r.t.Context(), "mxl:b"); err != nil {
		t.Fatal(err)
	}
	if !r.m.IsChannelActive("mxl:b") || !r.m.IsInTransitionCrossfade() {
		t.Fatal("a cut while the clock stops did not reach the mixer")
	}
}

// A cut made before a tick took the one before it hands both to the mixer
// as one, from the source on air before the first.
func TestCutOverAnUntakenCutKeepsItsOutgoingSource(t *testing.T) {
	t.Parallel()
	r := newMixRig(t, clock.Rate{Num: 50, Den: 1}, 48000, "mxl:a", true, false)
	t.Cleanup(r.close)
	for _, key := range []string{"mxl:b", "mxl:c"} {
		if err := r.s.Cut(r.t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	pc := r.s.hotState.Load().programCut
	if pc.from != "mxl:a" || pc.to != "mxl:c" {
		t.Fatalf("the cut runs from %s to %s, want mxl:a to mxl:c", pc.from, pc.to)
	}
	r.ticks(rampPeriods + 2)
	r.checkContinuous(0)
}

// A cut made while the tick hands the cut before it to the mixer waits
// for that hand-over and runs from the source it put on air: the cut
// before it is not handed over twice, nor taken over half done.
func TestCutWaitsForTheCutBeingHandedOver(t *testing.T) {
	t.Parallel()
	r := newMixRig(t, clock.Rate{Num: 50, Den: 1}, 48000, "mxl:c", true, false)
	t.Cleanup(r.close)
	hm := &handOverMixer{Mixer: r.m, started: make(chan string, 1)}
	r.s.SetMixer(hm)
	if err := r.s.Cut(r.t.Context(), "mxl:b"); err != nil {
		t.Fatal(err)
	}
	cut := make(chan error, 1)
	hm.inProgramChange = func() {
		go func() { cut <- r.s.Cut(r.t.Context(), "mxl:c") }()
		time.Sleep(50 * time.Millisecond)
	}
	r.tick()
	if err := <-cut; err != nil {
		t.Fatal(err)
	}
	if pc := r.s.hotState.Load().programCut; pc.from != "mxl:b" || pc.to != "mxl:c" {
		t.Fatalf("the second cut runs from %s to %s, want mxl:b to mxl:c", pc.from, pc.to)
	}
	r.ticks(rampPeriods + 2)
	r.checkContinuous(1)
}
