//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

func TestGaussianBlurP210_SmoothenSharpEdge(t *testing.T) {
	ctx := testContext(t)
	w, h := 64, 64
	pool, err := NewP210FramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Create P210 frame with sharp vertical edge: left half dark, right half bright.
	// P210: Y plane = w*h uint16, UV plane = w*h uint16 (interleaved CbCr, full height).
	yuv := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Y plane: 10-bit limited range (64=black, 940=white)
	ySize := w * h * 2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var val uint16
			if x < w/2 {
				val = 64 // 10-bit black
			} else {
				val = 940 // 10-bit white
			}
			binary.LittleEndian.PutUint16(yuv[(y*w+x)*2:], val)
		}
	}

	// UV plane: uniform 512 (neutral chroma for 10-bit)
	chromaW := w / 2
	cbOff := ySize
	crOff := ySize + chromaW*h*2
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(yuv[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(yuv[crOff+i*2:], 512)
	}

	require.NoError(t, Upload10(ctx, src, yuv, w, h))

	// Blur with radius 5
	err = GaussianBlurP210(ctx, dst, src, 5)
	require.NoError(t, err)

	// Download result
	result := make([]byte, video.YUV422_10bitFrameSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	// At the edge (x=32, middle row), the blurred Y value should be between dark and bright
	midIdx := (32*w + 32) * 2
	midVal := binary.LittleEndian.Uint16(result[midIdx:])
	assert.Greater(t, midVal, uint16(100), "blurred edge pixel should be above pure black (got %d)", midVal)
	assert.Less(t, midVal, uint16(900), "blurred edge pixel should be below pure white (got %d)", midVal)

	// Far from edge (x=5), should still be close to black
	farLeftIdx := (32*w + 5) * 2
	farLeftVal := binary.LittleEndian.Uint16(result[farLeftIdx:])
	assert.Less(t, farLeftVal, uint16(120), "far from edge should stay near black (got %d)", farLeftVal)

	// Far from edge (x=58), should still be close to white
	farRightIdx := (32*w + 58) * 2
	farRightVal := binary.LittleEndian.Uint16(result[farRightIdx:])
	assert.Greater(t, farRightVal, uint16(880), "far from edge should stay near white (got %d)", farRightVal)
}

func TestGaussianBlurP210_UniformPreserved(t *testing.T) {
	ctx := testContext(t)
	w, h := 32, 32
	pool, err := NewP210FramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	// Uniform 10-bit frame: Y=512, Cb=512, Cr=512
	yuv := make([]byte, video.YUV422_10bitFrameSize(w, h))
	ySize := w * h * 2
	chromaW := w / 2

	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 512)
	}
	cbOff := ySize
	crOff := ySize + chromaW*h*2
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(yuv[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(yuv[crOff+i*2:], 512)
	}

	require.NoError(t, Upload10(ctx, src, yuv, w, h))

	err = GaussianBlurP210(ctx, dst, src, 3)
	require.NoError(t, err)

	result := make([]byte, video.YUV422_10bitFrameSize(w, h))
	require.NoError(t, Download10(ctx, dst, result, w, h))

	// Uniform input blurred should still be ~512 everywhere (within +/-1 rounding)
	for i := 0; i < w*h; i++ {
		val := binary.LittleEndian.Uint16(result[i*2:])
		diff := int(val) - 512
		if diff < -1 || diff > 1 {
			t.Fatalf("Y pixel %d: got %d, want 512 (+/-1)", i, val)
		}
	}

	// UV should also be preserved
	for i := 0; i < chromaW*h; i++ {
		cbVal := binary.LittleEndian.Uint16(result[cbOff+i*2:])
		crVal := binary.LittleEndian.Uint16(result[crOff+i*2:])
		cbDiff := int(cbVal) - 512
		crDiff := int(crVal) - 512
		if cbDiff < -1 || cbDiff > 1 {
			t.Fatalf("Cb pixel %d: got %d, want 512 (+/-1)", i, cbVal)
		}
		if crDiff < -1 || crDiff > 1 {
			t.Fatalf("Cr pixel %d: got %d, want 512 (+/-1)", i, crVal)
		}
	}
}

func TestGaussianBlurP210_InvalidRadius(t *testing.T) {
	ctx := testContext(t)
	pool, err := NewP210FramePool(ctx, 32, 32, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, _ := pool.Acquire()
	dst, _ := pool.Acquire()
	defer src.Release()
	defer dst.Release()

	err = GaussianBlurP210(ctx, dst, src, 0)
	assert.Error(t, err, "radius 0 should return error")

	err = GaussianBlurP210(ctx, dst, src, -1)
	assert.Error(t, err, "negative radius should return error")

	err = GaussianBlurP210(ctx, dst, src, 51)
	assert.Error(t, err, "radius > 50 should return error")
}

func TestGPUGaussianBlurDispatch_RoutesP210(t *testing.T) {
	ctx := testContext(t)

	// Test P210 routing
	p210Pool, err := NewP210FramePool(ctx, 32, 32, 3)
	require.NoError(t, err)
	defer p210Pool.Close()

	p210Src, _ := p210Pool.Acquire()
	p210Dst, _ := p210Pool.Acquire()
	defer p210Src.Release()
	defer p210Dst.Release()

	// Fill with uniform data
	yuv := make([]byte, video.YUV422_10bitFrameSize(32, 32))
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 512)
	}
	require.NoError(t, Upload10(ctx, p210Src, yuv, 32, 32))

	// Dispatch should route to P210 variant
	err = GPUGaussianBlurDispatch(ctx, p210Dst, p210Src, 3)
	require.NoError(t, err)

	// Verify the frame format is preserved
	assert.Equal(t, GPUFormatP210, p210Dst.Format)

	// Test NV12 routing
	nv12Pool, err := NewFramePool(ctx, 32, 32, 3)
	require.NoError(t, err)
	defer nv12Pool.Close()

	nv12Src, _ := nv12Pool.Acquire()
	nv12Dst, _ := nv12Pool.Acquire()
	defer nv12Src.Release()
	defer nv12Dst.Release()

	nv12Yuv := make([]byte, video.YUV420FrameSize(32, 32))
	for i := range nv12Yuv {
		nv12Yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, nv12Src, nv12Yuv, 32, 32))

	// Dispatch should route to NV12 variant
	err = GPUGaussianBlurDispatch(ctx, nv12Dst, nv12Src, 3)
	require.NoError(t, err)

	assert.Equal(t, GPUFormatNV12, nv12Dst.Format)
}
