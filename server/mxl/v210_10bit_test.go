package mxl

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// makeYUV422P10Frame creates a YUV422P10LE frame filled with uniform values.
// Layout: Y plane (w*h uint16), Cb plane (w/2*h uint16), Cr plane (w/2*h uint16).
func makeYUV422P10Frame(w, h int, yVal, cbVal, crVal uint16) []byte {
	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	totalSamples := ySize + cbSize + crSize
	buf := make([]byte, totalSamples*2)

	// Y plane
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], yVal)
	}

	// Cb plane
	cbOffset := ySize * 2
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOffset+i*2:], cbVal)
	}

	// Cr plane
	crOffset := cbOffset + cbSize*2
	for i := 0; i < crSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOffset+i*2:], crVal)
	}

	return buf
}

// makeYUV422P10FrameGradient creates a YUV422P10LE frame with per-sample gradients.
func makeYUV422P10FrameGradient(w, h int) []byte {
	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	totalSamples := ySize + cbSize + crSize
	buf := make([]byte, totalSamples*2)

	// Y plane: gradient 0-1023
	for i := 0; i < ySize; i++ {
		val := uint16(i % 1024)
		binary.LittleEndian.PutUint16(buf[i*2:], val)
	}

	// Cb plane: gradient starting at 100
	cbOffset := ySize * 2
	for i := 0; i < cbSize; i++ {
		val := uint16((100 + i*7) % 1024)
		binary.LittleEndian.PutUint16(buf[cbOffset+i*2:], val)
	}

	// Cr plane: gradient starting at 500
	crOffset := cbOffset + cbSize*2
	for i := 0; i < crSize; i++ {
		val := uint16((500 + i*11) % 1024)
		binary.LittleEndian.PutUint16(buf[crOffset+i*2:], val)
	}

	return buf
}

func TestV210ToYUV422P10_RoundTrip(t *testing.T) {
	// Create a YUV422P10 frame, convert to V210, convert back, verify lossless.
	w, h := 48, 8 // width must be multiple of 6 for V210
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	// YUV422P10 -> V210
	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// V210 -> YUV422P10
	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Should be identical (lossless round-trip)
	if !bytes.Equal(yuv, result) {
		// Find the first mismatch
		for i := 0; i < len(yuv) && i < len(result); i += 2 {
			origVal := binary.LittleEndian.Uint16(yuv[i:])
			gotVal := binary.LittleEndian.Uint16(result[i:])
			if origVal != gotVal {
				t.Errorf("first mismatch at byte offset %d (sample %d): orig=%d, got=%d", i, i/2, origVal, gotVal)
				break
			}
		}
		t.Fatal("round-trip is not lossless")
	}
}

func TestV210ToYUV422P10_RoundTripGradient(t *testing.T) {
	// Gradient values exercise all 10-bit values to verify no truncation.
	w, h := 48, 8
	yuv := makeYUV422P10FrameGradient(w, h)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		mismatches := 0
		for i := 0; i < len(yuv) && i < len(result); i += 2 {
			origVal := binary.LittleEndian.Uint16(yuv[i:])
			gotVal := binary.LittleEndian.Uint16(result[i:])
			if origVal != gotVal {
				if mismatches < 5 {
					t.Errorf("mismatch at byte offset %d (sample %d): orig=%d, got=%d", i, i/2, origVal, gotVal)
				}
				mismatches++
			}
		}
		t.Fatalf("round-trip is not lossless: %d mismatches", mismatches)
	}
}

func TestV210ToYUV422P10_KnownValues(t *testing.T) {
	// Construct a V210 frame with known values and verify extraction.
	// 6 pixels = 4 words = minimum V210 group.
	w, h := 6, 1
	stride := V210LineStride(w)
	v210 := make([]byte, stride*h)

	// Pack known values into V210 words using the actual packing order:
	// Word 0: bits[9:0]=Cb0, bits[19:10]=Y0, bits[29:20]=Cr0
	// Word 1: bits[9:0]=Y1,  bits[19:10]=Cb1, bits[29:20]=Y2
	// Word 2: bits[9:0]=Cr1, bits[19:10]=Y3,  bits[29:20]=Cb2
	// Word 3: bits[9:0]=Y4,  bits[19:10]=Cr2, bits[29:20]=Y5

	// Y:  100, 200, 300, 400, 500, 600
	// Cb: 150, 250, 350
	// Cr: 450, 550, 650
	binary.LittleEndian.PutUint32(v210[0:], packV210Word(150, 100, 450))   // Cb0=150, Y0=100, Cr0=450
	binary.LittleEndian.PutUint32(v210[4:], packV210Word(200, 250, 300))   // Y1=200, Cb1=250, Y2=300
	binary.LittleEndian.PutUint32(v210[8:], packV210Word(550, 400, 350))   // Cr1=550, Y3=400, Cb2=350
	binary.LittleEndian.PutUint32(v210[12:], packV210Word(500, 650, 600))  // Y4=500, Cr2=650, Y5=600

	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// YUV422P10 layout:
	// Y plane:  w*h uint16 (offset 0)
	// Cb plane: w/2*h uint16 (offset w*h*2)
	// Cr plane: w/2*h uint16 (offset w*h*2 + w/2*h*2)

	yOffset := 0
	cbOffset := w * h * 2
	crOffset := cbOffset + (w/2)*h*2

	// Verify Y samples
	expectedY := []uint16{100, 200, 300, 400, 500, 600}
	for i, want := range expectedY {
		got := binary.LittleEndian.Uint16(result[yOffset+i*2:])
		if got != want {
			t.Errorf("Y[%d]: got %d, want %d", i, got, want)
		}
	}

	// Verify Cb samples
	expectedCb := []uint16{150, 250, 350}
	for i, want := range expectedCb {
		got := binary.LittleEndian.Uint16(result[cbOffset+i*2:])
		if got != want {
			t.Errorf("Cb[%d]: got %d, want %d", i, got, want)
		}
	}

	// Verify Cr samples
	expectedCr := []uint16{450, 550, 650}
	for i, want := range expectedCr {
		got := binary.LittleEndian.Uint16(result[crOffset+i*2:])
		if got != want {
			t.Errorf("Cr[%d]: got %d, want %d", i, got, want)
		}
	}
}

func TestV210ToYUV422P10_UniformBlack(t *testing.T) {
	// V210 black: Y=64 (limited range 10-bit), Cb=Cr=512
	w, h := 48, 8
	yuv := makeYUV422P10Frame(w, h, 64, 512, 512)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		t.Fatal("black frame round-trip is not lossless")
	}
}

func TestV210ToYUV422P10_UniformWhite(t *testing.T) {
	// V210 white: Y=940 (limited range 10-bit), Cb=Cr=512
	w, h := 48, 8
	yuv := makeYUV422P10Frame(w, h, 940, 512, 512)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		t.Fatal("white frame round-trip is not lossless")
	}
}

func TestV210ToYUV422P10_WidthValidation(t *testing.T) {
	// Width must be even for YUV422
	_, err := V210ToYUV422P10(nil, 0, 7, 1)
	if err == nil {
		t.Fatal("should reject odd width")
	}

	// Width 0
	_, err = V210ToYUV422P10(nil, 0, 0, 1)
	if err == nil {
		t.Fatal("should reject zero width")
	}
}

func TestV210ToYUV422P10_HeightValidation(t *testing.T) {
	_, err := V210ToYUV422P10(nil, 0, 6, 0)
	if err == nil {
		t.Fatal("should reject zero height")
	}
}

func TestV210ToYUV422P10_BufferTooSmall(t *testing.T) {
	w, h := 6, 2
	stride := V210LineStride(w)
	requiredSize := stride * h

	_, err := V210ToYUV422P10(make([]byte, requiredSize-1), stride, w, h)
	if err == nil {
		t.Fatal("should reject buffer too small")
	}
}

func TestYUV422P10ToV210_WidthValidation(t *testing.T) {
	// Odd width
	_, err := YUV422P10ToV210(nil, 7, 1)
	if err == nil {
		t.Fatal("should reject odd width")
	}

	// Width not divisible by 6 (required for V210)
	_, err = YUV422P10ToV210(make([]byte, 10000), 8, 1)
	if err == nil {
		t.Fatal("should reject width not divisible by 6")
	}
}

func TestYUV422P10ToV210_HeightValidation(t *testing.T) {
	_, err := YUV422P10ToV210(nil, 6, 0)
	if err == nil {
		t.Fatal("should reject zero height")
	}
}

func TestYUV422P10ToV210_BufferTooSmall(t *testing.T) {
	w, h := 6, 2
	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	requiredSize := (ySize + cbSize + crSize) * 2

	_, err := YUV422P10ToV210(make([]byte, requiredSize-1), w, h)
	if err == nil {
		t.Fatal("should reject buffer too small")
	}
}

func TestYUV422P10ToV210_BufferSize(t *testing.T) {
	w, h := 48, 8
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	expectedSize := V210LineStride(w) * h
	if len(v210) != expectedSize {
		t.Errorf("V210 size: got %d, want %d", len(v210), expectedSize)
	}
}

func TestV210ToYUV422P10_OutputSize(t *testing.T) {
	w, h := 48, 8
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	expectedSize := (ySize + cbSize + crSize) * 2
	if len(result) != expectedSize {
		t.Errorf("YUV422P10 size: got %d, want %d", len(result), expectedSize)
	}
}

func TestV210ToYUV422P10_ChromaFullHeight(t *testing.T) {
	// Unlike 4:2:0, 4:2:2 preserves chroma at full height.
	// Verify the last row of chroma is correct.
	w, h := 48, 4
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Check Cb at last row (row 3)
	ySize := w * h * 2
	chromaW := w / 2
	lastRowCbOffset := ySize + (3*chromaW)*2
	gotCb := binary.LittleEndian.Uint16(result[lastRowCbOffset:])
	if gotCb != 300 {
		t.Errorf("chroma last row: Cb=%d, want 300", gotCb)
	}

	// Check Cr at last row (row 3)
	crPlaneOffset := ySize + chromaW*h*2
	lastRowCrOffset := crPlaneOffset + (3*chromaW)*2
	gotCr := binary.LittleEndian.Uint16(result[lastRowCrOffset:])
	if gotCr != 700 {
		t.Errorf("chroma last row: Cr=%d, want 700", gotCr)
	}
}

func TestV210ToYUV422P10_OddHeight(t *testing.T) {
	// Unlike the 4:2:0 path, 4:2:2 should work with odd heights.
	w, h := 6, 3
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		t.Fatal("odd-height round-trip is not lossless")
	}
}

func TestV210ToYUV422P10_1080p(t *testing.T) {
	// Verify it works at real resolution (dimensions only, no value check).
	w, h := 1920, 1080
	stride := V210LineStride(w)
	v210 := make([]byte, stride*h)

	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	expectedSize := (ySize + cbSize + crSize) * 2
	if len(result) != expectedSize {
		t.Errorf("output size: got %d, want %d", len(result), expectedSize)
	}
}

func TestV210ToYUV422P10_MaxValues(t *testing.T) {
	// All 10-bit values at maximum (1023) to test no overflow.
	w, h := 6, 1
	yuv := makeYUV422P10Frame(w, h, 1023, 1023, 1023)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		t.Fatal("max-value round-trip is not lossless")
	}
}

func TestV210ToYUV422P10_ZeroValues(t *testing.T) {
	// All 10-bit values at 0.
	w, h := 6, 1
	yuv := makeYUV422P10Frame(w, h, 0, 0, 0)

	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		t.Fatal("zero-value round-trip is not lossless")
	}
}

func TestYUV422P10ToV210_MatchesBuildV210Frame(t *testing.T) {
	// Verify YUV422P10ToV210 produces the same V210 as the test helper buildV210Frame.
	w, h := 6, 2
	numPixels := w * h
	chromaW := w / 2
	numChroma := chromaW * h

	// Values to use
	y10 := make([]uint32, numPixels)
	cb10 := make([]uint32, numChroma)
	cr10 := make([]uint32, numChroma)
	for i := range y10 {
		y10[i] = uint32(64 + (i*73)%960)
	}
	for i := range cb10 {
		cb10[i] = uint32(100 + (i*47)%900)
	}
	for i := range cr10 {
		cr10[i] = uint32(200 + (i*59)%800)
	}

	// Build V210 using test helper
	expected := buildV210Frame(t, w, h, y10, cb10, cr10)

	// Build YUV422P10 frame with same values
	yuv := make([]byte, (numPixels+numChroma*2)*2)
	for i := 0; i < numPixels; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(y10[i]))
	}
	cbOff := numPixels * 2
	for i := 0; i < numChroma; i++ {
		binary.LittleEndian.PutUint16(yuv[cbOff+i*2:], uint16(cb10[i]))
	}
	crOff := cbOff + numChroma*2
	for i := 0; i < numChroma; i++ {
		binary.LittleEndian.PutUint16(yuv[crOff+i*2:], uint16(cr10[i]))
	}

	// Convert
	got, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Compare only the active data portion (not stride padding)
	stride := V210LineStride(w)
	groups := w / 6
	activeBytes := groups * 16
	for row := 0; row < h; row++ {
		rowOff := row * stride
		for i := 0; i < activeBytes; i++ {
			if got[rowOff+i] != expected[rowOff+i] {
				t.Fatalf("row %d byte %d: got 0x%02X, want 0x%02X", row, i, got[rowOff+i], expected[rowOff+i])
			}
		}
	}
}

func BenchmarkV210ToYUV422P10_1080p(b *testing.B) {
	w, h := 1920, 1080
	stride := V210LineStride(w)
	v210 := make([]byte, stride*h)
	for i := range v210 {
		v210[i] = byte(i % 256)
	}

	b.SetBytes(int64(len(v210)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = V210ToYUV422P10(v210, stride, w, h)
	}
}

func BenchmarkYUV422P10ToV210_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	b.SetBytes(int64(len(yuv)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = YUV422P10ToV210(yuv, w, h)
	}
}

func BenchmarkV210_10bit_RoundTrip_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := makeYUV422P10FrameGradient(w, h)

	b.SetBytes(int64(len(yuv)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v210, _ := YUV422P10ToV210(yuv, w, h)
		stride := V210LineStride(w)
		_, _ = V210ToYUV422P10(v210, stride, w, h)
	}
}
