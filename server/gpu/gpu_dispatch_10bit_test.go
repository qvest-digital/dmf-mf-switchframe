//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// makeP210TestFrame is defined in p210_shaders_test.go

// --- Upload/Download dispatch tests ---

func TestGPUUploadDispatch_NV12(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	assert.Equal(t, GPUFormatNV12, frame.Format)

	// Create a YUV420p buffer
	yuv := make([]byte, video.YUV420FrameSize(w, h))
	for i := range yuv {
		yuv[i] = byte(i % 256)
	}

	err = GPUUploadDispatch(ctx, frame, yuv, w, h)
	require.NoError(t, err)
}

func TestGPUUploadDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format)

	src := makeP210TestFrame(w, h, 500, 300, 700)
	err = GPUUploadDispatch(ctx, frame, src, w, h)
	require.NoError(t, err)
}

func TestGPUDownloadDispatch_NV12(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Upload a known pattern
	src := make([]byte, video.YUV420FrameSize(w, h))
	for i := range src {
		src[i] = byte(i % 256)
	}
	err = Upload(ctx, frame, src, w, h)
	require.NoError(t, err)

	// Download via dispatch
	dst := make([]byte, video.YUV420FrameSize(w, h))
	err = GPUDownloadDispatch(ctx, frame, dst, w, h)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(src, dst), "NV12 dispatch round-trip mismatch")
}

func TestGPUDownloadDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	src := makeP210TestFrame(w, h, 500, 300, 700)
	err = Upload10(ctx, frame, src, w, h)
	require.NoError(t, err)

	// Download via dispatch — should detect P210
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	dst := make([]byte, ySize+cbSize+crSize)
	err = GPUDownloadDispatch(ctx, frame, dst, w, h)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(src, dst), "P210 dispatch round-trip mismatch")
}

func TestGPUUploadDownloadDispatch_P210RoundTrip(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Dispatch upload (should detect P210 pool)
	src := makeP210TestFrame(w, h, 500, 300, 700)
	err = GPUUploadDispatch(ctx, frame, src, w, h)
	require.NoError(t, err)

	// Dispatch download
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	dst := make([]byte, ySize+cbSize+crSize)
	err = GPUDownloadDispatch(ctx, frame, dst, w, h)
	require.NoError(t, err)

	if !bytes.Equal(src, dst) {
		t.Error("dispatch round-trip not lossless")
	}
}

// --- Blend dispatch tests ---

func TestGPUBlendMixDispatch_NV12(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewFramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	a, err := pool.Acquire()
	require.NoError(t, err)
	defer a.Release()
	b, err := pool.Acquire()
	require.NoError(t, err)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload white and black NV12 frames
	white := make([]byte, video.YUV420FrameSize(w, h))
	black := make([]byte, video.YUV420FrameSize(w, h))
	for i := 0; i < w*h; i++ {
		white[i] = 235 // BT.709 white
		black[i] = 16  // BT.709 black
	}
	for i := w * h; i < len(white); i++ {
		white[i] = 128
		black[i] = 128
	}
	require.NoError(t, Upload(ctx, a, white, w, h))
	require.NoError(t, Upload(ctx, b, black, w, h))

	err = GPUBlendMixDispatch(ctx, dst, a, b, 0.5)
	require.NoError(t, err)

	// Verify format
	assert.Equal(t, GPUFormatNV12, dst.Format)
}

func TestGPUBlendMixDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	a, err := pool.Acquire()
	require.NoError(t, err)
	defer a.Release()
	b, err := pool.Acquire()
	require.NoError(t, err)
	defer b.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload white and black P210 frames (10-bit limited range)
	white := makeP210TestFrame(w, h, 940, 512, 512)
	black := makeP210TestFrame(w, h, 64, 512, 512)
	require.NoError(t, Upload10(ctx, a, white, w, h))
	require.NoError(t, Upload10(ctx, b, black, w, h))

	// Dispatch through format-aware blend (should auto-detect P210)
	err = GPUBlendMixDispatch(ctx, dst, a, b, 0.5)
	require.NoError(t, err)

	// Verify format preserved
	assert.Equal(t, GPUFormatP210, dst.Format, "format should be preserved as P210")

	// Download and verify midpoint blend
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, dst, result, w, h))

	gotY := binary.LittleEndian.Uint16(result[0:2])
	// Midpoint of 940 and 64 = 502
	if gotY < 498 || gotY > 506 {
		t.Errorf("blend midpoint Y: got %d, want ~502", gotY)
	}
}

func TestGPUBlendFTBDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, err := pool.Acquire()
	require.NoError(t, err)
	defer src.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload a white P210 frame
	white := makeP210TestFrame(w, h, 940, 512, 512)
	require.NoError(t, Upload10(ctx, src, white, w, h))

	// Full fade to black (position=1.0 means fully black)
	err = GPUBlendFTBDispatch(ctx, dst, src, 1.0)
	require.NoError(t, err)

	assert.Equal(t, GPUFormatP210, dst.Format)

	// Download and verify it's black
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, dst, result, w, h))

	gotY := binary.LittleEndian.Uint16(result[0:2])
	// Should be 64 (10-bit limited black)
	if gotY != 64 {
		t.Errorf("FTB Y: got %d, want 64", gotY)
	}
	gotCb := binary.LittleEndian.Uint16(result[ySize : ySize+2])
	if gotCb != 512 {
		t.Errorf("FTB Cb: got %d, want 512", gotCb)
	}
}

// --- Scale dispatch tests ---

func TestGPUScaleDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	srcW, srcH := 128, 64
	dstW, dstH := 64, 32

	srcPool, err := NewP210FramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()
	dstPool, err := NewP210FramePool(ctx, dstW, dstH, 2)
	require.NoError(t, err)
	defer dstPool.Close()

	src, err := srcPool.Acquire()
	require.NoError(t, err)
	defer src.Release()
	dst, err := dstPool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload a known pattern
	srcData := makeP210TestFrame(srcW, srcH, 500, 300, 700)
	require.NoError(t, Upload10(ctx, src, srcData, srcW, srcH))

	err = GPUScaleDispatch(ctx, dst, src)
	require.NoError(t, err)

	assert.Equal(t, GPUFormatP210, dst.Format)

	// Download and spot-check values are reasonable
	chromaW := dstW / 2
	ySize := dstW * dstH * 2
	cbSize := chromaW * dstH * 2
	crSize := cbSize
	result := make([]byte, ySize+cbSize+crSize)
	require.NoError(t, Download10(ctx, dst, result, dstW, dstH))

	gotY := binary.LittleEndian.Uint16(result[0:2])
	// Bilinear scale of uniform 500 should stay ~500
	if gotY < 495 || gotY > 505 {
		t.Errorf("scale P210 Y: got %d, want ~500", gotY)
	}
}

// --- PIP Composite dispatch tests ---

func TestGPUPIPCompositeDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 4)
	require.NoError(t, err)
	defer pool.Close()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()
	src, err := pool.Acquire()
	require.NoError(t, err)
	defer src.Release()

	// Fill dst with black, src with white
	black := makeP210TestFrame(w, h, 64, 512, 512)
	white := makeP210TestFrame(w, h, 940, 512, 512)
	require.NoError(t, Upload10(ctx, dst, black, w, h))
	require.NoError(t, Upload10(ctx, src, white, w, h))

	// Composite src into the top-left quadrant of dst
	rect := Rect{X: 0, Y: 0, W: w / 2, H: h / 2}
	err = GPUPIPCompositeDispatch(ctx, dst, src, rect, 1.0)
	require.NoError(t, err)

	assert.Equal(t, GPUFormatP210, dst.Format)
}

// --- CopyGPUFrame format-aware test ---

func TestCopyGPUFrame_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 3)
	require.NoError(t, err)
	defer pool.Close()

	src, err := pool.Acquire()
	require.NoError(t, err)
	defer src.Release()
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Upload known data
	srcData := makeP210TestFrame(w, h, 500, 300, 700)
	require.NoError(t, Upload10(ctx, src, srcData, w, h))

	// Copy
	err = CopyGPUFrame(dst, src)
	require.NoError(t, err)

	// Download both and compare
	chromaW := w / 2
	ySize := w * h * 2
	cbSize := chromaW * h * 2
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	srcResult := make([]byte, totalSize)
	dstResult := make([]byte, totalSize)
	require.NoError(t, Download10(ctx, src, srcResult, w, h))
	require.NoError(t, Download10(ctx, dst, dstResult, w, h))

	assert.True(t, bytes.Equal(srcResult, dstResult), "CopyGPUFrame P210 data mismatch")
}

// --- gpuRawSinkNode P210 download test ---

func TestGPURawSinkNode_FrameSizeForFormat(t *testing.T) {
	// yuv10FrameSize should return the CPU-side YUV422P10LE buffer size
	w, h := 64, 32
	// NV12 (8-bit 4:2:0): w*h*3/2
	assert.Equal(t, video.YUV420FrameSize(w, h), w*h*3/2)
	// P210 (10-bit 4:2:2): Y(w*h*2) + Cb(w/2*h*2) + Cr(w/2*h*2) = w*h*4
	assert.Equal(t, video.YUV422_10bitFrameSize(w, h), w*h*4)

	// cpuFrameSize should dispatch based on format
	assert.Equal(t, w*h*3/2, cpuFrameSize(w, h, GPUFormatNV12))
	assert.Equal(t, w*h*4, cpuFrameSize(w, h, GPUFormatP210))
}

// --- DSK overlay dispatch test ---

func TestGPUDSKOverlayDispatch_P210(t *testing.T) {
	ctx := testContext(t)

	w, h := 64, 32
	pool, err := NewP210FramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Upload a P210 frame
	srcData := makeP210TestFrame(w, h, 500, 512, 512)
	require.NoError(t, Upload10(ctx, frame, srcData, w, h))

	// Create a small RGBA overlay (fully opaque red)
	rgbaPitch := w * 4
	rgba := make([]byte, rgbaPitch*h)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 128 // half-transparent
	}

	// The DSKOverlayP210 function already exists, but dispatch should route to it
	err = DSKOverlayP210(ctx, frame, rgba, w, h, rgbaPitch, 256)
	require.NoError(t, err)
}

// --- Format detection helper tests ---

func TestYUV10FrameSize(t *testing.T) {
	// YUV422P10LE: Y(w*h*2) + Cb(w/2*h*2) + Cr(w/2*h*2)
	// = 2*w*h + w*h + w*h = 4*w*h
	w, h := 64, 32
	expected := w * h * 4
	got := video.YUV422_10bitFrameSize(w, h)
	assert.Equal(t, expected, got)
}
