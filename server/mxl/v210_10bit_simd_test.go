package mxl

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// ============================================================================
// Tests for SIMD-dispatched V210 <-> YUV422P10 lossless conversion.
// These verify bit-exact match against the Go reference implementation.
// ============================================================================

func TestV210ToYUV422P10_SIMD_MatchesReference(t *testing.T) {
	w, h := 48, 8 // must be multiple of 6 for V210
	rng := rand.New(rand.NewSource(42))

	// Create random YUV422P10 frame (values 0-1023)
	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	totalSamples := ySize + cbSize + crSize
	yuv := make([]byte, totalSamples*2)
	for i := 0; i < totalSamples; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	// Pack to V210 using Go reference
	v210, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)

	// Go reference unpack
	ref, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// SIMD dispatch unpack
	got, err := v210ToYUV422P10SIMD(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		// Find first mismatch
		for i := 0; i < len(ref) && i < len(got); i += 2 {
			refVal := binary.LittleEndian.Uint16(ref[i:])
			gotVal := binary.LittleEndian.Uint16(got[i:])
			if refVal != gotVal {
				t.Fatalf("mismatch at byte %d (sample %d): ref=%d, got=%d", i, i/2, refVal, gotVal)
			}
		}
		t.Fatal("SIMD V210 unpack doesn't match Go reference")
	}
}

func TestYUV422P10ToV210_SIMD_MatchesReference(t *testing.T) {
	w, h := 48, 8
	rng := rand.New(rand.NewSource(42))

	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	totalSamples := ySize + cbSize + crSize
	yuv := make([]byte, totalSamples*2)
	for i := 0; i < totalSamples; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	ref, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	got, err := yuv422P10ToV210SIMD(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		// Find first mismatch
		stride := V210LineStride(w)
		for row := 0; row < h; row++ {
			for i := 0; i < stride; i++ {
				if ref[row*stride+i] != got[row*stride+i] {
					t.Fatalf("mismatch at row %d, byte %d: ref=0x%02X, got=0x%02X",
						row, i, ref[row*stride+i], got[row*stride+i])
				}
			}
		}
		t.Fatal("SIMD V210 pack doesn't match Go reference")
	}
}

func TestV210_SIMD_RoundTrip_Lossless(t *testing.T) {
	w, h := 48, 8
	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	totalSamples := ySize + cbSize + crSize

	original := make([]byte, totalSamples*2)
	for i := 0; i < totalSamples; i++ {
		binary.LittleEndian.PutUint16(original[i*2:], uint16(i%1024))
	}

	v210, err := yuv422P10ToV210SIMD(original, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := v210ToYUV422P10SIMD(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(original, result) {
		// Find first mismatch
		for i := 0; i < len(original) && i < len(result); i += 2 {
			origVal := binary.LittleEndian.Uint16(original[i:])
			gotVal := binary.LittleEndian.Uint16(result[i:])
			if origVal != gotVal {
				t.Fatalf("round-trip mismatch at byte %d (sample %d): orig=%d, got=%d", i, i/2, origVal, gotVal)
			}
		}
		t.Fatal("SIMD round-trip not lossless")
	}
}

func TestV210_SIMD_RoundTrip_AllValues(t *testing.T) {
	// Exhaustively test all 1024 possible 10-bit values survive round-trip.
	// Use a 6x1024 frame: each row has 6 pixels, 1024 rows cover all values in Y.
	w, h := 6, 1024
	chromaW := w / 2
	ySize := w * h
	cbSize := chromaW * h
	crSize := chromaW * h
	totalSamples := ySize + cbSize + crSize

	yuv := make([]byte, totalSamples*2)
	// Fill Y plane with all values 0-1023 repeating
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(i%1024))
	}
	// Fill Cb plane with all values 0-1023 repeating
	cbOff := ySize * 2
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(yuv[cbOff+i*2:], uint16(i%1024))
	}
	// Fill Cr plane with all values 0-1023 repeating
	crOff := cbOff + cbSize*2
	for i := 0; i < crSize; i++ {
		binary.LittleEndian.PutUint16(yuv[crOff+i*2:], uint16(i%1024))
	}

	v210, err := yuv422P10ToV210SIMD(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	stride := V210LineStride(w)
	result, err := v210ToYUV422P10SIMD(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(yuv, result) {
		mismatches := 0
		for i := 0; i < len(yuv) && i < len(result); i += 2 {
			origVal := binary.LittleEndian.Uint16(yuv[i:])
			gotVal := binary.LittleEndian.Uint16(result[i:])
			if origVal != gotVal {
				if mismatches < 10 {
					t.Errorf("mismatch at sample %d: orig=%d, got=%d", i/2, origVal, gotVal)
				}
				mismatches++
			}
		}
		t.Fatalf("SIMD round-trip all-values not lossless: %d mismatches", mismatches)
	}
}

func TestV210_SIMD_Unpack_1080p(t *testing.T) {
	// Full HD resolution, verify SIMD matches reference.
	w, h := 1920, 1080
	stride := V210LineStride(w)
	rng := rand.New(rand.NewSource(99))

	// Create random V210 data (only active region, padding can be anything)
	v210 := make([]byte, stride*h)
	for i := range v210 {
		v210[i] = byte(rng.Intn(256))
	}

	ref, err := V210ToYUV422P10(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	got, err := v210ToYUV422P10SIMD(v210, stride, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		t.Fatal("1080p SIMD unpack doesn't match Go reference")
	}
}

func TestV210_SIMD_Pack_1080p(t *testing.T) {
	// Full HD resolution, verify SIMD matches reference.
	w, h := 1920, 1080
	yuv := makeYUV422P10FrameGradient(w, h)

	ref, err := YUV422P10ToV210(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	got, err := yuv422P10ToV210SIMD(yuv, w, h)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		t.Fatal("1080p SIMD pack doesn't match Go reference")
	}
}

func TestV210_SIMD_Unpack_SmallWidths(t *testing.T) {
	// Test various widths that are multiples of 6.
	widths := []int{6, 12, 18, 24, 30, 36, 42, 48, 96, 192}
	for _, w := range widths {
		h := 4
		yuv := makeYUV422P10FrameGradient(w, h)

		v210, err := YUV422P10ToV210(yuv, w, h)
		if err != nil {
			t.Fatalf("w=%d: pack error: %v", w, err)
		}

		stride := V210LineStride(w)
		ref, err := V210ToYUV422P10(v210, stride, w, h)
		if err != nil {
			t.Fatalf("w=%d: ref unpack error: %v", w, err)
		}

		got, err := v210ToYUV422P10SIMD(v210, stride, w, h)
		if err != nil {
			t.Fatalf("w=%d: simd unpack error: %v", w, err)
		}

		if !bytes.Equal(ref, got) {
			t.Fatalf("w=%d: SIMD unpack doesn't match Go reference", w)
		}
	}
}

func TestV210_SIMD_Pack_SmallWidths(t *testing.T) {
	widths := []int{6, 12, 18, 24, 30, 36, 42, 48, 96, 192}
	for _, w := range widths {
		h := 4
		yuv := makeYUV422P10FrameGradient(w, h)

		ref, err := YUV422P10ToV210(yuv, w, h)
		if err != nil {
			t.Fatalf("w=%d: ref pack error: %v", w, err)
		}

		got, err := yuv422P10ToV210SIMD(yuv, w, h)
		if err != nil {
			t.Fatalf("w=%d: simd pack error: %v", w, err)
		}

		if !bytes.Equal(ref, got) {
			t.Fatalf("w=%d: SIMD pack doesn't match Go reference", w)
		}
	}
}

func TestV210_SIMD_Unpack_MaxMinValues(t *testing.T) {
	// All max (1023) and all min (0)
	for _, val := range []uint16{0, 1023} {
		w, h := 48, 4
		yuv := makeYUV422P10Frame(w, h, val, val, val)

		v210, err := YUV422P10ToV210(yuv, w, h)
		if err != nil {
			t.Fatal(err)
		}

		stride := V210LineStride(w)
		ref, err := V210ToYUV422P10(v210, stride, w, h)
		if err != nil {
			t.Fatal(err)
		}

		got, err := v210ToYUV422P10SIMD(v210, stride, w, h)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(ref, got) {
			t.Fatalf("val=%d: SIMD unpack doesn't match Go reference", val)
		}
	}
}

// ============================================================================
// Row-level kernel tests (for the assembly functions directly)
// ============================================================================

func TestV210UnpackRow10_SingleGroup(t *testing.T) {
	// Build one V210 group (16 bytes) with known 10-bit values.
	v210 := make([]byte, 16)

	// Word 0: Cb0=400, Y0=512, Cr0=800
	// Word 1: Y1=256, Cb2=600, Y2=700
	// Word 2: Cr2=300, Y3=100, Cb4=900
	// Word 3: Y4=1000, Cr4=200, Y5=64
	binary.LittleEndian.PutUint32(v210[0:], packV210Word(400, 512, 800))
	binary.LittleEndian.PutUint32(v210[4:], packV210Word(256, 600, 700))
	binary.LittleEndian.PutUint32(v210[8:], packV210Word(300, 100, 900))
	binary.LittleEndian.PutUint32(v210[12:], packV210Word(1000, 200, 64))

	yOut := make([]uint16, 6)
	cbOut := make([]uint16, 3)
	crOut := make([]uint16, 3)

	v210UnpackRow10(yOut, cbOut, crOut, v210, 1)

	// Verify Y
	expectedY := []uint16{512, 256, 700, 100, 1000, 64}
	for i, want := range expectedY {
		if yOut[i] != want {
			t.Errorf("Y[%d] = %d, want %d", i, yOut[i], want)
		}
	}

	// Verify Cb
	expectedCb := []uint16{400, 600, 900}
	for i, want := range expectedCb {
		if cbOut[i] != want {
			t.Errorf("Cb[%d] = %d, want %d", i, cbOut[i], want)
		}
	}

	// Verify Cr
	expectedCr := []uint16{800, 300, 200}
	for i, want := range expectedCr {
		if crOut[i] != want {
			t.Errorf("Cr[%d] = %d, want %d", i, crOut[i], want)
		}
	}
}

func TestV210UnpackRow10_MultipleGroups(t *testing.T) {
	groups := 4
	v210 := make([]byte, groups*16)
	yOut := make([]uint16, groups*6)
	cbOut := make([]uint16, groups*3)
	crOut := make([]uint16, groups*3)

	rng := rand.New(rand.NewSource(77))
	for g := 0; g < groups; g++ {
		offset := g * 16
		for w := 0; w < 4; w++ {
			a := uint32(rng.Intn(1024))
			b := uint32(rng.Intn(1024))
			c := uint32(rng.Intn(1024))
			binary.LittleEndian.PutUint32(v210[offset+w*4:], packV210Word(a, b, c))
		}
	}

	v210UnpackRow10(yOut, cbOut, crOut, v210, groups)

	// Verify against manual extraction
	for g := 0; g < groups; g++ {
		offset := g * 16
		w0 := binary.LittleEndian.Uint32(v210[offset:])
		w1 := binary.LittleEndian.Uint32(v210[offset+4:])
		w2 := binary.LittleEndian.Uint32(v210[offset+8:])
		w3 := binary.LittleEndian.Uint32(v210[offset+12:])

		expY := [6]uint16{
			uint16((w0 >> 10) & 0x3FF),
			uint16(w1 & 0x3FF),
			uint16((w1 >> 20) & 0x3FF),
			uint16((w2 >> 10) & 0x3FF),
			uint16(w3 & 0x3FF),
			uint16((w3 >> 20) & 0x3FF),
		}
		for i, want := range expY {
			got := yOut[g*6+i]
			if got != want {
				t.Errorf("group %d Y[%d] = %d, want %d", g, i, got, want)
			}
		}

		expCb := [3]uint16{
			uint16(w0 & 0x3FF),
			uint16((w1 >> 10) & 0x3FF),
			uint16((w2 >> 20) & 0x3FF),
		}
		for i, want := range expCb {
			got := cbOut[g*3+i]
			if got != want {
				t.Errorf("group %d Cb[%d] = %d, want %d", g, i, got, want)
			}
		}

		expCr := [3]uint16{
			uint16((w0 >> 20) & 0x3FF),
			uint16(w2 & 0x3FF),
			uint16((w3 >> 10) & 0x3FF),
		}
		for i, want := range expCr {
			got := crOut[g*3+i]
			if got != want {
				t.Errorf("group %d Cr[%d] = %d, want %d", g, i, got, want)
			}
		}
	}
}

func TestV210UnpackRow10_ZeroGroups(t *testing.T) {
	// Should be a no-op, no crash.
	v210UnpackRow10(nil, nil, nil, nil, 0)
}

func TestV210PackRow10_SingleGroup(t *testing.T) {
	yIn := []uint16{512, 256, 700, 100, 1000, 64}
	cbIn := []uint16{400, 600, 900}
	crIn := []uint16{800, 300, 200}
	v210Out := make([]byte, 16)

	v210PackRow10(v210Out, yIn, cbIn, crIn, 1)

	w0 := binary.LittleEndian.Uint32(v210Out[0:])
	w1 := binary.LittleEndian.Uint32(v210Out[4:])
	w2 := binary.LittleEndian.Uint32(v210Out[8:])
	w3 := binary.LittleEndian.Uint32(v210Out[12:])

	// Word 0: Cb0 | (Y0 << 10) | (Cr0 << 20)
	if w0&0x3FF != 400 {
		t.Errorf("W0 Cb0 = %d, want 400", w0&0x3FF)
	}
	if (w0>>10)&0x3FF != 512 {
		t.Errorf("W0 Y0 = %d, want 512", (w0>>10)&0x3FF)
	}
	if (w0>>20)&0x3FF != 800 {
		t.Errorf("W0 Cr0 = %d, want 800", (w0>>20)&0x3FF)
	}

	// Word 1: Y1 | (Cb2 << 10) | (Y2 << 20)
	if w1&0x3FF != 256 {
		t.Errorf("W1 Y1 = %d, want 256", w1&0x3FF)
	}
	if (w1>>10)&0x3FF != 600 {
		t.Errorf("W1 Cb2 = %d, want 600", (w1>>10)&0x3FF)
	}
	if (w1>>20)&0x3FF != 700 {
		t.Errorf("W1 Y2 = %d, want 700", (w1>>20)&0x3FF)
	}

	// Word 2: Cr2 | (Y3 << 10) | (Cb4 << 20)
	if w2&0x3FF != 300 {
		t.Errorf("W2 Cr2 = %d, want 300", w2&0x3FF)
	}
	if (w2>>10)&0x3FF != 100 {
		t.Errorf("W2 Y3 = %d, want 100", (w2>>10)&0x3FF)
	}
	if (w2>>20)&0x3FF != 900 {
		t.Errorf("W2 Cb4 = %d, want 900", (w2>>20)&0x3FF)
	}

	// Word 3: Y4 | (Cr4 << 10) | (Y5 << 20)
	if w3&0x3FF != 1000 {
		t.Errorf("W3 Y4 = %d, want 1000", w3&0x3FF)
	}
	if (w3>>10)&0x3FF != 200 {
		t.Errorf("W3 Cr4 = %d, want 200", (w3>>10)&0x3FF)
	}
	if (w3>>20)&0x3FF != 64 {
		t.Errorf("W3 Y5 = %d, want 64", (w3>>20)&0x3FF)
	}

	// Verify padding bits (31:30) are zero
	if w0>>30 != 0 {
		t.Errorf("W0 padding bits = %d, want 0", w0>>30)
	}
	if w1>>30 != 0 {
		t.Errorf("W1 padding bits = %d, want 0", w1>>30)
	}
	if w2>>30 != 0 {
		t.Errorf("W2 padding bits = %d, want 0", w2>>30)
	}
	if w3>>30 != 0 {
		t.Errorf("W3 padding bits = %d, want 0", w3>>30)
	}
}

func TestV210PackRow10_ZeroGroups(t *testing.T) {
	v210PackRow10(nil, nil, nil, nil, 0)
}

func TestV210PackRow10_RoundTrip(t *testing.T) {
	groups := 320 // 1920 pixels
	rng := rand.New(rand.NewSource(123))

	yIn := make([]uint16, groups*6)
	cbIn := make([]uint16, groups*3)
	crIn := make([]uint16, groups*3)
	for i := range yIn {
		yIn[i] = uint16(rng.Intn(1024))
	}
	for i := range cbIn {
		cbIn[i] = uint16(rng.Intn(1024))
	}
	for i := range crIn {
		crIn[i] = uint16(rng.Intn(1024))
	}

	v210 := make([]byte, groups*16)
	v210PackRow10(v210, yIn, cbIn, crIn, groups)

	yOut := make([]uint16, groups*6)
	cbOut := make([]uint16, groups*3)
	crOut := make([]uint16, groups*3)
	v210UnpackRow10(yOut, cbOut, crOut, v210, groups)

	for i := range yIn {
		if yIn[i] != yOut[i] {
			t.Fatalf("Y[%d] round-trip: in=%d, out=%d", i, yIn[i], yOut[i])
		}
	}
	for i := range cbIn {
		if cbIn[i] != cbOut[i] {
			t.Fatalf("Cb[%d] round-trip: in=%d, out=%d", i, cbIn[i], cbOut[i])
		}
	}
	for i := range crIn {
		if crIn[i] != crOut[i] {
			t.Fatalf("Cr[%d] round-trip: in=%d, out=%d", i, crIn[i], crOut[i])
		}
	}
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkV210ToYUV422P10_Go_1080p(b *testing.B) {
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

func BenchmarkV210ToYUV422P10_SIMD_1080p(b *testing.B) {
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
		_, _ = v210ToYUV422P10SIMD(v210, stride, w, h)
	}
}

func BenchmarkYUV422P10ToV210_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	b.SetBytes(int64(len(yuv)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = YUV422P10ToV210(yuv, w, h)
	}
}

func BenchmarkYUV422P10ToV210_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := makeYUV422P10Frame(w, h, 500, 300, 700)

	b.SetBytes(int64(len(yuv)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = yuv422P10ToV210SIMD(yuv, w, h)
	}
}

func BenchmarkV210UnpackRow10_1080p(b *testing.B) {
	groups := 320 // 1920/6
	v210 := make([]byte, groups*16)
	yOut := make([]uint16, groups*6)
	cbOut := make([]uint16, groups*3)
	crOut := make([]uint16, groups*3)
	for i := range v210 {
		v210[i] = byte(i % 256)
	}

	b.SetBytes(int64(groups * 16))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v210UnpackRow10(yOut, cbOut, crOut, v210, groups)
	}
}

func BenchmarkV210PackRow10_1080p(b *testing.B) {
	groups := 320 // 1920/6
	v210Out := make([]byte, groups*16)
	yIn := make([]uint16, groups*6)
	cbIn := make([]uint16, groups*3)
	crIn := make([]uint16, groups*3)
	rng := rand.New(rand.NewSource(42))
	for i := range yIn {
		yIn[i] = uint16(rng.Intn(1024))
	}
	for i := range cbIn {
		cbIn[i] = uint16(rng.Intn(1024))
	}
	for i := range crIn {
		crIn[i] = uint16(rng.Intn(1024))
	}

	b.SetBytes(int64(groups * 16))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v210PackRow10(v210Out, yIn, cbIn, crIn, groups)
	}
}
