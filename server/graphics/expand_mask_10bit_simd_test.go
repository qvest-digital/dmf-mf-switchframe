package graphics

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// TestExpandChromaMask10Row_DirectKernel tests SIMD against Go reference.
func TestExpandChromaMask10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, count := range []int{1, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 100, 960} {
		t.Run(fmt.Sprintf("count=%d", count), func(t *testing.T) {
			src := make([]byte, count)
			for i := range src {
				src[i] = byte(rng.Intn(256))
			}

			// Go reference
			ref := make([]byte, count*2)
			expandChromaMask10RowGo(&ref[0], &src[0], count)

			// SIMD kernel
			got := make([]byte, count*2)
			expandChromaMask10Row(&got[0], &src[0], count)

			if !bytes.Equal(ref, got) {
				for i := 0; i < count; i++ {
					if ref[i*2] != got[i*2] || ref[i*2+1] != got[i*2+1] {
						t.Fatalf("sample %d: ref=[%d,%d], got=[%d,%d] (src=%d)",
							i, ref[i*2], ref[i*2+1], got[i*2], got[i*2+1], src[i])
					}
				}
			}
		})
	}
}

// TestExpandChromaMask10Row_AllZero verifies zero input.
func TestExpandChromaMask10Row_AllZero(t *testing.T) {
	t.Parallel()
	count := 32
	src := make([]byte, count)
	got := make([]byte, count*2)
	expandChromaMask10Row(&got[0], &src[0], count)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("byte %d: expected 0, got %d", i, v)
		}
	}
}

// TestExpandChromaMask10Row_AllMax verifies 255 input.
func TestExpandChromaMask10Row_AllMax(t *testing.T) {
	t.Parallel()
	count := 32
	src := make([]byte, count)
	for i := range src {
		src[i] = 255
	}
	got := make([]byte, count*2)
	expandChromaMask10Row(&got[0], &src[0], count)
	for i, v := range got {
		if v != 255 {
			t.Fatalf("byte %d: expected 255, got %d", i, v)
		}
	}
}

// TestExpandChromaMask10Row_Pattern verifies known pattern.
func TestExpandChromaMask10Row_Pattern(t *testing.T) {
	t.Parallel()
	src := []byte{10, 20, 30, 40}
	expected := []byte{10, 10, 20, 20, 30, 30, 40, 40}
	got := make([]byte, 8)
	expandChromaMask10Row(&got[0], &src[0], 4)
	if !bytes.Equal(expected, got) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// TestExpandChromaMask10Row_Large tests with 1080p chroma width.
func TestExpandChromaMask10Row_Large(t *testing.T) {
	t.Parallel()
	count := 960 // 1920/2
	rng := rand.New(rand.NewSource(7))
	src := make([]byte, count)
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}

	ref := make([]byte, count*2)
	expandChromaMask10RowGo(&ref[0], &src[0], count)

	got := make([]byte, count*2)
	expandChromaMask10Row(&got[0], &src[0], count)

	if !bytes.Equal(ref, got) {
		for i := 0; i < count; i++ {
			if ref[i*2] != got[i*2] || ref[i*2+1] != got[i*2+1] {
				t.Fatalf("sample %d: ref=[%d,%d], got=[%d,%d]",
					i, ref[i*2], ref[i*2+1], got[i*2], got[i*2+1])
			}
		}
	}
}

func BenchmarkExpandChromaMask10Row_SIMD_1080p(b *testing.B) {
	count := 960
	src := make([]byte, count)
	dst := make([]byte, count*2)
	for i := range src {
		src[i] = byte(i % 256)
	}
	b.SetBytes(int64(count))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expandChromaMask10Row(&dst[0], &src[0], count)
	}
}
