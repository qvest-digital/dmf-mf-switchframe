package switcher

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sourceFrame builds a synthetic YUV420 frame of size srcW x srcH with all
// Y samples set to yFill, all Cb samples to cbFill, all Cr samples to crFill.
func sourceFrame(srcW, srcH int, yFill, cbFill, crFill byte) []byte {
	ySize := srcW * srcH
	uvSize := (srcW / 2) * (srcH / 2)
	buf := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		buf[i] = yFill
	}
	for i := ySize; i < ySize+uvSize; i++ {
		buf[i] = cbFill
	}
	for i := ySize + uvSize; i < ySize+2*uvSize; i++ {
		buf[i] = crFill
	}
	return buf
}

func TestTileCache_RegisterAllocatesSlot(t *testing.T) {
	tc, err := NewTileCache(240, 136)
	require.NoError(t, err)
	tc.RegisterTile("cam1", "Camera 1")

	require.True(t, tc.HasTile("cam1"))
	require.Equal(t, "Camera 1", tc.Label("cam1"))
}

func TestTileCache_UnregisterRemovesSlot(t *testing.T) {
	tc, err := NewTileCache(240, 136)
	require.NoError(t, err)
	tc.RegisterTile("cam1", "Camera 1")
	require.True(t, tc.HasTile("cam1"))

	tc.UnregisterTile("cam1")
	require.False(t, tc.HasTile("cam1"))
}

func TestTileCache_SetLabelUpdatesLabel(t *testing.T) {
	tc, err := NewTileCache(240, 136)
	require.NoError(t, err)
	tc.RegisterTile("cam1", "Camera 1")
	tc.SetLabel("cam1", "CAM-01")
	require.Equal(t, "CAM-01", tc.Label("cam1"))
}

func TestTileCache_PublishDownsamplesYUV420(t *testing.T) {
	const tileW, tileH = 240, 136
	const srcW, srcH = 1920, 1088
	tc, err := NewTileCache(tileW, tileH)
	require.NoError(t, err)
	tc.RegisterTile("cam1", "Cam 1")

	src := sourceFrame(srcW, srcH, 200, 128, 64)
	tc.PublishYUV420("cam1", src, srcW, srcH)

	// Read back and verify the tile buffer contains downsampled pixels.
	tileSize := tileW*tileH + 2*(tileW/2)*(tileH/2)
	dst := make([]byte, tileSize)
	ok, stale := tc.ReadInto("cam1", dst)
	require.True(t, ok, "ReadInto should succeed for a registered, populated tile")
	require.False(t, stale, "freshly-published tile must not be reported stale")

	// Interior pixel only; corners may carry ±1 rounding from the 16.16
	// fixed-point coordinate mapping at the edge rows. A uniformly-filled
	// input must survive bilinear exactly at any interior sample.
	yCenter := dst[tileH/2*tileW+tileW/2]
	require.Equal(t, byte(200), yCenter, "Y plane center should be ~200 after downsample")

	// Cb plane begins at tileW*tileH; pick an interior Cb sample.
	cbStart := tileW * tileH
	cbCenter := dst[cbStart+(tileH/4)*(tileW/2)+(tileW/4)]
	require.Equal(t, byte(128), cbCenter, "Cb plane center should be ~128 after downsample")

	// Cr plane begins at tileW*tileH + (tileW/2)*(tileH/2).
	crStart := cbStart + (tileW/2)*(tileH/2)
	crCenter := dst[crStart+(tileH/4)*(tileW/2)+(tileW/4)]
	require.Equal(t, byte(64), crCenter, "Cr plane center should be ~64 after downsample")
}

func TestTileCache_ReadOnUnregisteredTileReturnsNotOK(t *testing.T) {
	tc, err := NewTileCache(240, 136)
	require.NoError(t, err)

	tileSize := 240*136 + 2*120*68
	dst := make([]byte, tileSize)
	ok, _ := tc.ReadInto("nope", dst)
	require.False(t, ok, "ReadInto on unregistered tile must return ok=false")
}

func TestTileCache_StaleAfterThreshold(t *testing.T) {
	tc, err := NewTileCache(240, 136)
	require.NoError(t, err)
	tc.SetStaleThreshold(5 * time.Millisecond)
	tc.RegisterTile("cam1", "Cam 1")

	src := sourceFrame(320, 180, 100, 128, 128)
	tc.PublishYUV420("cam1", src, 320, 180)

	time.Sleep(25 * time.Millisecond)

	dst := make([]byte, 240*136+2*120*68)
	ok, stale := tc.ReadInto("cam1", dst)
	require.True(t, ok, "stale tiles still read ok=true")
	require.True(t, stale, "tile not updated within threshold must be reported stale")
}

// TestTileCache_ConcurrentPublishReadNoTear stresses the atomic.Pointer
// swap-on-write under heavy contention. A writer repeatedly publishes
// frames with Y=A (uniform) or Y=B (uniform). A reader loops ReadInto
// and asserts that each tile it reads is either all-A or all-B — a
// mixed buffer would prove the atomic swap failed to provide a
// consistent per-frame snapshot.
//
// Chroma planes mirror the Y pattern so that torn writes across plane
// boundaries also surface as a mismatch.
func TestTileCache_ConcurrentPublishReadNoTear(t *testing.T) {
	const (
		tileW, tileH = 120, 68 // smaller tile keeps test fast
		srcW, srcH   = 640, 360
		iterations   = 20_000
	)
	tc, err := NewTileCache(tileW, tileH)
	require.NoError(t, err)
	tc.RegisterTile("cam1", "Cam 1")

	// Pre-allocate both source frames so the writer does no mid-test alloc.
	srcA := sourceFrame(srcW, srcH, 50, 50, 50)
	srcB := sourceFrame(srcW, srcH, 200, 200, 200)

	var wg sync.WaitGroup
	var stop atomic.Bool
	var torn atomic.Int64

	tileSize := tileW*tileH + 2*(tileW/2)*(tileH/2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations && !stop.Load(); i++ {
			if i%2 == 0 {
				tc.PublishYUV420("cam1", srcA, srcW, srcH)
			} else {
				tc.PublishYUV420("cam1", srcB, srcW, srcH)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		dst := make([]byte, tileSize)
		for i := 0; i < iterations*2; i++ {
			ok, _ := tc.ReadInto("cam1", dst)
			if !ok {
				continue
			}
			// Bilinear downsample of a uniform 50-fill source should not
			// produce any byte outside [49, 51]; likewise 200-fill stays
			// in [199, 201]. A mixed read shows >1 non-adjacent cluster.
			seenA, seenB := false, false
			for _, v := range dst {
				switch {
				case v >= 48 && v <= 52:
					seenA = true
				case v >= 198 && v <= 202:
					seenB = true
				}
				if seenA && seenB {
					torn.Add(1)
					break
				}
			}
			if stop.Load() {
				break
			}
		}
	}()

	wg.Wait()
	stop.Store(true)

	require.Zero(t, torn.Load(), "atomic swap must prevent torn tile reads across iterations")
}

func TestTileCache_NewReturnsErrorOnOddDimensions(t *testing.T) {
	_, err := NewTileCache(240, 135)
	require.ErrorIs(t, err, ErrOddDimensions)

	_, err = NewTileCache(0, 136)
	require.ErrorIs(t, err, ErrOddDimensions)

	_, err = NewTileCache(-1, 136)
	require.ErrorIs(t, err, ErrOddDimensions)
}

func TestTileCache_PublishRejectsShortSource(t *testing.T) {
	tc, err := NewTileCache(240, 136)
	require.NoError(t, err)
	tc.RegisterTile("cam1", "Cam 1")

	// Valid source size for 320x180 YUV420 is 320*180*3/2 = 86400 bytes.
	short := make([]byte, 100)
	// Must not panic.
	tc.PublishYUV420("cam1", short, 320, 180)

	tileSize := 240*136 + 2*120*68
	dst := make([]byte, tileSize)
	ok, _ := tc.ReadInto("cam1", dst)
	require.False(t, ok, "no publish should have succeeded from short source")
}
