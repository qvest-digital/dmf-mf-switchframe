//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlendMix50(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	a, _ := pool.Acquire()
	b, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer a.Release()
	defer b.Release()
	defer dst.Release()

	// Frame A: Y=64 (dark), Frame B: Y=192 (bright)
	yuvA := make([]byte, w*h*3/2)
	yuvB := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuvA[i] = 64
		yuvB[i] = 192
	}
	for i := w * h; i < len(yuvA); i++ {
		yuvA[i] = 128
		yuvB[i] = 128
	}

	require.NoError(t, Upload(ctx, a, yuvA, w, h))
	require.NoError(t, Upload(ctx, b, yuvB, w, h))

	// 50% mix
	err = BlendMix(ctx, dst, a, b, 0.5)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// At 50%, Y should be ~128 (midpoint of 64 and 192)
	assert.InDelta(t, 128, int(result[0]), 2, "50%% mix of 64+192 should be ~128")
	assert.InDelta(t, 128, int(result[w*h/2+w/2]), 2, "center pixel")
}

func TestBlendMixEndpoints(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 64, 64
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	a, _ := pool.Acquire()
	b, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer a.Release()
	defer b.Release()
	defer dst.Release()

	yuvA := make([]byte, w*h*3/2)
	yuvB := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuvA[i] = 100
		yuvB[i] = 200
	}
	for i := w * h; i < len(yuvA); i++ {
		yuvA[i] = 128
		yuvB[i] = 128
	}
	require.NoError(t, Upload(ctx, a, yuvA, w, h))
	require.NoError(t, Upload(ctx, b, yuvB, w, h))

	// 0% = all A
	require.NoError(t, BlendMix(ctx, dst, a, b, 0.0))
	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, dst, w, h))
	assert.Equal(t, byte(100), result[0], "0%% mix should be all A")

	// 100% = all B
	require.NoError(t, BlendMix(ctx, dst, a, b, 1.0))
	require.NoError(t, Download(ctx, result, dst, w, h))
	assert.Equal(t, byte(200), result[0], "100%% mix should be all B")
}

func TestBlendFTB(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 200 // bright
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, w, h))

	// Fade to black at 100%
	require.NoError(t, BlendFTB(ctx, dst, src, 1.0))

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// Should be BT.709 black (Y=16)
	assert.Equal(t, byte(16), result[0], "FTB 100%% should be Y=16 (BT.709 black)")

	// Fade at 0% should preserve source
	require.NoError(t, BlendFTB(ctx, dst, src, 0.0))
	require.NoError(t, Download(ctx, result, dst, w, h))
	assert.Equal(t, byte(200), result[0], "FTB 0%% should preserve source")
}

func TestBlendWipe(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	a, _ := pool.Acquire()
	b, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	mask, _ := pool.Acquire()
	defer a.Release()
	defer b.Release()
	defer dst.Release()
	defer mask.Release()

	yuvA := make([]byte, w*h*3/2)
	yuvB := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuvA[i] = 50
		yuvB[i] = 200
	}
	for i := w * h; i < len(yuvA); i++ {
		yuvA[i] = 128
		yuvB[i] = 128
	}
	require.NoError(t, Upload(ctx, a, yuvA, w, h))
	require.NoError(t, Upload(ctx, b, yuvB, w, h))

	// H-left wipe at 50%
	err = BlendWipe(ctx, dst, a, b, mask, 0.5, WipeHLeft, 4)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// Left side should be B (200), right side should be A (50)
	leftY := result[120*w+80]   // ~25% from left
	rightY := result[120*w+240] // ~75% from left
	assert.Greater(t, leftY, rightY, "left should be brighter (B) than right (A) at 50%% h-left wipe")
}

func TestBlendNilArgs(t *testing.T) {
	require.ErrorIs(t, BlendMix(nil, nil, nil, nil, 0.5), ErrGPUNotAvailable)
	require.ErrorIs(t, BlendFTB(nil, nil, nil, 0.5), ErrGPUNotAvailable)
}
