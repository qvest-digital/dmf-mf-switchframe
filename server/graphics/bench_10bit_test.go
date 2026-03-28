package graphics

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// Comprehensive 10-bit SIMD benchmark suite for the graphics package.
// Benchmarks Go reference vs SIMD dispatch for all 10-bit kernels at 1080p.

// ============================================================================
// LumaKey10 — 10-bit luma key mask generation (row kernel)
// ============================================================================

func BenchmarkLumaKey10Row_Go_1080pFull(b *testing.B) {
	count := 1920 * 1080
	yData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(yData[i*2:], uint16(rng.Intn(1024)))
	}
	mask := make([]byte, count)
	b.SetBytes(int64(count*2 + count)) // read Y + write mask
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lumaKey10RowGo(&mask[0], &yData[0], count, 200, 800)
	}
}

func BenchmarkLumaKey10Row_ASM_1080pFull(b *testing.B) {
	count := 1920 * 1080
	yData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(yData[i*2:], uint16(rng.Intn(1024)))
	}
	mask := make([]byte, count)
	b.SetBytes(int64(count*2 + count))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lumaKey10Row(&mask[0], &yData[0], count, 200, 800)
	}
}

// ============================================================================
// ChromaKey10 — 10-bit chroma key mask generation (row kernel)
// ============================================================================

func BenchmarkChromaKey10Row_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	mask := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
	}

	tCb, tCr := 512, 512
	tolerance, softness := 80, 40
	simDistSq := tolerance * tolerance
	totalDist := tolerance + softness
	totalDistSq := totalDist * totalDist
	rangeSq := totalDistSq - simDistSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	b.SetBytes(int64(count*4 + count)) // read Cb+Cr + write mask
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chromaKey10RowGo(&mask[0], &cbData[0], &crData[0], count,
			tCb, tCr, simDistSq, totalDistSq, invRange)
	}
}

func BenchmarkChromaKey10Row_ASM_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	mask := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
	}

	tCb, tCr := 512, 512
	tolerance, softness := 80, 40
	simDistSq := tolerance * tolerance
	totalDist := tolerance + softness
	totalDistSq := totalDist * totalDist
	rangeSq := totalDistSq - simDistSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	b.SetBytes(int64(count*4 + count))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chromaKey10Row(&mask[0], &cbData[0], &crData[0], count,
			tCb, tCr, simDistSq, totalDistSq, invRange)
	}
}

// ============================================================================
// AlphaBlendRGBA10 — Y row kernel (per-pixel RGBA→YUV alpha blend)
// ============================================================================

func BenchmarkAlphaBlendRGBA10YRow_Go_1080p(b *testing.B) {
	count := 1920 * 1080
	yPlane := make([]byte, count*2)
	rgba := make([]byte, count*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(yPlane[i*2:], uint16(rng.Intn(1024)))
	}
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count*2 + count*4)) // Y plane + RGBA
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBA10YRowGo(&yPlane[0], &rgba[0], count)
	}
}

func BenchmarkAlphaBlendRGBA10YRow_ASM_1080p(b *testing.B) {
	count := 1920 * 1080
	yPlane := make([]byte, count*2)
	rgba := make([]byte, count*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(yPlane[i*2:], uint16(rng.Intn(1024)))
	}
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count*2 + count*4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBA10YRow(&yPlane[0], &rgba[0], count)
	}
}

// AlphaBlendRGBA10 — UV row kernel (chroma alpha blend)

func BenchmarkAlphaBlendRGBA10UVRow_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbPlane := make([]byte, count*2)
	crPlane := make([]byte, count*2)
	rgba := make([]byte, w*h*4) // full-res RGBA
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbPlane[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crPlane[i*2:], uint16(rng.Intn(1024)))
	}
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count*4 + count*2*4)) // Cb+Cr + RGBA (2 pixels per chroma)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBA10UVRowGo(&cbPlane[0], &crPlane[0], &rgba[0], count)
	}
}

func BenchmarkAlphaBlendRGBA10UVRow_ASM_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbPlane := make([]byte, count*2)
	crPlane := make([]byte, count*2)
	rgba := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbPlane[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crPlane[i*2:], uint16(rng.Intn(1024)))
	}
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count*4 + count*2*4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBA10UVRow(&cbPlane[0], &crPlane[0], &rgba[0], count)
	}
}

// ============================================================================
// KeyMaskBlend10 — mask-based fill/bg blend for upstream keying
// ============================================================================

func BenchmarkKeyMaskBlend10Row_Go_1080pFull(b *testing.B) {
	count := 1920 * 1080
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(fill[i*2:], uint16(rng.Intn(1024)))
		mask[i] = byte(rng.Intn(256))
	}
	bgCopy := make([]byte, count*2)

	b.SetBytes(int64(count*2*2 + count)) // bg + fill + mask
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(bgCopy, bg)
		keyMaskBlend10RowGo(&bgCopy[0], &fill[0], &mask[0], count)
	}
}

func BenchmarkKeyMaskBlend10Row_ASM_1080pFull(b *testing.B) {
	count := 1920 * 1080
	bg := make([]byte, count*2)
	fill := make([]byte, count*2)
	mask := make([]byte, count)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(bg[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(fill[i*2:], uint16(rng.Intn(1024)))
		mask[i] = byte(rng.Intn(256))
	}
	bgCopy := make([]byte, count*2)

	b.SetBytes(int64(count*2*2 + count))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(bgCopy, bg)
		keyMaskBlend10Row(&bgCopy[0], &fill[0], &mask[0], count)
	}
}

// ============================================================================
// ExpandChromaMask10 — chroma→luma mask expansion (byte duplication)
// ============================================================================

func BenchmarkExpandChromaMask10Row_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	src := make([]byte, count)
	dst := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count + count*2)) // read src + write dst
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expandChromaMask10RowGo(&dst[0], &src[0], count)
	}
}

func BenchmarkExpandChromaMask10Row_ASM_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	src := make([]byte, count)
	dst := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}
	b.SetBytes(int64(count + count*2))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expandChromaMask10Row(&dst[0], &src[0], count)
	}
}

// ============================================================================
// SpillSuppress10 — chroma spill suppression (float32 distance-based)
// ============================================================================

func BenchmarkSpillSuppress10Row_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 4)) // read+write Cb+Cr
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spillSuppress10RowGo(&cbData[0], &crData[0], count, 300, 700, 0.5, 1.0/(200*200), 512, 512)
	}
}

func BenchmarkSpillSuppress10Row_ASM_1080p(b *testing.B) {
	w, h := 1920, 1080
	chromaW := w / 2
	count := chromaW * h
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(count * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spillSuppress10Row(&cbData[0], &crData[0], count, 300, 700, 0.5, 1.0/(200*200), 512, 512)
	}
}
