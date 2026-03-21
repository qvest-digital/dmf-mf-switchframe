//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadDownloadRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 2)
	require.NoError(t, err)
	defer pool.Close()

	w, h := 1920, 1080
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + cbSize + cbSize

	// Create test YUV420p data
	yuv := make([]byte, totalSize)
	// Fill Y with gradient (limited range 16-235)
	for i := 0; i < ySize; i++ {
		yuv[i] = byte(i%220) + 16
	}
	// Fill Cb with constant 128
	for i := ySize; i < ySize+cbSize; i++ {
		yuv[i] = 128
	}
	// Fill Cr with constant 128
	for i := ySize + cbSize; i < totalSize; i++ {
		yuv[i] = 128
	}

	// Upload to GPU (YUV420p → NV12)
	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = Upload(ctx, frame, yuv, w, h)
	require.NoError(t, err)

	// Download from GPU (NV12 → YUV420p)
	result := make([]byte, totalSize)
	err = Download(ctx, result, frame, w, h)
	require.NoError(t, err)

	// Verify Y plane round-trips correctly (sample first 1000 pixels)
	for i := 0; i < 1000; i++ {
		assert.Equal(t, yuv[i], result[i], "Y mismatch at pixel %d", i)
	}

	// Verify Cb plane
	for i := 0; i < 100; i++ {
		assert.Equal(t, yuv[ySize+i], result[ySize+i], "Cb mismatch at %d", i)
	}

	// Verify Cr plane
	for i := 0; i < 100; i++ {
		assert.Equal(t, yuv[ySize+cbSize+i], result[ySize+cbSize+i], "Cr mismatch at %d", i)
	}
}

func TestUploadDownloadSmallFrame(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool.Close()

	w, h := 320, 240
	totalSize := w * h * 3 / 2

	// Create deterministic pattern
	yuv := make([]byte, totalSize)
	for i := range yuv {
		yuv[i] = byte((i * 7) % 256)
	}

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = Upload(ctx, frame, yuv, w, h)
	require.NoError(t, err)

	result := make([]byte, totalSize)
	err = Download(ctx, result, frame, w, h)
	require.NoError(t, err)

	// Verify full round-trip for small frame
	for i := 0; i < totalSize; i++ {
		if yuv[i] != result[i] {
			t.Fatalf("mismatch at byte %d: expected %d, got %d", i, yuv[i], result[i])
		}
	}
}

func TestFillBlack(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillBlack(ctx, frame)
	require.NoError(t, err)

	// Download and verify black (Y=16, Cb=128, Cr=128)
	w, h := 320, 240
	yuv := make([]byte, w*h*3/2)
	err = Download(ctx, yuv, frame, w, h)
	require.NoError(t, err)

	// Check Y plane is 16
	for i := 0; i < w*h; i++ {
		if yuv[i] != 16 {
			t.Fatalf("Y[%d] = %d, expected 16", i, yuv[i])
		}
	}
	// Check Cb plane is 128
	cbOffset := w * h
	for i := 0; i < w/2*h/2; i++ {
		if yuv[cbOffset+i] != 128 {
			t.Fatalf("Cb[%d] = %d, expected 128", i, yuv[cbOffset+i])
		}
	}
	// Check Cr plane is 128
	crOffset := cbOffset + w/2*h/2
	for i := 0; i < w/2*h/2; i++ {
		if yuv[crOffset+i] != 128 {
			t.Fatalf("Cr[%d] = %d, expected 128", i, yuv[crOffset+i])
		}
	}
}

func TestUploadNilArgs(t *testing.T) {
	err := Upload(nil, nil, nil, 1920, 1080)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestDownloadNilArgs(t *testing.T) {
	err := Download(nil, nil, nil, 1920, 1080)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestUploadBufferTooSmall(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 1)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	small := make([]byte, 100) // way too small
	err = Upload(ctx, frame, small, 320, 240)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}
