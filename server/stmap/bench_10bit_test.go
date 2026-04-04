package stmap

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// Comprehensive 10-bit SIMD benchmark suite for the stmap package.
// Benchmarks Go reference vs SIMD dispatch for 10-bit bilinear warp at 1080p.

// ============================================================================
// WarpBilinearRow10 — per-pixel bilinear warp (the SIMD hot path)
// ============================================================================

func BenchmarkWarpPlane10_Go_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	planeSize := w * h * 2
	src := make([]byte, planeSize)
	dst := make([]byte, planeSize)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	b.SetBytes(int64(planeSize * 2)) // read src + write dst
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpPlane10(dst, src, w, h, p.lutSX, p.lutSY)
	}
}

func BenchmarkWarpPlane10_ASM_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	planeSize := w * h * 2
	src := make([]byte, planeSize)
	dst := make([]byte, planeSize)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	b.SetBytes(int64(planeSize * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpPlane10Dispatch(dst, src, w, h, p.lutSX, p.lutSY)
	}
}

// Full-frame Warp10 (Y + Cb + Cr planes) with identity ST map

func BenchmarkWarp10_Go_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)
	p.ensure422ChromaLUTs(w, h)

	frameSize := yuv422p10Size(w, h)
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < frameSize/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	ySize := w * h * 2
	chromaW := w / 2
	chromaPlaneSize := chromaW * h * 2

	b.SetBytes(int64(frameSize * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpPlane10(dst[:ySize], src[:ySize], w, h, p.lutSX, p.lutSY)
		cbOff := ySize
		warpPlane10(dst[cbOff:cbOff+chromaPlaneSize], src[cbOff:cbOff+chromaPlaneSize],
			chromaW, h, p.lut422CSX, p.lut422CSY)
		crOff := ySize + chromaPlaneSize
		warpPlane10(dst[crOff:crOff+chromaPlaneSize], src[crOff:crOff+chromaPlaneSize],
			chromaW, h, p.lut422CSX, p.lut422CSY)
	}
}

func BenchmarkWarp10_ASM_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)
	p.ensure422ChromaLUTs(w, h)

	frameSize := yuv422p10Size(w, h)
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < frameSize/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	b.SetBytes(int64(frameSize * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Warp10(dst, src, w, h)
	}
}
