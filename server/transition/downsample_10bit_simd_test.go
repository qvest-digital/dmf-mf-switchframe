package transition

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// downsampleAlpha10RowGo_ref is a standalone Go reference for testing.
// Averages pairs: dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
// Input/output are uint8 (alpha stays 8-bit). count = number of OUTPUT samples.
func downsampleAlpha10RowGo_ref(dst, src []byte, count int) {
	for i := 0; i < count; i++ {
		a := uint(src[2*i])
		b := uint(src[2*i+1])
		dst[i] = byte((a + b + 1) >> 1)
	}
}

// TestDownsampleAlpha10Row_DirectKernel tests the low-level kernel directly
// against the Go reference across a range of sizes.
func TestDownsampleAlpha10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, count := range []int{1, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 100, 960} {
		t.Run(fmt.Sprintf("count=%d", count), func(t *testing.T) {
			src := make([]byte, count*2)
			for i := range src {
				src[i] = byte(rng.Intn(256))
			}

			// Go reference
			ref := make([]byte, count)
			downsampleAlpha10RowGo_ref(ref, src, count)

			// SIMD kernel (dispatched)
			got := make([]byte, count)
			downsampleAlpha10Row(&got[0], &src[0], count)

			if !bytes.Equal(ref, got) {
				for i := 0; i < count; i++ {
					if ref[i] != got[i] {
						t.Fatalf("sample %d: ref=%d, got=%d (src[%d]=%d, src[%d]=%d)",
							i, ref[i], got[i], 2*i, src[2*i], 2*i+1, src[2*i+1])
					}
				}
			}
		})
	}
}

// TestDownsampleAlpha10Row_AllZero verifies zero input produces zero output.
func TestDownsampleAlpha10Row_AllZero(t *testing.T) {
	t.Parallel()
	count := 32
	src := make([]byte, count*2)
	got := make([]byte, count)
	downsampleAlpha10Row(&got[0], &src[0], count)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("pixel %d: expected 0, got %d", i, v)
		}
	}
}

// TestDownsampleAlpha10Row_AllMax verifies 255 input produces 255 output.
func TestDownsampleAlpha10Row_AllMax(t *testing.T) {
	t.Parallel()
	count := 32
	src := make([]byte, count*2)
	for i := range src {
		src[i] = 255
	}
	got := make([]byte, count)
	downsampleAlpha10Row(&got[0], &src[0], count)
	for i, v := range got {
		if v != 255 {
			t.Fatalf("pixel %d: expected 255, got %d", i, v)
		}
	}
}

// TestDownsampleAlpha10Row_KnownValues tests specific known patterns.
func TestDownsampleAlpha10Row_KnownValues(t *testing.T) {
	t.Parallel()
	// (0+1+1)>>1 = 1, (254+255+1)>>1 = 255, (100+200+1)>>1 = 150
	src := []byte{0, 1, 254, 255, 100, 200, 0, 0}
	expected := []byte{1, 255, 150, 0}
	got := make([]byte, 4)
	downsampleAlpha10Row(&got[0], &src[0], 4)
	if !bytes.Equal(expected, got) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// TestDownsampleAlpha10Row_Large tests with 1080p-width alpha row.
func TestDownsampleAlpha10Row_Large(t *testing.T) {
	t.Parallel()
	count := 960 // 1920/2 = 960 chroma samples
	rng := rand.New(rand.NewSource(7))
	src := make([]byte, count*2)
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}

	ref := make([]byte, count)
	downsampleAlpha10RowGo_ref(ref, src, count)

	got := make([]byte, count)
	downsampleAlpha10Row(&got[0], &src[0], count)

	if !bytes.Equal(ref, got) {
		for i := 0; i < count; i++ {
			if ref[i] != got[i] {
				t.Fatalf("sample %d: ref=%d, got=%d", i, ref[i], got[i])
			}
		}
	}
}

func BenchmarkDownsampleAlpha10Row_Go_1080p(b *testing.B) {
	count := 960
	src := make([]byte, count*2)
	dst := make([]byte, count)
	for i := range src {
		src[i] = byte(i % 256)
	}
	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsampleAlpha10RowGo(&dst[0], &src[0], count)
	}
}

func BenchmarkDownsampleAlpha10Row_SIMD_1080p(b *testing.B) {
	count := 960
	src := make([]byte, count*2)
	dst := make([]byte, count)
	for i := range src {
		src[i] = byte(i % 256)
	}
	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsampleAlpha10Row(&dst[0], &src[0], count)
	}
}
