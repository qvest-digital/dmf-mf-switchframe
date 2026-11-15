package mxl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// A producer may create its flow only once it has something to write, as
// an ingest does with its video on the first frame from its sender, and a
// producer that restarts deletes its flow and creates it again under the
// same id. A source whose flow is not there when it starts reads it once
// it is, from then on, at its media time and with its audio.

// lateFile is one file a lateFlow's writer creates under the flow's id: it
// exists from from on, and until until when that is set, after the start.
type lateFile struct{ from, until time.Duration }

// lateFlow is a flow whose writer creates its files at set times after the
// test started. Each file is written on synctest's clock from its creation
// on, lag behind media time, a grain or a 10 ms batch of samples at a
// time; a handle on a file that is gone reads ErrFlowInvalid, as libmxl
// reports for a flow deleted or created again. An open while no file
// exists fails after openCost, the time an mxlfs lookup waits for a flow.
type lateFlow struct {
	config   FlowConfig
	files    []lateFile
	lag      time.Duration
	openCost time.Duration
	start    time.Time

	mu     sync.Mutex
	opens  int
	inOpen int
	served []lateServed
}

// lateServed is a read a lateFlow answered with written data: the grain's
// index, or the first sample of an audio read, and the file read.
type lateServed struct {
	index uint64
	file  int
}

// lateRing is how many grains a lateFlow's video ring holds, and
// lateAudioRing how many samples its audio ring holds.
const (
	lateRing      = 16
	lateAudioRing = 48000
)

// lateBatch is how many samples a lateFlow's audio writer commits at once.
const lateBatch = 480

const lateW, lateH = 12, 2

var (
	lateVideoShape = func(r Rational) FlowConfig {
		return FlowConfig{Format: DataFormatVideo, GrainRate: r, SliceSizes: [4]uint32{uint32(V210LineStride(lateW))}}
	}
	lateAudioShape = FlowConfig{Format: DataFormatAudio, GrainRate: Rational{48000, 1}, ChannelCount: 2}
	lateDataShape  = FlowConfig{Format: DataFormatData, GrainRate: Rational{50, 1}}
	quietLog       = slog.New(slog.NewTextHandler(io.Discard, nil))
)

// lateY is the luma of every grain of file i of a lateFlow's video.
func lateY(file int) byte { return byte(64 + 100*file) }

func newLateFlow(config FlowConfig, files ...lateFile) *lateFlow {
	return &lateFlow{config: config, files: files, start: time.Now()}
}

func (f *lateFlow) rate() clock.Rate {
	return clock.Rate{Num: f.config.GrainRate.Numerator, Den: f.config.GrainRate.Denominator}
}

// file is the file that exists now, or -1.
func (f *lateFlow) file() int {
	since := time.Since(f.start)
	for i, fl := range f.files {
		if since >= fl.from && (fl.until == 0 || since < fl.until) {
			return i
		}
	}
	return -1
}

func (f *lateFlow) batch() uint64 {
	if f.config.Format == DataFormatAudio {
		return lateBatch
	}
	return 1
}

// indexAt is the head the writer has reached at t: the last index whose
// media time is lag or more before t, down to a whole batch.
func (f *lateFlow) indexAt(t time.Time) uint64 {
	r := f.rate()
	ts := t.Add(-f.lag).UnixNano()
	i := clock.TimestampToIndex(r, ts)
	if clock.IndexToTimestamp(r, i) > ts {
		i--
	}
	return i - i%f.batch()
}

// head is file's head now, or where it stopped once deleted.
func (f *lateFlow) head(file int) uint64 {
	end := time.Now()
	if u := f.files[file].until; u != 0 && end.After(f.start.Add(u)) {
		end = f.start.Add(u)
	}
	return f.indexAt(end)
}

// created is file's head when it was created: nothing up to it is written.
func (f *lateFlow) created(file int) uint64 {
	return f.indexAt(f.start.Add(f.files[file].from))
}

func (f *lateFlow) open() (*lateHandle, error) {
	f.mu.Lock()
	f.opens++
	f.inOpen++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inOpen--
		f.mu.Unlock()
	}()
	file := f.file()
	if file < 0 {
		time.Sleep(f.openCost)
		return nil, fmt.Errorf("stat flow: %w", fs.ErrNotExist)
	}
	return &lateHandle{f: f, file: file, frame: makeV210FrameWithY(lateW, lateH, lateY(file))}, nil
}

func (f *lateFlow) openVideo(string) (DiscreteReader, error) {
	h, err := f.open()
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (f *lateFlow) openAudio(string) (ContinuousReader, error) {
	h, err := f.open()
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (f *lateFlow) stats() (opens, inOpen int, served []lateServed) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens, f.inOpen, append([]lateServed(nil), f.served...)
}

func (f *lateFlow) serve(index uint64, file int) {
	f.mu.Lock()
	f.served = append(f.served, lateServed{index: index, file: file})
	f.mu.Unlock()
}

// lateHandle is a handle on one file of a lateFlow.
type lateHandle struct {
	f     *lateFlow
	file  int
	frame []byte
}

func (h *lateHandle) ConfigInfo() FlowConfig     { return h.f.config }
func (h *lateHandle) HeadIndex() (uint64, error) { return h.f.head(h.file), nil }
func (h *lateHandle) Close() error               { return nil }

// wait returns once index is written, ErrTimeout when it is not within
// timeoutNs, and ErrFlowInvalid once the file is gone.
func (h *lateHandle) wait(index, timeoutNs uint64) error {
	deadline := time.Now().Add(time.Duration(timeoutNs))
	b := h.f.batch()
	next := time.Unix(0, clock.IndexToTimestamp(h.f.rate(), (index+b-1)/b*b)).Add(h.f.lag)
	for {
		if h.f.file() != h.file {
			return fmt.Errorf("%w: flow deleted", ErrFlowInvalid)
		}
		if index <= h.f.head(h.file) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: index %d", ErrTimeout, index)
		}
		time.Sleep(max(min(time.Until(next), time.Until(deadline)), time.Microsecond))
	}
}

func (h *lateHandle) ReadGrain(index, timeoutNs uint64) ([]byte, GrainInfo, error) {
	if err := h.wait(index, timeoutNs); err != nil {
		return nil, GrainInfo{}, err
	}
	if index+lateRing <= h.f.head(h.file) {
		return nil, GrainInfo{}, fmt.Errorf("%w: grain %d", ErrTooLate, index)
	}
	// A slot the writer has not reached since it created the file holds
	// no grain.
	unwritten := index <= h.f.created(h.file)
	if !unwritten {
		h.f.serve(index, h.file)
	}
	return h.frame, GrainInfo{Index: index, GrainSize: uint32(len(h.frame)), TotalSlices: 1, ValidSlices: 1,
		Invalid: unwritten}, nil
}

// ReadSamples reads the count samples before index. A read of one sample
// is how the audio loop waits for the next commit, not a read it hands
// over, and is not counted as served.
func (h *lateHandle) ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error) {
	if err := h.wait(index, timeoutNs); err != nil {
		return nil, err
	}
	start := index - uint64(count)
	if start+lateAudioRing <= h.f.head(h.file) {
		return nil, fmt.Errorf("%w: samples from %d", ErrTooLate, start)
	}
	if count > 1 {
		h.f.serve(start, h.file)
	}
	return [][]float32{make([]float32, count), make([]float32, count)}, nil
}

// lateFrame is what the source handed over: the PTS, the luma of a video
// frame, when, and the shift of the source's audio then.
type lateFrame struct {
	pts   int64
	y     byte
	at    time.Time
	shift int64
}

type lateRun struct {
	mu           sync.Mutex
	video, audio []lateFrame
	data         int
}

func (r *lateRun) frames() (video, audio []lateFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]lateFrame(nil), r.video...), append([]lateFrame(nil), r.audio...)
}

// runLate runs a Source on flows opened as the engine opens those of a
// source keyed by its flows: once at the start, and from then on by the
// reader when that open failed. data may be nil. It stops the source
// after d and returns what the source handed over once Stop returned.
func runLate(t *testing.T, rate Rational, video, audio, data *lateFlow, d time.Duration) *lateRun {
	t.Helper()
	return runLateLogged(t, quietLog, rate, video, audio, data, d)
}

// runLateLogged is runLate with the source logging to log.
func runLateLogged(t *testing.T, log *slog.Logger, rate Rational, video, audio, data *lateFlow, d time.Duration) *lateRun {
	t.Helper()
	run := &lateRun{}
	var src *Source
	src = NewSource(SourceConfig{
		FlowName: "late",
		Width:    lateW,
		Height:   lateH,
		FPSNum:   int(rate.Numerator),
		FPSDen:   int(rate.Denominator),
		Logger:   log,
		OnRawVideo: func(_ string, yuv []byte, _, _ int, pts int64) {
			run.mu.Lock()
			run.video = append(run.video, lateFrame{pts: pts, y: yuv[0], at: time.Now(), shift: src.audioShift.Load()})
			run.mu.Unlock()
		},
		OnRawAudio: func(_ string, _ []float32, pts int64, _ int) {
			run.mu.Lock()
			run.audio = append(run.audio, lateFrame{pts: pts, at: time.Now(), shift: src.audioShift.Load()})
			run.mu.Unlock()
		},
		OnDataGrain: func(string, []byte, int64) {
			run.mu.Lock()
			run.data++
			run.mu.Unlock()
		},
	})

	// In the engine's order: each flow is opened, and its reader made,
	// before the next flow is opened.
	vf, _ := video.openVideo("v")
	vr := NewReopeningReader("v", vf, video.openVideo, quietLog)
	af, _ := audio.openAudio("a")
	ar := NewReopeningAudioReader("a", af, audio.openAudio, quietLog)
	var dr DiscreteReader
	if data != nil {
		df, _ := data.openVideo("d")
		dr = NewReopeningReader("d", df, data.openVideo, quietLog)
	}
	src.Start(context.Background(), vr, ar, dr)
	time.Sleep(d)
	src.Stop()
	return run
}

// checkMediaTime checks that each frame got is one flow served, in order,
// at its media time: a video grain moved by the whole grains of the
// audio's shift, an audio read by the shift itself. Frames still in
// flight at the stop may not have been handed over.
func checkMediaTime(t *testing.T, kind string, flow *lateFlow, got []lateFrame) {
	t.Helper()
	_, _, served := flow.stats()
	if len(got) > len(served) {
		t.Fatalf("%s: %d handed over, %d served", kind, len(got), len(served))
	}
	r := flow.rate()
	for i, g := range got {
		s := served[i]
		want := clock.IndexPTS(r, s.index) + g.shift
		if flow.config.Format == DataFormatVideo {
			want = clock.IndexPTS(r, s.index+videoGrains(r, g.shift))
			if g.y != lateY(s.file) {
				t.Fatalf("%s %d: luma %d, want %d of file %d", kind, i, g.y, lateY(s.file), s.file)
			}
		}
		if g.pts != want {
			t.Fatalf("%s %d: PTS %d, want %d for index %d of file %d moved by %d ticks",
				kind, i, g.pts, want, s.index, s.file, g.shift)
		}
	}
}

// firstOf returns the first frame got of flow's file, and the index it
// was served from.
func firstOf(t *testing.T, kind string, flow *lateFlow, got []lateFrame, file int) (lateFrame, uint64) {
	t.Helper()
	_, _, served := flow.stats()
	for i, g := range got {
		if served[i].file == file {
			return g, served[i].index
		}
	}
	t.Fatalf("%s: nothing handed over from file %d, created %v after the start; %d handed over in all",
		kind, file, flow.files[file].from, len(got))
	return lateFrame{}, 0
}

// sourceReadTimeout is the timeout of a Source's video and data reads,
// which a read sleeps while the flow is being reopened.
const sourceReadTimeout = 100 * time.Millisecond

// lateBound is how long after a flow is there its first frame may reach
// the source's consumer: the retry interval, the reader loop's wait for a
// flow, and a few periods of the flow for the wait until it is written
// here and the reads back to the head. A flow created for the first time
// is waited for by a poll; one created again by a read that times out.
func lateBound(period time.Duration, again bool) time.Duration {
	wait := noFlowPoll
	if again {
		wait = sourceReadTimeout
	}
	return reopenInterval + wait + (grainLookBack+3)*period
}

// checkFirst checks that the first frame of flow's file reached the
// consumer within lateBound of the file's creation, and was then no older
// than a few periods: read at the head, not at a stale position.
func checkFirst(t *testing.T, kind string, flow *lateFlow, got []lateFrame, file int, period time.Duration) lateFrame {
	t.Helper()
	g, index := firstOf(t, kind, flow, got, file)
	created := flow.start.Add(flow.files[file].from)
	if wait, bound := g.at.Sub(created), lateBound(period, file > 0); wait > bound {
		t.Errorf("%s: first frame of file %d %v after it was created, bound %v", kind, file, wait, bound)
	}
	media := time.Unix(0, clock.IndexToTimestamp(flow.rate(), index))
	if age := g.at.Sub(media) - flow.lag; age > (grainLookBack+3)*period || age < 0 {
		t.Errorf("%s: first frame of file %d handed over %v after its media time, want 0 to %v",
			kind, file, age, (grainLookBack+3)*period)
	}
	return g
}

// checkAligned checks that the first video frame of file sits on the
// program's timeline within half a grain of the audio of the same media
// time: how far the video was moved from its media time is compared with
// how far the audio handed over nearest to it in time was.
func checkAligned(t *testing.T, video, audio *lateFlow, v, a []lateFrame, file int) {
	t.Helper()
	g, index := firstOf(t, "video", video, v, file)
	_, _, served := audio.stats()
	if len(a) == 0 {
		t.Fatal("no audio handed over")
	}
	last := 0
	for i := range a {
		if a[i].at.Sub(g.at).Abs() < a[last].at.Sub(g.at).Abs() {
			last = i
		}
	}
	r := video.rate()
	moved := g.pts - clock.IndexPTS(r, index)
	audioMoved := a[last].pts - clock.IndexPTS(audio.rate(), served[last].index)
	half := (90000*r.Den + 2*r.Num - 1) / (2 * r.Num)
	if d := moved - audioMoved; d > half || d < -half {
		t.Errorf("video of index %d moved %d ticks from its media time, its audio %d: %d apart, bound %d",
			index, moved, audioMoved, d, half)
	}
}

// checkContinuous checks that frames of one file follow each other
// without a gap: each a grain or a read after the one before.
func checkContinuous(t *testing.T, kind string, flow *lateFlow) {
	t.Helper()
	_, _, served := flow.stats()
	step := uint64(1)
	if flow.config.Format == DataFormatAudio {
		step = lateBatch
	}
	for i := 1; i < len(served); i++ {
		if served[i].file == served[i-1].file && served[i].index != served[i-1].index+step {
			t.Fatalf("%s: read %d at %d after %d, a gap in a flow that was there throughout",
				kind, i, served[i].index, served[i-1].index)
		}
	}
}

// eachVideoRate runs f in a synctest bubble at each of videoRates.
func eachVideoRate(t *testing.T, f func(t *testing.T, rate Rational, period time.Duration)) {
	for _, rate := range videoRates {
		t.Run(fmt.Sprintf("%d/%d", rate.Numerator, rate.Denominator), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { f(t, rate, ratePeriod(rate)) })
		})
	}
}

// The video flow is created N grains after the source started, its audio
// running throughout: the first grain reaches the switcher, read at the
// head, on the same media time as the audio.
func TestLateFlow_VideoAppearsLate(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		const n = 25
		appear := n * period
		video := newLateFlow(lateVideoShape(rate), lateFile{from: appear})
		audio := newLateFlow(lateAudioShape, lateFile{})
		run := runLate(t, rate, video, audio, nil, appear+2*time.Second)

		v, a := run.frames()
		checkFirst(t, "video", video, v, 0, period)
		checkMediaTime(t, "video", video, v)
		checkMediaTime(t, "audio", audio, a)
		checkAligned(t, video, audio, v, a, 0)
		checkContinuous(t, "audio", audio)
	})
}

// The audio of a source is mixed later when it keeps arriving after the
// mixer's read point, and its video shown that much later in whole grains.
// A video flow created after that shift was settled is shown with it from
// its first frame, so its program A/V matches the source's.
func TestLateFlow_VideoAppearsAfterTheAudioWasShifted(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		cr := clock.Rate{Num: rate.Numerator, Den: rate.Denominator}
		saved, savedDelay, savedNow := programPeriod, programReadDelay, nowPTS
		programPeriod = func() int64 { return programPeriodAt(cr) }
		programReadDelay = func() uint64 { return clock.ReadDelay(cr) }
		nowPTS = func() int64 { return clock.PTS(time.Now().UnixNano()) }
		t.Cleanup(func() { programPeriod, programReadDelay, nowPTS = saved, savedDelay, savedNow })

		appear := 3*time.Second + 7*period
		video := newLateFlow(lateVideoShape(rate), lateFile{from: appear})
		audio := newLateFlow(lateAudioShape, lateFile{})
		audio.lag = 80 * time.Millisecond
		run := runLate(t, rate, video, audio, nil, appear+2*time.Second)

		v, a := run.frames()
		_, _, served := audio.stats()
		if moved := a[0].pts - clock.IndexPTS(audio.rate(), served[0].index); moved != 0 {
			t.Fatalf("first audio mixed %d ticks off its media time, want none before the shift", moved)
		}
		if moved := a[len(a)-1].pts - clock.IndexPTS(audio.rate(), served[len(a)-1].index); videoGrains(cr, moved) == 0 {
			t.Fatalf("audio mixed %d ticks late, want a grain or more", moved)
		}
		checkFirst(t, "video", video, v, 0, period)
		checkAligned(t, video, audio, v, a, 0)
		checkMediaTime(t, "video", video, v)
		checkMediaTime(t, "audio", audio, a)
	})
}

// The audio flow is created after the source started, its video running
// throughout.
func TestLateFlow_AudioAppearsLate(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		appear := 25 * period
		video := newLateFlow(lateVideoShape(rate), lateFile{})
		audio := newLateFlow(lateAudioShape, lateFile{from: appear})
		run := runLate(t, rate, video, audio, nil, appear+2*time.Second)

		v, a := run.frames()
		checkFirst(t, "audio", audio, a, 0, 10*time.Millisecond)
		checkMediaTime(t, "audio", audio, a)
		checkMediaTime(t, "video", video, v)
		checkAligned(t, video, audio, v, a, 0)
		checkContinuous(t, "video", video)
	})
}

// Both flows are created after the source started, at different times.
func TestLateFlow_BothAppearLate(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		video := newLateFlow(lateVideoShape(rate), lateFile{from: 40 * period})
		audio := newLateFlow(lateAudioShape, lateFile{from: 1300 * time.Millisecond})
		run := runLate(t, rate, video, audio, nil, 4*time.Second)

		v, a := run.frames()
		checkFirst(t, "video", video, v, 0, period)
		checkFirst(t, "audio", audio, a, 0, 10*time.Millisecond)
		checkMediaTime(t, "video", video, v)
		checkMediaTime(t, "audio", audio, a)
		checkAligned(t, video, audio, v, a, 0)
		checkContinuous(t, "video", video)
		checkContinuous(t, "audio", audio)
	})
}

// Flows that appear late are deleted and created again under the same id,
// as a producer that restarts does: the source reads the new files, and
// nothing of the old after they were deleted.
func TestLateFlow_DeletedAndRecreated(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		video := newLateFlow(lateVideoShape(rate),
			lateFile{from: 500 * time.Millisecond, until: 1800 * time.Millisecond},
			lateFile{from: 2600 * time.Millisecond})
		audio := newLateFlow(lateAudioShape,
			lateFile{from: 300 * time.Millisecond, until: 2200 * time.Millisecond},
			lateFile{from: 3100 * time.Millisecond})
		run := runLate(t, rate, video, audio, nil, 5*time.Second)

		v, a := run.frames()
		checkFirst(t, "video", video, v, 0, period)
		checkFirst(t, "audio", audio, a, 0, 10*time.Millisecond)
		checkFirst(t, "video", video, v, 1, period)
		checkFirst(t, "audio", audio, a, 1, 10*time.Millisecond)
		checkMediaTime(t, "video", video, v)
		checkMediaTime(t, "audio", audio, a)
		checkAligned(t, video, audio, v, a, 1)
		checkContinuous(t, "video", video)
		checkContinuous(t, "audio", audio)
		_, _, served := video.stats()
		for i := 1; i < len(served); i++ {
			if served[i].file < served[i-1].file {
				t.Fatalf("video read %d is of file %d after file %d", i, served[i].file, served[i-1].file)
			}
		}
	})
}

// Flows that never appear are retried for as long as the source runs, and
// no longer: when Stop returns no open is under way, as one would use the
// instance its caller closes next, and none follows. An open of a missing
// flow takes seconds on an mxlfs.
func TestLateFlow_NeverAppearsStopsCleanly(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		const cost = 2 * time.Second
		flows := map[string]*lateFlow{
			"video": newLateFlow(lateVideoShape(rate)),
			"audio": newLateFlow(lateAudioShape),
			"data":  newLateFlow(lateDataShape),
		}
		for _, f := range flows {
			f.openCost = cost
		}
		// The engine's opens at the start take a cost each, one after the
		// other, and each reader retries from when it was made: a retry
		// is under way at any time from the first reader on, and the
		// source stops with each reader having retried at least twice.
		run := runLate(t, rate, flows["video"], flows["audio"], flows["data"], 5500*time.Millisecond)

		opensAtStop := map[string]int{}
		for name, f := range flows {
			opens, inOpen, _ := f.stats()
			if inOpen != 0 {
				t.Errorf("%s: %d opens still under way after Stop returned", name, inOpen)
			}
			// One at the start, then the reader's own.
			if opens < 3 {
				t.Errorf("%s: opened %d times while the source ran, want the start's and two retries", name, opens)
			}
			opensAtStop[name] = opens
		}
		time.Sleep(10 * reopenInterval)
		for name, f := range flows {
			if opens, _, _ := f.stats(); opens != opensAtStop[name] {
				t.Errorf("%s: opened %d more times after Stop", name, opens-opensAtStop[name])
			}
		}
		v, a := run.frames()
		if len(v) != 0 || len(a) != 0 || run.data != 0 {
			t.Errorf("handed over %d video, %d audio, %d data grains of flows that never existed", len(v), len(a), run.data)
		}
	})
}

// The readers of a source whose flows are all missing each have an open
// under way most of the time, and an open of a missing flow takes seconds
// on an mxlfs. Stop ends every reader's retries before it waits for any,
// so it waits for the opens under way together: about one open, not one
// after another while the readers not yet closed start new ones.
func TestLateFlow_StopWaitsForOpensTogether(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		const cost = 2 * time.Second
		video, audio, data := newLateFlow(lateVideoShape(rate)), newLateFlow(lateAudioShape), newLateFlow(lateDataShape)
		for _, f := range []*lateFlow{video, audio, data} {
			f.openCost = cost
		}
		// Made a retry interval apart, so the readers' opens and waits are
		// out of step: at the stop, the video's open has 1.5 s to go, the
		// audio starts one in 0.5 s and the data one in 1.5 s.
		vr := NewReopeningReader("v", nil, video.openVideo, quietLog)
		time.Sleep(reopenInterval)
		ar := NewReopeningAudioReader("a", nil, audio.openAudio, quietLog)
		time.Sleep(reopenInterval)
		dr := NewReopeningReader("d", nil, data.openVideo, quietLog)
		src := NewSource(SourceConfig{FlowName: "late", Width: lateW, Height: lateH,
			FPSNum: int(rate.Numerator), FPSDen: int(rate.Denominator), Logger: quietLog})
		src.Start(context.Background(), vr, ar, dr)
		time.Sleep(1500 * time.Millisecond)

		start := time.Now()
		src.Stop()
		if took := time.Since(start); took > cost {
			t.Errorf("Stop took %v, want at most one open, %v", took, cost)
		}
		for name, f := range map[string]*lateFlow{"video": video, "audio": audio, "data": data} {
			if _, inOpen, _ := f.stats(); inOpen != 0 {
				t.Errorf("%s: %d opens still under way after Stop returned", name, inOpen)
			}
		}
	})
}

// logRecords keeps every record logged through it.
type logRecords struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	l.recs = append(l.recs, r.Clone())
	l.mu.Unlock()
	return nil
}
func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecords) WithGroup(string) slog.Handler      { return l }

// of returns the records logged with msg.
func (l *logRecords) of(msg string) []slog.Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []slog.Record
	for _, r := range l.recs {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

// attr returns the value of r's attribute key.
func attr(r slog.Record, key string) (v slog.Value, ok bool) {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v, ok = a.Value, true
			return false
		}
		return true
	})
	return v, ok
}

// A source waiting for a flow reads nothing and loses nothing, so the wait
// itself shows: the reader reports no flow open until the flow is read,
// and warns that the flow is missing at the first failed open and then
// once a minute, not at every retry. The open is logged as one, not as a
// move from another flow.
func TestLateFlow_WaitingShowsInFlowOpenAndLog(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		// Retries fail at once here, a reopenInterval apart, so a warning
		// is due at the first retry a retryWarnEvery after the last one.
		warnStep := (retryWarnEvery + reopenInterval - 1) / reopenInterval * reopenInterval
		appear := 2*warnStep + warnStep/2
		video := newLateFlow(lateVideoShape(rate), lateFile{from: appear})
		logged := &logRecords{}
		w := NewReopeningReader("v", nil, video.openVideo, slog.New(logged))
		r := NewVideoReader(ReaderConfig{BufSize: 1, TimeoutMs: 100, Width: lateW, Height: lateH, Logger: quietLog})
		ctx, cancel := context.WithCancel(context.Background())
		r.StartVideo(ctx, w)
		go func() {
			for range r.Video() {
			}
		}()
		open := w.(interface{ FlowOpen() bool })

		time.Sleep(appear - time.Millisecond)
		if open.FlowOpen() {
			t.Error("flow open before it exists")
		}
		var warned []time.Duration
		for _, rec := range logged.of("mxl: flow not open yet, retrying") {
			if rec.Level == slog.LevelWarn {
				warned = append(warned, rec.Time.Sub(video.start))
			}
		}
		if want := []time.Duration{0, warnStep, 2 * warnStep}; fmt.Sprint(warned) != fmt.Sprint(want) {
			t.Errorf("warned of the missing flow at %v, want %v", warned, want)
		}

		time.Sleep(lateBound(period, false))
		if !open.FlowOpen() {
			t.Errorf("flow not open %v after it appeared", lateBound(period, false))
		}
		if n := len(logged.of("mxl: flow opened")); n != 1 {
			t.Errorf("flow opened logged %d times, want once", n)
		}
		if n := len(logged.of("mxl: input moved to another flow")); n != 0 {
			t.Errorf("first open logged as a move %d times", n)
		}

		cancel()
		r.Wait()
		_ = w.Close()
		if open.FlowOpen() {
			t.Error("flow open after Close")
		}
	})
}

// The audio of a source can be shifted before its video's rate is known:
// that log line carries no video grains, which would read as none, and the
// shift is logged with its grains once the first video grain arrives.
func TestLateFlow_ShiftLoggedWithVideoGrainsOnceKnown(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		cr := clock.Rate{Num: rate.Numerator, Den: rate.Denominator}
		saved, savedDelay, savedNow := programPeriod, programReadDelay, nowPTS
		programPeriod = func() int64 { return programPeriodAt(cr) }
		programReadDelay = func() uint64 { return clock.ReadDelay(cr) }
		nowPTS = func() int64 { return clock.PTS(time.Now().UnixNano()) }
		t.Cleanup(func() { programPeriod, programReadDelay, nowPTS = saved, savedDelay, savedNow })

		appear := 3*time.Second + 7*period
		video := newLateFlow(lateVideoShape(rate), lateFile{from: appear})
		audio := newLateFlow(lateAudioShape, lateFile{})
		audio.lag = 80 * time.Millisecond
		logged := &logRecords{}
		runLateLogged(t, slog.New(logged), rate, video, audio, nil, appear+2*time.Second)

		shifts := logged.of("mxl source: audio arrives after the mixer's read point, mixing it later")
		if len(shifts) == 0 || !shifts[0].Time.Before(video.start.Add(appear)) {
			t.Fatal("audio not shifted before the video appeared")
		}
		var grains []uint64
		for _, rec := range append(shifts, logged.of("mxl source: video shown later to stay with its audio")...) {
			v, ok := attr(rec, "video_shift_grains")
			if !ok {
				continue
			}
			if rec.Time.Before(video.start.Add(appear)) {
				t.Errorf("%q logged %d video grains before the video's rate was known", rec.Message, v.Uint64())
			}
			grains = append(grains, v.Uint64())
		}
		if len(grains) == 0 || grains[0] == 0 {
			t.Errorf("video grains of the shift logged as %v once the video appeared, want a grain or more", grains)
		}
	})
}

// sparseData is a data flow whose head stays where its one grain put it,
// as that of a source carrying ancillary data only now and then does for
// longer than a retarget waits.
type sparseData struct{ config FlowConfig }

func (sparseData) ReadGrain(_, timeoutNs uint64) ([]byte, GrainInfo, error) {
	time.Sleep(time.Duration(timeoutNs))
	return nil, GrainInfo{}, ErrTimeout
}
func (f sparseData) ConfigInfo() FlowConfig   { return f.config }
func (sparseData) HeadIndex() (uint64, error) { return 1000, nil }
func (sparseData) Close() error               { return nil }

// A data flow that has received a grain is read, even when no second one
// follows before a retarget's wait is up: it would otherwise be reopened
// for as long as its source carries no ancillary data.
func TestLateFlow_SparseDataFlowIsRead(t *testing.T) {
	eachVideoRate(t, func(t *testing.T, rate Rational, period time.Duration) {
		flow := sparseData{FlowConfig{Format: DataFormatData, GrainRate: rate}}
		w := NewReopeningReader("d", nil, func(string) (DiscreteReader, error) { return flow, nil }, quietLog)
		defer func() { _ = w.Close() }()
		time.Sleep(writtenTimeout + 2*reopenInterval)

		if _, _, err := w.ReadGrain(1000, uint64(period)); !errors.Is(err, ErrTooLate) {
			t.Errorf("first read = %v, want ErrTooLate from the data flow taken over", err)
		}
		if !w.(interface{ FlowOpen() bool }).FlowOpen() {
			t.Error("data flow not open")
		}
	})
}
