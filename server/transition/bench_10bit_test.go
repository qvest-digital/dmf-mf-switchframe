package transition

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// Comprehensive 10-bit SIMD benchmark suite for the transition package.
// Benchmarks Go reference vs SIMD dispatch for all 10-bit kernels at 1080p.

// ============================================================================
// BlendMix10 — full-frame 10-bit mix blend
// ============================================================================

func BenchmarkBlendMix10Row_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	ySize := w * h
	chromaSize := (w / 2) * h
	count := ySize + 2*chromaSize // all Y+Cb+Cr samples
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, count*2)
	bb := make([]byte, count*2)
	dst := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(bb[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 2 * 3)) // read 2 src + write 1 dst
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendMix10RowGo(&dst[0], &a[0], &bb[0], count, 128)
	}
}

func BenchmarkBlendMix10Row_ASM_1080p(b *testing.B) {
	w, h := 1920, 1080
	ySize := w * h
	chromaSize := (w / 2) * h
	count := ySize + 2*chromaSize
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, count*2)
	bb := make([]byte, count*2)
	dst := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(bb[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 2 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendMix10Row(&dst[0], &a[0], &bb[0], count, 128)
	}
}

// ============================================================================
// DownsampleAlpha10 — horizontal 2:1 alpha downsample
// ============================================================================

func BenchmarkDownsampleAlpha10Row_Go_1080pFull(b *testing.B) {
	// Full-frame chroma width output count: 1920/2 * 1080 rows
	count := 960 * 1080
	src := make([]byte, count*2)
	dst := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsampleAlpha10RowGo(&dst[0], &src[0], count)
	}
}

func BenchmarkDownsampleAlpha10Row_ASM_1080pFull(b *testing.B) {
	count := 960 * 1080
	src := make([]byte, count*2)
	dst := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsampleAlpha10Row(&dst[0], &src[0], count)
	}
}

// ============================================================================
// ScaleBilinear10 — bilinear scaler (vertical blend is SIMD hot path)
// ============================================================================

func BenchmarkScaleBilinear10_Go_1080pTo720p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 1280, 720
	src := make([]byte, srcW*srcH*4)
	dst := make([]byte, dstW*dstH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * dstH * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleBilinear10_ASM_1080pTo720p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 1280, 720
	src := make([]byte, srcW*srcH*4)
	dst := make([]byte, dstW*dstH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * dstH * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleBilinear10Dispatch(src, srcW, srcH, dst, dstW, dstH)
	}
}

// BlendRows10 row kernel (the SIMD hot path of the bilinear scaler)

func BenchmarkBlendRows10_Go_1080p(b *testing.B) {
	count := 1920 // one row of Y samples
	top := make([]byte, count*2)
	bot := make([]byte, count*2)
	dst := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(top[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(bot[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 2 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendRows10Go(&dst[0], &top[0], &bot[0], 32768, count)
	}
}

func BenchmarkBlendRows10_ASM_1080p(b *testing.B) {
	count := 1920
	top := make([]byte, count*2)
	bot := make([]byte, count*2)
	dst := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(top[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(bot[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 2 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendRows10(&dst[0], &top[0], &bot[0], 32768, count)
	}
}

// ============================================================================
// ScaleLanczos10 — Lanczos-3 scaler (horiz + vert row kernels are SIMD)
// ============================================================================

func BenchmarkScaleLanczos10_Go_1080pTo720p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 1280, 720
	src := make([]byte, srcW*srcH*4)
	dst := make([]byte, dstW*dstH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * dstH * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleLanczos10_ASM_1080pTo720p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 1280, 720
	src := make([]byte, srcW*srcH*4)
	dst := make([]byte, dstW*dstH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * dstH * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleLanczos10Dispatch(src, srcW, srcH, dst, dstW, dstH)
	}
}

// Lanczos row kernels at full-row width

func BenchmarkLanczosHoriz10Row_Go_1080p(b *testing.B) {
	srcW := 1920
	dstW := 1280
	hKernel := getLanczosKernel(srcW, dstW)
	src := make([]byte, srcW*2)
	dst := make([]float32, dstW)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < srcW; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lanczosHoriz10RowGo(dst, src, srcW, hKernel.offsets, hKernel.weights, hKernel.maxTaps)
	}
}

func BenchmarkLanczosHoriz10Row_ASM_1080p(b *testing.B) {
	srcW := 1920
	dstW := 1280
	hKernel := getLanczosKernel(srcW, dstW)
	src := make([]byte, srcW*2)
	dst := make([]float32, dstW)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < srcW; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lanczosHoriz10Row(dst, src, srcW, hKernel.offsets, hKernel.weights, hKernel.maxTaps)
	}
}

func BenchmarkLanczosVert10Row_Go_1080p(b *testing.B) {
	dstW := 1280
	srcH := 1080
	dstH := 720
	vKernel := getLanczosKernel(srcH, dstH)

	// Build a temp buffer with random data (dstW * srcH float32 values)
	temp := make([]float32, dstW*srcH)
	rng := rand.New(rand.NewSource(42))
	for i := range temp {
		temp[i] = float32(rng.Intn(1024))
	}
	dst := make([]byte, dstW*2)

	off := vKernel.offsets[dstH/2]
	wBase := (dstH / 2) * vKernel.maxTaps

	b.SetBytes(int64(dstW * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lanczosVert10RowGo(dst, temp, dstW, off, vKernel.weights[wBase:wBase+vKernel.maxTaps], vKernel.maxTaps)
	}
}

func BenchmarkLanczosVert10Row_ASM_1080p(b *testing.B) {
	dstW := 1280
	srcH := 1080
	dstH := 720
	vKernel := getLanczosKernel(srcH, dstH)

	temp := make([]float32, dstW*srcH)
	rng := rand.New(rand.NewSource(42))
	for i := range temp {
		temp[i] = float32(rng.Intn(1024))
	}
	dst := make([]byte, dstW*2)

	off := vKernel.offsets[dstH/2]
	wBase := (dstH / 2) * vKernel.maxTaps

	b.SetBytes(int64(dstW * 2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lanczosVert10Row(dst, temp, dstW, off, vKernel.weights[wBase:wBase+vKernel.maxTaps], vKernel.maxTaps)
	}
}
