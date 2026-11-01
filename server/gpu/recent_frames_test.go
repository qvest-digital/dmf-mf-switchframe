package gpu

import (
	"math/rand"
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

type countedFrame struct{ refs int }

func (f *countedFrame) Ref()     { f.refs++ }
func (f *countedFrame) Release() { f.refs-- }

// srtRun is an SRT source muxed as MPEG-TS: a video frame every 20 ms and
// its AAC in PES packets of eight frames, 171 ms at 48 kHz, every packet
// 30 ms late. One TrackingAnchor places both essences, each delivered in
// order, at the latest arrival of either.
type srtRun struct {
	hiccupMs int64 // one PES packet at 10 s arrives this much later
	jitterMs int64 // every packet arrives up to this much later, at random
	seed     int64
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
	var store recentFrames[*countedFrame]
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
		read := clock.NowPTS() - readLag
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

// The store keeps a source's frames for its hold, counted in the source's
// frame period. A TrackingAnchor that lowers its offset at a window
// rollover places one frame closer to its predecessor than a frame period:
// one late PES packet does that two windows later. Counting the hold in
// that distance would keep a hundred frames and more, about 3 MB of VRAM
// each at 1080p, for two windows, and free them in one push. Network
// jitter moves the offset in many such steps. The store stays within the
// frames its hold takes and still holds every frame a tick shows.
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
			bound := recentFramesDepth + int((r.hold+1799)/1800) + 1
			if r.depth > bound {
				t.Errorf("the store kept %d frames for a hold of %d ticks, want at most %d", r.depth, r.hold, bound)
			}
			if r.misses > 0 {
				t.Errorf("%d of %d ticks found the frame they show gone (%d frames deep)", r.misses, r.ticks, r.depth)
			}
		})
	}
}
