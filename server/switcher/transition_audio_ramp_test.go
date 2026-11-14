package switcher

import (
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"sync"
	"testing"

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

func (m *interleavedMixer) OnTransitionAbort(token uint64) {
	m.between()
	m.Mixer.OnTransitionAbort(token)
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

// fadeToBlack fades the program to black, or back from black, and runs
// ticks until the switcher reaches state.
func (r *mixRig) fadeToBlack(state State) {
	r.t.Helper()
	pf := r.s.pipelineFormat.Load()
	ms := max(int(math.Round(mixTicks*1000*float64(pf.FPSDen)/float64(pf.FPSNum))), minFTBDurationMs)
	if err := r.s.SetFTBDuration(ms); err != nil {
		r.t.Fatal(err)
	}
	if err := r.s.FadeToBlack(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
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

// startMix starts a mix to key lasting mixTicks ticks.
func (r *mixRig) startMix(key string) {
	r.t.Helper()
	pf := r.s.pipelineFormat.Load()
	durationMs := int(math.Round(mixTicks * 1000 * float64(pf.FPSDen) / float64(pf.FPSNum)))
	if err := r.s.StartTransition(r.t.Context(), key, "mix", durationMs, ""); err != nil {
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
// cut to the mix's incoming source during it and a cut to a third source
// during it each carry every source on from the gain it aired, also when
// the sync loop produces a period between the calls an action makes, and
// leave each source where the action puts it.
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
