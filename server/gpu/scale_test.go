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
		srcYUV[i] = byte((i % 1920) * 219 / 1919 + 16) // horizontal gradient
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

	// Center pixel should be interpolated, not black
	centerIdx := 270*960 + 480
	assert.NotEqual(t, byte(0), dstYUV[centerIdx], "center pixel should not be zero")

	// Verify Y values are in limited range
	for i := 0; i < 100; i++ {
		assert.GreaterOrEqual(t, dstYUV[i], byte(16), "Y should be >= 16")
	}

	t.Logf("Scale 1920x1080 → 960x540: center Y=%d", dstYUV[centerIdx])
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

func TestGPUScaleNilArgs(t *testing.T) {
	err := ScaleBilinear(nil, nil, nil)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}
