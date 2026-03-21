//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFRUCAvailable(t *testing.T) {
	avail := FRUCAvailable()
	assert.True(t, avail, "FRUC should always be available on CUDA builds (blend fallback)")
	t.Logf("NVOFA hardware optical flow: %v", nvofaAvailable())
}

func TestFRUCCreate(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	fruc, err := NewFRUC(ctx, 1920, 1080)
	require.NoError(t, err)
	defer fruc.Close()

	assert.Equal(t, 1920, fruc.width)
	assert.Equal(t, 1080, fruc.height)
	assert.Equal(t, 480, fruc.flowW) // 1920/4
	assert.Equal(t, 270, fruc.flowH) // 1080/4
}

func TestFRUCInterpolate(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	fruc, err := NewFRUC(ctx, w, h)
	require.NoError(t, err)
	defer fruc.Close()

	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	prev, _ := pool.Acquire()
	curr, _ := pool.Acquire()
	output, _ := pool.Acquire()
	defer prev.Release()
	defer curr.Release()
	defer output.Release()

	// prev=dark (Y=64), curr=bright (Y=192)
	yuvPrev := make([]byte, w*h*3/2)
	yuvCurr := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuvPrev[i] = 64
		yuvCurr[i] = 192
	}
	for i := w * h; i < len(yuvPrev); i++ {
		yuvPrev[i] = 128
		yuvCurr[i] = 128
	}
	require.NoError(t, Upload(ctx, prev, yuvPrev, w, h))
	require.NoError(t, Upload(ctx, curr, yuvCurr, w, h))

	// Fill output with black to verify it gets written
	require.NoError(t, FillBlack(ctx, output))

	// Interpolate at 50%
	err = fruc.Interpolate(prev, curr, output, 0.5)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, output, w, h))

	// At 50%, should be ~128 (midpoint of 64 and 192)
	centerY := result[h/2*w+w/2]
	assert.InDelta(t, 128, int(centerY), 3, "50%% interpolation should be ~128")
	t.Logf("FRUC interpolate 50%%: center Y=%d (prev=64, curr=192)", centerY)
}

func TestFRUCInterpolateEndpoints(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	fruc, err := NewFRUC(ctx, w, h)
	require.NoError(t, err)
	defer fruc.Close()

	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	prev, _ := pool.Acquire()
	curr, _ := pool.Acquire()
	output, _ := pool.Acquire()
	defer prev.Release()
	defer curr.Release()
	defer output.Release()

	yuvPrev := make([]byte, w*h*3/2)
	yuvCurr := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuvPrev[i] = 80
		yuvCurr[i] = 200
	}
	for i := w * h; i < len(yuvPrev); i++ {
		yuvPrev[i] = 128
		yuvCurr[i] = 128
	}
	require.NoError(t, Upload(ctx, prev, yuvPrev, w, h))
	require.NoError(t, Upload(ctx, curr, yuvCurr, w, h))

	result := make([]byte, w*h*3/2)

	// alpha=0.0 should produce prev
	require.NoError(t, fruc.Interpolate(prev, curr, output, 0.0))
	require.NoError(t, Download(ctx, result, output, w, h))
	assert.InDelta(t, 80, int(result[h/2*w+w/2]), 2, "alpha=0 should be prev")

	// alpha=1.0 should produce curr
	require.NoError(t, fruc.Interpolate(prev, curr, output, 1.0))
	require.NoError(t, Download(ctx, result, output, w, h))
	assert.InDelta(t, 200, int(result[h/2*w+w/2]), 2, "alpha=1 should be curr")
}

func TestFRUCNilContext(t *testing.T) {
	_, err := NewFRUC(nil, 1920, 1080)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestFRUCDoubleClose(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	fruc, err := NewFRUC(ctx, 320, 240)
	require.NoError(t, err)

	fruc.Close()
	fruc.Close() // should not panic
}
