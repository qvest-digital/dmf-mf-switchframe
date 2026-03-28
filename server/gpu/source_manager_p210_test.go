//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceManager_P210Pool_IngestYUV(t *testing.T) {
	ctx := testContext(t)

	// Create P210 pool at 8x8
	pool, err := NewP210FramePool(ctx, 8, 8, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Ingest a 8x8 YUV420P frame with uniform Y=128
	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	for i := range yuv {
		yuv[i] = 128
	}

	mgr.IngestYUV("test-source", yuv, w, h, 12345)

	// GetFrame should return a P210 frame
	frame := mgr.GetFrame("test-source")
	if frame == nil {
		t.Fatal("expected non-nil frame after IngestYUV")
	}
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format, "expected P210 format")

	// Read back via Download10 (planar YUV422P10LE) and verify Y values are 128<<2 = 512.
	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	yVal := binary.LittleEndian.Uint16(buf[0:2])
	assert.Equal(t, uint16(512), yVal, "Y[0] should be 128<<2 = 512 in P210")

	// Check a few more Y samples across the first row.
	for i := 0; i < w; i++ {
		off := i * 2
		val := binary.LittleEndian.Uint16(buf[off : off+2])
		assert.Equal(t, uint16(512), val, "Y[%d] mismatch", i)
	}
}

func TestSourceManager_P210Pool_IngestYUV_DifferentResolution(t *testing.T) {
	ctx := testContext(t)

	// Create P210 pool at 16x16 (pipeline resolution)
	pool, err := NewP210FramePool(ctx, 16, 16, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Ingest a 8x8 YUV420P frame (source is different resolution)
	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	for i := range yuv {
		yuv[i] = 128
	}

	mgr.IngestYUV("test-source", yuv, w, h, 12345)

	// GetFrame should return a P210 frame at pool resolution (16x16)
	frame := mgr.GetFrame("test-source")
	if frame == nil {
		t.Fatal("expected non-nil frame after IngestYUV")
	}
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format, "expected P210 format")
	assert.Equal(t, 16, frame.Width, "expected pool width")
	assert.Equal(t, 16, frame.Height, "expected pool height")
}

func TestSourceManager_P210Pool_MultipleIngests(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 8, 8, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)

	// Ingest two frames with different Y values to verify atomic swap works.
	yuv1 := make([]byte, ySize+cbSize+cbSize)
	for i := 0; i < ySize; i++ {
		yuv1[i] = 100
	}
	for i := ySize; i < len(yuv1); i++ {
		yuv1[i] = 128
	}
	mgr.IngestYUV("test-source", yuv1, w, h, 1000)

	yuv2 := make([]byte, ySize+cbSize+cbSize)
	for i := 0; i < ySize; i++ {
		yuv2[i] = 200
	}
	for i := ySize; i < len(yuv2); i++ {
		yuv2[i] = 128
	}
	mgr.IngestYUV("test-source", yuv2, w, h, 2000)

	// Should have the latest frame (Y=200 -> P210 Y=800)
	frame := mgr.GetFrame("test-source")
	require.NotNil(t, frame)
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format)

	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))
	yVal := binary.LittleEndian.Uint16(buf[0:2])
	assert.Equal(t, uint16(800), yVal, "Y[0] should be 200<<2 = 800 in P210")
}

func TestSourceManager_NV12Pool_IngestYUV_Unchanged(t *testing.T) {
	ctx := testContext(t)

	// Verify NV12 path is unchanged when using an NV12 pool.
	pool, err := NewFramePool(ctx, 8, 8, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	for i := range yuv {
		yuv[i] = 128
	}

	mgr.IngestYUV("test-source", yuv, w, h, 12345)

	frame := mgr.GetFrame("test-source")
	require.NotNil(t, frame)
	defer frame.Release()

	assert.Equal(t, GPUFormatNV12, frame.Format, "NV12 pool should produce NV12 frames")
}

func TestSourceManager_P210Pool_IngestYUV_10bitInput(t *testing.T) {
	// When the source decoder upconverts to YUV422P10LE, IngestYUV receives
	// w*h*4 bytes instead of w*h*3/2. The P210 upload path must detect this
	// and use Upload10 instead of UploadYUV420PToP210.
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 8, 8, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Build YUV422P10LE data (w*h*4 bytes = 256 bytes for 8x8).
	// This simulates what the source decoder produces after upconversion.
	w, h := 8, 8
	yuv10 := make([]byte, yuv10FrameSize(w, h))

	// Fill Y plane with 10-bit value 400 (little-endian uint16).
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv10[i*2:], 400)
	}
	// Fill Cb with 300, Cr with 700.
	cbOffset := w * h * 2
	crOffset := cbOffset + (w/2)*h*2
	for i := 0; i < (w/2)*h; i++ {
		binary.LittleEndian.PutUint16(yuv10[cbOffset+i*2:], 300)
		binary.LittleEndian.PutUint16(yuv10[crOffset+i*2:], 700)
	}

	mgr.IngestYUV("test-10bit", yuv10, w, h, 99999)

	frame := mgr.GetFrame("test-10bit")
	if frame == nil {
		t.Fatal("expected non-nil frame after IngestYUV with 10-bit data")
	}
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format, "expected P210 format")

	// Verify Y values are ~400 (Upload10 preserves 10-bit values).
	// If the code incorrectly used UploadYUV420PToP210, the values would
	// be wildly wrong because it interprets uint8 bytes as Y samples.
	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))
	y0 := binary.LittleEndian.Uint16(buf[0:2])
	assert.InDelta(t, 400, int(y0), 20, "Y[0] = %d, want ~400 (10-bit preserved via Upload10)", y0)

	t.Logf("10-bit input: Y[0]=%d (want ~400)", y0)
}

func TestSourceManager_P210Pool_IngestYUV_10bitInput_DifferentResolution(t *testing.T) {
	// When the source decoder produces 10-bit data at a different resolution
	// than the pool, the scale path must also detect 10-bit input and use
	// Upload10 for the temporary source frame.
	ctx := testContext(t)

	// Pool at 16x16, source at 8x8.
	pool, err := NewP210FramePool(ctx, 16, 16, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	w, h := 8, 8
	yuv10 := make([]byte, yuv10FrameSize(w, h))

	// Fill with uniform 10-bit value 600.
	for i := 0; i < len(yuv10)/2; i++ {
		binary.LittleEndian.PutUint16(yuv10[i*2:], 600)
	}

	mgr.IngestYUV("test-10bit-scale", yuv10, w, h, 12345)

	frame := mgr.GetFrame("test-10bit-scale")
	if frame == nil {
		t.Fatal("expected non-nil frame after IngestYUV with 10-bit scaled data")
	}
	defer frame.Release()

	assert.Equal(t, 16, frame.Width, "expected pool width after scale")
	assert.Equal(t, 16, frame.Height, "expected pool height after scale")
	assert.Equal(t, GPUFormatP210, frame.Format, "expected P210 format")

	// Verify scaled Y values are approximately 600. Bilinear scaling of a
	// uniform field should preserve the value.
	buf := make([]byte, p210DownloadSize(16, 16))
	require.NoError(t, Download10(ctx, frame, buf, 16, 16))
	y0 := binary.LittleEndian.Uint16(buf[0:2])
	assert.InDelta(t, 600, int(y0), 30, "scaled Y[0] = %d, want ~600", y0)

	t.Logf("10-bit scaled input: Y[0]=%d (want ~600)", y0)
}

func TestSourceManager_P210Pool_CPUScaleFallback(t *testing.T) {
	ctx := testContext(t)

	// Create P210 pool at 16x16 but test the CPU scale path by using
	// a source at different resolution. The GPU path should work, but
	// this validates the uploadCPUScaled format dispatch is correct.
	pool, err := NewP210FramePool(ctx, 16, 16, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Create a test source entry and call uploadCPUScaled directly.
	entry := &gpuSourceEntry{width: 8, height: 8}
	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	for i := range yuv {
		yuv[i] = 128
	}

	err = mgr.uploadCPUScaled(entry, dst, yuv, w, h)
	require.NoError(t, err)

	// Verify the frame is still P210 format after CPU-scaled upload.
	assert.Equal(t, GPUFormatP210, dst.Format)
}
