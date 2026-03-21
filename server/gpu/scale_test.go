//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUScaleBilinear(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	srcPool, err := NewFramePool(ctx, 1920, 1080, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewFramePool(ctx, 960, 540, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload gradient pattern to source
	srcYUV := make([]byte, 1920*1080*3/2)
	for i := 0; i < 1920*1080; i++ {
		srcYUV[i] = byte((i%1920)*219/1919 + 16) // horizontal gradient
	}
	for i := 1920 * 1080; i < len(srcYUV); i++ {
		srcYUV[i] = 128
	}
	err = Upload(ctx, src, srcYUV, 1920, 1080)
	require.NoError(t, err)

	// Scale 1920x1080 → 960x540
	err = ScaleBilinear(ctx, dst, src)
	require.NoError(t, err)

	// Download and verify
	dstYUV := make([]byte, 960*540*3/2)
	err = Download(ctx, dstYUV, dst, 960, 540)
	require.NoError(t, err)

	// Verify scaling produced non-trivial output by checking that the Y plane
	// contains a range of values (source was a horizontal gradient 16-235)
	dstW, dstH := 960, 540
	minY, maxY := byte(255), byte(0)
	for y := 10; y < dstH-10; y++ {
		for x := 10; x < dstW-10; x++ {
			v := dstYUV[y*dstW+x]
			if v < minY {
				minY = v
			}
			if v > maxY {
				maxY = v
			}
		}
	}
	yRange := int(maxY) - int(minY)
	assert.Greater(t, yRange, 50, "scaled gradient should have Y range > 50 (got %d-%d)", minY, maxY)
	t.Logf("Scale 1920x1080 → 960x540: Y range %d-%d (range=%d)", minY, maxY, yRange)
}

func TestGPUScaleUpscale(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	srcPool, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewFramePool(ctx, 640, 480, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Fill source with known value
	srcYUV := make([]byte, 320*240*3/2)
	for i := 0; i < 320*240; i++ {
		srcYUV[i] = 128 // mid-gray Y
	}
	for i := 320 * 240; i < len(srcYUV); i++ {
		srcYUV[i] = 128
	}
	err = Upload(ctx, src, srcYUV, 320, 240)
	require.NoError(t, err)

	err = ScaleBilinear(ctx, dst, src)
	require.NoError(t, err)

	dstYUV := make([]byte, 640*480*3/2)
	err = Download(ctx, dstYUV, dst, 640, 480)
	require.NoError(t, err)

	// Uniform source → uniform output (128 everywhere)
	for i := 0; i < 100; i++ {
		assert.Equal(t, byte(128), dstYUV[i], "upscaled uniform should remain 128")
	}
}

func TestGPUScaleBilinearUVCorrectness(t *testing.T) {
	// Verify that UV plane scaling preserves CbCr channel independence.
	// Before the fix, the UV plane was scaled byte-by-byte, which mixed
	// Cb and Cr values and caused color corruption.
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	srcW, srcH := 640, 480
	dstW, dstH := 320, 240

	srcPool, err := NewFramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Create source with known UV pattern: Cb=50, Cr=200 everywhere.
	// If scaling mixes channels, we'll see values in between.
	srcYUV := make([]byte, srcW*srcH*3/2)
	for i := 0; i < srcW*srcH; i++ {
		srcYUV[i] = 128 // Y = mid-gray
	}
	// U (Cb) plane
	uvStart := srcW * srcH
	for i := 0; i < srcW/2*srcH/2; i++ {
		srcYUV[uvStart+i] = 50 // Cb
	}
	// V (Cr) plane
	crStart := uvStart + srcW/2*srcH/2
	for i := 0; i < srcW/2*srcH/2; i++ {
		srcYUV[crStart+i] = 200 // Cr
	}

	err = Upload(ctx, src, srcYUV, srcW, srcH)
	require.NoError(t, err)

	err = ScaleBilinear(ctx, dst, src)
	require.NoError(t, err)

	dstYUV := make([]byte, dstW*dstH*3/2)
	err = Download(ctx, dstYUV, dst, dstW, dstH)
	require.NoError(t, err)

	// Check Cb and Cr values in the output. They should remain separated:
	// Cb ≈ 50, Cr ≈ 200. If mixing occurred, both would be ~125.
	dstUVStart := dstW * dstH
	dstCrStart := dstUVStart + dstW/2*dstH/2

	// Sample Cb values (should be ~50)
	cbVal := dstYUV[dstUVStart+dstW/4*dstH/4/2+dstW/8]
	assert.InDelta(t, 50, int(cbVal), 10, "Cb should be ~50 after scaling, got %d", cbVal)

	// Sample Cr values (should be ~200)
	crVal := dstYUV[dstCrStart+dstW/4*dstH/4/2+dstW/8]
	assert.InDelta(t, 200, int(crVal), 10, "Cr should be ~200 after scaling, got %d", crVal)

	t.Logf("UV-aware scale: Cb=%d (want ~50), Cr=%d (want ~200)", cbVal, crVal)
}

func TestGPUScaleNilArgs(t *testing.T) {
	err := ScaleBilinear(nil, nil, nil)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}
