package graphics

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

// TestLumaKey10Row_DirectKernel tests the low-level lumaKey10Row kernel directly
// against the Go reference across a range of sizes and thresholds.
func TestLumaKey10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, count := range []int{1, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 100} {
		for _, tc := range []struct {
			low, high uint16
		}{
			{200, 800},
			{0, 1023},
			{512, 512},
			{0, 0},
			{1023, 1023},
			{100, 300},
		} {
			t.Run(fmt.Sprintf("count=%d/low=%d/high=%d", count, tc.low, tc.high), func(t *testing.T) {
				yData := make([]byte, count*2)
				for i := 0; i < count; i++ {
					binary.LittleEndian.PutUint16(yData[i*2:], uint16(rng.Intn(1024)))
				}

				// Go reference
				ref := make([]byte, count)
				lumaKey10RowGo(&ref[0], &yData[0], count, tc.low, tc.high)

				// SIMD kernel (dispatched)
				got := make([]byte, count)
				lumaKey10Row(&got[0], &yData[0], count, int(tc.low), int(tc.high))

				if !bytes.Equal(ref, got) {
					for i := 0; i < count; i++ {
						if ref[i] != got[i] {
							yVal := binary.LittleEndian.Uint16(yData[i*2:])
							t.Fatalf("sample %d: ref=%d, got=%d (y=%d, low=%d, high=%d)",
								i, ref[i], got[i], yVal, tc.low, tc.high)
						}
					}
				}
			})
		}
	}
}

// TestLumaKey10SIMD_MatchesReference verifies the full dispatch function
// produces bit-identical results to LumaKey10 for random input.
func TestLumaKey10SIMD_MatchesReference(t *testing.T) {
	t.Parallel()
	w, h := 64, 32
	rng := rand.New(rand.NewSource(42))

	yuv := make([]byte, w*h*4) // YUV422P10LE
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}
	// Fill chroma planes (don't matter for luma key)
	for i := w * h * 2; i < len(yuv); i++ {
		yuv[i] = 0
	}

	low, high := uint16(200), uint16(800)

	// Go reference (the existing LumaKey10)
	ref := LumaKey10(yuv, w, h, low, high)

	// SIMD dispatch
	got := lumaKey10Dispatch(yuv, w, h, low, high)

	if !bytes.Equal(ref, got) {
		mismatches := 0
		for i := range ref {
			if ref[i] != got[i] {
				if mismatches == 0 {
					t.Errorf("first mismatch at %d: ref=%d, got=%d", i, ref[i], got[i])
				}
				mismatches++
			}
		}
		t.Errorf("total mismatches: %d of %d", mismatches, len(ref))
	}
}

// TestLumaKey10SIMD_AllInRange verifies that all-in-range pixels produce mask=255.
func TestLumaKey10SIMD_AllInRange(t *testing.T) {
	t.Parallel()
	w, h := 16, 8
	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 500) // all in range
	}

	ref := LumaKey10(yuv, w, h, 400, 600)
	got := lumaKey10Dispatch(yuv, w, h, 400, 600)

	if !bytes.Equal(ref, got) {
		t.Fatal("all-in-range mismatch")
	}
	if ref[0] != 255 {
		t.Fatal("should be keyed (mask=255)")
	}
}

// TestLumaKey10SIMD_AllOutOfRange verifies that all-out-of-range pixels produce mask=0.
func TestLumaKey10SIMD_AllOutOfRange(t *testing.T) {
	t.Parallel()
	w, h := 16, 8
	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 100)
	}

	ref := LumaKey10(yuv, w, h, 400, 600)
	got := lumaKey10Dispatch(yuv, w, h, 400, 600)

	if !bytes.Equal(ref, got) {
		t.Fatal("all-out-range mismatch")
	}
	if ref[0] != 0 {
		t.Fatal("should not be keyed (mask=0)")
	}
}

// TestLumaKey10SIMD_NonAlignedWidth tests with a width that is not a multiple of 8 or 16.
func TestLumaKey10SIMD_NonAlignedWidth(t *testing.T) {
	t.Parallel()
	w, h := 10, 4 // not multiple of 8 or 16
	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(i%1024))
	}

	ref := LumaKey10(yuv, w, h, 200, 800)
	got := lumaKey10Dispatch(yuv, w, h, 200, 800)

	if !bytes.Equal(ref, got) {
		t.Fatal("non-aligned mismatch")
	}
}

// TestLumaKey10SIMD_BoundaryValues tests at exact threshold boundaries.
func TestLumaKey10SIMD_BoundaryValues(t *testing.T) {
	t.Parallel()
	w, h := 8, 1

	// Create frame with Y values: low-1, low, low+1, high-1, high, high+1, 0, 1023
	low, high := uint16(200), uint16(800)
	values := []uint16{low - 1, low, low + 1, high - 1, high, high + 1, 0, 1023}

	yuv := make([]byte, w*h*4) // extra space for YUV422P10LE
	for i, v := range values {
		binary.LittleEndian.PutUint16(yuv[i*2:], v)
	}

	ref := LumaKey10(yuv, w, h, low, high)
	got := lumaKey10Dispatch(yuv, w, h, low, high)

	if !bytes.Equal(ref, got) {
		for i := range ref {
			if ref[i] != got[i] {
				t.Errorf("boundary mismatch at %d (y=%d): ref=%d, got=%d", i, values[i], ref[i], got[i])
			}
		}
	}

	// Verify expected values
	expected := []byte{0, 255, 255, 255, 255, 0, 0, 0}
	if !bytes.Equal(ref, expected) {
		t.Errorf("reference output wrong: got %v, want %v", ref, expected)
	}
}

// TestLumaKey10SIMD_FullRange tests threshold 0-1023 (all pixels in range).
func TestLumaKey10SIMD_FullRange(t *testing.T) {
	t.Parallel()
	w, h := 32, 8
	rng := rand.New(rand.NewSource(99))

	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	ref := LumaKey10(yuv, w, h, 0, 1023)
	got := lumaKey10Dispatch(yuv, w, h, 0, 1023)

	if !bytes.Equal(ref, got) {
		t.Fatal("full range mismatch")
	}
	// All pixels should be 255
	for i, v := range ref {
		if v != 255 {
			t.Fatalf("pixel %d: expected 255 for full range, got %d", i, v)
		}
	}
}

// TestLumaKey10SIMD_EmptyRange tests threshold where low > high (nothing matches).
func TestLumaKey10SIMD_EmptyRange(t *testing.T) {
	t.Parallel()
	w, h := 32, 8
	rng := rand.New(rand.NewSource(77))

	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	// low > high means nothing should match
	ref := LumaKey10(yuv, w, h, 800, 200)
	got := lumaKey10Dispatch(yuv, w, h, 800, 200)

	if !bytes.Equal(ref, got) {
		t.Fatal("empty range mismatch")
	}
	// All pixels should be 0
	for i, v := range ref {
		if v != 0 {
			t.Fatalf("pixel %d: expected 0 for empty range, got %d", i, v)
		}
	}
}

// TestLumaKey10SIMD_LargeFrame tests with 1080p-sized frame.
func TestLumaKey10SIMD_LargeFrame(t *testing.T) {
	t.Parallel()
	w, h := 1920, 1080
	rng := rand.New(rand.NewSource(7))

	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	ref := LumaKey10(yuv, w, h, 200, 800)
	got := lumaKey10Dispatch(yuv, w, h, 200, 800)

	if !bytes.Equal(ref, got) {
		mismatches := 0
		for i := range ref {
			if ref[i] != got[i] {
				if mismatches == 0 {
					yVal := binary.LittleEndian.Uint16(yuv[i*2:])
					t.Errorf("1080p first mismatch at %d: ref=%d, got=%d (y=%d)", i, ref[i], got[i], yVal)
				}
				mismatches++
			}
		}
		t.Errorf("1080p total mismatches: %d of %d", mismatches, len(ref))
	}
}

// TestLumaKey10Row_NZero verifies n=0 doesn't crash.
func TestLumaKey10Row_NZero(t *testing.T) {
	t.Parallel()
	yData := []byte{0, 0}
	mask := []byte{99}

	// Should not panic
	lumaKey10Row(&mask[0], &yData[0], 0, int(200), int(800))

	// mask should be unchanged
	if mask[0] != 99 {
		t.Errorf("n=0 should not modify mask, got %d", mask[0])
	}
}

// TestLumaKey10SIMD_SingleThreshold tests where low == high.
func TestLumaKey10SIMD_SingleThreshold(t *testing.T) {
	t.Parallel()
	w, h := 16, 4

	// Only Y=512 should match
	yuv := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(i%1024))
	}

	ref := LumaKey10(yuv, w, h, 512, 512)
	got := lumaKey10Dispatch(yuv, w, h, 512, 512)

	if !bytes.Equal(ref, got) {
		t.Fatal("single threshold mismatch")
	}
}

func BenchmarkLumaKey10_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(w * h * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LumaKey10(yuv, w, h, 200, 800)
	}
}

func BenchmarkLumaKey10_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(w * h * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lumaKey10Dispatch(yuv, w, h, 200, 800)
	}
}

func BenchmarkLumaKey10Row_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	count := w * h
	yData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(yData[i*2:], uint16(rng.Intn(1024)))
	}
	mask := make([]byte, count)
	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lumaKey10RowGo(&mask[0], &yData[0], count, 200, 800)
	}
}

func BenchmarkLumaKey10Row_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	count := w * h
	yData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(yData[i*2:], uint16(rng.Intn(1024)))
	}
	mask := make([]byte, count)
	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lumaKey10Row(&mask[0], &yData[0], count, int(200), int(800))
	}
}
