package audio

import (
	"fmt"
	"testing"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
)

// relay is an arrival-stamped source delivering 1024-sample AAC frames at
// sr on its own cadence, read at program rate r ReadDelay periods behind
// the clock.
type relay struct {
	r        clock.Rate
	sr       int
	jitterNs int64 // every seventh frame arrives this much late
	ppm      int64 // the sender's clock runs this much slow (negative: fast)
	shift    int64 // the cadence starts this many ns after a grain
	secs     int64
	settle   int64 // seconds after which dry reads, moves and offsets count
	// stallAt and stallLen: the frames due in [stallAt, stallAt+stallLen)
	// ns are lost, or with burst arrive together at its end.
	stallAt, stallLen int64
	burst             bool
}

// relayResult is what a relay run measured after its settle.
type relayResult struct {
	dry, splices int64
	moves        int     // changes of the hold
	hold         int64   // the hold at the end, in ticks
	trace        []int64 // the hold each second, in periods
	off          int64   // the largest distance of an on-cadence push from its arrival plus the hold, ns
}

func (o relay) run(t *testing.T) relayResult {
	t.Helper()
	setProgramRate(t, o.r)
	base := clock.IndexToTimestamp(o.r, clock.TimestampToIndex(o.r, 1_798_000_000_000_000_000)) + o.shift
	var now int64
	clock.SetTAISource(func() int64 { return base + now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
		DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("relay:1")
	m.SetActive("relay:1", true)
	frameAt := func(k int64) (int64, bool) {
		at := (k + 1) * 1024 * 1_000_000_000 / int64(o.sr)
		at += at * o.ppm / 1_000_000
		if o.stallLen > 0 && at >= o.stallAt && at < o.stallAt+o.stallLen {
			if !o.burst {
				return -1, false
			}
			return o.stallAt + o.stallLen, false
		}
		if k%7 == 3 {
			return at + o.jitterNs, false
		}
		return at, true
	}
	counts := func() (int64, int64) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		ch := m.channels["relay:1"]
		return ch.underruns, ch.realigns
	}
	d := clock.ReadDelay(o.r)
	first := clock.TimestampToIndex(o.r, base)
	period := clock.ProgramPeriod()
	var res relayResult
	var dry0, splices0, k int64
	settled := false
	hold := int64(0)
	for f := uint64(1); ; f++ {
		n := first + f
		tick := clock.IndexToTimestamp(o.r, n) - base
		if tick > o.secs*1_000_000_000 {
			break
		}
		if !settled && tick >= o.settle*1_000_000_000 {
			settled = true
			dry0, splices0 = counts()
		}
		for {
			at, onTime := frameAt(k)
			if at > tick {
				break
			}
			k++
			if at < 0 {
				continue
			}
			now = at
			m.IngestFrame("relay:1", &media.AudioFrame{
				Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: o.sr, Channels: 2,
			})
			if settled && onTime && o.ppm == 0 {
				m.mu.RLock()
				ch := m.channels["relay:1"]
				d := ch.ringEnd - (base + now + ticksNs(ch.hold.ticks.Load()))
				m.mu.RUnlock()
				res.off = max(res.off, d, -d)
			}
		}
		now = tick
		rs := clock.TimestampToIndex(rate48k, clock.IndexToTimestamp(o.r, n-d))
		re := clock.TimestampToIndex(rate48k, clock.IndexToTimestamp(o.r, n-d+1))
		m.ProduceSamples(rs, int(re-rs), 0)
		if h := m.ArrivalHold("relay:1"); h != hold {
			hold = h
			if settled {
				res.moves++
			}
		}
		if (f-1)%uint64(o.r.Num/o.r.Den) == 0 {
			res.trace = append(res.trace, hold/period)
		}
	}
	dry, splices := counts()
	res.dry, res.splices, res.hold = dry-dry0, splices-splices0, hold
	return res
}

// relayRates and relayJitters are the program rates and jitters the
// relay hold test runs at: under the race detector, which finds nothing
// in its one goroutine, 59.94 fps, where every relay source needs a hold.
func relayRates() ([]clock.Rate, []int64) {
	if raceDetector {
		return []clock.Rate{{Num: 60000, Den: 1001}}, []int64{0, 10_000_000}
	}
	return programRates, []int64{0, 3_000_000, 10_000_000}
}

// A relay source delivers whole AAC frames on its own cadence, 21.3 ms of
// audio at 48 kHz and 23.2 ms at 44.1 kHz, some of them late, and is
// placed at its arrival. Where the audio budget, ReadDelay-1 periods, is
// shorter than a frame and its lateness, every read past the end of a
// frame before the next one arrives runs the source dry: at 50 and 60
// fps, several times a second. Placed later by whole periods, as many as
// its arrivals need, it reads without a dry read or a splice at every
// program rate, from every source rate, its hold settled, its audio on its
// arrival plus the hold to within the source's own jitter.
func TestRelaySourceIsHeldAsLongAsItsArrivalsNeed(t *testing.T) {
	// Each case starts the cadence at another offset from the grain and
	// sample grids, which decides where a read first meets a late frame.
	shift := int64(0)
	rates, jitters := relayRates()
	for _, r := range rates {
		for _, sr := range longSourceRates() {
			for _, jitterNs := range jitters {
				shift = (shift + 1_337_731) % 50_000_000
				t.Run(fmt.Sprintf("%s/%dHz/jitter%dms", rateName(r), sr, jitterNs/1_000_000), func(t *testing.T) {
					res := relay{r: r, sr: sr, jitterNs: jitterNs, shift: shift, secs: 8, settle: 3}.run(t)
					if res.dry != 0 || res.splices != 0 || res.moves != 0 {
						t.Errorf("after 3 s: %d dry reads, %d splices, %d hold moves; hold by second %v",
							res.dry, res.splices, res.moves, res.trace)
					}
					if period := clock.ProgramPeriod(); res.hold%period != 0 {
						t.Errorf("held %d ticks, not a whole number of %d-tick periods", res.hold, period)
					}
					// A late push may be placed at its arrival before the
					// hold first moves, and the pushes after it follow on:
					// never further off than the source's own jitter.
					if res.off > jitterNs+100_000 {
						t.Errorf("a push on its cadence placed %.2f ms off its arrival plus the hold, more than its %d ms jitter",
							float64(res.off)/1e6, jitterNs/1_000_000)
					}
				})
			}
		}
	}
}

// A sender whose clock runs off the program's arrives a little earlier
// or later every second, and its audio, continued push after push, moves
// off its arrival with it until the mixer moves it back. Its hold is
// measured on its arrivals, so it settles once and stays: a hold measured
// on where the audio was placed rose and fell as the audio moved, every
// move a splice on air.
func TestRelaySourceHoldIsStableUnderSenderDrift(t *testing.T) {
	rates := []clock.Rate{{Num: 50, Den: 1}, {Num: 60000, Den: 1001}, {Num: 120, Den: 1}}
	ppms := []int64{-100, -50, 50, 100}
	secs := int64(65)
	if raceDetector {
		rates, ppms, secs = rates[1:2], []int64{100}, 20
	}
	for _, r := range rates {
		for _, ppm := range ppms {
			t.Run(fmt.Sprintf("%s/%dppm", rateName(r), ppm), func(t *testing.T) {
				res := relay{r: r, sr: 48000, jitterNs: 3_000_000, ppm: ppm, secs: secs, settle: 4}.run(t)
				if res.dry != 0 || res.moves != 0 {
					t.Errorf("after 4 s: %d dry reads and %d hold moves; hold by second %v", res.dry, res.moves, res.trace)
				}
			})
		}
	}
}

// A sender that stalls delivers nothing for a while, then either goes on
// or delivers what it held back at once. No hold covers a stall, and a
// hold raised for one would delay the source by the stall for a minute.
// The hold stays what the source's steady arrivals need.
func TestRelaySourceHoldIgnoresAStall(t *testing.T) {
	rates := []clock.Rate{{Num: 25, Den: 1}, {Num: 50, Den: 1}, {Num: 60000, Den: 1001}, {Num: 120, Den: 1}}
	stalls := []int64{50_000_000, 400_000_000, 2_000_000_000}
	if raceDetector {
		rates, stalls = rates[2:3], stalls[1:2]
	}
	for _, r := range rates {
		steady := relay{r: r, sr: 48000, jitterNs: 3_000_000, secs: 8, settle: 4}.run(t)
		for _, stall := range stalls {
			for _, burst := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%dms/burst=%v", rateName(r), stall/1_000_000, burst), func(t *testing.T) {
					res := relay{r: r, sr: 48000, jitterNs: 3_000_000, secs: 14, settle: 4,
						stallAt: 6_300_000_000, stallLen: stall, burst: burst}.run(t)
					for s, h := range res.trace {
						if h*clock.ProgramPeriod() > steady.hold {
							t.Fatalf("held %d periods %d s in, against the %d the steady source needs; hold by second %v",
								h, s, steady.hold/clock.ProgramPeriod(), res.trace)
						}
					}
				})
			}
		}
	}
}

// holdWindows runs a hold through windows of pushes at 50 fps whose need
// is need(w) ns, the source running dry dry(w) times in window w,
// and returns the hold, in periods, after each window.
func holdWindows(t *testing.T, windows int, need func(w int) int64, dry func(w int) int64, placedFrom int) []int64 {
	t.Helper()
	setProgramRate(t, clock.Rate{Num: 50, Den: 1})
	var h arrivalHold
	const span = 20_000_000 // pushes a whole number of times a window
	var dryCount int64
	now := int64(1_000_000_000_000)
	var out []int64
	for w := 0; w <= windows; w++ {
		start := now
		for now < start+arrivalHoldWindow {
			now += span
			h.note(now, span, now-span+need(w), dryCount, w >= placedFrom)
		}
		if w < windows {
			dryCount += dry(w)
		}
		out = append(out, h.ticks.Load()/1800)
	}
	return out[1:]
}

func never(int) int64 { return 0 }

// The hold is raised only in a window in which the source ran dry, to
// that window's largest need: a need without a dry read, as a source
// continued ahead of its arrivals measures, does not raise it, in one
// window or in many. Dry reads before the source's audio was first
// placed, as at a start, do not count, nor does a single one.
func TestArrivalHoldRaises(t *testing.T) {
	once := func(w int) int64 {
		if w == 3 {
			return 30_000_000
		}
		return 0
	}
	always := func(int) int64 { return 30_000_000 }
	if got := holdWindows(t, 8, always, never, 0); got[7] != 0 {
		t.Errorf("windows needing 30 ms without a dry read raised the hold: %v", got)
	}
	ranDry := func(w int) int64 {
		if w == 3 {
			return 2
		}
		return 0
	}
	if got := holdWindows(t, 8, once, ranDry, 0); got[2] != 0 || got[3] != 2 || got[7] != 2 {
		t.Errorf("a window needing 30 ms that ran dry: hold by window %v, want 2 periods from window 3 on", got)
	}
	if got := holdWindows(t, 8, once, ranDry, 5); got[3] != 0 || got[4] != 0 {
		t.Errorf("a window that ran dry before the audio was placed raised the hold: %v", got)
	}
	// One dry read in a window is a start or a stall; one in each of two
	// windows in a row is the cadence.
	needs := func(int) int64 { return 30_000_000 }
	single := func(w int) int64 {
		if w == 3 {
			return 1
		}
		return 0
	}
	if got := holdWindows(t, 8, needs, single, 0); got[7] != 0 {
		t.Errorf("one dry read raised the hold: %v", got)
	}
	pair := func(w int) int64 {
		if w == 3 || w == 4 {
			return 1
		}
		return 0
	}
	if got := holdWindows(t, 8, needs, pair, 0); got[3] != 0 || got[4] != 2 {
		t.Errorf("a dry read in each of two windows: hold by window %v, want 2 periods from window 4 on", got)
	}
}

// The hold comes down only after arrivalHoldHistory windows in a row
// needed a period less, to the most any of them needed, and never more
// than MaxSourceHold, in whole periods.
func TestArrivalHoldLowersAfterAMinute(t *testing.T) {
	need := func(w int) int64 {
		switch {
		case w < 2:
			return 50_000_000
		case w < 2+arrivalHoldHistory:
			return 10_000_000
		}
		return 0
	}
	got := holdWindows(t, 2+2*arrivalHoldHistory+2, need, func(w int) int64 {
		if w == 1 {
			return 2
		}
		return 0
	}, 0)
	if got[1] != 3 {
		t.Fatalf("held %d periods after a window needing 50 ms ran dry, want 3", got[1])
	}
	if got[arrivalHoldHistory] != 3 {
		t.Errorf("lowered %d windows after the raise: %v", arrivalHoldHistory-1, got)
	}
	if got[1+arrivalHoldHistory] != 1 {
		t.Errorf("held %d periods %d windows after the raise, all needing 10 ms, want 1: %v",
			got[1+arrivalHoldHistory], arrivalHoldHistory, got)
	}
	if last := got[len(got)-1]; last != 0 {
		t.Errorf("held %d periods after %d windows needing nothing, want 0", last, arrivalHoldHistory)
	}
	huge := func(int) int64 { return 2_000_000_000 }
	if got := holdWindows(t, 3, huge, func(int) int64 { return 2 }, 0); got[2] == 0 || got[2]*1800 > clock.MaxSourceHold {
		t.Errorf("held %d periods, more than MaxSourceHold", got[2])
	}
}

// A push that arrives more than two AAC frames after the one before is a
// stall or a loss and does not count, however far the read has passed,
// even in windows in which the source ran dry.
func TestArrivalHoldSkipsAPushAfterAGap(t *testing.T) {
	setProgramRate(t, clock.Rate{Num: 50, Den: 1})
	var h arrivalHold
	const span = 21_333_333
	now := int64(1_000_000_000_000)
	for w := 0; w < 4; w++ {
		start := now
		for now < start+arrivalHoldWindow {
			gap := int64(span)
			if now-start < span {
				gap = 3 * span // the first push of every window after a gap
			}
			now += gap
			// The read has just reached the arrival before: no need,
			// but for the push after a gap.
			h.note(now, span, now-span, int64(2*w), true)
		}
	}
	if got := h.ticks.Load(); got != 0 {
		t.Errorf("held %d ticks for pushes after gaps", got)
	}
}

// When the hold moves, the audio buffered before it moves by the same and
// onto the arrival of the pushes on the source's cadence: a placement a
// late push left behind is undone, and the push that brings the move
// continues the moved audio even if it came late.
func TestArrivalHoldMovePlacesTheAudioOnTheCadence(t *testing.T) {
	r := clock.Rate{Num: 50, Den: 1}
	setProgramRate(t, r)
	base := int64(1_798_000_000_000_000_000)
	var now int64
	clock.SetTAISource(func() int64 { return base + now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
		DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("relay:1") // off program: nothing reads it
	push := func(at int64) int64 {
		now = at
		m.IngestFrame("relay:1", &media.AudioFrame{
			Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: 48000, Channels: 2,
		})
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.channels["relay:1"].ringEnd - base
	}
	const frame = 1024 * 1_000_000_000 / 48000
	push(frame)
	// A push 10 ms late, past arrivalLate, is placed at its arrival, and
	// the push on the cadence after it continues it, 10 ms after its own.
	push(2*frame + 10_000_000)
	if end := push(3 * frame); end-(3*frame+10_000_000) > 1_000_000_000/48000 || end-(3*frame+10_000_000) < -1_000_000_000/48000 {
		t.Fatalf("the push after a late one ends %.2f ms after its arrival, want 10", float64(end-3*frame)/1e6)
	}
	// The window closes with the hold raised a period.
	m.mu.Lock()
	ch := m.channels["relay:1"]
	ch.hold.ticks.Store(1800)
	ch.hold.moved, ch.hold.lastOff = 1800, ch.hold.off
	m.mu.Unlock()
	// The push that brings the move arrives 10 ms late.
	end := push(4*frame + 10_000_000)
	if want := int64(4*frame + 20_000_000); end-want > 1_000_000_000/48000 || end-want < -1_000_000_000/48000 {
		t.Errorf("after the move the audio ends %.2f ms off its cadence plus the hold", float64(end-want)/1e6)
	}
}

// Audio forwarded to another channel, as a live source's to a playout
// channel, arrives without its video, which the other channel takes from
// a producer stamping media time. It is never held there, while the
// source's own channel is.
func TestForwardedAudioIsNotHeld(t *testing.T) {
	r := clock.Rate{Num: 60, Den: 1}
	setProgramRate(t, r)
	base := clock.IndexToTimestamp(r, clock.TimestampToIndex(r, 1_798_000_000_000_000_000))
	var now int64
	clock.SetTAISource(func() int64 { return base + now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2,
		DecoderFactory: func(int, int) (Decoder, error) { return constStereoDecoder{}, nil }})
	t.Cleanup(func() { _ = m.Close() })
	for _, key := range []string{"relay:1", "playout"} {
		m.AddChannel(key)
		m.SetActive(key, true)
	}
	m.AddAudioForward("relay:1", "playout")
	first := clock.TimestampToIndex(r, base)
	d := clock.ReadDelay(r)
	var k int64
	for f := uint64(1); f < 8*60; f++ {
		n := first + f
		tick := clock.IndexToTimestamp(r, n) - base
		for ; (k+1)*1024*1_000_000_000/48000 <= tick; k++ {
			now = (k + 1) * 1024 * 1_000_000_000 / 48000
			m.IngestFrame("relay:1", &media.AudioFrame{
				Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: 48000, Channels: 2,
			})
		}
		now = tick
		rs := clock.TimestampToIndex(rate48k, clock.IndexToTimestamp(r, n-d))
		re := clock.TimestampToIndex(rate48k, clock.IndexToTimestamp(r, n-d+1))
		m.ProduceSamples(rs, int(re-rs), 0)
	}
	if h := m.ArrivalHold("relay:1"); h == 0 {
		t.Fatal("the relay source itself was not held; the case does not show the forward")
	}
	if h := m.ArrivalHold("playout"); h != 0 {
		t.Errorf("forwarded audio held %d ticks", h)
	}
}
