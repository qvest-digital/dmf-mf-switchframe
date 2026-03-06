package transition

import "testing"

// BenchmarkBlendMix720p benchmarks the YUV420 mix blend at 720p resolution.
// This is the most common transition path: linear interpolation between two
// source frames in the native YUV domain.
func BenchmarkBlendMix720p(b *testing.B) {
	blender := NewFrameBlender(1280, 720)
	yuvSize := 1280 * 720 * 3 / 2
	a := make([]byte, yuvSize)
	bSlice := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bSlice)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendMix(a, bSlice, 0.5)
	}
}

// BenchmarkBlendMix1080p benchmarks the YUV420 mix blend at 1080p resolution.
func BenchmarkBlendMix1080p(b *testing.B) {
	blender := NewFrameBlender(1920, 1080)
	yuvSize := 1920 * 1080 * 3 / 2
	a := make([]byte, yuvSize)
	bSlice := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bSlice)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendMix(a, bSlice, 0.5)
	}
}

// BenchmarkBlendDip1080p benchmarks the two-phase dip-to-black transition
// at 1080p. This is more complex than mix (per-plane black blending).
func BenchmarkBlendDip1080p(b *testing.B) {
	blender := NewFrameBlender(1920, 1080)
	yuvSize := 1920 * 1080 * 3 / 2
	a := make([]byte, yuvSize)
	bSlice := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bSlice)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendDip(a, bSlice, 0.5)
	}
}

// BenchmarkBlendFTB1080p benchmarks fade-to-black at 1080p resolution.
// FTB operates on a single source, fading toward YUV black (Y=0, Cb/Cr=128).
func BenchmarkBlendFTB1080p(b *testing.B) {
	blender := NewFrameBlender(1920, 1080)
	yuvSize := 1920 * 1080 * 3 / 2
	a := make([]byte, yuvSize)
	fillTestPattern(a)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendFTB(a, 0.5)
	}
}

// BenchmarkBlendWipe1080p benchmarks a horizontal-left wipe at 1080p.
// Wipe is the most expensive blend mode because of per-pixel threshold
// computation and the soft-edge alpha calculation.
func BenchmarkBlendWipe1080p(b *testing.B) {
	blender := NewFrameBlender(1920, 1080)
	yuvSize := 1920 * 1080 * 3 / 2
	a := make([]byte, yuvSize)
	bSlice := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bSlice)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendWipe(a, bSlice, 0.5, WipeHLeft)
	}
}

// fillTestPattern fills a buffer with a repeating byte pattern to
// simulate realistic YUV frame data rather than zero-filled memory.
func fillTestPattern(buf []byte) {
	for i := range buf {
		buf[i] = byte(i % 256)
	}
}
