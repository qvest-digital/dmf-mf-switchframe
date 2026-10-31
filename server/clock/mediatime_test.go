package clock

import (
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
