package replay

import "testing"

// TestUseGPUReplayPlayer verifies the routing decision that bounds GPU replay
// VRAM usage: the GPU player decodes the entire clip into VRAM up front
// (random access is needed for seek/loop), so clips longer than the frame
// budget must be routed to the CPU player, which uses a 2-GOP sliding window
// and handles arbitrary clip lengths with bounded memory.
func TestUseGPUReplayPlayer(t *testing.T) {
	tests := []struct {
		name       string
		gpuCapable bool
		clipFrames int
		want       bool
	}{
		{"no GPU", false, 10, false},
		{"GPU, short clip", true, 10, true},
		{"GPU, exactly at budget", true, maxGPUClipFrames, true},
		{"GPU, one over budget", true, maxGPUClipFrames + 1, false},
		{"GPU, 300s buffer at 30fps", true, 300 * 30, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := useGPUReplayPlayer(tt.gpuCapable, tt.clipFrames); got != tt.want {
				t.Errorf("useGPUReplayPlayer(%v, %d) = %v, want %v",
					tt.gpuCapable, tt.clipFrames, got, tt.want)
			}
		})
	}
}

// TestMaxGPUClipFramesIsBounded sanity-checks that the budget keeps worst-case
// Phase-1 VRAM usage in the low single-digit GiB range: a 1080p NV12 frame is
// ~3.1 MB, so the budget must stay well below the multi-GB blowups the
// unbounded path allowed (a 300s buffer at 30fps was ~28 GiB).
func TestMaxGPUClipFramesIsBounded(t *testing.T) {
	const bytesPerFrame1080pNV12 = 1920 * 1080 * 3 / 2
	worstCase := int64(maxGPUClipFrames) * bytesPerFrame1080pNV12
	const limit = 3 << 30 // 3 GiB
	if worstCase > limit {
		t.Fatalf("maxGPUClipFrames=%d implies %d bytes of VRAM at 1080p, want <= %d",
			maxGPUClipFrames, worstCase, limit)
	}
	if maxGPUClipFrames < 60 {
		t.Fatalf("maxGPUClipFrames=%d is too small to play even a 2s clip", maxGPUClipFrames)
	}
}
