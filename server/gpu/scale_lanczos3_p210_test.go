//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaleLanczos3P210_UniformDownscale(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 16, 16
	dstW, dstH := 8, 8

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewP210FramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	// Fill source with uniform Y=400, Cb=300, Cr=700
	srcData := makeP210TestFrame(srcW, srcH, 400, 300, 700)
	src := uploadP210(t, ctx, srcPool, srcData, srcW, srcH)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = ScaleLanczos3P210(ctx, dst, src)
	require.NoError(t, err)

	// Download and verify
	dstBuf := make([]byte, p210DownloadSize(dstW, dstH))
	err = Download10(ctx, dst, dstBuf, dstW, dstH)
	require.NoError(t, err)

	// Verify Y values are approximately 400 (Lanczos may ring slightly at edges)
	for y := 1; y < dstH-1; y++ {
		for x := 1; x < dstW-1; x++ {
			yVal := readP210YSample(dstBuf, y*dstW+x)
			assert.InDelta(t, 400, int(yVal), 15,
				"Y at (%d,%d) should be ~400, got %d", x, y, yVal)
		}
	}

	// Verify Cb values are approximately 300
	chromaDstW := dstW / 2
	for y := 1; y < dstH-1; y++ {
		for x := 1; x < chromaDstW-1; x++ {
			cbVal := readP210CbSample(dstBuf, dstW, dstH, y*chromaDstW+x)
			assert.InDelta(t, 300, int(cbVal), 15,
				"Cb at (%d,%d) should be ~300, got %d", x, y, cbVal)
		}
	}

	// Verify Cr values are approximately 700
	for y := 1; y < dstH-1; y++ {
		for x := 1; x < chromaDstW-1; x++ {
			crVal := readP210CrSample(dstBuf, dstW, dstH, y*chromaDstW+x)
			assert.InDelta(t, 700, int(crVal), 15,
				"Cr at (%d,%d) should be ~700, got %d", x, y, crVal)
		}
	}

	t.Logf("ScaleLanczos3P210 16x16 → 8x8: uniform values preserved")
}

func TestScaleLanczos3P210_OutputDimensions(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 32, 24
	dstW, dstH := 16, 12

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewP210FramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	srcData := makeP210TestFrame(srcW, srcH, 512, 512, 512)
	src := uploadP210(t, ctx, srcPool, srcData, srcW, srcH)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = ScaleLanczos3P210(ctx, dst, src)
	require.NoError(t, err)

	assert.Equal(t, dstW, dst.Width)
	assert.Equal(t, dstH, dst.Height)
}

func TestGPUScaleLanczos3Dispatch_RoutesP210(t *testing.T) {
	ctx := testContext(t)

	w, h := 16, 16

	// Test P210 routing
	p210Pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer p210Pool.Close()

	srcP210 := uploadP210(t, ctx, p210Pool, makeP210TestFrame(w, h, 500, 512, 512), w, h)
	defer srcP210.Release()
	dstP210, err := p210Pool.Acquire()
	require.NoError(t, err)
	defer dstP210.Release()

	err = GPUScaleLanczos3Dispatch(ctx, dstP210, srcP210)
	require.NoError(t, err)
	assert.Equal(t, GPUFormatP210, dstP210.Format, "dispatch should preserve P210 format")
}

func TestGPUScaleLanczos3Dispatch_RoutesNV12(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 32, 32
	dstW, dstH := 16, 16

	srcPool, err := NewFramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	// Upload uniform mid-gray NV12
	srcYUV := make([]byte, yuvFrameSize(srcW, srcH))
	for i := range srcYUV {
		srcYUV[i] = 128
	}
	err = Upload(ctx, src, srcYUV, srcW, srcH)
	require.NoError(t, err)

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	err = GPUScaleLanczos3Dispatch(ctx, dst, src)
	require.NoError(t, err)
	assert.Equal(t, GPUFormatNV12, dst.Format, "dispatch should preserve NV12 format")
}

func TestScaleLanczos3P210_NilArgs(t *testing.T) {
	err := ScaleLanczos3P210(nil, nil, nil)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}
