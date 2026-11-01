package gpu

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

type countedFrame struct{ refs int }

func (f *countedFrame) Ref()     { f.refs++ }
func (f *countedFrame) Release() { f.refs-- }

// An SRT source muxed as MPEG-TS sends a video frame every 20 ms and its
// AAC in PES packets of eight frames, 171 ms at 48 kHz. One TrackingAnchor
// places both, at the latest arrival of either, so each video frame waits
// about a PES packet past its arrival before a tick's read point reaches
// it: a tick shows a frame up to eleven frames older than the newest. The
// GPU cache must still hold every frame a tick shows, or the tick finds no
// pixels for a source that has none on the CPU and the program stalls.
func TestRecentFramesHoldTheFrameAnAnchoredSRTTickShows(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	clock.Epoch()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	const (
		period  = int64(20_000_000)
		lateV   = int64(30_000_000) // every video frame arrives 30 ms late
		aacNs   = int64(1024) * 1_000_000_000 / 48000
		perPES  = 8
		readLag = clock.ReaderDelayGrains * 1800
	)
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

	start := now
	nextV, nextA := int64(0), int64(0)
	ticks, misses := 0, 0
	for step := int64(0); step < 20_000; step++ { // 20 s in 1 ms steps
		now = start + step*1_000_000
		for nextV+lateV <= now-start {
			f := &countedFrame{refs: 1}
			all = append(all, f)
			pts := anchor.PTS(src(nextV))
			release(store.push(f, pts))
			f.Release() // the producer's reference; the store keeps its own
			arrived = append(arrived, arrival{f, pts})
			nextV += period
		}
		// A PES packet leaves once its last AAC frame is muxed.
		for nextA+perPES*aacNs+lateV <= now-start {
			for k := int64(0); k < perPES; k++ {
				anchor.PTS(src(nextA + k*aacNs))
			}
			nextA += perPES * aacNs
		}
		if step%20 != 0 || step < 2000 {
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
		ticks++
		f, ok := store.at(arrived[shown].pts)
		if !ok {
			misses++
			continue
		}
		f.Release()
	}
	if ticks == 0 {
		t.Fatal("no tick found a frame due")
	}
	if misses > 0 {
		t.Fatalf("%d of %d ticks found the frame they show gone from the GPU cache (%d frames deep)",
			misses, ticks, len(store.frames))
	}
	release(store.drain())
	for i, f := range all {
		if f.refs != 0 {
			t.Fatalf("frame %d holds %d references after the store drained", i, f.refs)
		}
	}
}
