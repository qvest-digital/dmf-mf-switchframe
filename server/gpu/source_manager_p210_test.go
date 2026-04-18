//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// TestSourceManager_P210Pool_PerSourceSTMap_HorizontalFlip mirrors
// TestGPUSourceManager_STMapCorrection (NV12) but against a P210 pool.
//
// Before the source-manager STMap dispatch fix, applySourceSTMap called
// the NV12-only STMapWarp kernel on P210 frames, producing green-tinted
// garbage or outright crashing depending on how the Metal/CUDA backend
// reacted to the format mismatch. This test validates that per-source
// STMap correction in 10-bit 4:2:2 mode actually applies the warp.
func TestSourceManager_P210Pool_PerSourceSTMap_HorizontalFlip(t *testing.T) {
	const w, h = 32, 32
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, w, h, 12) // extra frames for stmap tmp
	require.NoError(t, err)
	defer pool.Close()

	// Horizontal flip ST map: each pixel reads from (w-1-x, y).
	s := make([]float32, w*h)
	tArr := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = float32(w-1-x) / float32(w-1)
			tArr[y*w+x] = float32(y) / float32(h-1)
		}
	}

	provider := &mockSourceSTMapProvider{
		names:  map[string]string{"cam1": "hflip"},
		arrays: map[string][2][]float32{"cam1": {s, tArr}},
	}

	mgr := NewGPUSourceManager(ctx, pool, provider)
	defer mgr.Close()

	// Build a 10-bit 4:2:2 gradient: bright left (Y=880 ~= white), dark right
	// (Y=80 ~= black). Chroma at neutral 10-bit (512).
	yuv := make([]byte, video.YUV422_10bitFrameSize(w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var yv uint16 = 80
			if x < w/2 {
				yv = 880
			}
			binary.LittleEndian.PutUint16(yuv[(y*w+x)*2:], yv)
		}
	}
	ySize := w * h * 2
	chromaPlaneSize := (w / 2) * h * 2
	for i := 0; i < chromaPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[ySize+i:], 512)
		binary.LittleEndian.PutUint16(yuv[ySize+chromaPlaneSize+i:], 512)
	}

	mgr.IngestYUV("cam1", yuv, w, h, 90000)

	frame := mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	defer frame.Release()
	assert.Equal(t, GPUFormatP210, frame.Format, "expected P210 format")

	// Download and confirm the horizontal flip took effect.
	out := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, out, w, h))

	midY := h / 2
	leftPix := binary.LittleEndian.Uint16(out[(midY*w+2)*2:])
	rightPix := binary.LittleEndian.Uint16(out[(midY*w+(w-1-2))*2:])

	assert.Less(t, int(leftPix), 300,
		"after hflip, left pixel should be dark (was bright 880); got %d", leftPix)
	assert.Greater(t, int(rightPix), 600,
		"after hflip, right pixel should be bright (was dark 80); got %d", rightPix)

	t.Logf("P210 per-source STMap hflip: left=%d (want <300), right=%d (want >600)",
		leftPix, rightPix)
}

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
	yuv10 := make([]byte, video.YUV422_10bitFrameSize(w, h))

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
	yuv10 := make([]byte, video.YUV422_10bitFrameSize(w, h))

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

func TestSourceManager_P210Pool_IngestYUV_8bit_DistinctYUV(t *testing.T) {
	// Tests that 8-bit YUV420P with distinct Y, Cb, Cr values produces a correct
	// P210 frame at the same resolution as the pool.
	// On Metal: uses UploadYUV420PToP210 directly (unified memory).
	// On CUDA: uses uploadNV12ThenConvertP210 fallback (NV12 temp + convert kernel).
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 8, 8, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// Distinct values: Y=200, Cb=100, Cr=180
	for i := 0; i < ySize; i++ {
		yuv[i] = 200
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 100
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 180
	}

	mgr.IngestYUV("test-8bit-distinct", yuv, w, h, 12345)

	frame := mgr.GetFrame("test-8bit-distinct")
	require.NotNil(t, frame, "expected non-nil frame after IngestYUV")
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format, "expected P210 format")

	// Download and verify Y values are 200<<2 = 800.
	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))

	y0 := readP210YSample(buf, 0)
	assert.InDelta(t, 800, int(y0), 10, "Y[0] should be ~200<<2 = 800, got %d", y0)

	// Verify Cb and Cr values. The 8-bit→10-bit conversion should produce
	// Cb ≈ 100<<2 = 400 and Cr ≈ 180<<2 = 720.
	// Note: YUV420→YUV422 chroma upsampling may introduce slight interpolation
	// artifacts, but for a uniform field the values should be close.
	cb0 := readP210CbSample(buf, w, h, 0)
	cr0 := readP210CrSample(buf, w, h, 0)
	assert.InDelta(t, 400, int(cb0), 20, "Cb[0] should be ~100<<2 = 400, got %d", cb0)
	assert.InDelta(t, 720, int(cr0), 20, "Cr[0] should be ~180<<2 = 720, got %d", cr0)

	t.Logf("8-bit distinct YUV: Y[0]=%d (want ~800), Cb[0]=%d (want ~400), Cr[0]=%d (want ~720)", y0, cb0, cr0)
}

func TestSourceManager_P210Pool_IngestYUV_8bit_DifferentResolution_ValueCheck(t *testing.T) {
	// Tests that 8-bit YUV420P at a different resolution than the pool produces
	// a correct P210 frame after GPU scaling. Verifies actual pixel values.
	// On Metal: UploadYUV420PToP210 to P210 temp + ScaleBilinearP210.
	// On CUDA: uploadNV12ScaleThenConvertP210 fallback (NV12 temp + ScaleNV12ToP210).
	ctx := testContext(t)

	// Pool at 16x16, source at 8x8.
	pool, err := NewP210FramePool(ctx, 16, 16, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// Uniform Y=160, Cb=90, Cr=200
	for i := 0; i < ySize; i++ {
		yuv[i] = 160
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 90
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 200
	}

	mgr.IngestYUV("test-8bit-diff-res", yuv, w, h, 99999)

	frame := mgr.GetFrame("test-8bit-diff-res")
	require.NotNil(t, frame, "expected non-nil frame")
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format)
	assert.Equal(t, 16, frame.Width, "expected pool width after scaling")
	assert.Equal(t, 16, frame.Height, "expected pool height after scaling")

	// Download at pool dimensions and verify values.
	// Bilinear scaling of a uniform field should preserve values.
	buf := make([]byte, p210DownloadSize(16, 16))
	require.NoError(t, Download10(ctx, frame, buf, 16, 16))

	// Y should be ~160<<2 = 640 after 8-bit→10-bit promotion.
	y0 := readP210YSample(buf, 0)
	assert.InDelta(t, 640, int(y0), 30, "scaled Y[0] should be ~160<<2 = 640, got %d", y0)

	// Cb should be ~90<<2 = 360, Cr should be ~200<<2 = 800.
	cb0 := readP210CbSample(buf, 16, 16, 0)
	cr0 := readP210CrSample(buf, 16, 16, 0)
	assert.InDelta(t, 360, int(cb0), 40, "scaled Cb[0] should be ~90<<2 = 360, got %d", cb0)
	assert.InDelta(t, 800, int(cr0), 40, "scaled Cr[0] should be ~200<<2 = 800, got %d", cr0)

	t.Logf("8-bit diff-res: Y[0]=%d (want ~640), Cb[0]=%d (want ~360), Cr[0]=%d (want ~800)", y0, cb0, cr0)
}

func TestSourceManager_P210Pool_NV12TmpPoolReuse(t *testing.T) {
	// Tests that the NV12 temp pool (used for CUDA 8-bit→P210 fallback) is
	// correctly reused across multiple ingests. On Metal, the NV12 temp pool
	// is never created (UploadYUV420PToP210 succeeds directly), so this test
	// verifies that repeated ingests at the same resolution don't leak resources.
	ctx := testContext(t)

	pool, err := NewP210FramePool(ctx, 8, 8, 4)
	require.NoError(t, err)
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	w, h := 8, 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize

	// Ingest 10 frames with different Y values to verify no resource leaks.
	for i := 0; i < 10; i++ {
		yuv := make([]byte, ySize+cbSize+crSize)
		yVal := byte(50 + i*20)
		for j := 0; j < ySize; j++ {
			yuv[j] = yVal
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}
		mgr.IngestYUV("test-reuse", yuv, w, h, int64(i*3000))
	}

	// Final frame should have Y = 50+9*20 = 230, → P210 Y = 230<<2 = 920.
	frame := mgr.GetFrame("test-reuse")
	require.NotNil(t, frame)
	defer frame.Release()

	assert.Equal(t, GPUFormatP210, frame.Format)

	buf := make([]byte, p210DownloadSize(w, h))
	require.NoError(t, Download10(ctx, frame, buf, w, h))
	y0 := readP210YSample(buf, 0)
	assert.InDelta(t, 920, int(y0), 10, "Y[0] should be ~230<<2 = 920 after 10 ingests, got %d", y0)
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
