package audio

import (
	"math"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// sourceRig feeds one source's audio to a mixer in 1024-sample jobs, each as
// its last sample has passed, and reads the program in 20 ms periods 40 ms
// behind the clock, as a 50 fps program does. The left channel is a 1 kHz
// sine and the right one counts the source's samples, at up to half scale
// where the master limiter leaves it alone, so a read shows what the
// source aired and which of its samples it aired where. A slewed rig
// pushes with IngestPCMSlewed; otherwise with IngestPCM, placing the
// source again on its stamps from the first push they moved, as SRT audio
// was on each anchor move.
type sourceRig struct {
	t       *testing.T
	m       *Mixer
	sr      int64
	key     string
	slewed  bool
	now     int64
	start   int64 // TAI of source sample 0 placed on time
	next    int64 // next source sample to push
	readIdx uint64
	prevL   float32
	reads   int
	offset  int64 // offset of the last push
	skew    int64 // samples the stamps run later than offset places them
}

const sourceRigRamp = 1 << 15 // source samples the right channel counts before it wraps

func newSourceRig(t *testing.T, sampleRate int, slewed bool) *sourceRig {
	t.Helper()
	r := &sourceRig{t: t, sr: int64(sampleRate), key: "src", slewed: slewed}
	clock.Epoch()
	r.now = clock.TAINow()
	clock.SetTAISource(func() int64 { return r.now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	r.m = NewMixer(MixerConfig{SampleRate: sampleRate, Channels: 2})
	t.Cleanup(func() { _ = r.m.Close() })
	r.m.AddChannel(r.key)
	r.m.SetActive(r.key, true)
	rate := clock.Rate{Num: r.sr, Den: 1}
	r.start = clock.IndexToTimestamp(rate, clock.TimestampToIndex(rate, r.now))
	r.readIdx = clock.TimestampToIndex(rate, r.now-40_000_000)
	return r
}

// run advances the clock to until, pushing the source's jobs stamped with
// offset(at) ticks later than on time, at the time at their first sample,
// and reading the program each period. check sees each read: its first
// sample index and the aired samples.
func (r *sourceRig) run(until int64, offset func(at int64) int64, check func(idx uint64, out []float32)) {
	const job = 1024
	period := r.sr / 50
	rate := clock.Rate{Num: r.sr, Den: 1}
	for {
		jobEnd := r.start + (r.next+job)*1_000_000_000/r.sr
		readAt := clock.IndexToTimestamp(rate, r.readIdx+uint64(period)) + 40_000_000
		at := min(jobEnd, readAt)
		if at > until {
			return
		}
		r.now = at
		if at == jobEnd {
			pcm := make([]float32, 2*job)
			for i := range job {
				s := r.next + int64(i)
				pcm[2*i] = float32(0.25 * math.Sin(2*math.Pi*1000*float64(s)/float64(r.sr)))
				pcm[2*i+1] = float32(s%sourceRigRamp) / sourceRigRamp / 2
			}
			first := r.start + r.next*1_000_000_000/r.sr
			pts := clock.PTS(first+r.skew*1_000_000_000/r.sr) + offset(first-r.start)
			if r.slewed {
				r.m.IngestPCMSlewed(r.key, pcm, pts, 2)
			} else {
				// Placed again on its stamps from the first push they
				// moved, as SRT audio was on each anchor move.
				r.m.IngestPCM(r.key, pcm, pts, 2)
				if o := offset(first - r.start); o != r.offset {
					r.m.PlaceChannel(r.key)
					r.offset = o
				}
			}
			r.next += job
			continue
		}
		out := r.m.ProduceSamples(r.readIdx, int(period), 0)
		check(r.readIdx, out)
		r.readIdx += uint64(period)
		r.reads++
	}
}

// aired returns the source sample the right channel of out carries at
// frame i, modulo sourceRigRamp.
func aired(out []float32, i int) float64 {
	return float64(out[2*i+1]) * sourceRigRamp * 2
}

// Every read of a source on program that does not continue the audio the
// read before aired counts as a break, whatever the cause, so silence
// cannot air while the counters stay flat. A source placed again on stamps
// that moved 46 ms later airs 46 ms of silence and one 20 ms earlier
// splices 20 ms out, and neither is a realign or an underrun, as a live
// SRT source's anchor moves were not. A live source that stops counts
// once, where its audio runs out, and not when its input ended on purpose
// (EndChannelInput) or is not live, as a clip that ends. A read of a
// source that was off program the period before continues nothing and
// counts none, nor does its audio running out in the first read.
func TestBreaksCountEverySilenceAndSplice(t *testing.T) {
	r := newSourceRig(t, 48000, false)
	moves := map[int64]int64{} // offset from, in ns of source time
	offset := func(at int64) int64 {
		var o int64
		for from, ticks := range moves {
			if at >= from {
				o += ticks
			}
		}
		return o
	}
	none := func(uint64, []float32) {}
	r.run(r.start+4e9, offset, none)
	want := func(breaks int64, what string) {
		t.Helper()
		if b := r.m.ChannelBreaks()[r.key]; b != breaks {
			t.Fatalf("%s: %d breaks, want %d", what, b, breaks)
		}
	}
	want(0, "on its stamps")
	realigns, underruns := r.m.ChannelRealigns()[r.key], r.m.ChannelUnderruns()[r.key]

	moves[4e9] = 46 * 90
	r.run(r.start+6e9, offset, none)
	want(1, "placed again 46 ms later")

	moves[6e9] = -20 * 90
	r.run(r.start+8e9, offset, none)
	want(2, "placed again 20 ms earlier")
	if ra, u := r.m.ChannelRealigns()[r.key], r.m.ChannelUnderruns()[r.key]; ra != realigns || u != underruns {
		t.Fatalf("%d realigns and %d underruns after the placements, want %d and %d: they do not see them", ra, u, realigns, underruns)
	}

	r.m.SetActive(r.key, false)
	r.run(r.start+9e9, offset, none)
	r.m.SetActive(r.key, true)
	r.run(r.start+10e9, offset, none)
	want(2, "back on program")

	// The source, not live, stops for a second, then resumes: a clip that
	// ended and the next.
	stop := func(d int64) {
		stopped := r.now
		r.next += d * r.sr / 1e9
		r.run(stopped+d+1e9, offset, none)
	}
	stop(1e9)
	want(2, "not live, stopped and resumed")

	r.m.SetLiveInput(r.key)
	stop(1e9)
	want(3, "live, stopped and resumed")

	// Off program, it stops; it is taken back to program for the read in
	// which its audio runs out.
	rate := clock.Rate{Num: r.sr, Den: 1}
	period := uint64(r.sr / 50)
	readAt := func() int64 { return clock.IndexToTimestamp(rate, r.readIdx+period) + 40_000_000 }
	r.m.SetActive(r.key, false)
	last := r.start + r.next*1_000_000_000/r.sr + offset(math.MaxInt64)*100_000/9
	r.next += 100 * r.sr
	for clock.IndexToTimestamp(rate, r.readIdx+period) <= last {
		r.run(readAt(), offset, none)
	}
	r.m.SetActive(r.key, true)
	u := r.m.ChannelUnderruns()[r.key]
	r.run(readAt(), offset, none)
	if r.m.ChannelUnderruns()[r.key] == u {
		t.Fatal("the first read on program did not run out of audio: the case does not exercise it")
	}
	want(3, "back on program with its audio running out")

	// The input ends, as an SRT connection that closed, and the audio it
	// had queued is pushed after: running out is still no break.
	r.m.EndChannelInput(r.key)
	r.run(r.now+50e6, offset, none)
	stop(1e9)
	want(3, "live, ended, its queued audio pushed and run out")

	for _, c := range []struct {
		name        string
		live, ended bool
		breaks      int64
	}{{"not live", false, false, 0}, {"live, ended", true, true, 0}, {"live", true, false, 1}} {
		if b := breaksAtABoundaryEnd(t, c.live, c.ended); b != c.breaks {
			t.Fatalf("audio of a source %s ending on a read boundary: %d breaks, want %d", c.name, b, c.breaks)
		}
	}
}

// breaksAtABoundaryEnd pushes a second of audio on the 20 ms read grid,
// as a clip that ends on a period boundary, reads it and 200 ms more, and
// returns the breaks counted: the read after the last runs out at its
// start, not within it.
func breaksAtABoundaryEnd(t *testing.T, live, ended bool) int64 {
	t.Helper()
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("clip")
	m.SetActive("clip", true)
	if live {
		m.SetLiveInput("clip")
	}
	if ended {
		m.EndChannelInput("clip")
	}
	sr := clock.Rate{Num: 48000, Den: 1}
	base := clock.TimestampToIndex(sr, clock.TAINow()) / 960 * 960
	for k := range uint64(50) {
		pcm := make([]float32, 2*960)
		for i := range pcm {
			pcm[i] = 0.1
		}
		m.IngestPCM("clip", pcm, clock.IndexPTS(sr, base+k*960), 2)
	}
	for k := range uint64(60) {
		m.ProduceSamples(base+k*960, 960, 0)
	}
	if u := m.ChannelUnderruns()["clip"]; u != 1 {
		t.Fatalf("%d underruns, want the 1 of the end: the case does not run out", u)
	}
	return m.ChannelBreaks()["clip"]
}
