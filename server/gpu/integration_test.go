//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestColorBarRoundTrip creates a standard SMPTE-style color bar pattern in
// YUV420p, uploads to GPU (converting to NV12), downloads back (converting
// to YUV420p), and verifies pixel-level accuracy. This validates that the
// NV12 conversion is lossless for 8-bit content.
func TestColorBarRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 2)
	require.NoError(t, err)
	defer pool.Close()

	w, h := 1920, 1080
	yuv := generateColorBars(w, h)

	// Upload and download
	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = Upload(ctx, frame, yuv, w, h)
	require.NoError(t, err)

	result := make([]byte, len(yuv))
	err = Download(ctx, result, frame, w, h)
	require.NoError(t, err)

	// Verify every byte matches (lossless 8-bit NV12 round-trip)
	ySize := w * h
	cbSize := (w / 2) * (h / 2)

	mismatches := 0
	for i := 0; i < len(yuv); i++ {
		if yuv[i] != result[i] {
			mismatches++
			if mismatches <= 10 {
				plane := "Y"
				offset := i
				if i >= ySize+cbSize {
					plane = "Cr"
					offset = i - ySize - cbSize
				} else if i >= ySize {
					plane = "Cb"
					offset = i - ySize
				}
				t.Errorf("mismatch in %s plane at offset %d: expected %d, got %d",
					plane, offset, yuv[i], result[i])
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("... and %d more mismatches (total: %d/%d bytes)",
			mismatches-10, mismatches, len(yuv))
	}
	assert.Zero(t, mismatches, "round-trip should be lossless")
}

// TestMultipleFrameRoundTrip verifies that multiple distinct frames can be
// uploaded, downloaded, and compared independently. This validates that
// the pool correctly isolates frame buffers.
func TestMultipleFrameRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 4)
	require.NoError(t, err)
	defer pool.Close()

	w, h := 320, 240
	totalSize := w * h * 3 / 2

	// Create two distinct patterns
	yuv1 := make([]byte, totalSize)
	yuv2 := make([]byte, totalSize)
	for i := range yuv1 {
		yuv1[i] = byte(i % 256)
		yuv2[i] = byte((i * 3 + 127) % 256)
	}

	// Upload both
	f1, err := pool.Acquire()
	require.NoError(t, err)
	defer f1.Release()

	f2, err := pool.Acquire()
	require.NoError(t, err)
	defer f2.Release()

	err = Upload(ctx, f1, yuv1, w, h)
	require.NoError(t, err)

	err = Upload(ctx, f2, yuv2, w, h)
	require.NoError(t, err)

	// Download and verify each independently
	result1 := make([]byte, totalSize)
	err = Download(ctx, result1, f1, w, h)
	require.NoError(t, err)

	result2 := make([]byte, totalSize)
	err = Download(ctx, result2, f2, w, h)
	require.NoError(t, err)

	for i := 0; i < totalSize; i++ {
		if yuv1[i] != result1[i] {
			t.Fatalf("frame1 mismatch at %d: expected %d, got %d", i, yuv1[i], result1[i])
		}
		if yuv2[i] != result2[i] {
			t.Fatalf("frame2 mismatch at %d: expected %d, got %d", i, yuv2[i], result2[i])
		}
	}
}

// TestFillBlackAndDownload verifies FillBlack produces correct limited-range
// black values and that these round-trip correctly through download.
func TestFillBlackAndDownload(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillBlack(ctx, frame)
	require.NoError(t, err)

	w, h := 1920, 1080
	yuv := make([]byte, w*h*3/2)
	err = Download(ctx, yuv, frame, w, h)
	require.NoError(t, err)

	// Spot-check Y plane (should all be 16)
	for _, i := range []int{0, w / 2, w*h/2 + w/2, w*h - 1} {
		assert.Equal(t, byte(16), yuv[i], "Y[%d] should be 16 (limited-range black)", i)
	}

	// Spot-check Cb plane (should all be 128)
	cbOffset := w * h
	for _, i := range []int{0, w / 4, (w/2)*(h/2) - 1} {
		assert.Equal(t, byte(128), yuv[cbOffset+i], "Cb[%d] should be 128", i)
	}

	// Spot-check Cr plane (should all be 128)
	crOffset := cbOffset + (w/2)*(h/2)
	for _, i := range []int{0, w / 4, (w/2)*(h/2) - 1} {
		assert.Equal(t, byte(128), yuv[crOffset+i], "Cr[%d] should be 128", i)
	}
}

// generateColorBars creates a standard 8-bar color pattern in YUV420p.
// Bars (left to right): White, Yellow, Cyan, Green, Magenta, Red, Blue, Black
// BT.709 limited-range values.
func generateColorBars(w, h int) []byte {
	type yuvColor struct{ y, cb, cr byte }

	// BT.709 limited-range color bar values
	bars := []yuvColor{
		{235, 128, 128}, // White
		{210, 16, 146},  // Yellow
		{170, 166, 16},  // Cyan
		{145, 54, 34},   // Green
		{106, 202, 222}, // Magenta
		{81, 90, 240},   // Red
		{41, 240, 110},  // Blue
		{16, 128, 128},  // Black
	}

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)

	barWidth := w / 8

	// Fill Y plane
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bar := x / barWidth
			if bar >= 8 {
				bar = 7
			}
			yuv[y*w+x] = bars[bar].y
		}
	}

	// Fill Cb plane
	chromaW := w / 2
	chromaH := h / 2
	cbOffset := ySize
	for cy := 0; cy < chromaH; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			bar := (cx * 2) / barWidth
			if bar >= 8 {
				bar = 7
			}
			yuv[cbOffset+cy*chromaW+cx] = bars[bar].cb
		}
	}

	// Fill Cr plane
	crOffset := ySize + cbSize
	for cy := 0; cy < chromaH; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			bar := (cx * 2) / barWidth
			if bar >= 8 {
				bar = 7
			}
			yuv[crOffset+cy*chromaW+cx] = bars[bar].cr
		}
	}

	return yuv
}
