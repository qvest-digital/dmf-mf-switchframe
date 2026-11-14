package mxl

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// period50 is a 50 fps program frame period in 90 kHz ticks.
const period50 = 1800

// readTicks is how long a read of defaultSamplesPerRead samples at 48 kHz
// lasts, in 90 kHz ticks.
const readTicks = defaultSamplesPerRead * 90000 / 48000

// handover is a grain the mixer got: its media time, the PTS the mixer
// places it at, and when it was handed over.
type handover struct {
	start, placed, at int64
}

// feed runs secs seconds of reads through a Source on a 50 fps program
// clock the test moves. late returns, for read i, how long after its last
// sample's media time it is handed over, and false for a read that does
// not arrive at all. config sets up the source further.
func feed(t *testing.T, secs int, late func(i int) (int64, bool), config func(*SourceConfig)) (*Source, []handover) {
	t.Helper()
	var now int64
	savedPeriod, savedNow := programPeriod, nowPTS
	programPeriod = func() int64 { return period50 }
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
	pcm := [][]float32{make([]float32, defaultSamplesPerRead), make([]float32, defaultSamplesPerRead)}
	const base = 10 * 90000
	for i := 0; i < secs*48000/defaultSamplesPerRead; i++ {
		l, ok := late(i)
		if !ok {
			continue
		}
		start = base + int64(i)*readTicks
		now = start + readTicks + l
		src.processAudioGrain(AudioGrain{PCM: pcm, SampleRate: 48000, Channels: 2, PTS: start})
	}
	return src, got
}

// deadline is when a grain placed at placed has to reach the mixer: the
// tick that mixes its first sample reads ReaderDelayGrains periods behind
// itself, a period's audio at a time.
func deadline(placed int64) int64 {
	return placed + (clock.ReaderDelayGrains-1)*period50
}

func always(l int64) func(int) (int64, bool) { return func(int) (int64, bool) { return l, true } }

// A GStreamer MXL writer commits its audio 20 to 34 ms behind TAI, past
// the one period a 50 fps program leaves audio, and every period was mixed
// with the rest of its window silent. Once settled every read reaches the
// mixer by its read point, mixed lateAudioMargin later than it needs to
// the millisecond, and the video follows by whole periods to within half
// a period of the audio.
func TestSource_LateAudioReachesTheMixerByItsReadPoint(t *testing.T) {
	for _, lag := range []int64{20 * 90, 27 * 90, 34 * 90} {
		src, got := feed(t, 6, always(lag), nil)
		need := readTicks + lag - period50
		for _, h := range got {
			if h.at < got[0].at+3*90000 {
				continue
			}
			if h.at > deadline(h.placed) {
				t.Fatalf("lag %d: read at %d handed over %d ticks after its read point", lag, h.start, h.at-deadline(h.placed))
			}
			if shift := h.placed - h.start; shift < need+lateAudioMargin || shift >= need+lateAudioMargin+lateAudioStep {
				t.Fatalf("lag %d: mixed %d ticks late, want the %d it needs and the margin, to the ms", lag, shift, need)
			}
		}
		audio, video := src.audioPlace.shift, src.videoShift.Load()
		if video%period50 != 0 || 2*(audio-video) > period50 || 2*(video-audio) > period50 {
			t.Fatalf("lag %d: audio %d ticks and video %d ticks late, want whole periods within half a period", lag, audio, video)
		}
	}
}

// The mixer already reads a delayed source that much further back, so the
// delay counts against what the source needs.
func TestSource_LateAudioShiftLeavesOutTheReadDelay(t *testing.T) {
	const lag = 27 * 90
	need := int64(readTicks + lag - period50)
	for _, delayMs := range []int{0, 10, 40} {
		src, _ := feed(t, 6, always(lag), func(c *SourceConfig) {
			c.AudioReadDelayMs = func() int { return delayMs }
		})
		want := int64(0)
		if late := need - int64(delayMs)*90; late > 0 {
			want = late + lateAudioMargin
		}
		if got := src.audioPlace.shift; got < want || got >= want+lateAudioStep {
			t.Fatalf("delay %d ms: shift %d ticks, want %d", delayMs, got, want)
		}
	}
}

// A producer writing ahead of TAI, as srt-mxl-ingest does, is mixed at
// its media time, and one late burst does not move a source.
func TestSource_TimelyAudioKeepsItsMediaTime(t *testing.T) {
	const lead = -5400 // 60 ms ahead
	src, got := feed(t, 6, func(i int) (int64, bool) {
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
	if v := src.videoShift.Load(); v != 0 {
		t.Fatalf("video shown %d ticks late", v)
	}
}

// A start-up whose first seconds arrive later than the rest places the
// source as late as they need, and after a minute of needing less it
// comes back to what the source needs, its audio on time throughout.
func TestSource_LateStartUpComesBackAfterAMinute(t *testing.T) {
	const lag, startUp = 27 * 90, 3 * 48000 / defaultSamplesPerRead
	_, got := feed(t, 75, func(i int) (int64, bool) {
		if i < startUp {
			return lag + 5400, true
		}
		return lag, true
	}, nil)
	for _, h := range got {
		if h.at >= got[0].at+3*90000 && h.at > deadline(h.placed) {
			t.Fatalf("read at %d handed over %d ticks after its read point", h.start, h.at-deadline(h.placed))
		}
	}
	need := int64(readTicks + lag - period50)
	if s := got[startUp+200].placed - got[startUp+200].start; s < need+5400+lateAudioMargin {
		t.Fatalf("after the start-up mixed %d ticks late, want what it needed, %d, and the margin", s, need+5400)
	}
	if s := got[len(got)-1].placed - got[len(got)-1].start; s < need+lateAudioMargin || s >= need+lateAudioMargin+lateAudioStep {
		t.Fatalf("a minute on mixed %d ticks late, want the %d the source needs and the margin", s, need)
	}
}

// moves counts how often a source's shift moved over secs seconds of
// reads handed over late(i) after their media time, and returns its last
// shift.
func moves(t *testing.T, secs int, late func(i int) int64) (int, int64) {
	t.Helper()
	places := 0
	src, _ := feed(t, secs, func(i int) (int64, bool) { return late(i), true }, func(c *SourceConfig) {
		c.PlaceAudio = func() { places++ }
	})
	return places, src.audioPlace.shift
}

// window is the index of the window read i of feed is handed over in.
func window(i int) int { return i * readTicks / lateAudioWindow }

// A producer's lag wanders by a millisecond for seconds at a time; each
// move would splice the program's audio, so the shift does not follow it.
func TestSource_LateAudioShiftIgnoresAMillisecondOfJitter(t *testing.T) {
	n, _ := moves(t, 30, func(i int) int64 { return 27*90 + int64(window(i)/2%2)*90 })
	if n != 1 {
		t.Fatalf("shift moved %d times for a 1 ms jitter, want once on the first late windows", n)
	}
}

// A lag that steps up by less than the margin stays within the shift.
func TestSource_LateAudioShiftAbsorbsAStepWithinTheMargin(t *testing.T) {
	n, _ := moves(t, 20, func(i int) int64 {
		if window(i) < 10 {
			return 27 * 90
		}
		return 31 * 90
	})
	if n != 1 {
		t.Fatalf("shift moved %d times for a 4 ms step, want once on the first late windows", n)
	}
}

// A lag that drops by more than the margin for a minute lowers the shift
// once, to what the source then needs and the margin; one that drops by
// less leaves it.
func TestSource_LateAudioShiftComesDownOnlyForADropBeyondTheMargin(t *testing.T) {
	for _, drop := range []int64{4, 7} {
		n, shift := moves(t, 75, func(i int) int64 {
			if window(i) < 5 {
				return (27 + drop) * 90
			}
			return 27 * 90
		})
		high := int64(readTicks+(27+drop)*90-period50) + lateAudioMargin
		low := int64(readTicks+27*90-period50) + lateAudioMargin
		switch {
		case drop < lateAudioMargin/90 && (n != 1 || shift != high):
			t.Fatalf("a %d ms drop: %d moves, shift %d, want 1 and %d", drop, n, shift, high)
		case drop > lateAudioMargin/90 && (n != 2 || shift != low):
			t.Fatalf("a %d ms drop: %d moves, shift %d, want 2 and %d", drop, n, shift, low)
		}
	}
}

// Windows in which nothing arrived count as needing no shift, so a source
// that comes back on time after a long pause is mixed at its media time.
func TestSource_LateAudioShiftComesDownAcrossAPause(t *testing.T) {
	const reads = 48000 / defaultSamplesPerRead
	_, got := feed(t, 80, func(i int) (int64, bool) {
		switch {
		case i < 5*reads:
			return 27 * 90, true
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
}

// Every move of the shift has the mixer place the source again, and the
// video follows it.
func TestSource_LateAudioShiftPlacesTheMixerAndMovesTheVideo(t *testing.T) {
	places := 0
	var videoPTS []int64
	src, _ := feed(t, 6, always(27*90), func(c *SourceConfig) {
		c.PlaceAudio = func() { places++ }
		c.OnRawVideo = func(_ string, _ []byte, _, _ int, pts int64) { videoPTS = append(videoPTS, pts) }
	})
	if places == 0 {
		t.Fatal("the shift moved without placing the mixer's channel")
	}
	src.processVideoGrain(VideoGrain{V210: makeV210Frame(12, 2), Width: 12, Height: 2, PTS: 1000})
	if want := 1000 + src.videoShift.Load(); len(videoPTS) != 1 || videoPTS[0] != want || want == 1000 {
		t.Fatalf("video placed at %v, want %d", videoPTS, want)
	}
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
	if p.videoShift() != 0 {
		t.Fatal("video shifted without a program clock")
	}
}

// The relay's AAC encoder takes whole 1024-sample frames, which the source
// collects from shorter reads, stamped with their first sample's media
// time and moved with the video.
func TestSource_RelayAudioIsCollectedIntoAACFrames(t *testing.T) {
	relay := &mockMediaBroadcasterForTest{}
	factory := &limitedAudioEncoderFactory{}
	src := NewSource(SourceConfig{FlowName: "relay", Relay: relay, AudioEncoderFactory: factory.create})
	t.Cleanup(src.Stop)
	src.videoShift.Store(period50)
	const first = 48000 * 100
	for i := uint64(0); i < 9; i++ {
		start := first + i*defaultSamplesPerRead
		pcm := [][]float32{make([]float32, defaultSamplesPerRead), make([]float32, defaultSamplesPerRead)}
		for j := range pcm[0] {
			pcm[0][j] = float32(start + uint64(j))
		}
		src.processAudioGrain(AudioGrain{PCM: pcm, SampleRate: 48000, Channels: 2, Start: start})
	}
	frames := relay.getAudioFrames()
	if len(frames) != 9*defaultSamplesPerRead/aacFrameSamples {
		t.Fatalf("relay got %d frames from 9 reads", len(frames))
	}
	r := clock.Rate{Num: 48000, Den: 1}
	for k, f := range frames {
		s := first + uint64(k)*aacFrameSamples
		if want := clock.IndexPTS(r, s) + period50; f.PTS != want {
			t.Fatalf("frame %d at %d, want %d", k, f.PTS, want)
		}
		if pcm := factory.enc.pcm[k]; len(pcm) != 2*aacFrameSamples || pcm[0] != float32(s) || pcm[2*aacFrameSamples-2] != float32(s+aacFrameSamples-1) {
			t.Fatalf("frame %d does not hold samples %d to %d", k, s, s+aacFrameSamples-1)
		}
	}
}
