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

// TestUploadResolutionChange verifies that the persistent staging buffer in
// Context correctly reallocates when a larger frame is uploaded after a smaller
// one.  This exercises the "grow" branch of the lazy-alloc path.
func TestUploadResolutionChange(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// Small pool: 320x240
	smallW, smallH := 320, 240
	smallPool, err := NewFramePool(ctx, smallW, smallH, 2)
	require.NoError(t, err)
	defer smallPool.Close()

	// Large pool: 640x480
	largeW, largeH := 640, 480
	largePool, err := NewFramePool(ctx, largeW, largeH, 2)
	require.NoError(t, err)
	defer largePool.Close()

	// --- First upload: 320x240 -----------------------------------------------
	smallFrame, err := smallPool.Acquire()
	require.NoError(t, err)
	defer smallFrame.Release()

	smallYUV := make([]byte, smallW*smallH*3/2)
	for i := 0; i < smallW*smallH; i++ {
		smallYUV[i] = 64 // dark gray Y
	}
	for i := smallW * smallH; i < len(smallYUV); i++ {
		smallYUV[i] = 128
	}
	require.NoError(t, Upload(ctx, smallFrame, smallYUV, smallW, smallH))

	// Verify small frame round-trips correctly.
	smallResult := make([]byte, smallW*smallH*3/2)
	require.NoError(t, Download(ctx, smallResult, smallFrame, smallW, smallH))
	assert.Equal(t, byte(64), smallResult[0], "small frame Y[0] should be 64")

	// --- Second upload: 640x480 (forces staging buffer realloc) ---------------
	largeFrame, err := largePool.Acquire()
	require.NoError(t, err)
	defer largeFrame.Release()

	largeYUV := make([]byte, largeW*largeH*3/2)
	for i := 0; i < largeW*largeH; i++ {
		largeYUV[i] = 192 // bright gray Y
	}
	for i := largeW * largeH; i < len(largeYUV); i++ {
		largeYUV[i] = 128
	}
	require.NoError(t, Upload(ctx, largeFrame, largeYUV, largeW, largeH))

	// Verify large frame round-trips correctly after the realloc.
	largeResult := make([]byte, largeW*largeH*3/2)
	require.NoError(t, Download(ctx, largeResult, largeFrame, largeW, largeH))
	assert.Equal(t, byte(192), largeResult[0], "large frame Y[0] should be 192 after staging realloc")

	// Spot-check a few more pixels to confirm data integrity.
	assert.Equal(t, byte(192), largeResult[largeH/2*largeW+largeW/2], "center pixel should be 192")
	assert.Equal(t, byte(128), largeResult[largeW*largeH], "Cb[0] should be 128")

	t.Logf("ResolutionChange: small Y[0]=%d, large Y[0]=%d", smallResult[0], largeResult[0])
}
