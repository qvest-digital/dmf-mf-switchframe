package clock

import (
	"sync"
	"testing"
	"time"
)

// fixedTAI pins TAINow and the engine epoch for one test.
func fixedTAI(t *testing.T, now *int64) {
	t.Helper()
	SetTAISource(func() int64 { return *now })
	epochOnce.Do(func() {})
	saved := epochNs
	epochNs = *now
	t.Cleanup(func() {
		epochNs = saved
		SetTAISource(func() int64 { return time.Now().UnixNano() })
	})
}

// The conversions round to the nearest unit exactly as libmxl's
// timestampToIndex and indexToTimestamp do, so an index taken from a flow
// and one computed here for the same TAI agree.
func TestIndexConversionMatchesLibmxlRounding(t *testing.T) {
	ntsc := Rate{60000, 1001}
	for _, tc := range []struct {
		r    Rate
		ts   int64
		want uint64
	}{
		{Rate{50, 1}, 9_999_999, 0},
		{Rate{50, 1}, 10_000_000, 1}, // half a period rounds up
		{Rate{48000, 1}, 1_000_000_000, 48000},
		{ntsc, 16_683_333, 1}, // 1001/60000 s = 16.6833... ms
		{ntsc, 8_341_666, 0},
		{ntsc, 8_341_667, 1},
	} {
		if got := TimestampToIndex(tc.r, tc.ts); got != tc.want {
			t.Errorf("TimestampToIndex(%v, %d) = %d, want %d", tc.r, tc.ts, got, tc.want)
		}
	}
	// A current TAI, 1.8e18 ns, needs 128-bit intermediates.
	ts := int64(1_798_000_000_123_456_789)
	for _, r := range []Rate{{50, 1}, {60000, 1001}, {48000, 1}, {30000, 1001}} {
		i := TimestampToIndex(r, ts)
		if back := TimestampToIndex(r, IndexToTimestamp(r, i)); back != i {
			t.Errorf("%v: index %d -> %d after a round trip", r, i, back)
		}
	}
}

// A sample or grain index survives the round trip through a 90 kHz PTS:
// a 48 kHz sample lasts 20.8 us and a PTS tick 11.1 us, so rounding both
// ways to the nearest loses nothing.
func TestPTSRoundTripKeepsIndex(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	for _, r := range []Rate{{48000, 1}, {50, 1}, {60000, 1001}} {
		base := TimestampToIndex(r, now)
		for i := base; i < base+200_000; i += 7 {
			if got := PTSIndex(r, IndexPTS(r, i)); got != i {
				t.Fatalf("%v: index %d -> PTS %d -> index %d", r, i, IndexPTS(r, i), got)
			}
		}
	}
}

// Ticks are numbered by the program grain index on TAI, so their PTS is
// the grain's own and a tick that starts late still carries it.
func TestRealtimeTickIsTheTAIGrainIndex(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	r := Rate{50, 1}
	c := NewReferenceClock(50, 1, 48000, PTSBase, true)
	n := TimestampToIndex(r, now) + 1
	tk := c.tickAt(n)
	if tk.Index != n {
		t.Fatalf("tick index %d, want %d", tk.Index, n)
	}
	if want := IndexPTS(r, n); tk.VideoPTS != want {
		t.Fatalf("tick PTS %d, want %d (the grain's TAI)", tk.VideoPTS, want)
	}
	if want := IndexPTS(r, n-ReaderDelayGrains); tk.ReadPTS != want {
		t.Fatalf("read PTS %d, want %d: inputs are read %d grains behind the tick",
			tk.ReadPTS, want, ReaderDelayGrains)
	}
	if tk.VideoPTS-tk.ReadPTS != ReaderDelayGrains*1800 {
		t.Fatalf("read delay %d ticks", tk.VideoPTS-tk.ReadPTS)
	}
}

// At NTSC rates a frame holds a non-integer number of samples. Tick n
// carries [I(Ra, T(Rv, n)), I(Ra, T(Rv, n+1))), so consecutive ticks tile
// the sample timeline with no gap and no overlap, anchored on TAI.
func TestTickAudioRangesTileTheSampleTimeline(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	for _, fr := range []Rate{{60000, 1001}, {30000, 1001}, {50, 1}, {24000, 1001}} {
		c := NewReferenceClock(int(fr.Num), int(fr.Den), 48000, PTSBase, true)
		n0 := TimestampToIndex(fr, now)
		prevEnd := uint64(0)
		var total uint64
		for n := n0; n < n0+30000; n++ {
			tk := c.tickAt(n)
			if want := TimestampToIndex(Rate{48000, 1}, IndexToTimestamp(fr, n)); tk.AudioStart != want {
				t.Fatalf("%v tick %d: audio start %d, want %d", fr, n, tk.AudioStart, want)
			}
			if prevEnd != 0 && tk.AudioStart != prevEnd {
				t.Fatalf("%v tick %d: audio starts at %d, previous ended at %d", fr, n, tk.AudioStart, prevEnd)
			}
			prevEnd = tk.AudioStart + uint64(tk.AudioSamples)
			total += uint64(tk.AudioSamples)
		}
		start := TimestampToIndex(Rate{48000, 1}, IndexToTimestamp(fr, n0))
		end := TimestampToIndex(Rate{48000, 1}, IndexToTimestamp(fr, n0+30000))
		if total != end-start {
			t.Fatalf("%v: %d samples over 30000 ticks, want %d", fr, total, end-start)
		}
	}
}

// A tick that starts late but still within its own grain is delivered: only
// a grain that has passed completely is dropped. Rounding the current time
// to the nearest grain dropped every tick that woke more than half a frame
// late.
func TestRealtimeTickDropsOnlyPassedGrains(t *testing.T) {
	r := Rate{Num: 50, Den: 1}
	k := uint64(90_000_000_000)
	period := IndexToTimestamp(r, k+1) - IndexToTimestamp(r, k)
	Epoch() // set before the scripted clock, which it would consume
	var mu sync.Mutex
	calls := 0
	SetTAISource(func() int64 {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 { // the clock starts just before grain k
			return IndexToTimestamp(r, k) - period/10
		}
		return IndexToTimestamp(r, k+1) + period*6/10 // wakes 0.6 frame late
	})
	c := NewReferenceClock(50, 1, 48000, PTSBase, true)
	t.Cleanup(func() {
		c.Stop()
		SetTAISource(func() int64 { return time.Now().UnixNano() })
	})
	c.Start()
	select {
	case tk := <-c.Ticks():
		if tk.Index != k+1 || c.DroppedTicks() != 0 {
			t.Fatalf("first tick is grain k+%d with %d dropped; want k+1, none dropped: "+
				"a tick 0.6 frame late is still within its grain", tk.Index-k, c.DroppedTicks())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tick")
	}
}

// An anchored source keeps its own timestamp distances, video and audio
// alike, and is placed again only after a jump.
func TestAnchorKeepsSourceDistances(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a Anchor
	v0 := a.PTS(1_000_000)
	now += 10_000_000 // audio arrives 10 ms later than its timestamp says
	a0 := a.PTS(1_000_000 - 1800)
	if v0-a0 != 1800 {
		t.Fatalf("audio %d ticks before video, want the source's 1800", v0-a0)
	}
	now += 3_000_000_000
	if j := a.PTS(1_000_000 + 270_000); j-v0 != 270_000 || a.Moves() != 0 {
		t.Fatalf("PTS step %d after 3 s, want 270000 without a move", j-v0)
	}
	now += 1_000_000_000
	if j := a.PTS(5_000_000_000); a.Moves() != 1 || j != PTS(now) {
		t.Fatalf("a jump far from the clock was not placed at arrival (PTS %d, moves %d)", j, a.Moves())
	}
}

// A source whose audio arrives in bursts is placed at its latest arrival:
// every timestamp is placed no earlier than it arrived, and the bursts that
// follow keep the source's own distances.
func TestTrackingAnchorPlacesBurstsAtTheirLatestArrival(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	const frame = 1800 // 20 ms
	src := int64(1_000_000)
	var last int64
	for b := 0; b < 20; b++ {
		// Nine frames arrive together once the last of them has passed.
		now += 9 * 20_000_000
		for k := 0; k < 9; k++ {
			pts := a.PTS(src)
			if pts < PTS(now) {
				t.Fatalf("burst %d frame %d placed %d ticks before it arrived", b, k, PTS(now)-pts)
			}
			if b > 0 && pts-last != frame {
				t.Fatalf("burst %d frame %d steps %d ticks, want the source's %d", b, k, pts-last, frame)
			}
			last = pts
			src += frame
		}
	}
	if a.Moves() != 0 {
		t.Fatalf("%d moves for a source that never jumped", a.Moves())
	}
}

// A source whose sender clock runs fast is followed: its timestamps stay
// within a window's drift of their arrival rather than running ahead
// without bound.
func TestTrackingAnchorFollowsAFastSender(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	const ppm = 1000
	src := int64(1_000_000)
	for i := 0; i < 360*50; i++ { // 6 minutes of 20 ms frames
		now += 20_000_000
		src += 1800 + 1800*ppm/1_000_000
		ahead := a.PTS(src) - PTS(now)
		if ahead < 0 {
			t.Fatalf("frame %d placed %d ticks before it arrived", i, -ahead)
		}
		// Two windows of drift at 1000 ppm: 4 ms.
		if ahead > 2*TrackingWindow*ppm/1_000_000+1 {
			t.Fatalf("frame %d placed %d ticks after it arrived: the anchor does not follow the sender", i, ahead)
		}
	}
}

// A source whose sender clock runs slow arrives later with every frame.
// The anchor raises its offset with each one, so every frame is placed at
// its arrival: never before it, and never held behind it, however long the
// drift runs.
func TestTrackingAnchorFollowsASlowSender(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	const ppm = 1000
	src := int64(1_000_000)
	for i := 0; i < 360*50; i++ { // 6 minutes of 20 ms frames
		now += 20_000_000
		src += 1800 - 1800*ppm/1_000_000
		if ahead := a.PTS(src) - PTS(now); ahead != 0 {
			t.Fatalf("frame %d placed %d ticks off its arrival", i, ahead)
		}
	}
	if a.Moves() != 0 {
		t.Fatalf("%d moves for a source that drifted 0.36 s and never jumped", a.Moves())
	}
}

// scriptedTAI makes TAINow return *now, guarded by mu, from after the
// epoch is set, and counts the reads in *reads.
func scriptedTAI(t *testing.T, mu *sync.Mutex, now *int64, reads *int) {
	t.Helper()
	Epoch()
	SetTAISource(func() int64 {
		mu.Lock()
		defer mu.Unlock()
		*reads++
		return *now
	})
	t.Cleanup(func() { SetTAISource(func() int64 { return time.Now().UnixNano() }) })
}

// advanceAfterReads sets *now to to once the clock has read the time
// twice: for its first grain and for the wait until it.
func advanceAfterReads(t *testing.T, mu *sync.Mutex, now *int64, reads *int, to int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		if *reads >= 2 {
			*now = to
			mu.Unlock()
			return
		}
		mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("clock never read the time")
		}
		time.Sleep(time.Millisecond)
	}
}

// A consumer that falls behind by a few grains, as a sync loop does on a
// tick that takes several periods, still gets every grain's tick, in
// order: each tick carries its grain's audio, so a dropped tick is a gap
// in the program audio.
func TestRealtimeDeliversTicksAConsumerFellBehindOn(t *testing.T) {
	r := Rate{Num: 10, Den: 1}
	k := uint64(17_980_000_000)
	period := IndexToTimestamp(r, k+1) - IndexToTimestamp(r, k)
	var mu sync.Mutex
	reads := 0
	now := IndexToTimestamp(r, k) + period/10 // the first tick is grain k+1
	scriptedTAI(t, &mu, &now, &reads)
	c := NewReferenceClock(10, 1, 48000, PTSBase, true)
	t.Cleanup(c.Stop)
	c.Start()
	// The consumer is busy until three grains later.
	advanceAfterReads(t, &mu, &now, &reads, IndexToTimestamp(r, k+4)+period/10)

	for want := k + 1; want <= k+4; want++ {
		select {
		case tk := <-c.Ticks():
			if tk.Index != want {
				t.Fatalf("tick for grain k+%d, want k+%d (%d dropped)", tk.Index-k, want-k, c.DroppedTicks())
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no tick for grain k+%d", want-k)
		}
	}
	if d := c.DroppedTicks(); d != 0 {
		t.Fatalf("%d ticks dropped for a consumer %d grains behind", d, 3)
	}
}

// A consumer further behind than the catch-up bound (0.5 s) resumes at the
// current grain: the ticks in between are dropped and counted.
func TestRealtimeResumesAtTheClockPastTheCatchUpBound(t *testing.T) {
	r := Rate{Num: 10, Den: 1}
	k := uint64(17_980_000_000)
	period := IndexToTimestamp(r, k+1) - IndexToTimestamp(r, k)
	const behind = 8 // 0.8 s at 10/1
	var mu sync.Mutex
	reads := 0
	now := IndexToTimestamp(r, k) + period/10
	scriptedTAI(t, &mu, &now, &reads)
	c := NewReferenceClock(10, 1, 48000, PTSBase, true)
	t.Cleanup(c.Stop)
	c.Start()
	advanceAfterReads(t, &mu, &now, &reads, IndexToTimestamp(r, k+1+behind)+period/10)

	select {
	case tk := <-c.Ticks():
		if tk.Index != k+1+behind || c.DroppedTicks() != behind {
			t.Fatalf("first tick grain k+%d with %d dropped, want k+%d with %d",
				tk.Index-k, c.DroppedTicks(), 1+behind, behind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tick")
	}
}

// A consumer that stalls past the catch-up bound while the clock holds a
// tick in the channel's buffer and has the next one waiting to be sent
// resumes at the current grain too: those two ticks are older than the
// bound by the time it takes one, and are dropped and counted as well.
func TestRealtimeDropsTheTicksAStalledConsumerLeftWaiting(t *testing.T) {
	r := Rate{Num: 10, Den: 1}
	k := uint64(17_980_000_000)
	period := IndexToTimestamp(r, k+1) - IndexToTimestamp(r, k)
	var mu sync.Mutex
	reads := 0
	now := IndexToTimestamp(r, k) + period/10
	scriptedTAI(t, &mu, &now, &reads)
	c := NewReferenceClock(10, 1, 48000, PTSBase, true)
	t.Cleanup(c.Stop)
	c.Start()
	advanceAfterReads(t, &mu, &now, &reads, IndexToTimestamp(r, k+3)+period/10)

	select {
	case tk := <-c.Ticks():
		if tk.Index != k+1 {
			t.Fatalf("first tick grain k+%d, want k+1", tk.Index-k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tick")
	}
	// The consumer stalls: grain k+2's tick waits in the buffer.
	deadline := time.Now().Add(2 * time.Second)
	for len(c.ticks) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the clock never buffered the next tick")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	now = IndexToTimestamp(r, k+11) + period/10 // 0.8 s later
	mu.Unlock()
	// The clock notices the stale ticks within the bound, in wall time.
	deadline = time.Now().Add(2 * time.Second)
	for c.DroppedTicks() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	select {
	case tk := <-c.Ticks():
		if tk.Index != k+11 {
			t.Fatalf("after the stall the consumer got grain k+%d with %d dropped, want the current grain k+11",
				tk.Index-k, c.DroppedTicks())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tick after the stall")
	}
	if d := c.DroppedTicks(); d != 9 {
		t.Fatalf("%d ticks dropped, want the 9 of grains k+2 to k+10", d)
	}
}
