package mxl

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// The reader/producer contract: a grain's PTS is its index's TAI on the
// engine timeline, whatever was read before it, so a skip or an invalid
// grain costs exactly its own time and video and audio at the same TAI
// carry the same PTS.

// indexedRing serves grains by index from tail to head. Indices below tail
// are TOO_LATE, above head TOO_EARLY; invalid holds indices written
// INVALID by the producer.
type indexedRing struct {
	mu         sync.Mutex
	tail, head uint64
	start      uint64 // HeadIndex until the jump: where the reader begins
	invalid    map[uint64]bool
	rate       Rational
	reads      []uint64
	// After jumpAfter successful reads the producer is at jumpTo and the
	// ring holds nothing older: the reader falls behind its tail.
	jumpAfter, served int
	jumpTo            uint64
}

func (r *indexedRing) ReadGrain(index uint64, _ uint64) ([]byte, GrainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, index)
	if r.jumpAfter > 0 && r.served == r.jumpAfter {
		r.tail, r.head, r.start = r.jumpTo, r.jumpTo+10, r.jumpTo+10
		r.jumpAfter = 0
	}
	switch {
	case index < r.tail:
		return nil, GrainInfo{}, fmt.Errorf("%w: index %d", ErrTooLate, index)
	case index > r.head:
		return nil, GrainInfo{}, fmt.Errorf("%w: index %d", ErrTooEarly, index)
	}
	r.served++
	return []byte{byte(index)}, GrainInfo{Index: index, GrainSize: 1, TotalSlices: 1,
		ValidSlices: 1, Invalid: r.invalid[index]}, nil
}

func (r *indexedRing) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatVideo, GrainRate: r.rate}
}

func (r *indexedRing) HeadIndex() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.start, nil
}

func (r *indexedRing) Close() error { return nil }

func collectVideo(t *testing.T, flow DiscreteReader, n int) []VideoGrain {
	t.Helper()
	rd := NewVideoReader(ReaderConfig{BufSize: 64, TimeoutMs: 1, Width: 2, Height: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rd.StartVideo(ctx, flow)
	var got []VideoGrain
	for g := range rd.Video() {
		got = append(got, g)
		if len(got) == n {
			cancel()
		}
	}
	rd.Wait()
	if len(got) < n {
		t.Fatalf("got %d grains, want %d", len(got), n)
	}
	return got
}

func TestVideoGrainPTSIsItsIndexOnTAI(t *testing.T) {
	eachRate(t, func(t *testing.T, lr lateRate) {
		cr := lr.video
		head := clock.TimestampToIndex(cr, clock.TAINow())
		flow := &indexedRing{tail: head - 100, head: head + 10, start: head, rate: Rational{cr.Num, cr.Den},
			invalid: map[uint64]bool{head + 2: true}}

		got := collectVideo(t, flow, 6)
		prev := uint64(0)
		for i, g := range got {
			if g.Index <= prev && i > 0 {
				t.Fatalf("grain %d: index %d after %d", i, g.Index, prev)
			}
			prev = g.Index
			if want := clock.IndexPTS(cr, g.Index); g.PTS != want || g.Rate != cr {
				t.Fatalf("grain %d (index %d): PTS %d at %v, want %d at %v", i, g.Index, g.PTS, g.Rate, want, cr)
			}
		}
		// The invalid grain is not delivered, and the next one is two
		// grains after the one before it.
		for i := 1; i < len(got); i++ {
			if want := clock.IndexPTS(cr, head+3) - clock.IndexPTS(cr, head+1); got[i].Index == head+3 && got[i].PTS-got[i-1].PTS != want {
				t.Fatalf("PTS step across an invalid grain is %d, want %d", got[i].PTS-got[i-1].PTS, want)
			}
		}
	})
}

// After TOO_LATE the reader moves to the head; the PTS moves with it by
// exactly the indices skipped, instead of continuing a counter.
func TestVideoResyncAdvancesPTSBySkippedGrains(t *testing.T) {
	eachRate(t, func(t *testing.T, lr lateRate) {
		cr := lr.video
		head := clock.TimestampToIndex(cr, clock.TAINow())
		flow := &indexedRing{tail: head - 10, head: head + 1, start: head, rate: Rational{cr.Num, cr.Den},
			jumpAfter: 2, jumpTo: head + 300}

		got := collectVideo(t, flow, 4)
		for _, g := range got {
			if want := clock.IndexPTS(cr, g.Index); g.PTS != want {
				t.Fatalf("index %d: PTS %d, want %d", g.Index, g.PTS, want)
			}
		}
		if got[2].Index < head+300 {
			t.Fatalf("third grain index %d: the reader did not resync past the skip", got[2].Index)
		}
		if step, want := got[2].PTS-got[1].PTS, clock.IndexPTS(cr, got[2].Index)-clock.IndexPTS(cr, got[1].Index); step != want || got[2].Index-got[1].Index < 290 {
			t.Fatalf("PTS stepped %d across a skip from %d to %d, want %d", step, got[1].Index, got[2].Index, want)
		}
	})
}

// libmxl's ReadSamples(index, count) returns [index-count, index): the PTS
// of a read is the TAI of its first sample, index - count.
type sampleRing struct {
	mu         sync.Mutex
	tail, head uint64
	start      uint64 // HeadIndex: where the reader begins
	reads      [][2]uint64
	rate       int64 // sample rate; 48 kHz when zero
}

func (r *sampleRing) ReadSamples(index uint64, count int, _ uint64) ([][]float32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, [2]uint64{index, uint64(count)})
	switch {
	case index-uint64(count) < r.tail:
		return nil, fmt.Errorf("%w: index %d", ErrTooLate, index)
	case index > r.head:
		return nil, fmt.Errorf("%w: index %d", ErrTooEarly, index)
	}
	return [][]float32{make([]float32, count), make([]float32, count)}, nil
}

func (r *sampleRing) ConfigInfo() FlowConfig {
	rate := r.rate
	if rate == 0 {
		rate = 48000
	}
	return FlowConfig{Format: DataFormatAudio, GrainRate: Rational{rate, 1}, ChannelCount: 2}
}

func (r *sampleRing) HeadIndex() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.start, nil
}

func (r *sampleRing) Close() error { return nil }

func collectAudio(t *testing.T, flow ContinuousReader, n int) []AudioGrain {
	t.Helper()
	rd := NewAudioReader(ReaderConfig{BufSize: 64, TimeoutMs: 1, SamplesPerRead: 1024})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rd.StartAudio(ctx, flow)
	var got []AudioGrain
	for g := range rd.Audio() {
		got = append(got, g)
		if len(got) == n {
			cancel()
		}
	}
	rd.Wait()
	if len(got) < n {
		t.Fatalf("got %d reads, want %d", len(got), n)
	}
	return got
}

func TestAudioReadPTSIsItsFirstSampleOnTAI(t *testing.T) {
	for _, rate := range []int64{44100, 48000, 96000} {
		ra := clock.Rate{Num: rate, Den: 1}
		head := clock.TimestampToIndex(ra, clock.TAINow())
		flow := &sampleRing{tail: head - uint64(rate), head: head + uint64(rate), start: head, rate: rate}

		got := collectAudio(t, flow, 4)
		for i, g := range got {
			end := flow.reads[i][0]
			if g.Start != end-1024 || g.SampleRate != int(rate) {
				t.Fatalf("%d Hz: read %d ending at %d: first sample %d at %d Hz, want %d", rate, i, end, g.Start, g.SampleRate, end-1024)
			}
			if want := clock.IndexPTS(ra, g.Start); g.PTS != want {
				t.Fatalf("%d Hz: read %d: PTS %d, want %d (TAI of sample %d)", rate, i, g.PTS, want, g.Start)
			}
		}
	}
}

// A read takes 10 ms, what GStreamer MXL writers commit at a time, or the
// audio's budget behind the read point where that is shorter: a read is
// handed over only once its last sample is written, so its first sample
// is at least the read's duration late. Reads follow on from each other
// when a program clock starts or changes its rate.
func TestAudioReadFitsTheAudioBudget(t *testing.T) {
	eachRate(t, func(t *testing.T, lr lateRate) {
		var period atomic.Int64
		saved := programPeriod
		programPeriod = func() int64 { return period.Load() }
		t.Cleanup(func() { programPeriod = saved })

		rate := int64(lr.audio)
		v := lr.video
		// The budget, (ReaderDelayGrains-1) periods, in samples, and
		// what a read takes at least: up to a tick per period short of
		// it, and of a whole sample.
		budget := float64(rate*(clock.ReaderDelayGrains-1)*v.Den) / float64(v.Num)
		low := min(float64(rate/100), budget-float64(rate*(clock.ReaderDelayGrains-1))/90000) - 1
		reads := func(n int, started func(i int)) []AudioGrain {
			ra := clock.Rate{Num: rate, Den: 1}
			head := clock.TimestampToIndex(ra, clock.TAINow())
			flow := &sampleRing{tail: head - uint64(rate), head: head + uint64(rate), start: head, rate: rate}
			rd := NewAudioReader(ReaderConfig{BufSize: 1, TimeoutMs: 1})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			rd.StartAudio(ctx, flow)
			var got []AudioGrain
			for g := range rd.Audio() {
				if got = append(got, g); len(got) == n {
					cancel()
				}
				started(len(got))
			}
			rd.Wait()
			if len(got) < n {
				t.Fatalf("got %d reads, want %d", len(got), n)
			}
			for i := 1; i < len(got); i++ {
				if end := got[i-1].Start + uint64(len(got[i-1].PCM[0])); got[i].Start != end {
					t.Fatalf("read %d starts at %d, the last ended at %d", i, got[i].Start, end)
				}
			}
			return got
		}
		fits := func(g AudioGrain) bool {
			n := float64(len(g.PCM[0]))
			return n <= float64(rate/100) && n <= budget && n >= low
		}

		period.Store(programPeriodAt(v))
		for i, g := range reads(20, func(int) {}) {
			if !fits(g) {
				t.Fatalf("read %d takes %d samples, want at most 10 ms and the %.2f-sample budget, and at least %.2f",
					i, len(g.PCM[0]), budget, low)
			}
		}

		// Without a program clock a read takes 10 ms, and the reads that
		// follow the clock's start fit its budget.
		period.Store(0)
		got := reads(40, func(i int) {
			if i == 3 {
				period.Store(programPeriodAt(v))
			}
		})
		if n := len(got[0].PCM[0]); n != int(rate/100) {
			t.Fatalf("without a program clock a read takes %d samples, want %d", n, rate/100)
		}
		if g := got[len(got)-1]; !fits(g) {
			t.Fatalf("after the clock started a read takes %d samples, want at most %.2f and at least %.2f",
				len(g.PCM[0]), min(budget, float64(rate/100)), low)
		}
	})
}

// A resync to the head skips samples; the PTS of the next read is that of
// its own first sample, so the skip moves audio later by exactly the
// skipped time and lip sync with video is kept.
func TestAudioResyncKeepsMediaTime(t *testing.T) {
	ra := clock.Rate{Num: 48000, Den: 1}
	// The reader starts 2 s behind the clock, where the producer's ring
	// still holds audio.
	head := clock.TimestampToIndex(ra, clock.TAINow()) - 2*48000
	flow := &sampleRing{tail: head - 48000, head: head + 48000, start: head}

	rd := NewAudioReader(ReaderConfig{BufSize: 64, TimeoutMs: 1, SamplesPerRead: 1024})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rd.StartAudio(ctx, flow)
	first := <-rd.Audio()
	// The producer is at the clock and holds nothing older: the reader's
	// next window is gone.
	flow.mu.Lock()
	flow.tail = first.Start + 96000
	flow.head = first.Start + 96000 + 4800
	flow.start = flow.head
	flow.mu.Unlock()
	var after AudioGrain
	for g := range rd.Audio() {
		if g.Start >= first.Start+96000 {
			after = g
			cancel()
			break
		}
	}
	cancel()
	rd.Wait()
	if after.PTS == 0 {
		t.Fatal("no read after the resync")
	}
	if want := clock.IndexPTS(ra, after.Start); after.PTS != want {
		t.Fatalf("PTS after resync %d, want %d", after.PTS, want)
	}
	if skip := after.PTS - first.PTS; skip < 2*90000 {
		t.Fatalf("PTS advanced %d over a skip of more than 2 s", skip)
	}
}

// requireIndexPTS checks that each grain carries its own index's PTS.
func requireIndexPTS(t *testing.T, flow DiscreteReader, grains ...VideoGrain) {
	t.Helper()
	r := flow.ConfigInfo().GrainRate
	cr := clock.Rate{Num: r.Numerator, Den: r.Denominator}
	for i, g := range grains {
		if want := clock.IndexPTS(cr, g.Index); g.PTS != want {
			t.Fatalf("grain %d (index %d): PTS %d, want %d", i, g.Index, g.PTS, want)
		}
	}
}

// holdRates are the program frame rates and flow sample rates the hold is
// tested at, paired so each frame rate and each sample rate appears.
var holdRates = []struct {
	name   string
	frame  clock.Rate
	sample int64
}{
	{"23.976p/44.1k", clock.Rate{Num: 24000, Den: 1001}, 44100},
	{"25p/48k", clock.Rate{Num: 25, Den: 1}, 48000},
	{"29.97p/48k", clock.Rate{Num: 30000, Den: 1001}, 48000},
	{"50p/96k", clock.Rate{Num: 50, Den: 1}, 96000},
	{"59.94p/44.1k", clock.Rate{Num: 60000, Den: 1001}, 44100},
	{"120p/48k", clock.Rate{Num: 120, Den: 1}, 48000},
	{"240p/48k", clock.Rate{Num: 240, Den: 1}, 48000},
	{"240p/96k", clock.Rate{Num: 240, Den: 1}, 96000},
}

// withProgramRate runs the program clock at frame for the test.
func withProgramRate(t *testing.T, frame clock.Rate) time.Duration {
	t.Helper()
	ticks := (90000*frame.Den + frame.Num - 1) / frame.Num
	saved := programPeriod
	programPeriod = func() int64 { return ticks }
	t.Cleanup(func() { programPeriod = saved })
	return time.Duration(ticks) * time.Second / 90000
}

// receive reads n grains from rd, failing the test if they do not come.
func receive(t *testing.T, ctx context.Context, rd *Reader, n int) []AudioGrain {
	t.Helper()
	var got []AudioGrain
	for len(got) < n {
		select {
		case g := <-rd.Audio():
			got = append(got, g)
		case <-ctx.Done():
			t.Fatalf("read %d never handed over", len(got))
		}
	}
	return got
}

// A producer may commit its audio well ahead of TAI, 40 of its grains or
// more. Its reads reach the mixer no earlier than audioReleaseLead before
// their media time, all of them, rather than as soon as they are committed.
func TestAudioFarAheadIsHandedOverNearItsMediaTime(t *testing.T) {
	for _, tc := range holdRates {
		t.Run(tc.name, func(t *testing.T) {
			period := withProgramRate(t, tc.frame)
			ra := clock.Rate{Num: tc.sample, Den: 1}
			now := clock.TimestampToIndex(ra, clock.TAINow())
			ahead := uint64(12 * period * time.Duration(tc.sample) / time.Second)
			flow := &sampleRing{rate: tc.sample, tail: now - uint64(tc.sample), head: now + 3*ahead, start: now + ahead}

			rd := NewAudioReader(ReaderConfig{BufSize: 64, TimeoutMs: 1, SamplesPerRead: 1024})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			rd.StartAudio(ctx, flow)
			var prev AudioGrain
			for i, g := range receive(t, ctx, rd, 3) {
				lead := time.Duration(clock.IndexToTimestamp(ra, g.Start) - clock.TAINow())
				if want := audioReleaseLead(int64(period * 90000 / time.Second)); lead > want+2*time.Millisecond {
					t.Fatalf("read %d handed over %v before its media time, want at most %v", i, lead, want)
				}
				if i > 0 && g.Start != prev.Start+1024 {
					t.Fatalf("read %d starts at %d, want %d: held reads are not skipped", i, g.Start, prev.Start+1024)
				}
				prev = g
			}
			cancel()
			rd.Wait()
		})
	}
}

// Audio already at or behind its media time is handed over as soon as it
// is read: the hold costs a producer near TAI nothing.
func TestAudioNearTAIIsHandedOverAtOnce(t *testing.T) {
	for _, tc := range holdRates {
		t.Run(tc.name, func(t *testing.T) {
			withProgramRate(t, tc.frame)
			ra := clock.Rate{Num: tc.sample, Den: 1}
			now := clock.TimestampToIndex(ra, clock.TAINow())
			flow := &sampleRing{rate: tc.sample, tail: now - uint64(tc.sample), head: now + uint64(tc.sample)/10, start: now - 3*1024}

			rd := NewAudioReader(ReaderConfig{BufSize: 64, TimeoutMs: 1, SamplesPerRead: 1024})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			begin := time.Now()
			rd.StartAudio(ctx, flow)
			receive(t, ctx, rd, 3)
			if took := time.Since(begin); took > 20*time.Millisecond {
				t.Fatalf("three reads of audio already due took %v to hand over, want at most 20ms", took)
			}
			cancel()
			rd.Wait()
		})
	}
}

// A flow stamped far further ahead than any producer's lead, a wrong epoch
// or misstamped indices, does not park the audio loop: its reads are
// handed over as read.
func TestAudioStampedFarAheadDoesNotParkTheReader(t *testing.T) {
	for _, tc := range holdRates {
		t.Run(tc.name, func(t *testing.T) {
			withProgramRate(t, tc.frame)
			ra := clock.Rate{Num: tc.sample, Den: 1}
			now := clock.TimestampToIndex(ra, clock.TAINow())
			ahead := now + uint64(2*maxAudioHold*time.Duration(tc.sample)/time.Second)
			flow := &sampleRing{rate: tc.sample, tail: ahead - uint64(tc.sample), head: ahead + uint64(tc.sample), start: ahead}

			rd := NewAudioReader(ReaderConfig{BufSize: 64, TimeoutMs: 1, SamplesPerRead: 1024})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			begin := time.Now()
			rd.StartAudio(ctx, flow)
			receive(t, ctx, rd, 3)
			if took := time.Since(begin); took > 100*time.Millisecond {
				t.Fatalf("three reads of a flow %v ahead took %v", 2*maxAudioHold, took)
			}
			cancel()
			rd.Wait()
		})
	}
}

// A held read is handed over early enough to make the mixer's read point,
// ReaderDelayGrains-1 periods after its first sample's media time, even when
// it takes as long to get there as measured at 240 fps with every core
// busy: 17.8 ms from the end of the hold. Decided from the hold itself, so
// it does not depend on how loaded the machine running the test is.
func TestAudioReleaseLeadMakesTheReadPointUnderLoad(t *testing.T) {
	const loaded = 18 * time.Millisecond
	for _, tc := range holdRates {
		t.Run(tc.name, func(t *testing.T) {
			ticks := (90000*tc.frame.Den + tc.frame.Num - 1) / tc.frame.Num
			period := time.Duration(ticks) * time.Second / 90000
			lead := audioReleaseLead(ticks)
			if margin := lead + (clock.ReaderDelayGrains-1)*period; margin < loaded+2*time.Millisecond {
				t.Fatalf("a read handed over %v early makes its read point by %v, want at least %v",
					lead, margin, loaded+2*time.Millisecond)
			}
		})
	}
	if audioReleaseLead(0) != 0 {
		t.Fatal("a read is held without a program clock")
	}
}

// batchFlow is a producer that commits its audio a batch at a time, on a
// clock of its own that moves only while a read waits, as libmxl's read
// waits up to its timeout for samples not yet committed. head returns the
// head at TAI t; commitAt the TAI the head reaches index. It records when
// each read that starts at a sample returned.
type batchFlow struct {
	rate     clock.Rate
	head     func(t int64) uint64
	commitAt func(index uint64) int64

	mu   sync.Mutex
	now  int64
	read map[uint64]int64 // first sample of a read -> TAI it returned at
}

func (f *batchFlow) ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index > f.head(f.now) {
		at := f.commitAt(index)
		if at > f.now+int64(timeoutNs) {
			f.now += int64(timeoutNs)
			return nil, fmt.Errorf("%w: index %d", ErrTimeout, index)
		}
		f.now = at
	}
	f.read[index-uint64(count)] = f.now
	return [][]float32{make([]float32, count), make([]float32, count)}, nil
}

func (f *batchFlow) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatAudio, GrainRate: Rational{f.rate.Num, 1}, ChannelCount: 2}
}

func (f *batchFlow) HeadIndex() (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.head(f.now), nil
}

func (f *batchFlow) Close() error { return nil }

func (f *batchFlow) clock() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// floorIndex is the index of the last edit unit at rate r that has
// started by TAI ts: the number of whole units before ts.
func floorIndex(r clock.Rate, ts int64) uint64 {
	i := clock.TimestampToIndex(r, ts)
	if clock.IndexToTimestamp(r, i) > ts {
		i--
	}
	return i
}

// A producer commits its audio a batch at a time: 10 ms for a GStreamer
// MXL writer, other sizes for others, or a video grain's worth. A read
// that ends past the producer's head waits for its next batch, and a read
// size that does not divide the batch makes reads do that more and more
// as they drift against the batches: at 100 fps a 10 ms batch read 479
// samples at a time reached the mixer up to 20 ms after its media time,
// and late audio moves the source's audio and video later by its worst
// read. Read to the head, no read's first sample is later than the batch
// makes it, at every program rate and sample rate, whatever the batch.
func TestAudioReadIsNoLaterThanItsProducersBatch(t *testing.T) {
	eachRate(t, func(t *testing.T, lr lateRate) {
		withProgramRate(t, lr.video)
		ra, rv := clock.Rate{Num: int64(lr.audio), Den: 1}, lr.video
		batched := func(n uint64) (func(int64) uint64, func(uint64) int64) {
			return func(t int64) uint64 { return floorIndex(ra, t) / n * n },
				func(i uint64) int64 { return clock.IndexToTimestamp(ra, (i+n-1)/n*n) }
		}
		// A video grain's worth: the samples up to the start of the
		// newest grain, committed as the grain starts.
		perGrain := func(t int64) uint64 {
			return clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, floorIndex(rv, t)))
		}
		grainAt := func(i uint64) int64 {
			g := floorIndex(rv, clock.IndexToTimestamp(ra, i))
			for perGrain(clock.IndexToTimestamp(rv, g)) < i {
				g++
			}
			return clock.IndexToTimestamp(rv, g)
		}
		dur := func(n int64) int64 { return n * 1_000_000_000 / ra.Num }
		type kind struct {
			name     string
			head     func(int64) uint64
			commitAt func(uint64) int64
			batch    int64 // ns, the longest batch
		}
		kinds := []kind{{"grain", perGrain, grainAt, (1_000_000_000*rv.Den+rv.Num-1)/rv.Num + dur(1)}}
		for _, n := range []int64{ra.Num / 200, ra.Num / 100, 1024} {
			h, c := batched(uint64(n))
			kinds = append(kinds, kind{fmt.Sprintf("%d", n), h, c, dur(n)})
		}
		for _, k := range kinds {
			t.Run(k.name, func(t *testing.T) {
				start := clock.TAINow()
				f := &batchFlow{rate: ra, head: k.head, commitAt: k.commitAt, now: start, read: map[uint64]int64{}}
				saved := NsUntilIndex
				NsUntilIndex = func(index uint64, rate Rational) uint64 {
					return uint64(max(0, clock.IndexToTimestamp(ra, index)-f.clock()))
				}
				t.Cleanup(func() { NsUntilIndex = saved })

				rd := NewAudioReader(ReaderConfig{BufSize: 1, TimeoutMs: 1})
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				rd.StartAudio(ctx, f)
				var got []AudioGrain
				for g := range rd.Audio() {
					// 2 s of the producer's time.
					if got = append(got, g); f.clock()-start > 2_000_000_000 {
						cancel()
					}
				}
				rd.Wait()
				if f.clock()-start < 2_000_000_000 {
					t.Fatalf("%d reads in %v of the producer's time", len(got), time.Duration(f.clock()-start))
				}
				// The first read ends at the head and starts up to a
				// read before it.
				var worst time.Duration
				for _, g := range got[1:] {
					late := time.Duration(f.read[g.Start] - clock.IndexToTimestamp(ra, g.Start))
					worst = max(worst, late)
				}
				// Rounded to the nanosecond either way.
				if worst > time.Duration(k.batch)+time.Microsecond {
					t.Fatalf("a read reached the reader %v after its first sample's media time, want at most the %v batch",
						worst, time.Duration(k.batch))
				}
			})
		}
	})
}
