package main

import (
	"sync"
	"testing"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/switcher"
)

// TestScaleClipFrame_PerPlayerBuffers verifies that each clip player gets its
// own persistent scale buffer. The raw-video callback registered in initClips
// is invoked concurrently from up to clip.MaxPlayers decode goroutines
// (clip.Manager wires the same callback into every player, and each player
// runs `go p.run(ctx)`), so a single shared buffer would be concurrently
// reallocated and overwritten across players, corrupting frames mid-ingest.
func TestScaleClipFrame_PerPlayerBuffers(t *testing.T) {
	var bufs [clip.MaxPlayers][]byte

	const srcW, srcH = 64, 48
	const dstW, dstH = 128, 96
	src := make([]byte, switcher.ColorYUV420_8bit.FrameSize(srcW, srcH))

	out0 := scaleClipFrame(&bufs, 0, src, srcW, srcH, dstW, dstH)
	out1 := scaleClipFrame(&bufs, 1, src, srcW, srcH, dstW, dstH)

	if len(out0) == 0 || len(out1) == 0 {
		t.Fatalf("expected non-empty scaled frames, got %d and %d bytes", len(out0), len(out1))
	}
	if &out0[0] == &out1[0] {
		t.Fatal("players 0 and 1 share the same scale buffer backing array — " +
			"concurrent clip players would corrupt each other's frames")
	}

	// Buffers must persist (be reused) per player across frames.
	out0again := scaleClipFrame(&bufs, 0, src, srcW, srcH, dstW, dstH)
	if &out0[0] != &out0again[0] {
		t.Error("player 0 scale buffer was not reused across frames")
	}
}

// TestScaleClipFrame_ConcurrentPlayersRace drives scaleClipFrame from
// clip.MaxPlayers goroutines simultaneously, mimicking concurrent clip
// playback of resolution-mismatched clips. Run under -race: a shared scale
// buffer (the original bug) is flagged as a data race on both the slice
// header and the pixel data.
func TestScaleClipFrame_ConcurrentPlayersRace(t *testing.T) {
	var bufs [clip.MaxPlayers][]byte

	const srcW, srcH = 64, 48
	const dstW, dstH = 128, 96

	var wg sync.WaitGroup
	for idx := 0; idx < clip.MaxPlayers; idx++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Fill the source luma plane with a per-player value so cross-player
			// corruption is observable in the scaled output.
			fill := byte(idx + 1)
			src := make([]byte, switcher.ColorYUV420_8bit.FrameSize(srcW, srcH))
			for i := 0; i < srcW*srcH; i++ {
				src[i] = fill
			}
			for frame := 0; frame < 100; frame++ {
				out := scaleClipFrame(&bufs, idx, src, srcW, srcH, dstW, dstH)
				// Sample a few luma pixels: they must match this player's fill.
				for _, off := range []int{0, dstW * dstH / 2, dstW*dstH - 1} {
					if out[off] != fill {
						t.Errorf("player %d: scaled frame corrupted: luma[%d]=%d, want %d",
							idx, off, out[off], fill)
						return
					}
				}
			}
		}(idx)
	}
	wg.Wait()
}

// TestScaleClipFrame_InvalidPlayerIndex verifies that an unparseable clip key
// (idx out of range) still produces a valid scaled frame without panicking.
func TestScaleClipFrame_InvalidPlayerIndex(t *testing.T) {
	var bufs [clip.MaxPlayers][]byte

	const srcW, srcH = 64, 48
	const dstW, dstH = 128, 96
	src := make([]byte, switcher.ColorYUV420_8bit.FrameSize(srcW, srcH))

	for _, idx := range []int{-1, clip.MaxPlayers} {
		out := scaleClipFrame(&bufs, idx, src, srcW, srcH, dstW, dstH)
		want := switcher.ColorYUV420_8bit.FrameSize(dstW, dstH)
		if len(out) != want {
			t.Errorf("idx %d: scaled frame size = %d, want %d", idx, len(out), want)
		}
	}
}
