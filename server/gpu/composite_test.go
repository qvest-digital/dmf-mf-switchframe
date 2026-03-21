//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPIPComposite(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	dstW, dstH := 640, 480
	srcW, srcH := 320, 240

	dstPool, err := NewFramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	srcPool, err := NewFramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	dst, _ := dstPool.Acquire()
	src, _ := srcPool.Acquire()
	defer dst.Release()
	defer src.Release()

	// Fill dst with black
	require.NoError(t, FillBlack(ctx, dst))

	// Fill src with bright gray (Y=200)
	srcYUV := make([]byte, srcW*srcH*3/2)
	for i := 0; i < srcW*srcH; i++ {
		srcYUV[i] = 200
	}
	for i := srcW * srcH; i < len(srcYUV); i++ {
		srcYUV[i] = 128
	}
	require.NoError(t, Upload(ctx, src, srcYUV, srcW, srcH))

	// Composite PIP in center-right quadrant
	rect := Rect{X: 320, Y: 120, W: 300, H: 240}
	err = PIPComposite(ctx, dst, src, rect, 1.0)
	require.NoError(t, err)

	// Download and verify
	result := make([]byte, dstW*dstH*3/2)
	require.NoError(t, Download(ctx, result, dst, dstW, dstH))

	// Pixel inside PIP rect should be bright (~200)
	insideY := result[200*dstW+400] // row 200, col 400 (inside rect)
	assert.Greater(t, insideY, byte(100), "inside PIP should be bright, got Y=%d", insideY)

	// Pixel outside PIP rect should be black (16)
	outsideY := result[50*dstW+50] // row 50, col 50 (outside rect)
	assert.Equal(t, byte(16), outsideY, "outside PIP should be black")

	t.Logf("PIP composite: inside Y=%d, outside Y=%d", insideY, outsideY)
}

func TestPIPCompositeWithAlpha(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	dst, _ := pool.Acquire()
	src, _ := pool.Acquire()
	defer dst.Release()
	defer src.Release()

	// Dst: Y=100, Src: Y=200
	dstYUV := make([]byte, w*h*3/2)
	srcYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		dstYUV[i] = 100
		srcYUV[i] = 200
	}
	for i := w * h; i < len(dstYUV); i++ {
		dstYUV[i] = 128
		srcYUV[i] = 128
	}
	require.NoError(t, Upload(ctx, dst, dstYUV, w, h))
	require.NoError(t, Upload(ctx, src, srcYUV, w, h))

	// 50% alpha composite over full frame
	rect := Rect{X: 0, Y: 0, W: w, H: h}
	err = PIPComposite(ctx, dst, src, rect, 0.5)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, dst, w, h))

	// Should be ~150 (midpoint of 100 and 200)
	assert.InDelta(t, 150, int(result[h/2*w+w/2]), 3, "50%% alpha PIP should blend to ~150")
}

func TestFillRect(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, _ := pool.Acquire()
	defer frame.Release()

	// Fill full frame with white (Y=235)
	whiteYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		whiteYUV[i] = 235
	}
	for i := w * h; i < len(whiteYUV); i++ {
		whiteYUV[i] = 128
	}
	require.NoError(t, Upload(ctx, frame, whiteYUV, w, h))

	// Fill a black rectangle in the center
	rect := Rect{X: 80, Y: 60, W: 160, H: 120}
	err = FillRect(ctx, frame, rect, ColorBlack)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, frame, w, h))

	// Inside rect should be black
	insideY := result[120*w+160] // center of rect
	assert.Equal(t, byte(16), insideY, "inside rect should be black")

	// Outside rect should be white
	outsideY := result[10*w+10]
	assert.Equal(t, byte(235), outsideY, "outside rect should be white")
}

func TestCompositeNilArgs(t *testing.T) {
	require.ErrorIs(t, PIPComposite(nil, nil, nil, Rect{}, 1.0), ErrGPUNotAvailable)
	require.ErrorIs(t, DrawBorder(nil, nil, Rect{}, YUVColor{}, 2), ErrGPUNotAvailable)
	require.ErrorIs(t, FillRect(nil, nil, Rect{}, YUVColor{}), ErrGPUNotAvailable)
}
