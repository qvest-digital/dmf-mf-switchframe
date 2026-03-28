package frcasm

import (
	"encoding/binary"
	"math/rand"
	"testing"
	"unsafe"
)

// Comprehensive 10-bit SIMD benchmark suite for the frcasm package.
// Benchmarks Go reference vs SIMD dispatch for SAD10 and DownsampleY10.

// ============================================================================
// SAD10 — Sum of Absolute Differences on 10-bit uint16 blocks
// ============================================================================

func BenchmarkSad10Block_Go_16x16(b *testing.B) {
	stride := 16 * 2
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(bw * bh * 2 * 2)) // read 2 blocks
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		referenceSad10Block(a, buf, stride, bw, bh)
	}
}

func BenchmarkSad10Block_ASM_16x16(b *testing.B) {
	stride := 16 * 2
	bw, bh := 16, 16
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(bw * bh * 2 * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&buf[0])), stride, bw, bh)
	}
}

func BenchmarkSad10Block_Go_64x64(b *testing.B) {
	stride := 64 * 2
	bw, bh := 64, 64
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(bw * bh * 2 * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		referenceSad10Block(a, buf, stride, bw, bh)
	}
}

func BenchmarkSad10Block_ASM_64x64(b *testing.B) {
	stride := 64 * 2
	bw, bh := 64, 64
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, stride*bh)
	buf := make([]byte, stride*bh)
	for i := 0; i < len(a)/2; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(bw * bh * 2 * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Sad10Block((*byte)(unsafe.Pointer(&a[0])), (*byte)(unsafe.Pointer(&buf[0])), stride, bw, bh)
	}
}

// ============================================================================
// DownsampleY10_2x — 2x2 box-filter downsample (10-bit uint16 → 8-bit uint8)
// ============================================================================

func BenchmarkDownsampleY10_2x_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	b.SetBytes(int64(w * h * 2)) // read all source bytes
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		referenceDownsampleY10_2x(dst, src, srcStride, dstW, dstH)
	}
}

func BenchmarkDownsampleY10_2x_ASM_1080p(b *testing.B) {
	w, h := 1920, 1080
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	b.SetBytes(int64(w * h * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DownsampleY10_2x(&dst[0], &src[0], srcStride, dstW, dstH)
	}
}
