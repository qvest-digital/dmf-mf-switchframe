package mxl

import (
	"fmt"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// lateRates are the rates the late-audio tests run at: every program
// rate, with 120000/1001 and 240000/1001 for a period that is no whole
// number of 90 kHz ticks, each with one of the sample rates a flow
// carries. The source's video runs at the program rate.
var lateRates = []lateRate{
	{clock.Rate{Num: 24000, Den: 1001}, 44100},
	{clock.Rate{Num: 24, Den: 1}, 48000},
	{clock.Rate{Num: 25, Den: 1}, 96000},
	{clock.Rate{Num: 30000, Den: 1001}, 48000},
	{clock.Rate{Num: 30, Den: 1}, 44100},
	{clock.Rate{Num: 50, Den: 1}, 48000},
	{clock.Rate{Num: 60000, Den: 1001}, 44100},
	{clock.Rate{Num: 60, Den: 1}, 96000},
	{clock.Rate{Num: 100, Den: 1}, 48000},
	{clock.Rate{Num: 120, Den: 1}, 96000},
	{clock.Rate{Num: 120000, Den: 1001}, 48000},
	{clock.Rate{Num: 240, Den: 1}, 44100},
	{clock.Rate{Num: 240000, Den: 1001}, 96000},
}

type lateRate struct {
	video clock.Rate
	audio int
}

// eachRate runs f as a subtest at each of lateRates.
func eachRate(t *testing.T, f func(t *testing.T, r lateRate)) {
	for _, r := range lateRates {
		t.Run(fmt.Sprintf("%d_%dfps_%dHz", r.video.Num, r.video.Den, r.audio), func(t *testing.T) { f(t, r) })
	}
}

// programPeriodAt is the program period clock.ProgramPeriod reports at
// rate r: rounded up to a whole tick.
func programPeriodAt(r clock.Rate) int64 {
	return (90000*r.Den + r.Num - 1) / r.Num
}

// readTicks is how long a read of 10 ms of audio lasts, as producers
// write it, in 90 kHz ticks.
const readTicks = 900

// handover is a grain the mixer got: its media time, the PTS the mixer
// places it at, and when it was handed over.
type handover struct {
	start, placed, at int64
}

// feed runs secs seconds of 10 ms reads at sampleRate through a Source on
// a program clock at rate the test moves. late returns, for read i, how
// long after its last sample's media time it is handed over, and false
// for a read that does not arrive at all. config sets up the source
// further.
func feed(t *testing.T, rate clock.Rate, sampleRate, secs int, late func(i int) (int64, bool), config func(*SourceConfig)) (*Source, []handover) {
	t.Helper()
	var now int64
	savedPeriod, savedNow := programPeriod, nowPTS
	programPeriod = func() int64 { return programPeriodAt(rate) }
	nowPTS = func() int64 { return now }
	t.Cleanup(func() { programPeriod, nowPTS = savedPeriod, savedNow })

	var got []handover
	var start int64
	cfg := SourceConfig{
		FlowName: "late",
		OnRawAudio: func(_ string, _ []float32, pts int64, _ int) {
			got = append(got, handover{start: start, placed: pts, at: now})
		},
	}
	if config != nil {
		config(&cfg)
	}
	src := NewSource(cfg)
	t.Cleanup(src.Stop)
	n := sampleRate / 100
	pcm := [][]float32{make([]float32, n), make([]float32, n)}
	const base = 10 * 90000
	for i := 0; i < secs*100; i++ {
		l, ok := late(i)
		if !ok {
			continue
		}
		start = base + int64(i)*readTicks
		now = start + readTicks + l
		src.processAudioGrain(AudioGrain{PCM: pcm, SampleRate: sampleRate, Channels: 2, PTS: start})
	}
	return src, got
}

// deadline is when a grain placed at placed has to reach the mixer: the
// tick that mixes its first sample reads ReaderDelayGrains periods behind
// itself, a period's audio at a time.
func deadline(placed, period int64) int64 {
	return placed + (clock.ReaderDelayGrains-1)*period
}

// wantShift is how much later a source whose reads are handed over lag
// after their last sample is mixed at program period period: what its
// reads overrun their read point by and lateAudioMargin, up to the next
// millisecond, or nothing when they reach it.
func wantShift(lag, period int64) int64 {
	need := readTicks + lag - (clock.ReaderDelayGrains-1)*period
	if need <= 0 {
		return 0
	}
	return (need + lateAudioMargin + lateAudioStep - 1) / lateAudioStep * lateAudioStep
}

// withinHalfAGrain reports whether k grains at rate r are within half a
// grain of shift ticks.
func withinHalfAGrain(r clock.Rate, k uint64, shift int64) bool {
	d := int64(k)*90000*r.Den - shift*r.Num
	return 2*max(d, -d) <= 90000*r.Den
}

func always(l int64) func(int) (int64, bool) { return func(int) (int64, bool) { return l, true } }

// A GStreamer MXL writer commits its audio 20 to 34 ms behind TAI, past
// the one period a 50 fps program leaves audio, and every period was mixed
// with the rest of its window silent. Once settled every read reaches the
// mixer by its read point, mixed lateAudioMargin later than it needs to
// the millisecond, and the video follows by whole grains to within half a
// grain of the audio.
func TestSource_LateAudioReachesTheMixerByItsReadPoint(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		period := programPeriodAt(r.video)
		for _, lag := range []int64{20 * 90, 27 * 90, 34 * 90, 60 * 90} {
			src, got := feed(t, r.video, r.audio, 6, always(lag), nil)
			want := wantShift(lag, period)
			for _, h := range got {
				if h.at < got[0].at+3*90000 {
					continue
				}
				if h.at > deadline(h.placed, period) {
					t.Fatalf("lag %d: read at %d handed over %d ticks after its read point",
						lag, h.start, h.at-deadline(h.placed, period))
				}
				if shift := h.placed - h.start; shift != want {
					t.Fatalf("lag %d: mixed %d ticks late, want %d", lag, shift, want)
				}
			}
			audio := src.audioShift.Load()
			if k := videoGrains(r.video, audio); !withinHalfAGrain(r.video, k, audio) {
				t.Fatalf("lag %d: audio %d ticks and video %d grains late, want within half a grain",
					lag, audio, k)
			}
		}
	})
}

// The mixer already reads a delayed source that much further back, so the
// delay counts against what the source needs.
func TestSource_LateAudioShiftLeavesOutTheReadDelay(t *testing.T) {
	const lag = 60 * 90
	eachRate(t, func(t *testing.T, r lateRate) {
		for _, delayMs := range []int{0, 10, 40} {
			src, _ := feed(t, r.video, r.audio, 6, always(lag), func(c *SourceConfig) {
				c.AudioReadDelayMs = func() int { return delayMs }
			})
			want := wantShift(lag-int64(delayMs)*90, programPeriodAt(r.video))
			if got := src.audioPlace.shift; got != want {
				t.Fatalf("delay %d ms: shift %d ticks, want %d", delayMs, got, want)
			}
		}
	})
}

// A producer writing ahead of TAI, as srt-mxl-ingest does, is mixed at
// its media time, and one late burst does not move a source.
func TestSource_TimelyAudioKeepsItsMediaTime(t *testing.T) {
	const lead = -5400 // 60 ms ahead
	eachRate(t, func(t *testing.T, r lateRate) {
		src, got := feed(t, r.video, r.audio, 6, func(i int) (int64, bool) {
			if i >= 120 && i < 140 { // a 200 ms stall inside one window
				return lead + 18000, true
			}
			return lead, true
		}, nil)
		for _, h := range got {
			if h.placed != h.start {
				t.Fatalf("read at %d placed at %d, want its media time", h.start, h.placed)
			}
		}
		if v := src.audioShift.Load(); v != 0 {
			t.Fatalf("video moved with a %d tick shift", v)
		}
	})
}

// A start-up whose first seconds arrive later than the rest places the
// source as late as they need, and after a minute of needing less it
// comes back to what the source needs, its audio on time throughout.
func TestSource_LateStartUpComesBackAfterAMinute(t *testing.T) {
	const lag, startUp = 60 * 90, 3 * 100
	eachRate(t, func(t *testing.T, r lateRate) {
		period := programPeriodAt(r.video)
		_, got := feed(t, r.video, r.audio, 75, func(i int) (int64, bool) {
			if i < startUp {
				return lag + 5400, true
			}
			return lag, true
		}, nil)
		for _, h := range got {
			if h.at >= got[0].at+3*90000 && h.at > deadline(h.placed, period) {
				t.Fatalf("read at %d handed over %d ticks after its read point",
					h.start, h.at-deadline(h.placed, period))
			}
		}
		if s, want := got[startUp+200].placed-got[startUp+200].start, wantShift(lag+5400, period); s != want {
			t.Fatalf("after the start-up mixed %d ticks late, want %d", s, want)
		}
		if s, want := got[len(got)-1].placed-got[len(got)-1].start, wantShift(lag, period); s != want {
			t.Fatalf("a minute on mixed %d ticks late, want %d", s, want)
		}
	})
}

// moves counts how often a source's shift moved over secs seconds of
// reads handed over late(i) after their media time, and returns its last
// shift.
func moves(t *testing.T, rate clock.Rate, sampleRate, secs int, late func(i int) int64) (int, int64) {
	t.Helper()
	places := 0
	src, _ := feed(t, rate, sampleRate, secs, func(i int) (int64, bool) { return late(i), true }, func(c *SourceConfig) {
		c.PlaceAudio = func() { places++ }
	})
	return places, src.audioPlace.shift
}

// window is the index of the window read i of feed is handed over in.
func window(i int) int { return i * readTicks / lateAudioWindow }

// A producer's lag wanders by a millisecond for seconds at a time; each
// move would splice the program's audio, so the shift does not follow it.
func TestSource_LateAudioShiftIgnoresAMillisecondOfJitter(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		n, _ := moves(t, r.video, r.audio, 30, func(i int) int64 { return 60*90 + int64(window(i)/2%2)*90 })
		if n != 1 {
			t.Fatalf("shift moved %d times for a 1 ms jitter, want once on the first late windows", n)
		}
	})
}

// A lag that steps up by less than the margin stays within the shift.
func TestSource_LateAudioShiftAbsorbsAStepWithinTheMargin(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		n, _ := moves(t, r.video, r.audio, 20, func(i int) int64 {
			if window(i) < 10 {
				return 60 * 90
			}
			return 64 * 90
		})
		if n != 1 {
			t.Fatalf("shift moved %d times for a 4 ms step, want once on the first late windows", n)
		}
	})
}

// A lag that drops by more than the margin for a minute lowers the shift
// once, to what the source then needs and the margin; one that drops by
// less leaves it.
func TestSource_LateAudioShiftComesDownOnlyForADropBeyondTheMargin(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		period := programPeriodAt(r.video)
		for _, drop := range []int64{4, 7} {
			n, shift := moves(t, r.video, r.audio, 75, func(i int) int64 {
				if window(i) < 5 {
					return (60 + drop) * 90
				}
				return 60 * 90
			})
			high, low := wantShift((60+drop)*90, period), wantShift(60*90, period)
			switch {
			case drop < lateAudioMargin/90 && (n != 1 || shift != high):
				t.Fatalf("a %d ms drop: %d moves, shift %d, want 1 and %d", drop, n, shift, high)
			case drop > lateAudioMargin/90 && (n != 2 || shift != low):
				t.Fatalf("a %d ms drop: %d moves, shift %d, want 2 and %d", drop, n, shift, low)
			}
		}
	})
}

// Windows in which nothing arrived count as needing no shift, so a source
// that comes back on time after a long pause is mixed at its media time.
func TestSource_LateAudioShiftComesDownAcrossAPause(t *testing.T) {
	const reads = 100
	eachRate(t, func(t *testing.T, r lateRate) {
		_, got := feed(t, r.video, r.audio, 80, func(i int) (int64, bool) {
			switch {
			case i < 5*reads:
				return 60 * 90, true
			case i < 75*reads:
				return 0, false
			default:
				return -5400, true
			}
		}, nil)
		last := got[len(got)-1]
		if last.placed != last.start {
			t.Fatalf("after the pause mixed %d ticks late, want its media time", last.placed-last.start)
		}
	})
}

// nowIndex is the index of the grain at rate r that starts about now,
// where the engine timeline is.
func nowIndex(r clock.Rate) uint64 {
	return clock.TimestampToIndex(r, clock.TAINow())
}

// grain returns grain i of a 12x2 video at rate r as the reader delivers
// it.
func grain(r clock.Rate, i uint64) VideoGrain {
	return VideoGrain{V210: makeV210Frame(12, 2), Width: 12, Height: 2, Index: i, Rate: r, PTS: clock.IndexPTS(r, i)}
}

// Every move of the shift has the mixer place the source again, and the
// video follows it by whole grains.
func TestSource_LateAudioShiftPlacesTheMixerAndMovesTheVideo(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		places := 0
		var videoPTS []int64
		src, _ := feed(t, r.video, r.audio, 6, always(60*90), func(c *SourceConfig) {
			c.PlaceAudio = func() { places++ }
			c.OnRawVideo = func(_ string, _ []byte, _, _ int, pts int64) { videoPTS = append(videoPTS, pts) }
		})
		if places == 0 {
			t.Fatal("the shift moved without placing the mixer's channel")
		}
		k := videoGrains(r.video, src.audioShift.Load())
		i := nowIndex(r.video)
		src.processVideoGrain(grain(r.video, i))
		if want := clock.IndexPTS(r.video, i+k); len(videoPTS) != 1 || videoPTS[0] != want || k == 0 {
			t.Fatalf("video placed at %v, want %d, %d grains later", videoPTS, want, k)
		}
	})
}

// A video moved for late audio is taken by the program's ticks exactly
// the grains it moved later, every frame on its own tick. Moved by a
// period rounded to a whole tick instead, a frame at 60000/1001 lands
// half a tick per grain past the read point that should take it, and the
// program repeats one frame and drops the next.
func TestSource_LateVideoIsTakenWholeTicksLater(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		for ms := int64(1); ms <= 500; ms += 7 {
			shift := ms * 90
			k := videoGrains(r.video, shift)
			if !withinHalfAGrain(r.video, k, shift) {
				t.Fatalf("a %d ms shift moves the video %d grains", ms, k)
			}
			var pts []int64
			src := NewSource(SourceConfig{FlowName: "grid", OnRawVideo: func(_ string, _ []byte, _, _ int, p int64) {
				pts = append(pts, p)
			}})
			src.audioShift.Store(shift)
			first, frames := nowIndex(r.video), uint64(64)
			for i := uint64(first); i < first+frames; i++ {
				src.processVideoGrain(grain(r.video, i))
			}
			src.Stop()
			// A tick reading grain n of the program takes the newest
			// frame at or before its PTS, as the FrameSynchronizer does.
			for n := uint64(first) + k; n < first+frames; n++ {
				read := clock.IndexPTS(r.video, n)
				taken := -1
				for j, p := range pts {
					if p <= read {
						taken = j
					}
				}
				if want := int(n - first - k); taken != want {
					t.Fatalf("%d ms shift (%d grains): the tick reading grain %d took frame %d, want %d",
						ms, k, n-first, taken, want)
				}
			}
		}
	})
}

// Data grains, such as timecode and captions, move with their video.
func TestSource_LateDataMovesWithItsVideo(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		var video, data []int64
		src := NewSource(SourceConfig{
			FlowName:    "data",
			OnRawVideo:  func(_ string, _ []byte, _, _ int, p int64) { video = append(video, p) },
			OnDataGrain: func(_ string, _ []byte, p int64) { data = append(data, p) },
		})
		src.audioShift.Store(60 * 90)
		first := nowIndex(r.video)
		for i := first; i < first+10; i++ {
			src.processVideoGrain(grain(r.video, i))
			src.processDataGrain(DataGrain{Index: i, PTS: clock.IndexPTS(r.video, i)})
		}
		src.Stop()
		for j := range video {
			if data[j] != video[j] || video[j] == clock.IndexPTS(r.video, first+uint64(j)) {
				t.Fatalf("data grain %d at %d, its video at %d", j, data[j], video[j])
			}
		}
	})
}

// Without a running program clock grains keep their PTS.
func TestAudioPlacementNeedsAProgramClock(t *testing.T) {
	saved := programPeriod
	programPeriod = func() int64 { return 0 }
	t.Cleanup(func() { programPeriod = saved })
	var p audioPlacement
	for i := 0; i < 1000; i++ {
		if got := p.pts(int64(i), 0); got != int64(i) {
			t.Fatalf("pts %d placed at %d", i, got)
		}
	}
	if p.shift != 0 {
		t.Fatal("shifted without a program clock")
	}
}

// anyRateAudioEncoderFactory opens a recording encoder at any rate.
type anyRateAudioEncoderFactory struct{ enc *limitedAudioEncoder }

func (f *anyRateAudioEncoderFactory) create(int, int) (AudioEnc, error) {
	f.enc = &limitedAudioEncoder{}
	return f.enc, nil
}

// The relay's AAC encoder takes whole 1024-sample frames, which the source
// collects from shorter reads, stamped with their first sample's media
// time and moved with the video, by its whole grains to the nearest tick,
// the same for every frame: a step between frames would be a click.
func TestSource_RelayAudioIsCollectedIntoAACFrames(t *testing.T) {
	eachRate(t, func(t *testing.T, r lateRate) {
		relay := &mockMediaBroadcasterForTest{}
		factory := &anyRateAudioEncoderFactory{}
		src := NewSource(SourceConfig{FlowName: "relay", Relay: relay, AudioEncoderFactory: factory.create})
		const shift = 60 * 90
		src.audioShift.Store(shift)
		src.processVideoGrain(grain(r.video, nowIndex(r.video)))
		k := videoGrains(r.video, shift)
		a := clock.Rate{Num: int64(r.audio), Den: 1}
		first := nowIndex(a)
		// Reads of what the reader takes at a time at this program rate.
		n := uint64(audioReadSize(r.audio, programPeriodAt(r.video)))
		const reads = 100
		for i := uint64(0); i < reads; i++ {
			start := first + i*n
			pcm := [][]float32{make([]float32, n), make([]float32, n)}
			for j := range pcm[0] {
				pcm[0][j] = float32(start - first + uint64(j))
			}
			src.processAudioGrain(AudioGrain{PCM: pcm, SampleRate: r.audio, Channels: 2, Start: start})
		}
		src.Stop()
		frames := relay.getAudioFrames()
		if want := int(reads * n / aacFrameSamples); len(frames) != want {
			t.Fatalf("relay got %d frames from %d reads, want %d", len(frames), reads, want)
		}
		for j, f := range frames {
			s := first + uint64(j)*aacFrameSamples
			// k grains are no whole number of ticks at a rate such as
			// 60000/1001: within half a tick of them.
			moved := f.PTS - clock.IndexPTS(a, s)
			if d := 2 * (moved*r.video.Num - int64(k)*90000*r.video.Den); max(d, -d) > r.video.Num {
				t.Fatalf("frame %d moved %d ticks, want %d grains", j, moved, k)
			}
			if first := frames[0].PTS - clock.IndexPTS(a, first); moved != first {
				t.Fatalf("frame %d moved %d ticks, frame 0 %d", j, moved, first)
			}
			if pcm := factory.enc.pcm[j]; len(pcm) != 2*aacFrameSamples || pcm[0] != float32(s-first) || pcm[2*aacFrameSamples-2] != float32(s-first+aacFrameSamples-1) {
				t.Fatalf("frame %d does not hold samples %d to %d", j, s, s+aacFrameSamples-1)
			}
		}
	})
}
