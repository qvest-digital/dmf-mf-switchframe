//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGaussianBlur_SmoothenSharpEdge(t *testing.T) {
	ctx := testContext(t)
	w, h := 64, 64
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Create frame with sharp vertical edge: left half black, right half white
	yuv := make([]byte, yuvFrameSize(w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				yuv[y*w+x] = 16 // black (limited range)
			} else {
				yuv[y*w+x] = 235 // white (limited range)
			}
		}
	}
	// Fill UV with neutral
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, w, h))

	// Blur with radius 5
	err = GaussianBlur(ctx, dst, src, 5)
	require.NoError(t, err)

	// Download result
	result := make([]byte, yuvFrameSize(w, h))
	require.NoError(t, Download(ctx, result, dst, w, h))

	// At the edge (x=32, middle row), the blurred value should be between black and white
	mid := result[32*w+32]
	assert.Greater(t, mid, byte(30), "blurred edge pixel should be above pure black")
	assert.Less(t, mid, byte(220), "blurred edge pixel should be below pure white")

	// Far from edge (x=5), should still be close to black
	farLeft := result[32*w+5]
	assert.Less(t, farLeft, byte(30), "far from edge should stay near black")

	// Far from edge (x=58), should still be close to white
	farRight := result[32*w+58]
	assert.Greater(t, farRight, byte(220), "far from edge should stay near white")
}

func TestGaussianBlur_Radius1(t *testing.T) {
	ctx := testContext(t)
	pool, err := NewFramePool(ctx, 32, 32, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Uniform frame should be unchanged after blur
	yuv := make([]byte, yuvFrameSize(32, 32))
	for i := 0; i < 32*32; i++ {
		yuv[i] = 128
	}
	for i := 32 * 32; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, 32, 32))

	err = GaussianBlur(ctx, dst, src, 1)
	require.NoError(t, err)

	result := make([]byte, yuvFrameSize(32, 32))
	require.NoError(t, Download(ctx, result, dst, 32, 32))

	// Uniform input blurred should still be ~128 everywhere (within +/-1 rounding)
	for i := 0; i < 32*32; i++ {
		diff := int(result[i]) - 128
		if diff < -1 || diff > 1 {
			t.Fatalf("pixel %d: got %d, want 128 (+/-1)", i, result[i])
		}
	}
}

func TestGaussianBlur_InvalidRadius(t *testing.T) {
	ctx := testContext(t)
	pool, err := NewFramePool(ctx, 32, 32, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Radius 0 should return error
	err = GaussianBlur(ctx, dst, src, 0)
	assert.Error(t, err, "radius 0 should return error")

	// Negative radius should return error
	err = GaussianBlur(ctx, dst, src, -1)
	assert.Error(t, err, "negative radius should return error")
}

func TestGaussianBlur_UVPlanePreserved(t *testing.T) {
	ctx := testContext(t)
	w, h := 64, 64
	pool, err := NewFramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Uniform UV should remain unchanged after blur
	yuv := make([]byte, yuvFrameSize(w, h))
	// Y plane: gradient (will change)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yuv[y*w+x] = byte(x * 4) // 0..252
		}
	}
	// UV plane: uniform 100
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 100
	}
	require.NoError(t, Upload(ctx, src, yuv, w, h))

	err = GaussianBlur(ctx, dst, src, 3)
	require.NoError(t, err)

	result := make([]byte, yuvFrameSize(w, h))
	require.NoError(t, Download(ctx, result, dst, w, h))

	// UV (Cb and Cr) should be ~100 everywhere (uniform input stays uniform)
	cbOff := w * h
	crOff := cbOff + (w/2)*(h/2)
	for i := 0; i < (w/2)*(h/2); i++ {
		cbDiff := int(result[cbOff+i]) - 100
		crDiff := int(result[crOff+i]) - 100
		if cbDiff < -1 || cbDiff > 1 {
			t.Fatalf("Cb pixel %d: got %d, want 100 (+/-1)", i, result[cbOff+i])
		}
		if crDiff < -1 || crDiff > 1 {
			t.Fatalf("Cr pixel %d: got %d, want 100 (+/-1)", i, result[crOff+i])
		}
	}
}
