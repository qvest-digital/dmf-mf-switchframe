package audio

import (
	"fmt"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// The program reads every source the same media time behind the clock,
// and a source may deliver its audio well before that: srt-mxl-ingest
// writes its MXL flow 200 ms and more ahead of TAI, and an MXL reader
// hands it over as soon as it is written. Cutting or mixing to such a
// source has to find the audio due at the read point in its ring, at
// every program rate and mixer rate.

const (
	cutLevel = 0.25                   // the incoming source's constant level
	early    = 230 * time.Millisecond // srt-mxl-ingest's lead
)

// cutSource delivers grains, each at the tick when the clock has passed
// its last sample by lag samples (negative: ahead of it), until the clock
// reaches stop when that is set. A grain is size samples, or with size 0
// one program period, as an MXL producer at the program rate commits it.
type cutSource struct {
	key       string
	size, lag int64
	stop      uint64
	next      uint64 // first sample of the next grain
	grain     uint64 // with size 0: the program index of the next grain
	value     func(index uint64) float32
}

// cutClock is one run's program rate r and mixer rate sr.
type cutClock struct {
	r  clock.Rate
	sr int
}

func (c cutClock) samples(n uint64) uint64 {
	return clock.TimestampToIndex(clock.Rate{Num: int64(c.sr), Den: 1}, clock.IndexToTimestamp(c.r, n))
}

// ticks is how many program periods last d.
func (c cutClock) ticks(d time.Duration) int {
	return int(clock.TimestampToIndex(c.r, int64(d)))
}

// budget is how late in samples a grain may arrive and still be read
// whole: the read delay less the period a tick reads, less a sample for
// the rounding of tick times to samples.
func (c cutClock) budget() int64 {
	return (int64(clock.ReadDelay(c.r))-1)*int64(c.sr)*c.r.Den/c.r.Num - 1
}

func (s *cutSource) deliver(m *Mixer, c cutClock, now uint64) {
	if s.stop != 0 && now >= s.stop {
		return
	}
	for {
		end := s.next + uint64(s.size)
		if s.size == 0 {
			end = c.samples(s.grain + 1)
		}
		if int64(now)-int64(end) < s.lag {
			return
		}
		pcm := make([]float32, 2*(end-s.next))
		for i := range pcm {
			pcm[i] = s.value(s.next + uint64(i/2))
		}
		m.IngestPCM(s.key, pcm, clock.IndexPTS(clock.Rate{Num: int64(c.sr), Den: 1}, s.next), 2)
		s.next = end
		s.grain++
	}
}

// period is one tick's program audio and the sample index it was read at.
type period struct {
	read uint64
	pcm  []float32
}

// runCut feeds src on the clock for warm ticks while the program is on
// another source that has no audio, switches the program to src through
// change and returns the periods the program carries from then on, each
// tick reading its period clock.ReadDelay periods behind it. start sets
// src up once the clock is known.
func runCut(t *testing.T, c cutClock, src *cutSource, warm int, start func(now uint64), change func(m *Mixer)) []period {
	t.Helper()
	m := NewMixer(MixerConfig{SampleRate: c.sr, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("other")
	m.SetActive("other", true)
	m.AddChannel(src.key)

	n := clock.TimestampToIndex(c.r, clock.TAINow())
	src.grain = n - 64
	src.next = c.samples(src.grain)
	if start != nil {
		start(c.samples(n))
	}
	d := clock.ReadDelay(c.r)
	var out []period
	for tick := 0; tick < warm+c.ticks(400*time.Millisecond); tick++ {
		src.deliver(m, c, c.samples(n))
		if tick == warm {
			change(m)
		}
		read := c.samples(n - d)
		pcm := m.ProduceSamples(read, int(c.samples(n-d+1)-read), 0)
		m.AdvanceCrossfade()
		if tick >= warm {
			out = append(out, period{read, append([]float32(nil), pcm...)})
		}
		n++
	}
	return out
}

// forEachCutClock runs f at every program rate and mixer rate.
func forEachCutClock(t *testing.T, f func(t *testing.T, c cutClock)) {
	for _, r := range programRates {
		for _, sr := range longSourceRates() {
			c := cutClock{r, sr}
			t.Run(fmt.Sprintf("%s/%dHz", rateName(r), sr), func(t *testing.T) { f(t, c) })
		}
	}
}

func cut(m *Mixer, key string) { m.OnCut("other", key) }

func mix(m *Mixer, key string) {
	m.OnTransitionStart("other", key, Crossfade, 200)
	m.OnTransitionPosition(0.5)
}

var changes = map[string]func(*Mixer, string){"cut": cut, "mix": mix}

// requireNoGap fails on a millisecond of silence after the first period,
// in which an equal-power ramp starts the incoming source at zero gain.
func requireNoGap(t *testing.T, c cutClock, periods []period) {
	t.Helper()
	run := 0
	for p, pd := range periods[1:] {
		for i := 0; i < len(pd.pcm); i += 2 {
			if pd.pcm[i] != 0 {
				run = 0
			} else if run++; run == c.sr/1000 {
				t.Fatalf("1 ms of silence in period %d after the switch, at sample %d", p+1, i/2)
			}
		}
	}
}

func constant(uint64) float32 { return cutLevel }

func TestCutToEarlySourceHasNoGap(t *testing.T) {
	forEachCutClock(t, func(t *testing.T, c cutClock) {
		for name, change := range changes {
			t.Run(name, func(t *testing.T) {
				lead := int64(early) * int64(c.sr) / int64(time.Second)
				src := &cutSource{key: "srt", size: 1024, lag: -lead, value: constant}
				requireNoGap(t, c, runCut(t, c, src, c.ticks(time.Second/2), nil, func(m *Mixer) { change(m, src.key) }))
			})
		}
	})
}

// A source delivering each grain by the tick that reads it, and one doing
// so as late as the read delay lets it, are mixed whole across the switch
// as well.
func TestCutToTimelyAndLateSourcesHasNoGap(t *testing.T) {
	forEachCutClock(t, func(t *testing.T, c cutClock) {
		for name, lag := range map[string]int64{"timely": 0, "late": c.budget()} {
			for cname, change := range changes {
				t.Run(name+"/"+cname, func(t *testing.T) {
					src := &cutSource{key: "mxl", lag: lag, value: constant}
					requireNoGap(t, c, runCut(t, c, src, c.ticks(time.Second/2), nil, func(m *Mixer) { change(m, src.key) }))
				})
			}
		}
	})
}

// A source off program for longer than its ring holds is played at the
// read point once on program, not from the oldest audio it kept.
func TestCutToSourceBufferedOffProgramPlaysTheReadPoint(t *testing.T) {
	forEachCutClock(t, func(t *testing.T, c cutClock) {
		lead := int64(early) * int64(c.sr) / int64(time.Second)
		src := &cutSource{key: "srt", size: 1024, lag: -lead, value: func(i uint64) float32 {
			return float32(i%rampMod) / (2 * rampMod)
		}}
		periods := runCut(t, c, src, c.ticks(3*time.Second), nil, func(m *Mixer) { cut(m, src.key) })
		// After the four-period cut ramp the source plays at unity gain. Its
		// grains are stamped in 90 kHz ticks, which place a mixer above
		// 90 kHz to within a sample.
		tol := int64(c.sr / 90000)
		for p := 4; p < len(periods); p++ {
			got, want := int64(firstLeft(periods[p].pcm)), int64(periods[p].read%rampMod)
			if d := ((got-want)%rampMod + rampMod + rampMod/2) % rampMod; d < rampMod/2-tol || d > rampMod/2+tol {
				t.Fatalf("period %d starts at sample %d, want the read point's %d", p, got, want)
			}
		}
	})
}

// A source that stopped delivering before it goes on program is not
// replayed: everything it left is older than the read point.
func TestCutToStoppedSourceDoesNotReplayItsAudio(t *testing.T) {
	forEachCutClock(t, func(t *testing.T, c cutClock) {
		src := &cutSource{key: "gone", size: 1024, lag: 0, value: constant}
		stop := uint64(200*time.Millisecond) * uint64(c.sr) / uint64(time.Second)
		periods := runCut(t, c, src, c.ticks(time.Second/2), func(now uint64) { src.stop = now + stop },
			func(m *Mixer) { cut(m, src.key) })
		for p, pd := range periods {
			for i, v := range pd.pcm {
				if v != 0 {
					t.Fatalf("period %d sample %d is %v: audio from before the source stopped replayed", p, i/2, v)
				}
			}
		}
	})
}
