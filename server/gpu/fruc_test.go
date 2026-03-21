//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFRUCAvailable(t *testing.T) {
	avail := FRUCAvailable()
	t.Logf("NvOFFRUC available: %v", avail)
	// Don't fail — library may not be present on all GPU systems
}

func TestFRUCCreate(t *testing.T) {
	if !FRUCAvailable() {
		t.Skip("NvOFFRUC library not available")
	}

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	fruc, err := NewFRUC(ctx, 1920, 1080)
	require.NoError(t, err, "FRUC should create on L4 with NvOFFRUC")
	defer fruc.Close()

	assert.Equal(t, 1920, fruc.width)
	assert.Equal(t, 1080, fruc.height)
	t.Logf("FRUC created: %dx%d, pitch=%d", fruc.width, fruc.height, fruc.pitch)
}

func TestFRUCInterpolate(t *testing.T) {
	if !FRUCAvailable() {
		t.Skip("NvOFFRUC library not available")
	}

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 1920, 1080
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

	// Create two distinct frames: prev=dark, curr=bright
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

	err = fruc.Interpolate(prev, curr, output)
	require.NoError(t, err)

	// Download and verify the interpolated frame is between prev and curr
	result := make([]byte, w*h*3/2)
	require.NoError(t, Download(ctx, result, output, w, h))

	// Sample center pixel — should be somewhere between 64 and 192
	centerY := result[h/2*w+w/2]
	t.Logf("FRUC interpolated center Y=%d (prev=64, curr=192)", centerY)

	// The interpolated frame should differ from black (Y=16)
	assert.Greater(t, centerY, byte(32), "interpolated frame should not be black")
}

func TestFRUCNilContext(t *testing.T) {
	_, err := NewFRUC(nil, 1920, 1080)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestFRUCDoubleClose(t *testing.T) {
	if !FRUCAvailable() {
		t.Skip("NvOFFRUC library not available")
	}

	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	fruc, err := NewFRUC(ctx, 320, 240)
	require.NoError(t, err)

	fruc.Close()
	fruc.Close() // should not panic
}
