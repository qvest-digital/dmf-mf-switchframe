package clock

import "testing"

// programRate50 has the ticks read at 50 fps for the test's duration.
func programRate50(t *testing.T) {
	t.Helper()
	saved := readLag.Load()
	SetProgramRate(Rate{Num: 50, Den: 1})
	t.Cleanup(func() { readLag.Store(saved) })
}

// The read lag is ReaderDelayGrains program frame periods and the period
// to the next tick, in whole 90 kHz ticks, and a realtime clock sets it
// for its rate as it starts.
func TestSetProgramRateSetsTheReadLag(t *testing.T) {
	saved := readLag.Load()
	t.Cleanup(func() { readLag.Store(saved) })
	for _, c := range []struct {
		rate Rate
		want int64
	}{
		{Rate{Num: 50, Den: 1}, 5400},
		{Rate{Num: 25, Den: 1}, 10800},
		{Rate{Num: 24000, Den: 1001}, 11262}, // 11261.25, rounded up
		{Rate{Num: 60000, Den: 1001}, 4505},  // 4504.5
	} {
		SetProgramRate(c.rate)
		if got := readLag.Load(); got != c.want {
			t.Errorf("rate %d/%d: read lag %d ticks, want %d", c.rate.Num, c.rate.Den, got, c.want)
		}
	}
	c := NewReferenceClock(25, 1, 48000, PTSBase, true)
	readLag.Store(0)
	c.Start()
	c.Stop()
	if got := readLag.Load(); got != 10800 {
		t.Fatalf("a realtime 25 fps clock left a read lag of %d ticks, want 10800", got)
	}
}

// A source's hold counts the frames a tick has not read yet: those placed
// after the current time less the program's read lag (60 ms at 50 fps)
// and the source's delay, never more than (MaxSourceHold + delay) /
// minFramePeriod. The source steps its PTS by 20 ms; the offset it is
// placed under moves as an anchor's does.
func TestSourceHoldCountsTheFramesNotYetRead(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	programRate50(t)
	var h SourceHold
	const frame = 1800 // 20 ms
	src := int64(1_000_000)
	off := NowPTS() - 3600 - src // read 40 ms behind arrival, as an MXL source is
	delay := int64(0)
	step := func() int {
		now += 20_000_000
		src += frame
		return h.Frames(src+off, delay)
	}
	for _, c := range []struct {
		name  string
		off   int64 // added to the offset before the frames
		delay int64
		want  int
	}{
		{"placed 40 ms behind arrival", 0, 0, 1},
		{"placed 40 ms behind arrival, delay 100 ms", 0, 9000, 6},
		{"placed 170 ms after arrival", 3600 + 15_300, 0, 12},
		{"placed 5 s after arrival", 5 * 90000, 0, MaxSourceHold / minFramePeriod},
		{"placed 5 s after arrival, delay 200 ms", 0, 18000, (MaxSourceHold + 18000) / minFramePeriod},
	} {
		off += c.off
		delay = c.delay
		for range len(h.placed) {
			step()
		}
		if n := step(); n != c.want {
			t.Errorf("%s: %d frames not yet read, want %d", c.name, n, c.want)
		}
	}
}

// A TrackingAnchor places the tail of a burst, the backlog a connection's
// socket built before it was first read, at arrival plus MaxSourceHold:
// frames that arrive a few milliseconds apart wait in the store as close
// together. Once frames arrive a period apart again, the burst still
// waits, and the frames a tick has not read include those placed up to a
// read lag and the source's delay before the current time. The hold counts
// every one of them up to its bound, or the store drops the oldest, the
// next ones due, and the program freezes until the read point reaches the
// rest.
func TestSourceHoldKeepsABurstWholeUntilItIsRead(t *testing.T) {
	for _, delay := range []int64{0, 18000} {
		for _, decodeNs := range []int64{2_000_000, 4_000_000} {
			now := int64(1_798_000_000_000_000_000)
			fixedTAI(t, &now)
			programRate50(t)
			var h SourceHold
			var placed []int64
			push := func(stepNs int64) {
				now += stepNs
				pts := NowPTS() + MaxSourceHold
				placed = append(placed, pts)
				n := h.Frames(pts, delay)
				unread := 0
				for _, p := range placed {
					if p > NowPTS()-5400-delay {
						unread++
					}
				}
				if want := min(unread, int((MaxSourceHold+delay)/minFramePeriod)); n != want {
					t.Fatalf("delay %d, decode %d ns, frame %d: the hold counts %d frames while %d are not yet read, want %d",
						delay, decodeNs, len(placed), n, unread, want)
				}
			}
			for range 40 { // the burst, a frame per decode
				push(decodeNs)
			}
			for range 40 { // real time, a frame per 20 ms
				push(20_000_000)
			}
		}
	}
}
