package audio

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// The program reads every source the same media time behind the clock,
// and a source may deliver its audio well before that: srt-mxl-ingest
// writes its MXL flow 200 ms and more ahead of TAI, and an MXL reader
// hands it over as soon as it is written. Cutting or mixing to such a
// source has to find the audio due at the read point in its ring.

const (
	cutPeriod  = 960      // 20 ms at 50 fps
	cutReadLag = 2 * 960  // a tick reads two periods behind its grain
	cutLevel   = 0.25     // the incoming source's constant level
	early      = 230 * 48 // srt-mxl-ingest's lead, in samples
)

// cutSource delivers grains of size samples, each at the tick when the
// clock has passed its last sample by lag (negative: ahead of it), until
// the clock reaches stop when that is set.
type cutSource struct {
	key       string
	size, lag int64
	stop      uint64
	next      uint64 // first sample of the next grain
	value     func(index uint64) float32
}

func (s *cutSource) deliver(m *Mixer, now uint64) {
	if s.stop != 0 && now >= s.stop {
		return
	}
	for int64(now)-int64(s.next+uint64(s.size)) >= s.lag {
		pcm := make([]float32, 2*s.size)
		for i := range pcm {
			pcm[i] = s.value(s.next + uint64(i/2))
		}
		m.IngestPCM(s.key, pcm, clock.IndexPTS(rate48k, s.next), 2)
		s.next += uint64(s.size)
	}
}

// period is one tick's program audio and the sample index it was read at.
type period struct {
	read uint64
	pcm  []float32
}

// runCut feeds src on the clock for warm ticks while the program is on
// another source that has no audio, switches the program to src through
// change and returns the periods the program carries from then on. start
// sets src up once the clock is known.
func runCut(t *testing.T, src *cutSource, warm int, start func(now uint64), change func(m *Mixer)) []period {
	t.Helper()
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("other")
	m.SetActive("other", true)
	m.AddChannel(src.key)

	now := clock.TimestampToIndex(rate48k, clock.TAINow())
	now -= now % cutPeriod
	src.next = now - 64*cutPeriod
	if start != nil {
		start(now)
	}
	var out []period
	for tick := 0; tick < warm+40; tick++ {
		src.deliver(m, now)
		if tick == warm {
			change(m)
		}
		read := now - cutReadLag
		pcm := m.ProduceSamples(read, cutPeriod, 0)
		m.AdvanceCrossfade()
		if tick >= warm {
			out = append(out, period{read, append([]float32(nil), pcm...)})
		}
		now += cutPeriod
	}
	return out
}

func cut(m *Mixer, key string) { m.OnCut("other", key) }

func mix(m *Mixer, key string) {
	m.OnTransitionStart("other", key, Crossfade, 200)
	m.OnTransitionPosition(0.5)
}

var changes = map[string]func(*Mixer, string){"cut": cut, "mix": mix}

// requireNoGap fails on a millisecond of silence after the first period,
// in which an equal-power ramp starts the incoming source at zero gain.
func requireNoGap(t *testing.T, periods []period) {
	t.Helper()
	run := 0
	for p, pd := range periods[1:] {
		for i := 0; i < len(pd.pcm); i += 2 {
			if pd.pcm[i] != 0 {
				run = 0
			} else if run++; run == 48 {
				t.Fatalf("1 ms of silence in period %d after the switch, at sample %d", p+1, i/2)
			}
		}
	}
}

func constant(uint64) float32 { return cutLevel }

func TestCutToEarlySourceHasNoGap(t *testing.T) {
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			src := &cutSource{key: "srt", size: 1024, lag: -early, value: constant}
			requireNoGap(t, runCut(t, src, 50, nil, func(m *Mixer) { change(m, src.key) }))
		})
	}
}

// A source delivering each grain by the tick that reads it, and one doing
// so as late as it can, are mixed whole across the switch as well.
func TestCutToTimelyAndLateSourcesHasNoGap(t *testing.T) {
	for name, lag := range map[string]int64{"timely": 0, "late": cutPeriod} {
		for cname, change := range changes {
			t.Run(name+"/"+cname, func(t *testing.T) {
				src := &cutSource{key: "mxl", size: cutPeriod, lag: lag, value: constant}
				requireNoGap(t, runCut(t, src, 50, nil, func(m *Mixer) { change(m, src.key) }))
			})
		}
	}
}

// A source off program for longer than its ring holds is played at the
// read point once on program, not from the oldest audio it kept.
func TestCutToSourceBufferedOffProgramPlaysTheReadPoint(t *testing.T) {
	src := &cutSource{key: "srt", size: 1024, lag: -early, value: func(i uint64) float32 {
		return float32(i%rampMod) / (2 * rampMod)
	}}
	periods := runCut(t, src, 150, nil, func(m *Mixer) { cut(m, src.key) })
	// After the four-period cut ramp the source plays at unity gain.
	for p := 4; p < len(periods); p++ {
		if got, want := firstLeft(periods[p].pcm), periods[p].read%rampMod; got != want {
			t.Fatalf("period %d starts at sample %d, want the read point's %d", p, got, want)
		}
	}
}

// A source that stopped delivering before it goes on program is not
// replayed: everything it left is older than the read point.
func TestCutToStoppedSourceDoesNotReplayItsAudio(t *testing.T) {
	src := &cutSource{key: "gone", size: 1024, lag: 0, value: constant}
	periods := runCut(t, src, 50, func(now uint64) { src.stop = now + 20*cutPeriod },
		func(m *Mixer) { cut(m, src.key) })
	for p, pd := range periods {
		for i, v := range pd.pcm {
			if v != 0 {
				t.Fatalf("period %d sample %d is %v: audio from before the source stopped replayed", p, i/2, v)
			}
		}
	}
}
