//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpload10_Download10_RoundTrip(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	// YUV422P10LE layout: Y (w*h uint16) + Cb (w/2*h uint16) + Cr (w/2*h uint16)
	ySize := w * h * 2 // uint16 per sample
	chromaW := w / 2
	cbSize := chromaW * h * 2 // uint16 per sample
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	src := make([]byte, totalSize)

	// Fill Y plane with known pattern (10-bit values: 0-1023)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(i%1024))
	}
	// Fill Cb plane
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+i*2:], uint16((i*2)%1024))
	}
	// Fill Cr plane
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+cbSize+i*2:], uint16((i*3)%1024))
	}

	// Allocate P210 GPU frame
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Upload
	err = Upload10(ctx, frame, src, w, h)
	require.NoError(t, err)

	// Download
	dst := make([]byte, totalSize)
	err = Download10(ctx, frame, dst, w, h)
	require.NoError(t, err)

	// Compare Y plane
	for i := 0; i < w*h; i++ {
		srcVal := binary.LittleEndian.Uint16(src[i*2:])
		dstVal := binary.LittleEndian.Uint16(dst[i*2:])
		if srcVal != dstVal {
			t.Errorf("Y mismatch at sample %d: src=%d, dst=%d", i, srcVal, dstVal)
			break
		}
	}

	// Compare Cb plane
	for i := 0; i < chromaW*h; i++ {
		srcVal := binary.LittleEndian.Uint16(src[ySize+i*2:])
		dstVal := binary.LittleEndian.Uint16(dst[ySize+i*2:])
		if srcVal != dstVal {
			t.Errorf("Cb mismatch at sample %d: src=%d, dst=%d", i, srcVal, dstVal)
			break
		}
	}

	// Compare Cr plane
	for i := 0; i < chromaW*h; i++ {
		srcVal := binary.LittleEndian.Uint16(src[ySize+cbSize+i*2:])
		dstVal := binary.LittleEndian.Uint16(dst[ySize+cbSize+i*2:])
		if srcVal != dstVal {
			t.Errorf("Cr mismatch at sample %d: src=%d, dst=%d", i, srcVal, dstVal)
			break
		}
	}
}

func TestUpload10_Download10_AllBlack(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 64
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	src := make([]byte, totalSize)

	// BT.709 limited-range 10-bit black: Y=64, Cb=512, Cr=512
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 64)
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+i*2:], 512)
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+cbSize+i*2:], 512)
	}

	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = Upload10(ctx, frame, src, w, h)
	require.NoError(t, err)

	dst := make([]byte, totalSize)
	err = Download10(ctx, frame, dst, w, h)
	require.NoError(t, err)

	// Verify all Y samples are 64
	for i := 0; i < w*h; i++ {
		val := binary.LittleEndian.Uint16(dst[i*2:])
		if val != 64 {
			t.Errorf("Y[%d] = %d, want 64", i, val)
			break
		}
	}
	// Verify all Cb samples are 512
	for i := 0; i < chromaW*h; i++ {
		val := binary.LittleEndian.Uint16(dst[ySize+i*2:])
		if val != 512 {
			t.Errorf("Cb[%d] = %d, want 512", i, val)
			break
		}
	}
	// Verify all Cr samples are 512
	for i := 0; i < chromaW*h; i++ {
		val := binary.LittleEndian.Uint16(dst[ySize+cbSize+i*2:])
		if val != 512 {
			t.Errorf("Cr[%d] = %d, want 512", i, val)
			break
		}
	}
}

func TestP210FramePool_Allocation(t *testing.T) {
	ctx := testContext(t)

	w, h := 1920, 1080
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format, "format should be P210")
	assert.Equal(t, w, frame.Width)
	assert.Equal(t, h, frame.Height)

	// Pitch should be at least width*2 (uint16 per sample), and 256-byte aligned
	minPitch := w * 2
	assert.GreaterOrEqual(t, frame.Pitch, minPitch,
		"pitch %d too small for %d-wide P210 (need >= %d)", frame.Pitch, w, minPitch)
	assert.Equal(t, 0, frame.Pitch%256,
		"pitch %d not 256-byte aligned", frame.Pitch)
}

func TestP210FramePool_FormatPreserved(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Acquire and release
	frame, err := pool.Acquire()
	require.NoError(t, err)
	assert.Equal(t, GPUFormatP210, frame.Format)
	frame.Release()

	// Re-acquire from pool — format should be preserved
	frame2, err := pool.Acquire()
	require.NoError(t, err)
	defer frame2.Release()
	assert.Equal(t, GPUFormatP210, frame2.Format)
}

func TestUpload10_BufferTooSmall(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 1)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Buffer that's too small
	tooSmall := make([]byte, 100)
	err = Upload10(ctx, frame, tooSmall, w, h)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}

func TestDownload10_BufferTooSmall(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 1)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	tooSmall := make([]byte, 100)
	err = Download10(ctx, frame, tooSmall, w, h)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}

func TestUpload10_NilContext(t *testing.T) {
	err := Upload10(nil, &GPUFrame{}, make([]byte, 1024), 16, 16)
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestDownload10_NilContext(t *testing.T) {
	err := Download10(nil, &GPUFrame{}, make([]byte, 1024), 16, 16)
	assert.Equal(t, ErrGPUNotAvailable, err)
}

func TestUpload10_Download10_LargeResolution(t *testing.T) {
	ctx := testContext(t)

	// 1920x1080 10-bit 4:2:2
	w, h := 1920, 1080
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	src := make([]byte, totalSize)
	// Fill with a gradient pattern
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(i%1024))
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+i*2:], uint16((i*7)%1024))
	}
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[ySize+cbSize+i*2:], uint16((i*13)%1024))
	}

	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = Upload10(ctx, frame, src, w, h)
	require.NoError(t, err)

	dst := make([]byte, totalSize)
	err = Download10(ctx, frame, dst, w, h)
	require.NoError(t, err)

	// Spot-check samples across the frame
	samplePoints := []int{0, 100, w*h/2 - 1, w*h - 1}
	for _, idx := range samplePoints {
		srcVal := binary.LittleEndian.Uint16(src[idx*2:])
		dstVal := binary.LittleEndian.Uint16(dst[idx*2:])
		assert.Equal(t, srcVal, dstVal, "Y mismatch at sample %d", idx)
	}

	// Spot-check Cb
	chromaSamples := []int{0, 50, chromaW*h/2 - 1, chromaW*h - 1}
	for _, idx := range chromaSamples {
		srcVal := binary.LittleEndian.Uint16(src[ySize+idx*2:])
		dstVal := binary.LittleEndian.Uint16(dst[ySize+idx*2:])
		assert.Equal(t, srcVal, dstVal, "Cb mismatch at sample %d", idx)
	}

	// Spot-check Cr
	for _, idx := range chromaSamples {
		srcVal := binary.LittleEndian.Uint16(src[ySize+cbSize+idx*2:])
		dstVal := binary.LittleEndian.Uint16(dst[ySize+cbSize+idx*2:])
		assert.Equal(t, srcVal, dstVal, "Cr mismatch at sample %d", idx)
	}
}
