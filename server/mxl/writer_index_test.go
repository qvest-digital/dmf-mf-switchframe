package mxl

import "testing"

func TestNextVideoIndex(t *testing.T) {
	for _, tc := range []struct {
		name     string
		now      uint64
		last     uint64
		haveLast bool
		want     uint64
		wantOK   bool
	}{
		{"first tick writes the clock", 100, 0, false, 100, true},
		{"on the grid", 101, 100, true, 101, true},
		// Two ticks in one grain period: the second takes the next index
		// rather than repeating one libmxl refuses.
		{"second tick in a period", 100, 100, true, 101, true},
		// Already a grain ahead: another early tick must not run further.
		{"early tick while ahead", 100, 101, true, 0, false},
		// A late tick catches up to the clock; libmxl invalidates the gap.
		{"late tick", 105, 100, true, 105, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nextVideoIndex(tc.now, tc.last, tc.haveLast)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("nextVideoIndex(%d, %d, %v) = %d, %v; want %d, %v",
					tc.now, tc.last, tc.haveLast, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// Over a run of ticks that jitter around the grain grid, every committed index
// is past the one before it and none is skipped.
func TestNextVideoIndex_JitteredTicksStayContiguous(t *testing.T) {
	// Wall-clock grain index seen at each tick: a period with two ticks,
	// then one with none.
	clock := []uint64{10, 11, 11, 13, 14, 14, 16, 17, 18, 18, 20}
	var last uint64
	have := false
	var written []uint64
	for _, now := range clock {
		idx, ok := nextVideoIndex(now, last, have)
		if !ok {
			continue
		}
		written = append(written, idx)
		last, have = idx, true
	}
	for i := 1; i < len(written); i++ {
		if written[i] != written[i-1]+1 {
			t.Fatalf("indices %v are not contiguous at %d", written, i)
		}
	}
	if written[len(written)-1] < clock[len(clock)-1] {
		t.Fatalf("writer fell behind the clock: last %d, clock %d", written[len(written)-1], clock[len(clock)-1])
	}
}
