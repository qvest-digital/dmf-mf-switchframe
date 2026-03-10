package graphics

import "testing"

func makeBenchYUV420Frame(width, height int, yVal, cbVal, crVal byte) []byte {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	frame := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		frame[i] = yVal
	}
	for i := 0; i < uvSize; i++ {
		frame[ySize+i] = cbVal
		frame[ySize+uvSize+i] = crVal
	}
	return frame
}

// BenchmarkKeyProcessor_ChromaKey_1080p benchmarks the full chroma key pipeline
// including mask allocation, keying, and compositing at 1080p.
func BenchmarkKeyProcessor_ChromaKey_1080p(b *testing.B) {
	width, height := 1920, 1080

	kp := NewKeyProcessor()
	kp.SetKey("green-screen", KeyConfig{
		Type:          KeyTypeChroma,
		Enabled:       true,
		KeyColorY:     182,
		KeyColorCb:    30,
		KeyColorCr:    12,
		Similarity:    0.3,
		Smoothness:    0.1,
		SpillSuppress: 0.5,
	})

	bg := makeBenchYUV420Frame(width, height, 128, 128, 128)
	fill := makeBenchYUV420Frame(width, height, 200, 30, 12) // green screen content
	fills := map[string][]byte{"green-screen": fill}

	b.SetBytes(int64(width * height))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		kp.Process(bg, fills, width, height)
	}
}

// BenchmarkKeyProcessor_LumaKey_1080p benchmarks the full luma key pipeline
// including mask allocation, keying, and compositing at 1080p.
func BenchmarkKeyProcessor_LumaKey_1080p(b *testing.B) {
	width, height := 1920, 1080

	kp := NewKeyProcessor()
	kp.SetKey("luma-src", KeyConfig{
		Type:     KeyTypeLuma,
		Enabled:  true,
		LowClip:  0.1,
		HighClip: 0.9,
		Softness: 0.05,
	})

	bg := makeBenchYUV420Frame(width, height, 128, 128, 128)
	fill := makeBenchYUV420Frame(width, height, 200, 100, 140)
	fills := map[string][]byte{"luma-src": fill}

	b.SetBytes(int64(width * height))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		kp.Process(bg, fills, width, height)
	}
}

// BenchmarkChromaKeyWithSpillColor_1080p benchmarks just the mask generation.
func BenchmarkChromaKeyWithSpillColor_1080p(b *testing.B) {
	width, height := 1920, 1080
	frame := makeBenchYUV420Frame(width, height, 200, 30, 12)
	keyColor := YCbCr{Y: 182, Cb: 30, Cr: 12}

	b.SetBytes(int64(width * height))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ChromaKeyWithSpillColor(frame, width, height, keyColor, 0.3, 0.1, 0.5, 128, 128)
	}
}

// BenchmarkLumaKey_1080p_Full benchmarks just the luma mask generation.
func BenchmarkLumaKey_1080p_Full(b *testing.B) {
	width, height := 1920, 1080
	frame := makeBenchYUV420Frame(width, height, 128, 128, 128)

	b.SetBytes(int64(width * height))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = LumaKey(frame, width, height, 0.1, 0.9, 0.05)
	}
}
