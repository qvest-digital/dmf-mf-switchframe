package mxl

import "testing"

func TestVideoIndexFor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		now      uint64
		last     uint64
		haveLast bool
		fresh    bool
		stalled  bool
		want     uint64
		wantOK   bool
	}{
		{"first frame writes the clock", 100, 0, false, true, false, 100, true},
		{"new frame on the grid", 101, 100, true, true, false, 101, true},
		// A second frame in one period takes the next grain instead of
		// replacing the first, which would lose it.
		{"new frame a grain ahead", 100, 100, true, true, false, 101, true},
		// Two grains ahead it waits for the clock.
		{"new frame two grains ahead", 100, 101, true, true, false, 0, false},
		// A frame late for its period still takes the grain it was late for.
		{"new frame a grain late", 102, 100, true, true, false, 101, true},
		// Far behind, the writer resumes at the grain before the clock.
		{"new frame far behind", 110, 100, true, true, false, 109, true},
		// Nothing new: the last frame is repeated only once the clock is
		// past the grain after it, so a late frame can still take it.
		{"repeat while the next grain is current", 101, 100, true, false, false, 0, false},
		{"repeat once the next grain has passed", 102, 100, true, false, false, 101, true},
		{"repeat while ahead", 100, 100, true, false, false, 0, false},
		{"repeat far behind", 110, 100, true, false, false, 109, true},
		// Stalled: each further repeat takes its grain as the grain starts.
		{"stalled repeat as its grain starts", 101, 100, true, false, true, 101, true},
		{"stalled repeat ahead of its grain", 100, 100, true, false, true, 0, false},
		// A frame arriving after a stall is new and goes in at once.
		{"new frame after a stall", 101, 101, true, true, true, 102, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := videoIndexFor(tc.now, tc.last, tc.haveLast, tc.fresh, tc.stalled)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("videoIndexFor(%d, %d, %v, %v, %v) = %d, %v; want %d, %v",
					tc.now, tc.last, tc.haveLast, tc.fresh, tc.stalled, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
