package mxl

import (
	"encoding/binary"
	"testing"
)

// packV210Word packs three 10-bit values into a single 32-bit V210 word.
// The values are placed at bits [9:0], [19:10], and [29:20] respectively,
// with bits [31:30] set to zero.
func packV210Word(a, b, c uint32) uint32 {
	return (a & 0x3FF) | ((b & 0x3FF) << 10) | ((c & 0x3FF) << 20)
}

func TestV210LineStride(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  int
	}{
		{
			name:  "6 pixels (minimum group)",
			width: 6,
			want:  128, // ceil(6/6)*16 = 16, rounded up to 128
		},
		{
			name:  "720 (SD)",
			width: 720,
			want:  1920, // ceil(720/6)*16 = 120*16 = 1920, already 128-aligned (15*128)
		},
		{
			name:  "1280 (720p)",
			width: 1280,
			want:  3456, // ceil(1280/6)*16 = 214*16 = 3424, rounded up to 3456 (27*128)
		},
		{
			name:  "1920 (1080p)",
			width: 1920,
			want:  5120, // ceil(1920/6)*16 = 320*16 = 5120, already 128-aligned
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := V210LineStride(tt.width)
			if got != tt.want {
				t.Errorf("V210LineStride(%d) = %d, want %d", tt.width, got, tt.want)
			}
			if got%128 != 0 {
				t.Errorf("V210LineStride(%d) = %d, not 128-byte aligned", tt.width, got)
			}
		})
	}
}

// buildV210Frame constructs a V210 frame from per-pixel Y, Cb, Cr values (10-bit).
// pixels must have exactly width*height entries in row-major order.
// Cb and Cr are subsampled horizontally: one Cb/Cr pair per two horizontal pixels (4:2:2).
// cbcr should have width/2 * height entries. cbcr[row*(width/2)+col] gives (Cb, Cr) for pixel pair col.
func buildV210Frame(t *testing.T, width, height int, y []uint32, cb, cr []uint32) []byte {
	t.Helper()
	stride := V210LineStride(width)
	buf := make([]byte, stride*height)

	for row := 0; row < height; row++ {
		rowOffset := row * stride
		for group := 0; group < width/6; group++ {
			// 6 pixels per group, 4 words per group
			// Pixel indices in this group
			px := row*width + group*6
			// Chroma indices (4:2:2: one Cb/Cr per 2 pixels)
			cx := row*(width/2) + group*3

			// Word 0: Cb0, Y0, Cr0
			w0 := packV210Word(cb[cx+0], y[px+0], cr[cx+0])
			// Word 1: Y1, Cb2, Y2
			w1 := packV210Word(y[px+1], cb[cx+1], y[px+2])
			// Word 2: Cr2, Y3, Cb4
			w2 := packV210Word(cr[cx+1], y[px+3], cb[cx+2])
			// Word 3: Y4, Cr4, Y5
			w3 := packV210Word(y[px+4], cr[cx+2], y[px+5])

			wordOffset := rowOffset + group*16
			binary.LittleEndian.PutUint32(buf[wordOffset:], w0)
			binary.LittleEndian.PutUint32(buf[wordOffset+4:], w1)
			binary.LittleEndian.PutUint32(buf[wordOffset+8:], w2)
			binary.LittleEndian.PutUint32(buf[wordOffset+12:], w3)
		}
	}
	return buf
}

func TestV210ToYUV420p_Black(t *testing.T) {
	// 6x2 all-black frame in limited range: Y=16, Cb=Cr=128 (10-bit: Y=64, Cb=Cr=512)
	width, height := 6, 2
	numPixels := width * height
	numChroma := (width / 2) * height

	y10 := make([]uint32, numPixels)
	cb10 := make([]uint32, numChroma)
	cr10 := make([]uint32, numChroma)

	for i := range y10 {
		y10[i] = 64 // 16 << 2
	}
	for i := range cb10 {
		cb10[i] = 512 // 128 << 2
	}
	for i := range cr10 {
		cr10[i] = 512
	}

	v210 := buildV210Frame(t, width, height, y10, cb10, cr10)
	yuv, err := V210ToYUV420p(v210, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p() error: %v", err)
	}

	// Check output dimensions
	ySize := width * height
	cSize := (width / 2) * (height / 2)
	expectedSize := ySize + 2*cSize
	if len(yuv) != expectedSize {
		t.Fatalf("output size = %d, want %d", len(yuv), expectedSize)
	}

	// All Y should be 16
	for i := 0; i < ySize; i++ {
		if yuv[i] != 16 {
			t.Errorf("Y[%d] = %d, want 16", i, yuv[i])
		}
	}

	// All Cb should be 128
	cbStart := ySize
	for i := 0; i < cSize; i++ {
		if yuv[cbStart+i] != 128 {
			t.Errorf("Cb[%d] = %d, want 128", i, yuv[cbStart+i])
		}
	}

	// All Cr should be 128
	crStart := ySize + cSize
	for i := 0; i < cSize; i++ {
		if yuv[crStart+i] != 128 {
			t.Errorf("Cr[%d] = %d, want 128", i, yuv[crStart+i])
		}
	}
}

func TestV210ToYUV420p_White(t *testing.T) {
	// 6x2 all-white frame in limited range: Y=235, Cb=Cr=128 (10-bit: Y=940, Cb=Cr=512)
	width, height := 6, 2
	numPixels := width * height
	numChroma := (width / 2) * height

	y10 := make([]uint32, numPixels)
	cb10 := make([]uint32, numChroma)
	cr10 := make([]uint32, numChroma)

	for i := range y10 {
		y10[i] = 940 // 235 << 2
	}
	for i := range cb10 {
		cb10[i] = 512
	}
	for i := range cr10 {
		cr10[i] = 512
	}

	v210 := buildV210Frame(t, width, height, y10, cb10, cr10)
	yuv, err := V210ToYUV420p(v210, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p() error: %v", err)
	}

	ySize := width * height
	cSize := (width / 2) * (height / 2)

	// All Y should be 235
	for i := 0; i < ySize; i++ {
		if yuv[i] != 235 {
			t.Errorf("Y[%d] = %d, want 235", i, yuv[i])
		}
	}

	// All Cb should be 128
	cbStart := ySize
	for i := 0; i < cSize; i++ {
		if yuv[cbStart+i] != 128 {
			t.Errorf("Cb[%d] = %d, want 128", i, yuv[cbStart+i])
		}
	}

	// All Cr should be 128
	crStart := ySize + cSize
	for i := 0; i < cSize; i++ {
		if yuv[crStart+i] != 128 {
			t.Errorf("Cr[%d] = %d, want 128", i, yuv[crStart+i])
		}
	}
}

func TestV210ToYUV420p_ChromaDownsample(t *testing.T) {
	// 6x2 frame where top row has Cb=100, Cr=200 and bottom row has Cb=200, Cr=100 (8-bit values).
	// After 4:2:0 downsampling, chroma should be averaged: Cb=150, Cr=150.
	width, height := 6, 2
	numPixels := width * height
	numChroma := (width / 2) * height

	y10 := make([]uint32, numPixels)
	cb10 := make([]uint32, numChroma)
	cr10 := make([]uint32, numChroma)

	// All Y at mid-gray (Y=128 -> 10-bit: 512)
	for i := range y10 {
		y10[i] = 512
	}

	// Top row (first width/2 = 3 chroma samples): Cb=100, Cr=200 (10-bit: 400, 800)
	for i := 0; i < width/2; i++ {
		cb10[i] = 400 // 100 << 2
		cr10[i] = 800 // 200 << 2
	}

	// Bottom row (next 3 chroma samples): Cb=200, Cr=100 (10-bit: 800, 400)
	for i := width / 2; i < numChroma; i++ {
		cb10[i] = 800 // 200 << 2
		cr10[i] = 400 // 100 << 2
	}

	v210 := buildV210Frame(t, width, height, y10, cb10, cr10)
	yuv, err := V210ToYUV420p(v210, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p() error: %v", err)
	}

	ySize := width * height
	cSize := (width / 2) * (height / 2)

	// All Y should be 128
	for i := 0; i < ySize; i++ {
		if yuv[i] != 128 {
			t.Errorf("Y[%d] = %d, want 128", i, yuv[i])
		}
	}

	// 4:2:0 chroma: average of top row and bottom row
	// Cb: (100 + 200) / 2 = 150
	cbStart := ySize
	for i := 0; i < cSize; i++ {
		if yuv[cbStart+i] != 150 {
			t.Errorf("Cb[%d] = %d, want 150", i, yuv[cbStart+i])
		}
	}

	// Cr: (200 + 100) / 2 = 150
	crStart := ySize + cSize
	for i := 0; i < cSize; i++ {
		if yuv[crStart+i] != 150 {
			t.Errorf("Cr[%d] = %d, want 150", i, yuv[crStart+i])
		}
	}
}

func TestYUV420pToV210_RoundTrip(t *testing.T) {
	// Create a YUV420p frame with known values, convert to V210 and back.
	// Due to 8→10→8 bit truncation, values should match within ±1.
	width, height := 6, 2
	ySize := width * height
	cSize := (width / 2) * (height / 2)
	totalSize := ySize + 2*cSize

	yuv := make([]byte, totalSize)

	// Set Y values to a gradient
	for i := 0; i < ySize; i++ {
		yuv[i] = byte(16 + i*18) // range ~16-232
	}

	// Set Cb values
	cbStart := ySize
	for i := 0; i < cSize; i++ {
		yuv[cbStart+i] = byte(64 + i*40) // various chroma values
	}

	// Set Cr values
	crStart := ySize + cSize
	for i := 0; i < cSize; i++ {
		yuv[crStart+i] = byte(80 + i*50)
	}

	// Convert to V210
	v210, err := YUV420pToV210(yuv, width, height)
	if err != nil {
		t.Fatalf("YUV420pToV210() error: %v", err)
	}

	// Convert back to YUV420p
	yuv2, err := V210ToYUV420p(v210, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p() error: %v", err)
	}

	if len(yuv2) != totalSize {
		t.Fatalf("round-trip output size = %d, want %d", len(yuv2), totalSize)
	}

	// Y values should match exactly (8→10→8 with shift is lossless for values 0-255)
	for i := 0; i < ySize; i++ {
		diff := int(yuv[i]) - int(yuv2[i])
		if diff < -1 || diff > 1 {
			t.Errorf("Y[%d]: original=%d, round-trip=%d, diff=%d (want ±1)", i, yuv[i], yuv2[i], diff)
		}
	}

	// Chroma values go through upsample (duplicate) then downsample (average of identical values),
	// so they should also match within tolerance.
	for i := 0; i < cSize; i++ {
		diffCb := int(yuv[cbStart+i]) - int(yuv2[cbStart+i])
		if diffCb < -1 || diffCb > 1 {
			t.Errorf("Cb[%d]: original=%d, round-trip=%d, diff=%d (want ±1)", i, yuv[cbStart+i], yuv2[cbStart+i], diffCb)
		}
		diffCr := int(yuv[crStart+i]) - int(yuv2[crStart+i])
		if diffCr < -1 || diffCr > 1 {
			t.Errorf("Cr[%d]: original=%d, round-trip=%d, diff=%d (want ±1)", i, yuv[crStart+i], yuv2[crStart+i], diffCr)
		}
	}
}

func TestV210ToYUV420p_InvalidWidth(t *testing.T) {
	// Width not divisible by 6 should return an error
	_, err := V210ToYUV420p(make([]byte, 1024), 7, 2)
	if err == nil {
		t.Fatal("expected error for width not divisible by 6, got nil")
	}

	_, err = V210ToYUV420p(make([]byte, 1024), 1921, 2)
	if err == nil {
		t.Fatal("expected error for width 1921 (not divisible by 6), got nil")
	}
}

func TestV210ToYUV420p_InvalidHeight(t *testing.T) {
	// Odd height should return an error
	_, err := V210ToYUV420p(make([]byte, 1024), 6, 3)
	if err == nil {
		t.Fatal("expected error for odd height, got nil")
	}

	_, err = V210ToYUV420p(make([]byte, 1024), 6, 1)
	if err == nil {
		t.Fatal("expected error for height 1, got nil")
	}
}

func TestV210ToYUV420p_BufferTooSmall(t *testing.T) {
	// Buffer smaller than required for the given dimensions
	width, height := 6, 2
	stride := V210LineStride(width)
	requiredSize := stride * height

	// Provide a buffer that is too small
	_, err := V210ToYUV420p(make([]byte, requiredSize-1), width, height)
	if err == nil {
		t.Fatal("expected error for buffer too small, got nil")
	}

	// Empty buffer
	_, err = V210ToYUV420p(nil, width, height)
	if err == nil {
		t.Fatal("expected error for nil buffer, got nil")
	}
}

func TestV210ToYUV420p_1080p(t *testing.T) {
	// 1920x1080 frame to verify correct operation at real resolution.
	// Use zero-filled buffer and verify output dimensions.
	width, height := 1920, 1080
	stride := V210LineStride(width)
	v210 := make([]byte, stride*height)

	yuv, err := V210ToYUV420p(v210, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p() error: %v", err)
	}

	ySize := width * height
	cSize := (width / 2) * (height / 2)
	expectedSize := ySize + 2*cSize
	if len(yuv) != expectedSize {
		t.Fatalf("output size = %d, want %d", len(yuv), expectedSize)
	}

	// Zero V210 data means all 10-bit values are 0, so 8-bit values should also be 0
	for i := 0; i < 100; i++ {
		if yuv[i] != 0 {
			t.Errorf("Y[%d] = %d, want 0 for zero-filled input", i, yuv[i])
			break
		}
	}
}

func TestYUV420pToV210_InvalidWidth(t *testing.T) {
	_, err := YUV420pToV210(make([]byte, 1024), 7, 2)
	if err == nil {
		t.Fatal("expected error for width not divisible by 6, got nil")
	}
}

func TestYUV420pToV210_InvalidHeight(t *testing.T) {
	_, err := YUV420pToV210(make([]byte, 1024), 6, 3)
	if err == nil {
		t.Fatal("expected error for odd height, got nil")
	}
}

func TestYUV420pToV210_BufferTooSmall(t *testing.T) {
	width, height := 6, 2
	ySize := width * height
	cSize := (width / 2) * (height / 2)
	requiredSize := ySize + 2*cSize

	_, err := YUV420pToV210(make([]byte, requiredSize-1), width, height)
	if err == nil {
		t.Fatal("expected error for buffer too small, got nil")
	}
}

func TestPackV210Word(t *testing.T) {
	// Verify the test helper itself
	word := packV210Word(0x3FF, 0x000, 0x3FF)
	// bits [9:0] = 0x3FF, [19:10] = 0x000, [29:20] = 0x3FF
	expected := uint32(0x3FF) | (uint32(0x3FF) << 20)
	if word != expected {
		t.Errorf("packV210Word(0x3FF, 0x000, 0x3FF) = 0x%08X, want 0x%08X", word, expected)
	}

	word2 := packV210Word(0x040, 0x100, 0x200)
	expected2 := uint32(0x040) | (uint32(0x100) << 10) | (uint32(0x200) << 20)
	if word2 != expected2 {
		t.Errorf("packV210Word(0x040, 0x100, 0x200) = 0x%08X, want 0x%08X", word2, expected2)
	}
}
