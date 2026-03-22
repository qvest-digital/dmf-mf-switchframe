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
	ctx := testContext(t)

	fruc, err := NewFRUC(ctx, 1920, 1080)
	require.NoError(t, err)
	defer fruc.Close()

	assert.Equal(t, 1920, fruc.width)
	assert.Equal(t, 1080, fruc.height)
	assert.Equal(t, 480, fruc.flowW) // 1920/4
	assert.Equal(t, 270, fruc.flowH) // 1080/4
}

func TestFRUCInterpolate(t *testing.T) {
	ctx := testContext(t)

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
	ctx := testContext(t)

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
	ctx := testContext(t)

	fruc, err := NewFRUC(ctx, 320, 240)
	require.NoError(t, err)

	fruc.Close()
	fruc.Close() // should not panic
}

func TestFRUCMotionBlur(t *testing.T) {
	ctx := testContext(t)

	w, h := 320, 240
	fruc, err := NewFRUC(ctx, w, h)
	require.NoError(t, err)
	defer fruc.Close()

	t.Logf("HasNVOFA: %v", fruc.HasNVOFA())

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

	// Compute optical flow (may return false if no NVOFA hardware)
	hasFlow := fruc.ComputeFlow(prev, curr)
	t.Logf("ComputeFlow returned: %v", hasFlow)

	// Fill output with black to verify it gets written
	require.NoError(t, FillBlack(ctx, output))

	// MotionBlur at alpha=0.5, shutterAngle=0.5 (180°)
	err = fruc.MotionBlur(prev, curr, output, 0.5, 0.5)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, output, w, h))

	// Center pixel should be ~128 (midpoint of 64 and 192)
	centerY := result[h/2*w+w/2]
	assert.InDelta(t, 128, int(centerY), 5, "motion blur 50%% should be ~128")
	t.Logf("FRUC MotionBlur 50%%: center Y=%d (prev=64, curr=192, shutter=180°)", centerY)
}

func TestFRUCMotionBlurZeroShutter(t *testing.T) {
	ctx := testContext(t)

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

	// shutterAngle=0.0 should fall back to standard interpolation
	err = fruc.MotionBlur(prev, curr, output, 0.5, 0.0)
	require.NoError(t, err)

	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, output, w, h))

	// Fallback to Interpolate at alpha=0.5: midpoint of 64 and 192 = 128
	centerY := result[h/2*w+w/2]
	assert.InDelta(t, 128, int(centerY), 5, "zero shutter fallback should be ~128")
	t.Logf("FRUC MotionBlur zero-shutter fallback: center Y=%d", centerY)
}

func TestFRUCComputeFlowNilFrames(t *testing.T) {
	ctx := testContext(t)

	fruc, err := NewFRUC(ctx, 320, 240)
	require.NoError(t, err)
	defer fruc.Close()

	// ComputeFlow with nil frames should return false
	assert.False(t, fruc.ComputeFlow(nil, nil), "ComputeFlow(nil, nil) should return false")
}

func TestFRUCHasNVOFA(t *testing.T) {
	ctx := testContext(t)

	fruc, err := NewFRUC(ctx, 320, 240)
	require.NoError(t, err)
	defer fruc.Close()

	// Log the value — hardware-dependent, no assertion on specific value
	t.Logf("HasNVOFA: %v (L4 GPU = true, dev Mac = false)", fruc.HasNVOFA())
}

func TestFRUCMotionBlurMultipleAlphas(t *testing.T) {
	ctx := testContext(t)

	w, h := 320, 240
	fruc, err := NewFRUC(ctx, w, h)
	require.NoError(t, err)
	defer fruc.Close()

	t.Logf("HasNVOFA: %v", fruc.HasNVOFA())

	pool, err := NewFramePool(ctx, w, h, 5)
	require.NoError(t, err)
	defer pool.Close()

	prev, _ := pool.Acquire()
	curr, _ := pool.Acquire()
	output, _ := pool.Acquire()
	defer prev.Release()
	defer curr.Release()
	defer output.Release()

	// Horizontal gradient: pixel value = x * 255 / w
	yuvPrev := make([]byte, w*h*3/2)
	yuvCurr := make([]byte, w*h*3/2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yuvPrev[y*w+x] = byte(x * 255 / w)
			yuvCurr[y*w+x] = byte(x * 255 / w)
		}
	}
	// Fill UV with neutral chroma
	for i := w * h; i < len(yuvPrev); i++ {
		yuvPrev[i] = 128
		yuvCurr[i] = 128
	}
	require.NoError(t, Upload(ctx, prev, yuvPrev, w, h))
	require.NoError(t, Upload(ctx, curr, yuvCurr, w, h))

	// Compute flow once
	hasFlow := fruc.ComputeFlow(prev, curr)
	t.Logf("ComputeFlow returned: %v", hasFlow)

	// Call MotionBlur at multiple alphas with shutterAngle=0.5
	alphas := []float64{0.2, 0.4, 0.6, 0.8}
	for _, alpha := range alphas {
		require.NoError(t, FillBlack(ctx, output))
		err := fruc.MotionBlur(prev, curr, output, alpha, 0.5)
		require.NoError(t, err, "MotionBlur at alpha=%.1f should not error", alpha)
	}

	t.Logf("MotionBlur completed for %d alpha values, hasNVOFA=%v", len(alphas), fruc.HasNVOFA())
}
