package clock

import (
	"fmt"
	"testing"
)

// programRates are the program rates every rate-dependent test runs at:
// the broadcast rates, 100 to 240 fps, and 120000/1001 and 240000/1001,
// whose periods are not whole 90 kHz ticks.
var programRates = []Rate{
	{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1}, {50, 1},
	{60000, 1001}, {60, 1}, {100, 1}, {120000, 1001}, {120, 1},
	{240000, 1001}, {240, 1},
}

// programRate has the ticks read at r for the test's duration.
func programRate(t *testing.T, r Rate) {
	t.Helper()
	lag, period, delay, hold := readLag.Load(), programPeriod.Load(), programDelay.Load(), holdPeriod.Load()
	SetProgramRate(r)
	t.Cleanup(func() {
		readLag.Store(lag)
		programPeriod.Store(period)
		programDelay.Store(delay)
		holdPeriod.Store(hold)
	})
}

// programRate50 has the ticks read at 50 fps for the test's duration.
func programRate50(t *testing.T) {
	t.Helper()
	programRate(t, Rate{Num: 50, Den: 1})
}

// The read lag is ReadDelay program frame periods and the period to the
// next tick, in whole 90 kHz ticks, rounded up, and a realtime clock sets
// it for its rate as it starts. Up to 60 fps it is three periods; above,
// the read delay is as many periods as cover 40 ms.
func TestSetProgramRateSetsTheReadLag(t *testing.T) {
	programRate50(t)
	want := map[Rate]int64{
		{24000, 1001}:  11262, // 3 x 3753.75
		{24, 1}:        11250,
		{25, 1}:        10800,
		{30000, 1001}:  9009,
		{30, 1}:        9000,
		{50, 1}:        5400,
		{60000, 1001}:  4505, // 3 x 1501.5
		{60, 1}:        4500,
		{100, 1}:       4500, // 5 x 900
		{120000, 1001}: 4505, // 6 x 750.75
		{120, 1}:       4500,
		{240000, 1001}: 4130, // 11 x 375.375
		{240, 1}:       4125,
	}
	for _, r := range programRates {
		SetProgramRate(r)
		if got := readLag.Load(); got != want[r] {
			t.Errorf("rate %d/%d: read lag %d ticks, want %d", r.Num, r.Den, got, want[r])
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

// A source at the program rate that a TrackingAnchor places a whole hold
// after its arrival has every frame from MaxSourceHold ahead to the read
// lag behind waiting for a tick, at any program rate: about 131 frames at
// 240 fps. A hold kept for 120 fps at most counted 60 of them, and the
// store dropped the rest, the next ones due.
func TestSourceHoldCountsASourceAtTheProgramRateWhole(t *testing.T) {
	for _, r := range programRates {
		t.Run(fmt.Sprintf("%d/%d", r.Num, r.Den), func(t *testing.T) {
			now := int64(1_798_000_000_000_000_000)
			fixedTAI(t, &now)
			programRate(t, r)
			var h SourceHold
			var placed []int64
			first := TimestampToIndex(r, now) + 1
			for i := uint64(0); i < 2*uint64(r.Num/r.Den); i++ {
				now = IndexToTimestamp(r, first+i)
				pts := NowPTS() + MaxSourceHold
				placed = append(placed, pts)
				got := h.Frames(pts, 0)
				unread := 0
				for _, p := range placed {
					if p > NowPTS()-readLag.Load() {
						unread++
					}
				}
				if got != unread {
					t.Fatalf("frame %d: the hold counts %d frames while %d are not yet read", i, got, unread)
				}
			}
		})
	}
}

// A source's hold counts the frames a tick has not read yet: those placed
// after the current time less the program's read lag (60 ms at 50 fps)
// and the source's delay, never more than (MaxSourceHold + the read lag +
// delay) / minFramePeriod. The source steps its PTS by 20 ms; the offset it is
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
		{"placed 5 s after arrival", 5 * 90000, 0, (MaxSourceHold + 5400) / minFramePeriod},
		{"placed 5 s after arrival, delay 200 ms", 0, 18000, (MaxSourceHold + 5400 + 18000) / minFramePeriod},
	} {
		off += c.off
		delay = c.delay
		for range (MaxSourceHold + MaxSourceDelay + 5400) / minFramePeriod {
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
				if want := min(unread, int((MaxSourceHold+5400+delay)/minFramePeriod)); n != want {
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
