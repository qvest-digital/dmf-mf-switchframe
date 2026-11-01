package switcher

import (
	"testing"

	"github.com/zsiec/switchframe/server/clock"
)

// An SRT source with 64 kbit/s AAC muxes its audio in PES packets of
// seventeen AAC frames, 363 ms at 48 kHz, and one TrackingAnchor places
// its video at the latest arrival of either: each video frame waits about
// a PES packet past its arrival before a tick's read point reaches it,
// more than syncRingSize frames at 50 fps. The ring must keep every frame
// until a tick has passed it, or ticks skip frames the source sent. The
// ticks run from the first frame on, so the ring is read throughout.
func TestFrameSyncKeepsTheFramesAnAnchoredSRTSourceHolds(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	clock.Epoch()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })

	const (
		period  = int64(20_000_000)
		lateV   = int64(30_000_000)
		aacNs   = int64(1024) * 1_000_000_000 / 48000
		perPES  = 17
		readLag = clock.ReaderDelayGrains * 1800
	)
	var anchor clock.TrackingAnchor
	fs := NewFrameSynchronizer()
	fs.AddSource("srt:cam")
	ss := fs.source("srt:cam")
	src := func(ns int64) int64 { return 1_000_000 + ns*9/100_000 }

	var placed []int64 // PTS of every frame the source sent, in order
	start := now
	nextV, nextA := int64(0), int64(0)
	shown, skipped, empty := 0, 0, 0
	// last is the index in placed of the last frame a tick showed.
	last := -1
	for step := int64(0); step < 20_000; step++ { // 20 s in 1 ms steps
		now = start + step*1_000_000
		for nextV+lateV <= now-start {
			pts := anchor.PTS(src(nextV))
			placed = append(placed, pts)
			fs.IngestRawVideo("srt:cam", &ProcessingFrame{PTS: pts})
			nextV += period
		}
		for nextA+perPES*aacNs+lateV <= now-start {
			for k := int64(0); k < perPES; k++ {
				anchor.PTS(src(nextA + k*aacNs))
			}
			nextA += perPES * aacNs
		}
		if step%20 != 0 {
			continue
		}
		ss.mu.Lock()
		pf := ss.popAt(clock.NowPTS() - readLag)
		ss.mu.Unlock()
		// Counted once the anchor has placed the first PES packet: the
		// offset it raises then holds the video back once.
		settled := step >= 2000
		if pf == nil {
			if settled {
				empty++ // the source sends a frame a period; one is always due
			}
			continue
		}
		i := last + 1
		for i < len(placed) && placed[i] != pf.PTS {
			i++
		}
		if settled && i > last+1 {
			skipped += i - last - 1
		}
		last = i
		if settled {
			shown++
		}
	}
	if empty > 0 || skipped > 0 || ss.overflows.Load() > 0 {
		t.Fatalf("%d ticks found no frame due and %d showed one, skipping %d; %d dropped by a full ring",
			empty, shown, skipped, ss.overflows.Load())
	}
}
