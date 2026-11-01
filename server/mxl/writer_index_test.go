package mxl

import "testing"

func TestVideoIndexFor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		now     uint64
		last    uint64
		stalled bool
		want    uint64
		wantOK  bool
	}{
		// The last frame is repeated only once the clock is past the
		// grain after it, so a late frame can still take it.
		{"repeat while the next grain is current", 101, 100, false, 0, false},
		{"repeat once the next grain has passed", 102, 100, false, 101, true},
		{"repeat while ahead", 100, 100, false, 0, false},
		{"repeat far behind", 110, 100, false, 109, true},
		// Stalled: each further repeat takes its grain as the grain starts.
		{"stalled repeat as its grain starts", 101, 100, true, 101, true},
		{"stalled repeat ahead of its grain", 100, 100, true, 0, false},
		{"stalled repeat far behind", 110, 100, true, 109, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := videoIndexFor(tc.now, tc.last, tc.stalled)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("videoIndexFor(%d, %d, %v) = %d, %v; want %d, %v",
					tc.now, tc.last, tc.stalled, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
