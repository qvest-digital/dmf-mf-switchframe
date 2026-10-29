package main

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/switcher"
)

// TestScaleClipFrame_IndependentBuffersPerCall verifies that every call
// returns an independent buffer. The frames built from these buffers are
// retained by the frame-sync ring (and as the freeze-repeat lastRawVideo)
// across ticks, so a reused persistent scratch buffer — the original bug —
// would let the decode goroutine mutate pixels under the sync loop, tearing
// frames whenever the clip resolution differs from the pipeline format.
func TestScaleClipFrame_IndependentBuffersPerCall(t *testing.T) {
	const srcW, srcH = 64, 48
	const dstW, dstH = 128, 96
	pool := switcher.NewFramePool(8, dstW, dstH, switcher.ColorYUV420_8bit)
	want := switcher.ColorYUV420_8bit.FrameSize(dstW, dstH)

	src := make([]byte, switcher.ColorYUV420_8bit.FrameSize(srcW, srcH))
	for i := 0; i < srcW*srcH; i++ {
		src[i] = 40
	}

	out0, pooled0 := scaleClipFrame(pool, src, srcW, srcH, dstW, dstH)
	out1, pooled1 := scaleClipFrame(pool, src, srcW, srcH, dstW, dstH)

	if len(out0) != want || len(out1) != want {
		t.Fatalf("scaled frame sizes = %d, %d; want %d", len(out0), len(out1), want)
	}
	if !pooled0 || !pooled1 {
		t.Errorf("expected pool-backed buffers when frame fits pool (got %v, %v)", pooled0, pooled1)
	}
	if &out0[0] == &out1[0] {
		t.Fatal("consecutive calls share the same backing array — the next decoded " +
			"frame would overwrite pixels retained by the frame-sync ring (torn frames)")
	}

	// Mutating the second frame must not affect the first (retained) frame.
	prev := out0[0]
	for i := range out1 {
		out1[i] = 0xEE
	}
	if out0[0] != prev {
		t.Error("scaling a new frame corrupted the previously returned frame")
	}
}

// TestScaleClipFrame_HeapFallback verifies that when the pool cannot hold the
// target frame (or no pool is available), a correctly sized heap buffer is
// returned and reported as not pool-owned, so the caller skips SetPool and
// ReleaseYUV never pushes a foreign buffer into the pool.
func TestScaleClipFrame_HeapFallback(t *testing.T) {
	const srcW, srcH = 64, 48
	const dstW, dstH = 128, 96
	want := switcher.ColorYUV420_8bit.FrameSize(dstW, dstH)
	src := make([]byte, switcher.ColorYUV420_8bit.FrameSize(srcW, srcH))

	// nil pool.
	out, pooled := scaleClipFrame(nil, src, srcW, srcH, dstW, dstH)
	if pooled {
		t.Error("nil pool: buffer reported as pool-owned")
	}
	if len(out) != want {
		t.Errorf("nil pool: scaled frame size = %d, want %d", len(out), want)
	}

	// Pool too small for the target resolution.
	smallPool := switcher.NewFramePool(2, 32, 24, switcher.ColorYUV420_8bit)
	out, pooled = scaleClipFrame(smallPool, src, srcW, srcH, dstW, dstH)
	if pooled {
		t.Error("undersized pool: buffer reported as pool-owned")
	}
	if len(out) != want {
		t.Errorf("undersized pool: scaled frame size = %d, want %d", len(out), want)
	}
}

// TestScaleClipFrame_ConcurrentPlayersRace drives scaleClipFrame from many
// goroutines simultaneously, mimicking concurrent clip playback of
// resolution-mismatched clips (each clip player invokes the raw-video
// callback from its own decode goroutine). Run under -race: any shared
// scratch buffer is flagged on both the slice header and the pixel data.
func TestScaleClipFrame_ConcurrentPlayersRace(t *testing.T) {
	const srcW, srcH = 64, 48
	const dstW, dstH = 128, 96
	const players = 4
	pool := switcher.NewFramePool(players, dstW, dstH, switcher.ColorYUV420_8bit)

	var wg sync.WaitGroup
	for idx := 0; idx < players; idx++ {
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
				out, _ := scaleClipFrame(pool, src, srcW, srcH, dstW, dstH)
				// Sample a few luma pixels: they must match this player's fill.
				for _, off := range []int{0, dstW * dstH / 2, dstW*dstH - 1} {
					if out[off] != fill {
						t.Errorf("player %d: scaled frame corrupted: luma[%d]=%d, want %d",
							idx, off, out[off], fill)
						return
					}
				}
				pool.Release(out)
			}
		}(idx)
	}
	wg.Wait()
}

// TestAcquireClipFrameBuf verifies the exact-size slicing and pool/heap
// routing of the per-frame buffer acquisition helper.
func TestAcquireClipFrameBuf(t *testing.T) {
	pool := switcher.NewFramePool(2, 128, 96, switcher.ColorYUV420_8bit)

	buf, pooled := acquireClipFrameBuf(pool, 100)
	if !pooled {
		t.Error("expected pool buffer for size within pool BufSize")
	}
	if len(buf) != 100 {
		t.Errorf("buffer length = %d, want 100", len(buf))
	}

	big := pool.BufSize() + 1
	buf, pooled = acquireClipFrameBuf(pool, big)
	if pooled {
		t.Error("expected heap fallback for oversize request")
	}
	if len(buf) != big {
		t.Errorf("buffer length = %d, want %d", len(buf), big)
	}
}

// Each engine of a pair exports the replay a request started and adds it to
// its own store. Under the request's clip ID, a delete or pin sent to both
// engines by that ID finds the clip on each.
func TestAddReplayClip_UsesRequestClipID(t *testing.T) {
	const id = "00112233445566778899aabbccddeeff"
	store, err := clip.NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := addReplayClip(store, &clip.Clip{Name: "cam1 replay", Ephemeral: true}, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(id); err != nil {
		t.Fatalf("replay clip not stored under the request's ID: %v", err)
	}

	fresh := &clip.Clip{Name: "cam1 replay", Ephemeral: true}
	if err := addReplayClip(store, fresh, ""); err != nil {
		t.Fatal(err)
	}
	if !clip.ValidID(fresh.ID) || fresh.ID == id {
		t.Fatalf("replay without a requested ID got %q", fresh.ID)
	}
}

// A playlist names a clip by the path /api/playout/clips reports, and the
// UI takes that path from one engine and sends the playlist to both. Both
// engines store a replay under one clip ID; its file has to carry that ID,
// not a local time stamp, or the other engine cannot open it.
func TestReplayClipFilename_FollowsClipID(t *testing.T) {
	const id = "00112233445566778899aabbccddeeff"
	a := replayClipFilename("mxl:mxl-1", id, time.Date(2026, 1, 2, 3, 4, 5, 299e6, time.UTC))
	b := replayClipFilename("mxl:mxl-1", id, time.Date(2026, 1, 2, 3, 4, 5, 435e6, time.UTC))
	if a != id+".ts" || b != a {
		t.Fatalf("replay files %q and %q, want both %q", a, b, id+".ts")
	}
	if got := replayClipFilename("cam1", "", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); got != "replay_cam1_20260102_030405.000.ts" {
		t.Fatalf("replay without a requested ID named %q", got)
	}
}
