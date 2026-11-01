package clock

import "testing"

// A source's hold adds the frames that wait for the read point: none for
// frames due on arrival, the frames placed after the current time
// otherwise, never more than MaxSourceHold/minFramePeriod, and once the
// offset comes down, the frames placed under the raised one among its last
// MaxSourceHold/minFramePeriod frames. The source steps its PTS by its own period; the offset it is
// placed under moves as an anchor's does.
func TestSourceHoldCountsTheFramesAHoldKeeps(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var h SourceHold
	const frame = 1800 // 20 ms
	src := int64(1_000_000)
	off := NowPTS() - 3600 - src // read 40 ms behind arrival, as an MXL source is
	step := func() int {
		now += 20_000_000
		src += frame
		return h.Frames(src + off)
	}
	for range 10 {
		step()
	}
	if n := step(); n != 0 {
		t.Fatalf("a frame due on arrival adds %d frames, want none", n)
	}
	off += 3600 + 15_300 // now placed 170 ms after its arrival
	for range 20 {
		step()
	}
	if n := step(); n != 9 {
		t.Fatalf("a 170 ms hold at 20 ms frames adds %d frames, want 9", n)
	}
	off += 5 * 90000
	for range 100 {
		step()
	}
	if n := step(); n != maxHoldFrames {
		t.Fatalf("a 5 s hold adds %d frames, want the bound's %d", n, maxHoldFrames)
	}
	// The offset comes down again: the frames placed under the raised one
	// count until as many new ones have come, then none do.
	off -= 5*90000 + 15_300
	for i := range maxHoldFrames - 1 {
		if n := step(); n != maxHoldFrames-1-i {
			t.Fatalf("frame %d after the hold came down: %d frames added, want the %d placed under it", i, n, maxHoldFrames-1-i)
		}
	}
	if n := step(); n != 0 {
		t.Fatalf("%d frames added once the frames placed under the hold were replaced, want none", n)
	}
}

// A TrackingAnchor that lowers its offset at a window rollover places one
// frame closer to its predecessor than the source's frame period. The hold
// is still counted in the source's frame period, not in that distance.
func TestSourceHoldCountsInTheFramePeriodAcrossAnOffsetDrop(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var h SourceHold
	const frame = 1800 // 20 ms
	src := int64(1_000_000)
	off := NowPTS() + 15_300 - src // placed 170 ms after its arrival
	most := 0
	for i := 0; i < 5*50; i++ {
		now += 20_000_000
		src += frame
		if i == 50 {
			off -= frame - 90 // one step of 90 ticks
		}
		most = max(most, h.Frames(src+off))
	}
	if want := (15_300 + frame - 1) / frame; most > want {
		t.Fatalf("a 170 ms hold at 20 ms frames adds up to %d frames after one 90-tick step, want at most %d", most, want)
	}
}

// A TrackingAnchor places the tail of a burst, the backlog a connection's
// socket built before it was first read, at arrival plus MaxSourceHold:
// frames that arrive a few milliseconds apart wait in the store as close
// together. Once frames arrive a period apart again, the source's period
// is its own again while those frames still wait. The hold adds every
// frame that waits, not the hold in frame periods, or the store drops the
// oldest of them, the next ones due, and the program freezes until the
// read point reaches the rest.
func TestSourceHoldKeepsABurstPlacedAheadWhole(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var h SourceHold
	var placed []int64
	push := func(stepNs int64) int {
		now += stepNs
		pts := NowPTS() + MaxSourceHold
		placed = append(placed, pts)
		return h.Frames(pts)
	}
	for range 40 { // the burst, decoded a frame per 4 ms
		push(4_000_000)
	}
	for range 20 { // real time, a frame per 20 ms
		n := push(20_000_000)
		waiting := 0
		for _, p := range placed {
			if p > NowPTS() {
				waiting++
			}
		}
		if n < waiting {
			t.Fatalf("frame %d: the hold adds %d frames while %d wait", len(placed), n, waiting)
		}
	}
}
