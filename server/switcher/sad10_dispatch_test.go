package switcher

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSAD10SIMD_MatchesReference(t *testing.T) {
	stride := 64 // samples per row
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))

	a := make([]byte, stride*32*2) // big enough buffer
	b := make([]byte, stride*32*2)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := SAD10(a, b, 0, 0, stride, bw, bh)
	got := sad10Dispatch(a, b, 0, 0, stride, bw, bh)

	require.Equal(t, ref, got, "SIMD dispatch should match Go reference")
}

func TestSAD10SIMD_Identical(t *testing.T) {
	stride := 16
	bw, bh := 16, 8
	rng := rand.New(rand.NewSource(123))
	buf := make([]byte, stride*bh*2)
	for i := 0; i < len(buf)/2; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	got := sad10Dispatch(buf, buf, 0, 0, stride, bw, bh)
	require.Equal(t, 0, got, "identical blocks should have SAD=0")
}

func TestSAD10SIMD_MaxDiff(t *testing.T) {
	stride := 16
	bw, bh := 16, 8
	a := make([]byte, stride*bh*2)
	b := make([]byte, stride*bh*2)
	for i := 0; i < stride*bh; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], 0)
		binary.LittleEndian.PutUint16(b[i*2:], 1023)
	}

	ref := SAD10(a, b, 0, 0, stride, bw, bh)
	got := sad10Dispatch(a, b, 0, 0, stride, bw, bh)

	require.Equal(t, bw*bh*1023, ref)
	require.Equal(t, ref, got, "max diff: SIMD should match reference")
}

func TestSAD10SIMD_NonAligned(t *testing.T) {
	// Test with block width not a multiple of SIMD width
	stride := 20
	bw, bh := 12, 4
	rng := rand.New(rand.NewSource(99))
	a := make([]byte, stride*bh*2)
	b := make([]byte, stride*bh*2)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := SAD10(a, b, 0, 0, stride, bw, bh)
	got := sad10Dispatch(a, b, 0, 0, stride, bw, bh)
	require.Equal(t, ref, got, "non-aligned: SIMD should match reference")
}

func TestSAD10SIMD_WithOffset(t *testing.T) {
	stride := 32
	rng := rand.New(rand.NewSource(77))
	a := make([]byte, stride*16*2)
	b := make([]byte, stride*16*2)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	// Test with non-zero offset
	aOff := (3*stride + 4) * 2
	bOff := (3*stride + 4) * 2
	bw, bh := 8, 6

	ref := SAD10(a, b, aOff, bOff, stride, bw, bh)
	got := sad10Dispatch(a, b, aOff, bOff, stride, bw, bh)
	require.Equal(t, ref, got, "with offset: SIMD should match reference")
}

func TestSAD10SIMD_FuzzSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	sizes := [][2]int{
		{4, 4}, {8, 8}, {16, 16}, {8, 4}, {4, 8},
		{12, 12}, {6, 10}, {16, 8}, {32, 16}, {3, 5},
		{1, 1}, {7, 3}, {64, 64},
	}
	for _, sz := range sizes {
		bw, bh := sz[0], sz[1]
		stride := bw // tight stride
		a := make([]byte, stride*bh*2)
		b := make([]byte, stride*bh*2)
		for i := 0; i < len(a)/2; i++ {
			binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
			binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
		}

		ref := SAD10(a, b, 0, 0, stride, bw, bh)
		got := sad10Dispatch(a, b, 0, 0, stride, bw, bh)
		require.Equal(t, ref, got, "fuzz %dx%d: SIMD should match reference", bw, bh)
	}
}

// --- Benchmarks ---

func BenchmarkSAD10_Go_16x16(b *testing.B) {
	stride := 16
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh*2)
	buf := make([]byte, stride*bh*2)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SAD10(a, buf, 0, 0, stride, bw, bh)
	}
}

func BenchmarkSAD10_SIMD_16x16(b *testing.B) {
	stride := 16
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh*2)
	buf := make([]byte, stride*bh*2)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sad10Dispatch(a, buf, 0, 0, stride, bw, bh)
	}
}
