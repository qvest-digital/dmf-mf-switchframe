package transition

import "testing"

// BenchmarkBlendMix720p benchmarks the YUV420 mix blend at 720p resolution.
// This is the most common transition path: linear interpolation between two
// source frames in the native YUV domain.
func BenchmarkBlendMix720p(b *testing.B) {
	blender := mustNewFrameBlender(b,1280, 720)
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
	blender := mustNewFrameBlender(b,1920, 1080)
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
	blender := mustNewFrameBlender(b,1920, 1080)
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
	blender := mustNewFrameBlender(b,1920, 1080)
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
func BenchmarkBlendWipe1080p(b *testing.B) {
	blender := mustNewFrameBlender(b,1920, 1080)
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

// BenchmarkBlendWipeVTop1080p benchmarks a vertical top-to-bottom wipe at 1080p.
func BenchmarkBlendWipeVTop1080p(b *testing.B) {
	blender := mustNewFrameBlender(b,1920, 1080)
	yuvSize := 1920 * 1080 * 3 / 2
	a := make([]byte, yuvSize)
	bSlice := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bSlice)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendWipe(a, bSlice, 0.5, WipeVTop)
	}
}

// BenchmarkBlendWipeBox1080p benchmarks a box-center-out wipe at 1080p (per-pixel).
func BenchmarkBlendWipeBox1080p(b *testing.B) {
	blender := mustNewFrameBlender(b,1920, 1080)
	yuvSize := 1920 * 1080 * 3 / 2
	a := make([]byte, yuvSize)
	bSlice := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bSlice)

	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		blender.BlendWipe(a, bSlice, 0.5, WipeBoxCenterOut)
	}
}

func BenchmarkWipeAlphaHLeft1080p(b *testing.B) {
	blender := mustNewFrameBlender(b,1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.generateWipeAlpha(0.5, WipeHLeft)
	}
}

func BenchmarkWipeAlphaVTop1080p(b *testing.B) {
	blender := mustNewFrameBlender(b,1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.generateWipeAlpha(0.5, WipeVTop)
	}
}

func BenchmarkWipeAlphaBoxCenterOut1080p(b *testing.B) {
	blender := mustNewFrameBlender(b,1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.generateWipeAlpha(0.5, WipeBoxCenterOut)
	}
}

// --- 4K Benchmarks ---

func BenchmarkBlendMix4K(b *testing.B) {
	blender := mustNewFrameBlender(b,3840, 2160)
	yuvSize := 3840 * 2160 * 3 / 2
	a := make([]byte, yuvSize)
	bs := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bs)
	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.BlendMix(a, bs, 0.5)
	}
}

func BenchmarkBlendDip4K(b *testing.B) {
	blender := mustNewFrameBlender(b,3840, 2160)
	yuvSize := 3840 * 2160 * 3 / 2
	a := make([]byte, yuvSize)
	bs := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bs)
	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.BlendDip(a, bs, 0.5)
	}
}

func BenchmarkBlendFTB4K(b *testing.B) {
	blender := mustNewFrameBlender(b,3840, 2160)
	yuvSize := 3840 * 2160 * 3 / 2
	a := make([]byte, yuvSize)
	fillTestPattern(a)
	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.BlendFTB(a, 0.5)
	}
}

func BenchmarkBlendWipe4K(b *testing.B) {
	blender := mustNewFrameBlender(b,3840, 2160)
	yuvSize := 3840 * 2160 * 3 / 2
	a := make([]byte, yuvSize)
	bs := make([]byte, yuvSize)
	fillTestPattern(a)
	fillTestPattern(bs)
	b.SetBytes(int64(yuvSize))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blender.BlendWipe(a, bs, 0.5, WipeHLeft)
	}
}

// --- Per-kernel benchmarks ---

func BenchmarkKernelUniform1080p(b *testing.B) {
	n := 1920 * 1080 * 3 / 2
	a := make([]byte, n)
	bs := make([]byte, n)
	dst := make([]byte, n)
	fillTestPattern(a)
	fillTestPattern(bs)
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendUniform(&dst[0], &a[0], &bs[0], n, 128, 128)
	}
}

func BenchmarkKernelFadeConst1080p(b *testing.B) {
	n := 1920 * 1080
	src := make([]byte, n)
	dst := make([]byte, n)
	fillTestPattern(src)
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendFadeConst(&dst[0], &src[0], n, 128, 128*128)
	}
}

func BenchmarkKernelAlpha1080p(b *testing.B) {
	n := 1920 * 1080
	a := make([]byte, n)
	bs := make([]byte, n)
	alpha := make([]byte, n)
	dst := make([]byte, n)
	fillTestPattern(a)
	fillTestPattern(bs)
	fillTestPattern(alpha)
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendAlpha(&dst[0], &a[0], &bs[0], &alpha[0], n)
	}
}

// fillTestPattern fills a buffer with a repeating byte pattern to
// simulate realistic YUV frame data rather than zero-filled memory.
func fillTestPattern(buf []byte) {
	for i := range buf {
		buf[i] = byte(i % 256)
	}
}
