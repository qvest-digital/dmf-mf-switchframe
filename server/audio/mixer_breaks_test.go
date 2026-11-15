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
// source aired and which of its samples it aired where. It pushes with
// IngestPCM, placing the source again on its stamps from the first push
// they moved, as SRT audio is on each anchor move.
type sourceRig struct {
	t       *testing.T
	m       *Mixer
	sr      int64
	key     string
	now     int64
	start   int64 // TAI of source sample 0 placed on time
	next    int64 // next source sample to push
	readIdx uint64
	prevL   float32
	reads   int
	offset  int64 // offset of the last push
}

const sourceRigRamp = 1 << 15 // source samples the right channel counts before it wraps

func newSourceRig(t *testing.T, sampleRate int) *sourceRig {
	t.Helper()
	r := &sourceRig{t: t, sr: int64(sampleRate), key: "src"}
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
			pts := clock.PTS(first) + offset(first-r.start)
			r.m.IngestPCM(r.key, pcm, pts, 2)
			if o := offset(first - r.start); o != r.offset {
				r.m.PlaceChannel(r.key)
				r.offset = o
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
// SRT source's anchor moves were not. A source that stops counts once,
// where its audio runs out. A read of a source that was off program the
// period before continues nothing and counts none.
func TestBreaksCountEverySilenceAndSplice(t *testing.T) {
	r := newSourceRig(t, 48000)
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

	// The source stops for a second, then resumes.
	stopped := r.now
	r.next += r.sr
	r.run(stopped+2e9, offset, none)
	want(3, "stopped and resumed")
}
