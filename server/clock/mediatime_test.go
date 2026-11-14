package clock

import (
	"fmt"
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

// Up to 60 fps a tick reads every input two grains behind the clock, as
// it always has. Above, two periods are less time than video transit and
// scheduling take, and part of what the read delay covers, AAC frames
// delivered whole among it, is a time that does not shrink with the
// period: a tick reads at least 40 ms behind, in the fewest grains that
// cover it.
func TestTicksReadTwoGrainsUpTo60FpsAnd40msAbove(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	const floor = 40 * 90 // 40 ms in 90 kHz ticks
	for _, r := range programRates {
		c := NewReferenceClock(int(r.Num), int(r.Den), 48000, PTSBase, true)
		n := TimestampToIndex(r, now)
		tk := c.tickAt(n)
		k := uint64(1)
		for IndexPTS(r, n-k) > tk.ReadPTS {
			k++
		}
		if IndexPTS(r, n-k) != tk.ReadPTS {
			t.Fatalf("%d/%d: read point %d is not on the grain grid", r.Num, r.Den, tk.ReadPTS)
		}
		lag := tk.VideoPTS - tk.ReadPTS
		if r.Num <= 60*r.Den {
			if k != 2 {
				t.Errorf("%d/%d: reads %d grains behind the clock, want 2", r.Num, r.Den, k)
			}
			continue
		}
		if lag < floor {
			t.Errorf("%d/%d: reads %d grains, %.1f ms, behind the clock, under 40 ms",
				r.Num, r.Den, k, float64(lag)/90)
		}
		if k > 2 && tk.VideoPTS-IndexPTS(r, n-k+1) >= floor {
			t.Errorf("%d/%d: reads %d grains behind the clock, where %d cover 40 ms", r.Num, r.Den, k, k-1)
		}
	}
}

// A settled TrackingAnchor's source may arrive up to TrackingSlack later
// than its offset without raising it, so a tick has to read the audio of
// such a frame on time: a tick mixes the period after its read point, so
// audio has a period less than the read delay to arrive in. At 240 fps
// two periods left 4.2 ms of it, under the slack, and a lateness between
// the two ran the audio dry read after read without ever raising.
func TestTrackingSlackIsInsideTheAudioReadBudgetAtEveryRate(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	for _, r := range programRates {
		programRate(t, r)
		c := NewReferenceClock(int(r.Num), int(r.Den), 48000, PTSBase, true)
		n := TimestampToIndex(r, now)
		tk := c.tickAt(n)
		period := IndexPTS(r, n+1) - tk.VideoPTS
		if budget := tk.VideoPTS - tk.ReadPTS - period; budget <= TrackingSlack() {
			t.Errorf("%d/%d: audio has %.1f ms to arrive in, under the %.1f ms TrackingSlack",
				r.Num, r.Den, float64(budget)/90, float64(TrackingSlack())/90)
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

// A connection whose first frames are a backlog, decoded faster than real
// time, arrives each frame less late than the one before. Placed by the
// first, the frames that follow in real time would sit that backlog ahead
// of their arrival, longer than any store holds them. No frame is placed
// more than MaxSourceHold after it arrived, nor before.
func TestTrackingAnchorPlacesABacklogNoFurtherThanAHoldAhead(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	const backlog = 1_500_000_000 // under MaxAnchorSkew: no move places it again
	src := int64(1_000_000)
	for i := 0; i < 6*50; i++ { // 6 s of 20 ms frames
		if i >= backlog/20_000_000 {
			now += 20_000_000 // in real time; the backlog arrives at once
		}
		ahead := a.PTS(src) - PTS(now)
		if ahead < 0 || ahead > MaxSourceHold {
			t.Fatalf("frame %d placed %d ticks after it arrived, want 0 to %d", i, ahead, MaxSourceHold)
		}
		src += 1800
	}
	if a.Moves() != 0 {
		t.Fatalf("%d moves for a backlog under MaxAnchorSkew", a.Moves())
	}
}

// senderPTS is the 90 kHz timestamp of frame i of a sender at rate r
// whose clock runs ppm fast (negative: slow).
func senderPTS(r Rate, i, ppm int64) int64 {
	return 1_000_000 + roundDiv(i*90000*r.Den*(1_000_000+ppm), r.Num*1_000_000)
}

// A source whose sender clock runs fast is followed: its timestamps stay
// within TrackingHistory windows' drift of their arrival rather than
// running ahead without bound, at any frame rate.
func TestTrackingAnchorFollowsAFastSender(t *testing.T) {
	for _, r := range programRates {
		now := int64(1_798_000_000_000_000_000)
		fixedTAI(t, &now)
		start := now
		var a TrackingAnchor
		const ppm = 1000
		for i := int64(1); i <= 360*r.Num/r.Den; i++ { // 6 minutes
			now = start + IndexToTimestamp(r, uint64(i))
			ahead := a.PTS(senderPTS(r, i, ppm)) - PTS(now)
			if ahead < 0 {
				t.Fatalf("%d/%d: frame %d placed %d ticks before it arrived", r.Num, r.Den, i, -ahead)
			}
			// The drift over the settle and history windows at 1000 ppm: 64 ms.
			if ahead > (TrackingSettle+TrackingHistory)*TrackingWindow*ppm/1_000_000+1 {
				t.Fatalf("%d/%d: frame %d placed %d ticks after it arrived: the anchor does not follow the sender",
					r.Num, r.Den, i, ahead)
			}
		}
	}
}

// A source whose sender clock runs slow arrives later with every frame.
// Once settled, the anchor raises its offset each time two windows in a
// row arrived more than TrackingSlack later than it, so no frame is placed
// after its arrival nor more than that and two windows' drift before it,
// however long the drift runs, at any frame rate.
func TestTrackingAnchorFollowsASlowSender(t *testing.T) {
	for _, r := range programRates {
		programRate(t, r)
		now := int64(1_798_000_000_000_000_000)
		fixedTAI(t, &now)
		start := now
		var a TrackingAnchor
		const ppm = -1000
		for i := int64(1); i <= 360*r.Num/r.Den; i++ { // 6 minutes
			now = start + IndexToTimestamp(r, uint64(i))
			if ahead := a.PTS(senderPTS(r, i, ppm)) - PTS(now); ahead > 0 || ahead < -(TrackingSlack()-2*TrackingWindow*ppm/1_000_000+1) {
				t.Fatalf("%d/%d: frame %d placed %d ticks off its arrival", r.Num, r.Den, i, ahead)
			}
		}
		if a.Moves() != 0 {
			t.Fatalf("%d/%d: %d moves for a source that drifted 0.36 s and never jumped", r.Num, r.Den, a.Moves())
		}
	}
}

// Once settled, a TrackingAnchor's offset stays where it is through a late
// burst, as a sender or decode loop that stalls for 200 ms delivers: every
// frame of the stall arrives at its end. Following it, the offset moved
// the source's audio 200 ms later, and back once the burst was forgotten,
// and the mixer spliced the audio at both steps. A lateness that lasts
// two windows raises the offset, once and by exactly that lateness.
func TestTrackingAnchorMovesOnlyOnALastingLateness(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	src := int64(1_000_000)
	frame := func(lateNs int64) {
		now += 20_000_000
		src += 1800
		now += lateNs
		a.PTS(src)
		now -= lateNs
	}
	const perWindow = TrackingWindow / 1800
	for range (TrackingSettle + 1) * perWindow {
		frame(0)
	}
	settled := a.Stats()
	for w := range 300 { // ten minutes, a stall in every fourth window
		for f := 0; f < perWindow; f++ {
			if w%4 == 0 && f == perWindow/2 {
				for j := range int64(10) {
					frame((10 - j) * 20_000_000)
				}
				f += 9
				continue
			}
			frame(0)
			if s := a.Stats(); s.Offset != settled.Offset {
				t.Fatalf("window %d frame %d: offset moved %d ticks after a stall", w, f, s.Offset-settled.Offset)
			}
		}
	}
	for range 3 * perWindow {
		frame(100_000_000)
	}
	if s := a.Stats(); s.Offset != settled.Offset+9000 || s.Raises != settled.Raises+1 {
		t.Fatalf("after three windows 100 ms later: %+v, want one raise of 9000 ticks over %+v", s, settled)
	}
}

// A settled source whose frames arrive later than its offset for good,
// by less than its slack, is read on time where it is, its audio
// included. A raise would move its video that much later and leave its
// audio, which the mixer realigns only past a period, so the two would
// sit apart until the offset came down a minute later. A lateness past
// the audio budget, ReadDelay-1 periods, a period up to 60 fps, would
// have its audio run dry at reads, so two windows of it raise the offset,
// once. Both hold at every program rate; a slack fixed in time would have
// to be under the 4.2 ms period at 240 fps read two periods behind, and
// would then raise for a lateness under the period at every slower rate.
func TestTrackingAnchorRaisesOnlyPastAnAudioPeriod(t *testing.T) {
	for _, r := range programRates {
		t.Run(fmt.Sprintf("%d/%d", r.Num, r.Den), func(t *testing.T) {
			programRate(t, r)
			now := int64(1_798_000_000_000_000_000)
			fixedTAI(t, &now)
			var a TrackingAnchor
			src := int64(1_000_000)
			frame := func(late int64) { // late in 90 kHz ticks
				now += 20_000_000
				src += 1800
				lateNs := late * 100_000 / 9
				now += lateNs
				a.PTS(src)
				now -= lateNs
			}
			const perWindow = TrackingWindow / 1800
			for range (TrackingSettle + 1) * perWindow {
				frame(0)
			}
			settled := a.Stats()
			// Past the audio budget, ReadDelay-1 periods, audio runs dry.
			period := ProgramPeriod()
			under, over := TrackingSlack()-10, (int64(ProgramReadDelay())-1)*period+10
			for range 5 * perWindow {
				frame(under)
			}
			if s := a.Stats(); s != settled {
				t.Fatalf("after five windows %d ticks late, period %d: %+v, want the settled %+v", under, period, s, settled)
			}
			for range 3 * perWindow {
				frame(over)
			}
			if s := a.Stats(); s.Raises != settled.Raises+1 || s.Offset < settled.Offset+over-1 || s.Offset > settled.Offset+over+1 {
				t.Fatalf("after three windows %d ticks late, period %d: %+v, want one raise of %d ticks over %+v", over, period, s, over, settled)
			}
		})
	}
}

// The slack is a program period less TrackingDriftMargin at the read
// delay of two periods, every rate up to 60 fps: audio is read for the
// period after the read point. Above, the read delay is longer, and
// video's half of it is the lesser bound.
func TestTrackingSlackFollowsTheProgramPeriodAndReadDelay(t *testing.T) {
	// Up to 60 fps a period less TrackingDriftMargin; above, the read
	// delay is longer and video's half of it is the lesser bound.
	want := map[Rate]int64{
		{24000, 1001}: 3664, {24, 1}: 3660, {25, 1}: 3510, {30000, 1001}: 2913, {30, 1}: 2910,
		{50, 1}: 1710, {60000, 1001}: 1412, {60, 1}: 1410, {100, 1}: 1710, {120000, 1001}: 1787,
		{120, 1}: 1785, {240000, 1001}: 1790, {240, 1}: 1785,
	}
	for _, r := range programRates {
		t.Run(fmt.Sprintf("%d/%d", r.Num, r.Den), func(t *testing.T) {
			programRate(t, r)
			if got := TrackingSlack(); got != want[r] {
				t.Fatalf("slack %d ticks at period %d, want %d", got, ProgramPeriod(), want[r])
			}
		})
	}
	for _, c := range []struct{ period, delay, want int64 }{
		{750, 5, 1785},  // 120 fps read 5 periods behind: video's 2.5 periods
		{375, 10, 1785}, // 240 fps read 10 periods behind
		{1800, 3, 2610}, // 50 fps read 3 periods behind
	} {
		if got := trackingSlack(c.period, c.delay); got != c.want {
			t.Errorf("period %d read %d periods behind: slack %d, want %d", c.period, c.delay, got, c.want)
		}
	}
}

// Once settled, SettledPTS reports each move of the offset by half a
// program period or more once, a raise and the lowering a minute later
// alike: the caller places the source's audio on its stamps again, as the
// move places its video, and a move under a period is one the mixer never
// realigns. A smaller lowering, as a sender clock's drift brings, is not
// reported: placing the audio again would splice it. Holds at every rate.
func TestTrackingAnchorReportsEachMoveOnce(t *testing.T) {
	for _, r := range programRates {
		t.Run(fmt.Sprintf("%d/%d", r.Num, r.Den), func(t *testing.T) {
			programRate(t, r)
			now := int64(1_798_000_000_000_000_000)
			fixedTAI(t, &now)
			var a TrackingAnchor
			src := int64(1_000_000)
			reports := 0
			frames := func(count int, late int64) { // late in 90 kHz ticks
				lateNs := late * 100_000 / 9
				for range count {
					now += 20_000_000
					src += 1800
					now += lateNs
					if _, moved := a.SettledPTS(src); moved {
						reports++
					}
					now -= lateNs
				}
			}
			const perWindow = TrackingWindow / 1800
			frames((TrackingSettle+1)*perWindow, 0)
			if reports != 1 {
				t.Fatalf("%d reports after the settle, want 1", reports)
			}
			late := TrackingSlack() + TrackingDriftMargin/2 // past the slack, inside the audio budget
			frames(3*perWindow, late)
			if s := a.Stats(); s.Raises != 1 || reports != 2 {
				t.Fatalf("after three windows %d ticks late: %+v, %d reports, want 1 raise and 2 reports", late, s, reports)
			}
			frames((TrackingHistory+3)*perWindow, 0)
			if s := a.Stats(); s.Offset != 0 || reports != 3 {
				t.Fatalf("a minute on time again: %+v, %d reports, want the offset back and 3 reports", s, reports)
			}
			small := ProgramPeriod()/2 - 10
			frames(1, -small) // a window whose latest frame came early, never matched again
			frames((TrackingHistory+3)*perWindow, -small)
			if s := a.Stats(); s.Offset != -small || reports != 3 {
				t.Fatalf("a minute %d ticks earlier: %+v, %d reports, want the offset lowered by it and still 3 reports", small, s, reports)
			}
		})
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

// A TrackingAnchor counts the timestamps that raised its offset, and
// reports how far the offset moved since the source was placed: a frame
// that arrives late raises it once, by its excess lateness, and two
// windows later the offset is back where it was. Neither the first
// placement nor a placement after a jump is a raise.
func TestTrackingAnchorCountsItsRaises(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	src := int64(1_000_000)
	frame := func(lateNs int64) {
		now += 20_000_000
		src += 1800
		now += lateNs
		a.PTS(src)
		now -= lateNs
	}
	for range 50 {
		frame(0)
	}
	if s := a.Stats(); s != (TrackingStats{}) {
		t.Fatalf("a source arriving in time: %+v, want no raise and no offset change", s)
	}
	frame(15_000_000) // 15 ms late
	if s := a.Stats(); s.Raises != 1 || s.Offset != 1350 {
		t.Fatalf("after one frame 15 ms late: %+v, want 1 raise and an offset 1350 ticks up", s)
	}
	for range 2*TrackingWindow/1800 + 1 {
		frame(0)
	}
	if s := a.Stats(); s.Raises != 1 || s.Offset != 0 {
		t.Fatalf("two windows later: %+v, want the 1 raise and the offset back at 0", s)
	}
	src += 10 * 90000 // the source restarts 10 s on
	frame(0)
	if s := a.Stats(); s.Raises != 1 || s.Offset != 0 || s.Moves != 1 {
		t.Fatalf("after a jump: %+v, want 1 move, no new raise and an offset of 0 from the new placement", s)
	}
}

// SettledPTS reports the settle once per placement, on the first
// timestamp it maps on the settled offset, also when a PTS call made the
// anchor settle: the caller places the source's audio on its stamps again
// from that timestamp, and placed again after a jump, settles again.
func TestTrackingAnchorReportsEachSettleOnce(t *testing.T) {
	now := int64(1_798_000_000_000_000_000)
	fixedTAI(t, &now)
	var a TrackingAnchor
	src := int64(1_000_000)
	settles := 0
	frames := func(count int) {
		for range count {
			now += 20_000_000
			src += 1800
			a.PTS(src)
			if _, settled := a.SettledPTS(src); settled {
				settles++
			}
		}
	}
	frames(TrackingSettle*TrackingWindow/1800 - 1)
	if settles != 0 {
		t.Fatalf("%d settles reported before the settle windows passed", settles)
	}
	frames(2)
	if settles != 1 {
		t.Fatalf("%d settles reported once the settle windows passed, want 1", settles)
	}
	frames(2 * TrackingWindow / 1800)
	if settles != 1 {
		t.Fatalf("%d settles reported two windows on, want still 1", settles)
	}
	src += 10 * 90000 // the source restarts 10 s on
	frames((TrackingSettle+1)*TrackingWindow/1800 + 1)
	if settles != 2 {
		t.Fatalf("%d settles reported after a jump, want 2", settles)
	}
}
