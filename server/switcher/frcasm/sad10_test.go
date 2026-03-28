package frcasm

import (
	"encoding/binary"
	"math/rand"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// referenceSad10Block computes SAD for 10-bit (uint16 LE) blocks.
// a and b point to the start of Y planes. stride is row stride in bytes.
// bw and bh are block dimensions in samples.
func referenceSad10Block(a, b []byte, stride, bw, bh int) int {
	sum := 0
	for y := 0; y < bh; y++ {
		rowOff := y * stride
		for x := 0; x < bw; x++ {
			aVal := int(binary.LittleEndian.Uint16(a[rowOff+x*2:]))
			bVal := int(binary.LittleEndian.Uint16(b[rowOff+x*2:]))
			diff := aVal - bVal
			if diff < 0 {
				diff = -diff
			}
			sum += diff
		}
	}
	return sum
}

func TestSad10Block_MatchesReference(t *testing.T) {
	stride := 64 * 2 // 64 samples per row, 2 bytes each
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))

	a := make([]byte, stride*32) // big enough buffer
	b := make([]byte, stride*32)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := referenceSad10Block(a, b, stride, bw, bh)
	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)

	require.Equal(t, ref, got, "SIMD SAD10 should match reference")
}

func TestSad10Block_Identical(t *testing.T) {
	stride := 16 * 2 // 16 samples, 2 bytes each
	bw, bh := 16, 8
	buf := make([]byte, stride*bh)
	rng := rand.New(rand.NewSource(123))
	for i := 0; i < len(buf)/2; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	got := Sad10Block((*byte)(unsafe.Pointer(&buf[0])), (*byte)(unsafe.Pointer(&buf[0])), stride, bw, bh)
	require.Equal(t, 0, got, "identical blocks should have SAD=0")
}

func TestSad10Block_MaxDiff(t *testing.T) {
	stride := 16 * 2
	bw, bh := 16, 8
	a := make([]byte, stride*bh)
	b := make([]byte, stride*bh)
	for i := 0; i < stride*bh/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], 0)
		binary.LittleEndian.PutUint16(b[i*2:], 1023)
	}

	ref := referenceSad10Block(a, b, stride, bw, bh)
	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)

	require.Equal(t, bw*bh*1023, ref)
	require.Equal(t, ref, got, "max diff: SIMD should match reference")
}

func TestSad10Block_NonAligned(t *testing.T) {
	// Test with block width not a multiple of SIMD width (8 for NEON, 16 for AVX2)
	stride := 20 * 2 // 20 samples per row
	bw, bh := 12, 4
	rng := rand.New(rand.NewSource(99))
	a := make([]byte, stride*bh)
	b := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := referenceSad10Block(a, b, stride, bw, bh)
	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)
	require.Equal(t, ref, got, "non-aligned width: SIMD should match reference")
}

func TestSad10Block_OddWidth(t *testing.T) {
	// bw=7 is odd, not a multiple of any SIMD width
	stride := 16 * 2
	bw, bh := 7, 3
	rng := rand.New(rand.NewSource(77))
	a := make([]byte, stride*bh)
	b := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := referenceSad10Block(a, b, stride, bw, bh)
	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)
	require.Equal(t, ref, got, "odd width: SIMD should match reference")
}

func TestSad10Block_SinglePixel(t *testing.T) {
	stride := 4 * 2
	bw, bh := 1, 1
	a := make([]byte, stride)
	b := make([]byte, stride)
	binary.LittleEndian.PutUint16(a, 100)
	binary.LittleEndian.PutUint16(b, 350)

	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)
	require.Equal(t, 250, got)
}

func TestSad10Block_LargeBlock(t *testing.T) {
	// 64x64 block to stress accumulator overflow handling
	stride := 64 * 2
	bw, bh := 64, 64
	rng := rand.New(rand.NewSource(2024))
	a := make([]byte, stride*bh)
	b := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := referenceSad10Block(a, b, stride, bw, bh)
	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)
	require.Equal(t, ref, got, "large block: SIMD should match reference")
}

func TestSad10Block_StrideWiderThanBlock(t *testing.T) {
	// Stride is much wider than block — tests correct row advancement
	stride := 128 * 2 // 128 samples per row
	bw, bh := 8, 4    // only use 8 of those samples
	rng := rand.New(rand.NewSource(55))
	a := make([]byte, stride*bh)
	b := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
	}

	ref := referenceSad10Block(a, b, stride, bw, bh)
	got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)
	require.Equal(t, ref, got, "wide stride: SIMD should match reference")
}

func TestSad10Block_FuzzMultipleSizes(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	sizes := [][2]int{
		{4, 4}, {8, 8}, {16, 16}, {8, 4}, {4, 8},
		{12, 12}, {6, 10}, {16, 8}, {32, 16}, {3, 5},
	}
	for _, sz := range sizes {
		bw, bh := sz[0], sz[1]
		stride := bw * 2 // tight stride
		a := make([]byte, stride*bh)
		b := make([]byte, stride*bh)
		for i := 0; i < len(a)/2; i++ {
			binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
			binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
		}

		ref := referenceSad10Block(a, b, stride, bw, bh)
		got := Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&b[0])), stride, bw, bh)
		require.Equal(t, ref, got, "fuzz %dx%d: SIMD should match reference", bw, bh)
	}
}

// --- Benchmarks ---

func BenchmarkSad10Block_16x16_Go(b *testing.B) {
	stride := 16 * 2
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		referenceSad10Block(a, buf, stride, bw, bh)
	}
}

func BenchmarkSad10Block_16x16_SIMD(b *testing.B) {
	stride := 16 * 2
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&buf[0])), stride, bw, bh)
	}
}

func BenchmarkSad10Block_64x64_Go(b *testing.B) {
	stride := 64 * 2
	bw, bh := 64, 64
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		referenceSad10Block(a, buf, stride, bw, bh)
	}
}

func BenchmarkSad10Block_64x64_SIMD(b *testing.B) {
	stride := 64 * 2
	bw, bh := 64, 64
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&buf[0])), stride, bw, bh)
	}
}
