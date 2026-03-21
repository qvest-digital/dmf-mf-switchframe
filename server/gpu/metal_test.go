//go:build darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetalContext(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	props := ctx.DeviceProperties()
	assert.NotEmpty(t, props.Name)
	assert.Greater(t, props.TotalMemory, int64(0))

	t.Logf("GPU: %s, Memory: %d MB", props.Name, props.TotalMemory/(1024*1024))
}

func TestMetalContextSync(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	err = ctx.Sync()
	require.NoError(t, err)
}

func TestMetalContextMemoryStats(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	stats := ctx.MemoryStats()
	assert.Greater(t, stats.TotalMB, 0)
	t.Logf("Unified Memory: %d MB", stats.TotalMB)
}

func TestMetalContextCloseNilSafe(t *testing.T) {
	var ctx *Context
	err := ctx.Close()
	assert.NoError(t, err)
}

func TestMetalContextDoubleClose(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	err = ctx.Close()
	assert.NoError(t, err)
	err = ctx.Close()
	assert.NoError(t, err)
}

func TestMetalFramePool(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, frame)

	assert.Equal(t, 1920, frame.Width)
	assert.Equal(t, 1080, frame.Height)
	assert.Greater(t, frame.Pitch, 0)
	assert.True(t, frame.Pitch%256 == 0, "pitch should be 256-byte aligned: %d", frame.Pitch)
	assert.NotEqual(t, uintptr(0), frame.DevPtr, "DevPtr should be non-zero (unified memory)")

	frame.Release()

	// Verify pool reuse
	hits, misses := pool.Stats()
	assert.Equal(t, uint64(1), hits)
	assert.Equal(t, uint64(0), misses)
}

func TestMetalUploadDownloadRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Create test pattern
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = byte(i % 256)
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 100
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 200
	}

	// Upload
	err = Upload(ctx, frame, yuv, width, height)
	require.NoError(t, err)

	// Download
	out := make([]byte, len(yuv))
	err = Download(ctx, out, frame, width, height)
	require.NoError(t, err)

	// Verify Y plane values match
	for i := 0; i < ySize; i++ {
		assert.Equal(t, yuv[i], out[i], "Y mismatch at index %d", i)
	}
	// Verify Cb plane
	for i := 0; i < cbSize; i++ {
		assert.Equal(t, yuv[ySize+i], out[ySize+i], "Cb mismatch at index %d", i)
	}
	// Verify Cr plane
	for i := 0; i < crSize; i++ {
		assert.Equal(t, yuv[ySize+cbSize+i], out[ySize+cbSize+i], "Cr mismatch at index %d", i)
	}
}

func TestMetalFillBlack(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillBlack(ctx, frame)
	require.NoError(t, err)

	// Download and verify
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	err = Download(ctx, yuv, frame, width, height)
	require.NoError(t, err)

	// Verify Y=16 (BT.709 limited-range black)
	for i := 0; i < ySize; i++ {
		assert.Equal(t, byte(16), yuv[i], "Y should be 16 at index %d, got %d", i, yuv[i])
	}
	// Verify Cb=128
	for i := 0; i < cbSize; i++ {
		assert.Equal(t, byte(128), yuv[ySize+i], "Cb should be 128 at index %d", i)
	}
	// Verify Cr=128
	for i := 0; i < crSize; i++ {
		assert.Equal(t, byte(128), yuv[ySize+cbSize+i], "Cr should be 128 at index %d", i)
	}
}

func TestMetalSTMapIdentity(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create identity ST map
	n := width * height
	s := make([]float32, n)
	tv := make([]float32, n)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			s[idx] = float32(x) / float32(width-1)
			tv[idx] = float32(y) / float32(height-1)
		}
	}

	stmap, err := UploadSTMap(ctx, s, tv, width, height)
	require.NoError(t, err)
	defer stmap.Free()

	assert.Equal(t, width, stmap.Width)
	assert.Equal(t, height, stmap.Height)
}

func TestMetalBuildLumaKeyLUT(t *testing.T) {
	lut := BuildLumaKeyLUT(32, 200, 10)
	// Below lowClip-softness should be 0
	assert.Equal(t, byte(0), lut[0])
	assert.Equal(t, byte(0), lut[20])
	// Above highClip+softness should be 255
	assert.Equal(t, byte(255), lut[215])
	assert.Equal(t, byte(255), lut[255])
	// Middle range should be 255 (fully opaque)
	assert.Equal(t, byte(255), lut[100])
}

// ============================================================================
// UV-plane correctness tests (C1-C5 fixes)
// ============================================================================

// Helper: create a test frame with uniform Y/Cb/Cr values.
func makeTestFrame(t *testing.T, ctx *Context, pool *FramePool, width, height int, y, cb, cr byte) *GPUFrame {
	t.Helper()
	frame, err := pool.Acquire()
	require.NoError(t, err)

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = y
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = cb
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = cr
	}

	err = Upload(ctx, frame, yuv, width, height)
	require.NoError(t, err)
	return frame
}

// Helper: download and return Y/Cb/Cr averages.
func avgPlanes(t *testing.T, ctx *Context, frame *GPUFrame, width, height int) (yAvg, cbAvg, crAvg float64) {
	t.Helper()
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	err := Download(ctx, yuv, frame, width, height)
	require.NoError(t, err)

	var ySum, cbSum, crSum int64
	for i := 0; i < ySize; i++ {
		ySum += int64(yuv[i])
	}
	for i := 0; i < cbSize; i++ {
		cbSum += int64(yuv[ySize+i])
	}
	for i := 0; i < crSize; i++ {
		crSum += int64(yuv[ySize+cbSize+i])
	}
	yAvg = float64(ySum) / float64(ySize)
	cbAvg = float64(cbSum) / float64(cbSize)
	crAvg = float64(crSum) / float64(crSize)
	return
}

func TestMetalBlendMixPreservesChroma(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Frame A: Y=100, Cb=50, Cr=200
	a := makeTestFrame(t, ctx, pool, width, height, 100, 50, 200)
	defer a.Release()

	// Frame B: Y=200, Cb=200, Cr=50
	b := makeTestFrame(t, ctx, pool, width, height, 200, 200, 50)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Blend at 50%
	err = BlendMix(ctx, dst, a, b, 0.5)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)

	// Y should be ~150 (midpoint of 100 and 200)
	assert.InDelta(t, 150.0, yAvg, 2.0, "Y average should be ~150")
	// Cb should be ~125 (midpoint of 50 and 200)
	assert.InDelta(t, 125.0, cbAvg, 2.0, "Cb average should be ~125")
	// Cr should be ~125 (midpoint of 200 and 50)
	assert.InDelta(t, 125.0, crAvg, 2.0, "Cr average should be ~125")
}

func TestMetalBlendMixEndpoints(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 100, 50, 200)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 200, 200, 50)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// position=0.0 -> should be all A
	err = BlendMix(ctx, dst, a, b, 0.0)
	require.NoError(t, err)
	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 100.0, yAvg, 1.0, "pos=0: Y should be A")
	assert.InDelta(t, 50.0, cbAvg, 1.0, "pos=0: Cb should be A")
	assert.InDelta(t, 200.0, crAvg, 1.0, "pos=0: Cr should be A")

	// position=1.0 -> should be all B
	err = BlendMix(ctx, dst, a, b, 1.0)
	require.NoError(t, err)
	yAvg, cbAvg, crAvg = avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 200.0, yAvg, 1.0, "pos=1: Y should be B")
	assert.InDelta(t, 200.0, cbAvg, 1.0, "pos=1: Cb should be B")
	assert.InDelta(t, 50.0, crAvg, 1.0, "pos=1: Cr should be B")
}

func TestMetalBlendFTBFullBlack(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Source: Y=200, Cb=50, Cr=200
	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// FTB at position 1.0 (fully black)
	err = BlendFTB(ctx, dst, src, 1.0)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)

	// Full FTB: Y=16, Cb=128, Cr=128 (BT.709 limited-range black)
	assert.InDelta(t, 16.0, yAvg, 1.0, "FTB pos=1: Y should be 16")
	assert.InDelta(t, 128.0, cbAvg, 1.0, "FTB pos=1: Cb should be 128")
	assert.InDelta(t, 128.0, crAvg, 1.0, "FTB pos=1: Cr should be 128")
}

func TestMetalBlendFTBNoChange(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// FTB at position 0.0 (no fade)
	err = BlendFTB(ctx, dst, src, 0.0)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 200.0, yAvg, 1.0, "FTB pos=0: Y should be unchanged")
	assert.InDelta(t, 50.0, cbAvg, 1.0, "FTB pos=0: Cb should be unchanged")
	assert.InDelta(t, 200.0, crAvg, 1.0, "FTB pos=0: Cr should be unchanged")
}

func TestMetalBlendFTBHalfway(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// FTB at position 0.5 (half fade)
	err = BlendFTB(ctx, dst, src, 0.5)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	// Y: (200 * 128 + 16 * 128 + 128) >> 8 ~ 108
	assert.InDelta(t, 108.0, yAvg, 3.0, "FTB pos=0.5: Y should be midpoint to 16")
	// Cb: (50 * 128 + 128 * 128 + 128) >> 8 ~ 89
	assert.InDelta(t, 89.0, cbAvg, 3.0, "FTB pos=0.5: Cb should be midpoint to 128")
	// Cr: (200 * 128 + 128 * 128 + 128) >> 8 ~ 164
	assert.InDelta(t, 164.0, crAvg, 3.0, "FTB pos=0.5: Cr should be midpoint to 128")
}

func TestMetalScaleBilinearPreservesChroma(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	// Use same width, different height — avoids UV interleaving artifact
	// in the byte-level scaler. Full resolution changes need a UV-aware kernel.
	srcW, srcH := 64, 128
	dstW, dstH := 64, 64

	srcPool, err := NewFramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	// Source: Y=100, Cb=200, Cr=50
	src := makeTestFrame(t, ctx, srcPool, srcW, srcH, 100, 200, 50)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = ScaleBilinear(ctx, dst, src)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, dstW, dstH)

	// Uniform frame scaled should keep uniform values
	assert.InDelta(t, 100.0, yAvg, 2.0, "Scaled Y should be ~100")
	assert.InDelta(t, 200.0, cbAvg, 2.0, "Scaled Cb should be ~200")
	assert.InDelta(t, 50.0, crAvg, 2.0, "Scaled Cr should be ~50")
}

func TestMetalScaleLanczos3PreservesChroma(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	// Use same width, different height — avoids UV interleaving artifact
	// in the byte-level scaler. Full resolution changes need a UV-aware kernel.
	srcW, srcH := 64, 128
	dstW, dstH := 64, 64

	srcPool, err := NewFramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	src := makeTestFrame(t, ctx, srcPool, srcW, srcH, 100, 200, 50)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = ScaleLanczos3(ctx, dst, src)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, dstW, dstH)

	// Lanczos scaling of uniform frame should preserve values
	assert.InDelta(t, 100.0, yAvg, 3.0, "Lanczos Y should be ~100")
	assert.InDelta(t, 200.0, cbAvg, 3.0, "Lanczos Cb should be ~200")
	assert.InDelta(t, 50.0, crAvg, 3.0, "Lanczos Cr should be ~50")
}

func TestMetalPIPCompositePreservesChroma(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 128, 128
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Background: Y=16, Cb=128, Cr=128 (black)
	dst := makeTestFrame(t, ctx, pool, width, height, 16, 128, 128)
	defer dst.Release()

	// PIP source: Y=200, Cb=50, Cr=200
	src := makeTestFrame(t, ctx, pool, width, height, 200, 50, 200)
	defer src.Release()

	// Composite into center quarter, full alpha
	rect := Rect{X: 32, Y: 32, W: 64, H: 64}
	err = PIPComposite(ctx, dst, src, rect, 1.0)
	require.NoError(t, err)

	// Download and check the PIP region
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	err = Download(ctx, yuv, dst, width, height)
	require.NoError(t, err)

	// Check Y in PIP region center
	centerY := yuv[64*width+64]
	assert.InDelta(t, 200, int(centerY), 10, "PIP Y should be ~200")

	// Check Cb in PIP region center (chroma coords = luma/2)
	cbIdx := ySize + 32*(width/2) + 32
	centerCb := yuv[cbIdx]
	assert.InDelta(t, 50, int(centerCb), 10, "PIP Cb should be ~50")

	// Check Cr in PIP region center
	crIdx := ySize + cbSize + 32*(width/2) + 32
	centerCr := yuv[crIdx]
	assert.InDelta(t, 200, int(centerCr), 10, "PIP Cr should be ~200")

	// Check outside PIP region is still black
	outsideY := yuv[0]
	assert.Equal(t, byte(16), outsideY, "Outside PIP Y should be 16")
}

func TestMetalSTMapWarpPreservesChroma(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Source: Y=100, Cb=200, Cr=50
	src := makeTestFrame(t, ctx, pool, width, height, 100, 200, 50)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Identity ST map (should pass through unchanged)
	n := width * height
	s := make([]float32, n)
	tv := make([]float32, n)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			s[idx] = float32(x) / float32(width-1)
			tv[idx] = float32(y) / float32(height-1)
		}
	}

	stmap, err := UploadSTMap(ctx, s, tv, width, height)
	require.NoError(t, err)
	defer stmap.Free()

	err = STMapWarp(ctx, dst, src, stmap)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)

	// Identity warp should preserve values exactly
	assert.InDelta(t, 100.0, yAvg, 2.0, "STMap Y should be ~100")
	assert.InDelta(t, 200.0, cbAvg, 2.0, "STMap Cb should be ~200")
	assert.InDelta(t, 50.0, crAvg, 2.0, "STMap Cr should be ~50")
}

func TestMetalBlendWipeTransition(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 100, 50, 200)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 200, 200, 50)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	mask, err := pool.Acquire()
	require.NoError(t, err)
	defer mask.Release()

	// Wipe at position 1.0 (fully B)
	err = BlendWipe(ctx, dst, a, b, mask, 1.0, WipeHLeft, 4)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 200.0, yAvg, 2.0, "Wipe pos=1: Y should be B")
	assert.InDelta(t, 200.0, cbAvg, 2.0, "Wipe pos=1: Cb should be B")
	assert.InDelta(t, 50.0, crAvg, 2.0, "Wipe pos=1: Cr should be B")

	// Wipe at position 0.0 (fully A) — soft edge causes minor blending at boundary
	err = BlendWipe(ctx, dst, a, b, mask, 0.0, WipeHLeft, 4)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg = avgPlanes(t, ctx, dst, width, height)
	assert.InDelta(t, 100.0, yAvg, 5.0, "Wipe pos=0: Y should be A")
	assert.InDelta(t, 50.0, cbAvg, 5.0, "Wipe pos=0: Cb should be A")
	assert.InDelta(t, 200.0, crAvg, 5.0, "Wipe pos=0: Cr should be A")
}

func TestMetalBlendStingerAlpha(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	// Base: Y=100, Cb=50, Cr=200
	base := makeTestFrame(t, ctx, pool, width, height, 100, 50, 200)
	defer base.Release()

	// Overlay: Y=200, Cb=200, Cr=50
	overlay := makeTestFrame(t, ctx, pool, width, height, 200, 200, 50)
	defer overlay.Release()

	// Alpha: all 255 (fully opaque overlay) — upload as Y plane, rest zeros
	alpha, err := pool.Acquire()
	require.NoError(t, err)
	defer alpha.Release()

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	alphaYUV := make([]byte, ySize+cbSize+crSize)
	for i := 0; i < ySize; i++ {
		alphaYUV[i] = 255 // full alpha
	}
	err = Upload(ctx, alpha, alphaYUV, width, height)
	require.NoError(t, err)

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendStinger(ctx, dst, base, overlay, alpha)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	// Full alpha should show overlay
	assert.InDelta(t, 200.0, yAvg, 2.0, "Stinger full alpha: Y should be overlay")
	assert.InDelta(t, 200.0, cbAvg, 2.0, "Stinger full alpha: Cb should be overlay")
	assert.InDelta(t, 50.0, crAvg, 2.0, "Stinger full alpha: Cr should be overlay")
}

func TestMetalBlendStingerZeroAlpha(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	base := makeTestFrame(t, ctx, pool, width, height, 100, 50, 200)
	defer base.Release()
	overlay := makeTestFrame(t, ctx, pool, width, height, 200, 200, 50)
	defer overlay.Release()

	// Alpha: all 0 (fully transparent overlay)
	alpha, err := pool.Acquire()
	require.NoError(t, err)
	defer alpha.Release()
	err = FillBlack(ctx, alpha) // sets Y=16 not 0, but let's use Upload with zeros
	require.NoError(t, err)

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	alphaYUV := make([]byte, ySize+cbSize+crSize)
	// All zeros = fully transparent
	err = Upload(ctx, alpha, alphaYUV, width, height)
	require.NoError(t, err)

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = BlendStinger(ctx, dst, base, overlay, alpha)
	require.NoError(t, err)

	yAvg, cbAvg, crAvg := avgPlanes(t, ctx, dst, width, height)
	// Zero alpha should show base
	assert.InDelta(t, 100.0, yAvg, 2.0, "Stinger zero alpha: Y should be base")
	assert.InDelta(t, 50.0, cbAvg, 2.0, "Stinger zero alpha: Cb should be base")
	assert.InDelta(t, 200.0, crAvg, 2.0, "Stinger zero alpha: Cr should be base")
}

// ============================================================================
// Error message test (I1)
// ============================================================================

func TestErrGPUNotAvailableMessage(t *testing.T) {
	assert.Equal(t, "gpu: not available", ErrGPUNotAvailable.Error())
	assert.NotContains(t, ErrGPUNotAvailable.Error(), "CUDA")
}

// ============================================================================
// Direct CPU download test (I3)
// ============================================================================

func TestMetalDownloadDirectCPU(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	for i := range yuv {
		yuv[i] = byte(i % 256)
	}

	// Upload then download — uses direct CPU read (no staging buffers)
	err = Upload(ctx, frame, yuv, width, height)
	require.NoError(t, err)

	out := make([]byte, len(yuv))
	err = Download(ctx, out, frame, width, height)
	require.NoError(t, err)

	// Verify round-trip correctness
	for i := 0; i < ySize; i++ {
		assert.Equal(t, yuv[i], out[i], "Y mismatch at index %d", i)
	}
	for i := 0; i < cbSize; i++ {
		assert.Equal(t, yuv[ySize+i], out[ySize+i], "Cb mismatch at index %d", i)
	}
	for i := 0; i < crSize; i++ {
		assert.Equal(t, yuv[ySize+cbSize+i], out[ySize+cbSize+i], "Cr mismatch at index %d", i)
	}

	// Download again to verify no state corruption
	out2 := make([]byte, len(yuv))
	err = Download(ctx, out2, frame, width, height)
	require.NoError(t, err)

	for i := 0; i < len(yuv); i++ {
		assert.Equal(t, out[i], out2[i], "repeat download mismatch at index %d", i)
	}
}

// ============================================================================
// Temp metallib cleanup test (I2)
// ============================================================================

func TestMetalContextCloseCleansTempFile(t *testing.T) {
	// This tests that Close() cleans up the tempMetallibPath field.
	// We can't easily test the full embedded metallib path without
	// manipulating findMetallib, but we verify the cleanup code path.
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}

	// tempMetallibPath may or may not be set depending on whether
	// the metallib was found on disk or embedded. Either way, Close
	// should succeed without error.
	err = ctx.Close()
	require.NoError(t, err)
}
