package video

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

func TestUpconvert420to422_10_BitDepthWhite(t *testing.T) {
	w, h := 16, 8
	src := make([]byte, w*h*3/2)
	// Fill Y with 235 (white in limited range)
	for i := 0; i < w*h; i++ {
		src[i] = 235
	}
	dst := make([]byte, YUV422_10bit.FrameSize(w, h))
	err := Upconvert420to422_10(src, dst, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// 235 * 4 = 940 (limited range white in 10-bit)
	gotY := binary.LittleEndian.Uint16(dst[0:])
	if gotY != 940 {
		t.Errorf("white Y: got %d, want 940", gotY)
	}
}

func TestUpconvert420to422_10_BitDepthBlack(t *testing.T) {
	w, h := 16, 8
	src := make([]byte, w*h*3/2)
	// Fill Y with 16 (limited-range black)
	for i := 0; i < w*h; i++ {
		src[i] = 16
	}
	// Fill chroma with 128 (neutral)
	for i := w * h; i < len(src); i++ {
		src[i] = 128
	}
	dst := make([]byte, YUV422_10bit.FrameSize(w, h))
	err := Upconvert420to422_10(src, dst, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Y: 16 * 4 = 64
	gotY := binary.LittleEndian.Uint16(dst[0:])
	if gotY != 64 {
		t.Errorf("black Y: got %d, want 64", gotY)
	}

	// Chroma: 128 * 4 = 512
	ySize := YUV422_10bit.YPlaneSize(w, h)
	gotCb := binary.LittleEndian.Uint16(dst[ySize:])
	if gotCb != 512 {
		t.Errorf("neutral Cb: got %d, want 512", gotCb)
	}
}

func TestUpconvert420to422_10_AllYValues(t *testing.T) {
	w, h := 16, 8
	// Test several key 8-bit values map correctly via val*4
	testCases := []struct {
		val8  byte
		val10 uint16
	}{
		{0, 0},
		{1, 4},
		{16, 64},
		{128, 512},
		{235, 940},
		{255, 1020},
	}
	for _, tc := range testCases {
		src := make([]byte, w*h*3/2)
		for i := 0; i < w*h; i++ {
			src[i] = tc.val8
		}
		dst := make([]byte, YUV422_10bit.FrameSize(w, h))
		_ = Upconvert420to422_10(src, dst, w, h)
		got := binary.LittleEndian.Uint16(dst[0:])
		if got != tc.val10 {
			t.Errorf("val8=%d: got %d, want %d", tc.val8, got, tc.val10)
		}
	}
}

func TestUpconvert420to422_10_ChromaFullHeight(t *testing.T) {
	w, h := 16, 8
	src := make([]byte, w*h*3/2)
	// Set all chroma to 200
	for i := w * h; i < len(src); i++ {
		src[i] = 200
	}
	dst := make([]byte, YUV422_10bit.FrameSize(w, h))
	_ = Upconvert420to422_10(src, dst, w, h)

	// In 422 output, chroma has full height (h rows, not h/2)
	ySize := YUV422_10bit.YPlaneSize(w, h)
	chromaW := w / 2

	// Check first row of Cb plane
	gotFirst := binary.LittleEndian.Uint16(dst[ySize:])
	if gotFirst != 800 { // 200 * 4
		t.Errorf("first chroma row: Cb=%d, want 800", gotFirst)
	}

	// Check last row of Cb plane (row h-1)
	lastRowOffset := ySize + (h-1)*chromaW*2
	gotLast := binary.LittleEndian.Uint16(dst[lastRowOffset:])
	if gotLast != 800 { // uniform input = uniform output
		t.Errorf("last chroma row: Cb=%d, want 800", gotLast)
	}
}

func TestUpconvert420to422_10_ChromaInterpolation(t *testing.T) {
	// Test that odd chroma rows are interpolated from adjacent source rows.
	// Source is 420: chroma has h/2 rows. Destination 422: full h rows.
	w, h := 4, 4
	src := make([]byte, w*h*3/2) // Y=16, Cb/Cr data below

	// Fill Y with zeros
	for i := 0; i < w*h; i++ {
		src[i] = 0
	}

	// Cb plane in 420: 2 rows x 2 cols
	cbOff := w * h
	chromaW := w / 2
	// Row 0: Cb = 100, Row 1: Cb = 200
	for x := 0; x < chromaW; x++ {
		src[cbOff+0*chromaW+x] = 100
		src[cbOff+1*chromaW+x] = 200
	}

	// Cr plane in 420: same layout
	crOff := cbOff + chromaW*(h/2)
	for x := 0; x < chromaW; x++ {
		src[crOff+0*chromaW+x] = 50
		src[crOff+1*chromaW+x] = 150
	}

	dst := make([]byte, YUV422_10bit.FrameSize(w, h))
	_ = Upconvert420to422_10(src, dst, w, h)

	// Read the 4 Cb rows from 422 output
	ySize422 := YUV422_10bit.YPlaneSize(w, h) // w*h*2
	cbVals := make([]uint16, h*chromaW)
	for i := 0; i < h*chromaW; i++ {
		cbVals[i] = binary.LittleEndian.Uint16(dst[ySize422+i*2:])
	}

	// Row 0 (even, from source row 0): 100 * 4 = 400
	if cbVals[0] != 400 {
		t.Errorf("Cb row 0: got %d, want 400", cbVals[0])
	}

	// Row 1 (odd, interpolated between source row 0=100 and row 1=200):
	// avg = (100 + 200 + 1) >> 1 = 150; 150 * 4 = 600
	if cbVals[chromaW] != 600 {
		t.Errorf("Cb row 1 (interpolated): got %d, want 600", cbVals[chromaW])
	}

	// Row 2 (even, from source row 1): 200 * 4 = 800
	if cbVals[2*chromaW] != 800 {
		t.Errorf("Cb row 2: got %d, want 800", cbVals[2*chromaW])
	}

	// Row 3 (odd, clamp: interpolated between source row 1 and row 1): 200 * 4 = 800
	if cbVals[3*chromaW] != 800 {
		t.Errorf("Cb row 3 (clamped): got %d, want 800", cbVals[3*chromaW])
	}

	// Check Cr similarly
	crStart422 := ySize422 + YUV422_10bit.ChromaPlaneSize(w, h)
	crRow0 := binary.LittleEndian.Uint16(dst[crStart422:])
	if crRow0 != 200 { // 50 * 4
		t.Errorf("Cr row 0: got %d, want 200", crRow0)
	}
	crRow1 := binary.LittleEndian.Uint16(dst[crStart422+chromaW*2:])
	// avg = (50 + 150 + 1) >> 1 = 100; 100 * 4 = 400
	if crRow1 != 400 {
		t.Errorf("Cr row 1 (interpolated): got %d, want 400", crRow1)
	}
}

func TestUpconvert420to422_10_OutputSize(t *testing.T) {
	w, h := 1920, 1080
	srcSize := YUV420_8bit.FrameSize(w, h)
	dstSize := YUV422_10bit.FrameSize(w, h)

	src := make([]byte, srcSize)
	dst := make([]byte, dstSize)
	err := Upconvert420to422_10(src, dst, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// YUV420 8-bit: w*h*3/2 = 3110400
	if srcSize != 1920*1080*3/2 {
		t.Errorf("420 size: got %d, want %d", srcSize, 1920*1080*3/2)
	}
	// YUV422 10-bit: w*h*2 (Y) + w/2*h*2 (Cb) + w/2*h*2 (Cr) = w*h*4
	if dstSize != 1920*1080*4 {
		t.Errorf("422 size: got %d, want %d", dstSize, 1920*1080*4)
	}
}

func TestDownconvert422_10to420_BitDepth(t *testing.T) {
	w, h := 16, 8
	// Create a 422 10-bit frame with known values
	src := make([]byte, YUV422_10bit.FrameSize(w, h))

	// Fill Y with 940 (10-bit white)
	ySize := YUV422_10bit.YPlaneSize(w, h)
	for i := 0; i < ySize; i += 2 {
		binary.LittleEndian.PutUint16(src[i:], 940)
	}

	// Fill chroma with 512 (10-bit neutral)
	for i := ySize; i < len(src); i += 2 {
		binary.LittleEndian.PutUint16(src[i:], 512)
	}

	dst := make([]byte, YUV420_8bit.FrameSize(w, h))
	err := Downconvert422_10to420(src, dst, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Y: (940 + 2) >> 2 = 235
	if dst[0] != 235 {
		t.Errorf("white Y: got %d, want 235", dst[0])
	}

	// Cb: (512 + 2) >> 2 = 128 (with rounding, depends on path)
	cbOff := w * h
	if dst[cbOff] != 128 {
		t.Errorf("neutral Cb: got %d, want 128", dst[cbOff])
	}
}

func TestDownconvert422_10to420_PeakClamp(t *testing.T) {
	// Peak 10-bit samples (1022-1023) round to >=256 via (val+2)>>2.
	// Without clamping, byte() truncation wraps them to 0 (black). Verify
	// both the Y plane and the chroma plane saturate to 255 instead.
	w, h := 16, 8
	chromaW := w / 2

	testCases := []struct {
		val10 uint16
		want8 byte
	}{
		{1020, 255}, // (1020+2)>>2 = 255
		{1021, 255}, // (1021+2)>>2 = 255 (without clamp: 255, but lock it)
		{1022, 255}, // (1022+2)>>2 = 256 -> byte wraps to 0 without clamp
		{1023, 255}, // (1023+2)>>2 = 256 -> byte wraps to 0 without clamp
	}

	for _, tc := range testCases {
		src := make([]byte, YUV422_10bit.FrameSize(w, h))
		ySize := YUV422_10bit.YPlaneSize(w, h)
		// Fill Y with the peak value.
		for i := 0; i < ySize; i += 2 {
			binary.LittleEndian.PutUint16(src[i:], tc.val10)
		}
		// Fill chroma with the peak value too (exercises the averaged path:
		// avg(peak, peak) == peak).
		for i := ySize; i < len(src); i += 2 {
			binary.LittleEndian.PutUint16(src[i:], tc.val10)
		}

		dst := make([]byte, YUV420_8bit.FrameSize(w, h))
		if err := Downconvert422_10to420(src, dst, w, h); err != nil {
			t.Fatalf("val10=%d: %v", tc.val10, err)
		}

		if dst[0] != tc.want8 {
			t.Errorf("Y peak val10=%d: got %d, want %d", tc.val10, dst[0], tc.want8)
		}
		cbOff := w * h
		if dst[cbOff] != tc.want8 {
			t.Errorf("Cb peak val10=%d: got %d, want %d", tc.val10, dst[cbOff], tc.want8)
		}
		crOff := cbOff + chromaW*(h/2)
		if dst[crOff] != tc.want8 {
			t.Errorf("Cr peak val10=%d: got %d, want %d", tc.val10, dst[crOff], tc.want8)
		}
	}
}

func TestDownconvert422_10to420_ChromaSubsample(t *testing.T) {
	// Verify that 422 chroma rows get averaged into 420 (half height)
	w, h := 4, 4
	src := make([]byte, YUV422_10bit.FrameSize(w, h))
	chromaW := w / 2

	// Fill Y with zeros
	ySize := YUV422_10bit.YPlaneSize(w, h)

	// Cb plane in 422: 4 rows of chromaW samples (each 2 bytes LE)
	cbOff := ySize
	// Row 0: 400 (maps to 100 in 8-bit)
	// Row 1: 800 (maps to 200 in 8-bit)
	// Row 2: 400
	// Row 3: 800
	for x := 0; x < chromaW; x++ {
		binary.LittleEndian.PutUint16(src[cbOff+(0*chromaW+x)*2:], 400)
		binary.LittleEndian.PutUint16(src[cbOff+(1*chromaW+x)*2:], 800)
		binary.LittleEndian.PutUint16(src[cbOff+(2*chromaW+x)*2:], 400)
		binary.LittleEndian.PutUint16(src[cbOff+(3*chromaW+x)*2:], 800)
	}

	dst := make([]byte, YUV420_8bit.FrameSize(w, h))
	_ = Downconvert422_10to420(src, dst, w, h)

	// 420 Cb has 2 rows. Row 0 = avg(400, 800) = 600 -> (600+2)>>2 = 150
	// Row 1 = avg(400, 800) = 600 -> 150
	cbDst := w * h
	if dst[cbDst] != 150 {
		t.Errorf("Cb row 0: got %d, want 150", dst[cbDst])
	}
	if dst[cbDst+chromaW] != 150 {
		t.Errorf("Cb row 1: got %d, want 150", dst[cbDst+chromaW])
	}
}

func TestDownconvert422_10to420_RoundTrip(t *testing.T) {
	w, h := 16, 8
	// Create a uniform 420 8-bit frame
	original := make([]byte, YUV420_8bit.FrameSize(w, h))
	for i := 0; i < w*h; i++ {
		original[i] = 180
	}
	for i := w * h; i < len(original); i++ {
		original[i] = 100
	}

	// Upconvert to 422 10-bit
	intermediate := make([]byte, YUV422_10bit.FrameSize(w, h))
	err := Upconvert420to422_10(original, intermediate, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Downconvert back to 420 8-bit
	result := make([]byte, YUV420_8bit.FrameSize(w, h))
	err = Downconvert422_10to420(intermediate, result, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Should be identical for uniform content
	if !bytes.Equal(original, result) {
		t.Error("round-trip should be lossless for uniform content")
		t.Logf("original Y[0]=%d, result Y[0]=%d", original[0], result[0])
		t.Logf("original Cb[0]=%d, result Cb[0]=%d", original[w*h], result[w*h])
	}
}

func TestDownconvert422_10to420_RoundTripNonUniform(t *testing.T) {
	w, h := 16, 8
	rng := rand.New(rand.NewSource(42))

	// Create non-uniform 420 8-bit with vertically uniform chroma
	// (uniform within each pair of rows that get averaged)
	original := make([]byte, YUV420_8bit.FrameSize(w, h))
	for i := 0; i < w*h; i++ {
		original[i] = byte(rng.Intn(256))
	}
	chromaW := w / 2
	for row := 0; row < h/2; row++ {
		for x := 0; x < chromaW; x++ {
			original[w*h+row*chromaW+x] = byte(rng.Intn(256))
		}
	}
	// Cr plane
	crOff := w*h + chromaW*(h/2)
	for row := 0; row < h/2; row++ {
		for x := 0; x < chromaW; x++ {
			original[crOff+row*chromaW+x] = byte(rng.Intn(256))
		}
	}

	intermediate := make([]byte, YUV422_10bit.FrameSize(w, h))
	_ = Upconvert420to422_10(original, intermediate, w, h)
	result := make([]byte, YUV420_8bit.FrameSize(w, h))
	_ = Downconvert422_10to420(intermediate, result, w, h)

	// Y plane should round-trip exactly: val*4 -> (val*4+2)>>2 = val
	for i := 0; i < w*h; i++ {
		if result[i] != original[i] {
			t.Errorf("Y[%d]: got %d, want %d", i, result[i], original[i])
			break
		}
	}

	// Chroma round-trip is lossy for non-uniform content because:
	// - Upconvert interpolates odd rows: odd[r] = avg(src[r/2], src[r/2+1])
	// - Downconvert averages row pairs: dst[r] = avg(even[2r], odd[2r+1])
	//   = avg(src[r], avg(src[r], src[r+1])) = (3*src[r] + src[r+1]) / 4
	// So the error is bounded by |src[r] - src[r+1]| / 4 + rounding.
	// For random data (max adjacent diff ~255), worst case is ~64.
	// We just verify Y is exact and chroma error stays reasonable.
	maxErr := 0
	for i := w * h; i < len(original); i++ {
		diff := int(result[i]) - int(original[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxErr {
			maxErr = diff
		}
	}
	// Theoretical max: ceil(255/4) + 1 rounding = 65
	if maxErr > 65 {
		t.Errorf("chroma round-trip max error: %d (want <= 65)", maxErr)
	}
	t.Logf("chroma round-trip max error: %d (non-uniform content, expected lossy)", maxErr)
}

func TestUpconvert420to422_10_Validation(t *testing.T) {
	tests := []struct {
		name    string
		src     []byte
		dst     []byte
		w, h    int
		wantErr bool
	}{
		{
			name:    "odd width",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       15,
			h:       8,
			wantErr: true,
		},
		{
			name:    "odd height",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       16,
			h:       7,
			wantErr: true,
		},
		{
			name:    "zero width",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       0,
			h:       8,
			wantErr: true,
		},
		{
			name:    "zero height",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       16,
			h:       0,
			wantErr: true,
		},
		{
			name:    "negative width",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       -2,
			h:       8,
			wantErr: true,
		},
		{
			name:    "negative height",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       16,
			h:       -4,
			wantErr: true,
		},
		{
			name:    "src too small",
			src:     make([]byte, 10),
			dst:     make([]byte, YUV422_10bit.FrameSize(16, 8)),
			w:       16,
			h:       8,
			wantErr: true,
		},
		{
			name:    "dst too small",
			src:     make([]byte, YUV420_8bit.FrameSize(16, 8)),
			dst:     make([]byte, 10),
			w:       16,
			h:       8,
			wantErr: true,
		},
		{
			name:    "nil src",
			src:     nil,
			dst:     make([]byte, 100),
			w:       16,
			h:       8,
			wantErr: true,
		},
		{
			name:    "nil dst",
			src:     make([]byte, 100),
			dst:     nil,
			w:       16,
			h:       8,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Upconvert420to422_10(tc.src, tc.dst, tc.w, tc.h)
			if (err != nil) != tc.wantErr {
				t.Errorf("Upconvert420to422_10() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDownconvert422_10to420_Validation(t *testing.T) {
	tests := []struct {
		name    string
		src     []byte
		dst     []byte
		w, h    int
		wantErr bool
	}{
		{
			name:    "odd width",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       15,
			h:       8,
			wantErr: true,
		},
		{
			name:    "odd height",
			src:     make([]byte, 100),
			dst:     make([]byte, 100),
			w:       16,
			h:       7,
			wantErr: true,
		},
		{
			name:    "src too small",
			src:     make([]byte, 10),
			dst:     make([]byte, YUV420_8bit.FrameSize(16, 8)),
			w:       16,
			h:       8,
			wantErr: true,
		},
		{
			name:    "dst too small",
			src:     make([]byte, YUV422_10bit.FrameSize(16, 8)),
			dst:     make([]byte, 10),
			w:       16,
			h:       8,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Downconvert422_10to420(tc.src, tc.dst, tc.w, tc.h)
			if (err != nil) != tc.wantErr {
				t.Errorf("Downconvert422_10to420() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConvertParams(t *testing.T) {
	tests := []struct {
		name    string
		w, h    int
		wantErr bool
	}{
		{"valid", 16, 8, false},
		{"valid 1080p", 1920, 1080, false},
		{"odd width", 15, 8, true},
		{"odd height", 16, 7, true},
		{"zero width", 0, 8, true},
		{"zero height", 16, 0, true},
		{"negative width", -2, 8, true},
		{"negative height", 16, -4, true},
		{"both odd", 15, 7, true},
		{"both zero", 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConvertParams(tc.w, tc.h)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateConvertParams(%d, %d) error = %v, wantErr %v", tc.w, tc.h, err, tc.wantErr)
			}
		})
	}
}

func BenchmarkUpconvert420to422_10_1080p(b *testing.B) {
	w, h := 1920, 1080
	src := make([]byte, YUV420_8bit.FrameSize(w, h))
	dst := make([]byte, YUV422_10bit.FrameSize(w, h))
	// Fill with realistic values
	for i := range src {
		src[i] = byte(i & 0xFF)
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Upconvert420to422_10(src, dst, w, h)
	}
}

func BenchmarkDownconvert422_10to420_1080p(b *testing.B) {
	w, h := 1920, 1080
	src := make([]byte, YUV422_10bit.FrameSize(w, h))
	dst := make([]byte, YUV420_8bit.FrameSize(w, h))
	for i := 0; i < len(src)-1; i += 2 {
		binary.LittleEndian.PutUint16(src[i:], uint16(i&0x3FF))
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Downconvert422_10to420(src, dst, w, h)
	}
}
