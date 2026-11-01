package clock

import "testing"

// A source's hold adds the frames that arrive while one waits for the read
// point: none for frames due on arrival, the hold in the source's own frame
// periods otherwise, never more than MaxSourceHold, and a raised hold is
// kept over the current and the previous window, as a TrackingAnchor keeps
// the offset that caused it. The source steps its PTS by its own period;
// the offset it is placed under moves as an anchor's does.
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
	for i := 0; i < 10; i++ {
		step()
	}
	if n := step(); n != 0 {
		t.Fatalf("a frame due on arrival adds %d frames, want none", n)
	}
	off += 3600 + 15_300 // now placed 170 ms after its arrival
	if n := step(); n != 9 {
		t.Fatalf("a 170 ms hold at 20 ms frames adds %d frames, want 9", n)
	}
	off += 5 * 90000
	if n := step(); n != MaxSourceHold/frame {
		t.Fatalf("a 5 s hold adds %d frames, want the bound's %d", n, MaxSourceHold/frame)
	}
	// The offset comes down again; the raised hold is kept for one to two
	// windows.
	off -= 5*90000 + 15_300
	kept := 0
	for i := 0; i < 5*50; i++ {
		if step() == MaxSourceHold/frame {
			kept++
		}
	}
	if lo, hi := int(TrackingWindow/frame), int(2*TrackingWindow/frame); kept < lo || kept > hi {
		t.Fatalf("a raised hold kept for %d frames, want one to two windows (%d to %d)", kept, lo, hi)
	}
	if n := step(); n != 0 {
		t.Fatalf("%d frames added once the hold came down, want none", n)
	}
}
