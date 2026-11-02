package gpu

import (
	"math/rand"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

type countedFrame struct{ refs int }

func (f *countedFrame) Ref()     { f.refs++ }
func (f *countedFrame) Release() { f.refs-- }

// dueDepth is how many frames a store keeps at 50 fps for a source whose
// frames are due on arrival: the reader delay's, the frame the next tick
// reads and the one the last tick read.
const dueDepth = recentFramesDepth + clock.ReaderDelayGrains + 1

// srtRun is an SRT source muxed as MPEG-TS: a video frame every 20 ms and
// its AAC in PES packets of eight frames, 171 ms at 48 kHz, every packet
// 30 ms late. One TrackingAnchor places both essences, each delivered in
// order, at the latest arrival of either.
type srtRun struct {
	hiccupMs int64 // one PES packet at 10 s arrives this much later
	jitterMs int64 // every packet arrives up to this much later, at random
	seed     int64
	delay    int64 // the source's delay (SetSourceDelay), in 90 kHz ticks
}

type srtResult struct {
	ticks, misses int
	depth         int   // most frames the store kept
	hold          int64 // largest hold a frame was placed under, in ticks
}

// run pushes the source's frames into a store for 30 s and has a tick every
// 20 ms look up the newest frame due at its read point, as a GPU program
// tick looks up the frame it shows.
func (c srtRun) run(t *testing.T) srtResult {
	t.Helper()
	now := int64(1_798_000_000_000_000_000)
	clock.Epoch()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	clock.SetProgramRate(clock.Rate{Num: 50, Den: 1})

	const (
		period  = int64(20_000_000)
		late    = int64(30_000_000)
		aacNs   = int64(1024) * 1_000_000_000 / 48000
		perPES  = 8
		readLag = clock.ReaderDelayGrains * 1800
	)
	rng := rand.New(rand.NewSource(c.seed))
	jitter := func() int64 {
		if c.jitterMs == 0 {
			return 0
		}
		return rng.Int63n(c.jitterMs * 1_000_000)
	}
	type packet struct {
		at  int64   // arrival, ns from the start
		src []int64 // source PTS of the frames it carries
	}
	var video, audio []packet
	var lastV, lastA int64 // arrival of the previous packet, in order per essence
	var anchor clock.TrackingAnchor
	store := recentFrames[*countedFrame]{delay: c.delay}
	type arrival struct {
		f   *countedFrame
		pts int64
	}
	var arrived []arrival
	var all []*countedFrame
	release := func(fs []*countedFrame) {
		for _, f := range fs {
			f.Release()
		}
	}
	src := func(ns int64) int64 { return 1_000_000 + ns*9/100_000 }

	var res srtResult
	start := now
	nextV, nextA := int64(0), int64(0)
	hiccupDone := false
	for step := int64(0); step < 30_000; step++ { // 30 s in 1 ms steps
		now = start + step*1_000_000
		el := now - start
		for nextV <= el {
			lastV = max(lastV, nextV+late+jitter())
			video = append(video, packet{lastV, []int64{src(nextV)}})
			nextV += period
		}
		// A PES packet leaves once its last AAC frame is muxed.
		for nextA+perPES*aacNs <= el {
			at := nextA + perPES*aacNs + late + jitter()
			if c.hiccupMs > 0 && !hiccupDone && el >= 10_000_000_000 {
				at += c.hiccupMs * 1_000_000
				hiccupDone = true
			}
			lastA = max(lastA, at)
			p := packet{at: lastA}
			for k := int64(0); k < perPES; k++ {
				p.src = append(p.src, src(nextA+k*aacNs))
			}
			audio = append(audio, p)
			nextA += perPES * aacNs
		}
		for len(audio) > 0 && audio[0].at <= el {
			for _, s := range audio[0].src {
				anchor.PTS(s)
			}
			audio = audio[1:]
		}
		for len(video) > 0 && video[0].at <= el {
			pts := anchor.PTS(video[0].src[0])
			video = video[1:]
			res.hold = max(res.hold, min(pts-clock.NowPTS(), clock.MaxSourceHold))
			f := &countedFrame{refs: 1}
			all = append(all, f)
			release(store.push(f, pts))
			f.Release() // the producer's reference; the store keeps its own
			arrived = append(arrived, arrival{f, pts})
			res.depth = max(res.depth, len(store.frames))
		}
		if step%20 != 0 || step < 3000 {
			continue
		}
		// The tick shows the newest frame due at its read point.
		read := clock.NowPTS() - readLag - c.delay
		shown := -1
		for i := len(arrived) - 1; i >= 0; i-- {
			if arrived[i].pts <= read {
				shown = i
				break
			}
		}
		if shown < 0 {
			continue
		}
		res.ticks++
		f, ok := store.at(arrived[shown].pts)
		if !ok {
			res.misses++
			continue
		}
		f.Release()
	}
	if res.ticks == 0 {
		t.Fatal("no tick found a frame due")
	}
	release(store.drain())
	for i, f := range all {
		if f.refs != 0 {
			t.Fatalf("frame %d holds %d references after the store drained", i, f.refs)
		}
	}
	return res
}

// Over an SRT source each video frame waits about a PES packet past its
// arrival before a tick's read point reaches it: a tick shows a frame up
// to eleven frames older than the newest. The GPU cache must still hold
// every frame a tick shows, or the tick finds no pixels for a source that
// has none on the CPU and the program stalls.
func TestRecentFramesHoldTheFrameAnAnchoredSRTTickShows(t *testing.T) {
	if r := (srtRun{}).run(t); r.misses > 0 {
		t.Fatalf("%d of %d ticks found the frame they show gone from the GPU cache (%d frames deep)",
			r.misses, r.ticks, r.depth)
	}
}

// The store keeps the frames that wait in a source's hold. A
// TrackingAnchor that lowers its offset at a window rollover places one
// frame closer to its predecessor than a frame period: one late PES packet
// does that two windows later. A hold counted in that distance would keep
// a hundred frames and more, about 3 MB of VRAM each at 1080p, for two
// windows, and free them in one push. Network jitter moves the offset in
// many such steps. The store stays within the frames its hold takes and
// still holds every frame a tick shows.
func TestRecentFramesStayWithinTheHoldAcrossLatePackets(t *testing.T) {
	for _, c := range []struct {
		name string
		run  srtRun
	}{
		{"one PES packet 19 ms late", srtRun{hiccupMs: 19}},
		{"jitter 40 ms", srtRun{jitterMs: 40, seed: 1}},
		{"jitter 40 ms, seed 2", srtRun{jitterMs: 40, seed: 2}},
		{"jitter 100 ms", srtRun{jitterMs: 100, seed: 1}},
		{"jitter 100 ms, seed 2", srtRun{jitterMs: 100, seed: 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := c.run.run(t)
			bound := dueDepth + int((r.hold+1799)/1800) + 1
			if r.depth > bound {
				t.Errorf("the store kept %d frames for a hold of %d ticks, want at most %d", r.depth, r.hold, bound)
			}
			if r.misses > 0 {
				t.Errorf("%d of %d ticks found the frame they show gone (%d frames deep)", r.misses, r.ticks, r.depth)
			}
		})
	}
}

// A source delay (SetSourceDelay) has ticks read the source that much
// further behind, so its frames wait that much longer in the store. The
// store keeps them, or a source without pixels on the CPU runs from the
// oldest frame kept and shows its video early against its delayed audio.
func TestRecentFramesHoldTheFrameADelayedSourceShows(t *testing.T) {
	for _, delayMs := range []int64{100, 300, 500} {
		r := srtRun{delay: delayMs * 90}.run(t)
		if r.misses > 0 {
			t.Errorf("delay %d ms: %d of %d ticks found the frame they show gone (%d frames deep)",
				delayMs, r.misses, r.ticks, r.depth)
		}
		if bound := dueDepth + int((r.hold+delayMs*90+1799)/1800) + 1; r.depth > bound {
			t.Errorf("delay %d ms: the store kept %d frames, want at most %d", delayMs, r.depth, bound)
		}
	}
}

// A frame without pixels of its own that is no longer kept is stood in for
// by the kept frame nearest to it: the newest before it, else the oldest.
func TestRecentFramesNearStandsInForAFrameNoLongerKept(t *testing.T) {
	// A 50 fps tick reads up to 5400 ticks behind: at PTS 12400 the frames
	// from 9000 on are not yet read, and the one before them is kept too.
	now := clock.TimestampOf(12_400)
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	clock.SetProgramRate(clock.Rate{Num: 50, Den: 1})
	var store recentFrames[*countedFrame]
	frames := map[int64]*countedFrame{}
	for _, pts := range []int64{3000, 6000, 9000, 12000, 15000} {
		f := &countedFrame{}
		frames[pts] = f
		for _, e := range store.push(f, pts) {
			e.Release()
		}
	}
	for _, c := range []struct{ want, got int64 }{
		{6000, 6000},   // kept
		{10000, 9000},  // between two kept frames
		{20000, 15000}, // after the newest
		{3000, 6000},   // evicted, older than every kept frame
	} {
		f, ok := store.near(c.want)
		if !ok || f != frames[c.got] {
			t.Fatalf("near(%d) is not the frame kept under %d", c.want, c.got)
		}
		f.Release()
	}
	if _, ok := store.at(3000); ok {
		t.Fatal("the oldest frame is still kept; the case does not test eviction")
	}
	for _, f := range store.drain() {
		f.Release()
	}
	if _, ok := store.near(9000); ok {
		t.Fatal("an empty store stands in a frame")
	}
	for pts, f := range frames {
		if f.refs != 0 {
			t.Fatalf("frame %d holds %d references", pts, f.refs)
		}
	}
}

// A source registered again under its key, as an SRT source is by a
// reconnect, keeps serving the frame the program holds until its new
// connection's frames come: the new store takes over the old one's frames
// with their references, and a pixel-less repeat still finds its frame.
func TestRecentFramesAdoptKeepsTheHeldFrame(t *testing.T) {
	var old, renewed recentFrames[*countedFrame]
	frames := []*countedFrame{{refs: 1}, {refs: 1}, {refs: 1}}
	for i, f := range frames {
		old.push(f, int64(1000+i*1800))
	}
	renewed.adopt(&old)

	if len(old.frames) != 0 {
		t.Fatalf("the old store still keeps %d frames", len(old.frames))
	}
	f, ok := renewed.near(1000 + 2*1800)
	if !ok || f != frames[2] {
		t.Fatal("the held frame is gone after the source was registered again")
	}
	f.Release()
	for i, f := range frames {
		if f.refs != 2 {
			t.Fatalf("frame %d has %d references, want its own and the store's (2)", i, f.refs)
		}
	}
	for _, f := range renewed.drain() {
		f.Release()
	}
}

// A connection's backlog burst is placed by its TrackingAnchor at arrival
// plus clock.MaxSourceHold from the frame on that would wait longer: those
// frames arrive a decode apart and wait as close together. The store keeps
// every frame a tick has not read yet, also once frames arrive a period
// apart again, and also those already behind the current time that a tick
// reading the program's reader delay and the source's delay behind has
// not reached: a store that kept only the frames placed after the current
// time evicted the oldest unread ones, the next due, and a source without
// pixels on the CPU ran from a later frame instead.
func TestRecentFramesKeepABacklogBurstUntilItIsRead(t *testing.T) {
	const readLag = clock.ReaderDelayGrains * 1800 // a 50 fps tick's read point
	for _, delay := range []int64{0, 18000} {
		for _, decodeNs := range []int64{2_000_000, 4_000_000} {
			now := int64(1_798_000_000_000_000_000)
			clock.Epoch()
			clock.SetTAISource(func() int64 { return now })
			t.Cleanup(func() { clock.SetTAISource(nil) })
			clock.SetProgramRate(clock.Rate{Num: 50, Den: 1})
			store := recentFrames[*countedFrame]{delay: delay}
			var placed []int64
			push := func(stepNs int64) {
				now += stepNs
				pts := clock.NowPTS() + clock.MaxSourceHold
				placed = append(placed, pts)
				f := &countedFrame{refs: 1}
				for _, e := range store.push(f, pts) {
					e.Release()
				}
				f.Release()
				for _, p := range placed {
					if p <= clock.NowPTS()-readLag-delay {
						continue
					}
					if f, ok := store.at(p); ok {
						f.Release()
						continue
					}
					t.Fatalf("delay %d, decode %d ns, frame %d: a frame placed %d ticks after the read point is gone while it waits (%d kept)",
						delay, decodeNs, len(placed), p-(clock.NowPTS()-readLag-delay), len(store.frames))
				}
			}
			for range 25 { // the burst's tail, a frame per decode
				push(decodeNs)
			}
			for range 50 { // real time, a frame per 20 ms
				push(20_000_000)
			}
			for _, f := range store.drain() {
				f.Release()
			}
		}
	}
}
