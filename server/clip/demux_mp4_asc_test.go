package clip

import "testing"

func TestASCChannelCount(t *testing.T) {
	for cfg, want := range map[int]int{0: 0, 1: 1, 2: 2, 6: 6, 7: 8, 8: 0, 11: 7, 12: 8, 13: 24, 14: 8, 15: 0} {
		if got := ascChannelCount(cfg); got != want {
			t.Errorf("channelConfiguration %d: %d channels, want %d", cfg, got, want)
		}
	}
}
