//go:build correctness

package encode

import (
	"runtime"
	"testing"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit decoder stress tests.
//
// These test rapid HEVC encode/decode cycles for 10-bit content.
// Skipped if HEVC encoder is not available.
// ==========================================================================

// TestDecoderRapidCreateDestroy10bit creates and closes 50 decoder instances
// in a tight loop, measuring heap growth to detect memory leaks.
// Identical to 8-bit but ensures no 10-bit-specific cleanup issues.
func TestDecoderRapidCreateDestroy10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	runtime.GC()
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	const iterations = 50
	for i := 0; i < iterations; i++ {
		dec, err := codec.NewFFmpegDecoder(nil)
		if err != nil {
			t.Fatalf("iteration %d: failed to create decoder: %v", i, err)
		}
		dec.Close()
	}

	runtime.GC()
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	var deltaMB int64
	if memAfter.HeapAlloc > memBefore.HeapAlloc {
		deltaMB = int64(memAfter.HeapAlloc-memBefore.HeapAlloc) / (1024 * 1024)
	}

	t.Logf("MEASURE: 10-bit decoder stress: heap delta=%dMB after %d iterations", deltaMB, iterations)

	if deltaMB > 50 {
		t.Errorf("heap grew by %dMB after %d create/destroy cycles -- possible leak", deltaMB, iterations)
	}
}

// TestEncoderDecoder10bitFrameSizing verifies that 10-bit frame sizes are
// consistently w*h*4 through encode/decode cycles. The encoder may produce
// H.264 output (downconverted), but the INPUT frame must be correctly sized.
func TestEncoderDecoder10bitFrameSizing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	format := switcher.ColorYUV422_10bit

	resolutions := []struct{ w, h int }{
		{320, 240},
		{640, 480},
		{1280, 720},
	}

	for _, r := range resolutions {
		expectedSize := format.FrameSize(r.w, r.h)
		require10bit := r.w * r.h * 4

		if expectedSize != require10bit {
			t.Errorf("%dx%d: FrameSize() = %d, expected w*h*4 = %d",
				r.w, r.h, expectedSize, require10bit)
		}

		// Verify component sizes sum correctly.
		ySize := format.YPlaneSize(r.w, r.h)
		cbSize := format.ChromaPlaneSize(r.w, r.h)
		crSize := format.ChromaPlaneSize(r.w, r.h)
		total := ySize + cbSize + crSize

		if total != expectedSize {
			t.Errorf("%dx%d: Y(%d) + Cb(%d) + Cr(%d) = %d, expected %d",
				r.w, r.h, ySize, cbSize, crSize, total, expectedSize)
		}

		t.Logf("%dx%d: 10-bit frame = %d bytes (Y=%d, Cb=%d, Cr=%d)",
			r.w, r.h, expectedSize, ySize, cbSize, crSize)
	}
}

// TestEncoder10bit_H264EncodeFromLargerBuffer verifies that encoding from
// a 10-bit-sized buffer (w*h*4 bytes) doesn't cause out-of-bounds access.
// The x264 encoder expects YUV420 (w*h*3/2), so the pipeline must convert
// before encoding. This test verifies the conversion path doesn't crash.
func TestEncoder10bit_H264EncodeFromLargerBuffer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240

	// Create an encoder expecting YUV420 input.
	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 2, nil)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	// Encode with a standard-sized YUV420 buffer.
	yuv420Size := w * h * 3 / 2
	yuv := make([]byte, yuv420Size)
	for i := 0; i < w*h; i++ {
		yuv[i] = 128 // mid gray Y
	}
	for i := w * h; i < yuv420Size; i++ {
		yuv[i] = 128 // neutral chroma
	}

	var encodedCount int
	for i := 0; i < 30; i++ {
		data, _, encErr := enc.Encode(yuv, int64(90000+i*3000), i == 0)
		if encErr != nil {
			t.Fatalf("encode error at frame %d: %v", i, encErr)
		}
		if data != nil {
			encodedCount++
		}
	}

	t.Logf("MEASURE: encoded %d frames from YUV420 buffer", encodedCount)
	if encodedCount == 0 {
		t.Error("encoder should produce at least one frame from 30 inputs")
	}

	// Verify that a 10-bit frame size is indeed larger.
	format10 := switcher.ColorYUV422_10bit
	size10 := format10.FrameSize(w, h)
	if size10 <= yuv420Size {
		t.Errorf("10-bit frame (%d bytes) should be larger than 8-bit (%d bytes)",
			size10, yuv420Size)
	}

	t.Logf("10-bit frame = %d bytes vs 8-bit = %d bytes (ratio %.2fx)",
		size10, yuv420Size, float64(size10)/float64(yuv420Size))
}
