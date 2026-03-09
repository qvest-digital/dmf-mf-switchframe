package graphics

import "testing"

// BenchmarkAlphaBlendChromaRow_1080p_FullOpaque benchmarks the chroma kernel
// directly at 1080p with fully opaque overlay.
func BenchmarkAlphaBlendChromaRow_1080p_FullOpaque(b *testing.B) {
	width, height := 1920, 1080
	halfW := width / 2
	halfH := height / 2
	yuv := makeYUV420(width, height, 128, 128, 128)
	ySize := width * height
	cbOffset := ySize
	crOffset := ySize + halfW*halfH

	rgba := make([]byte, width*height*4)
	for i := 0; i < width*height; i++ {
		rgba[i*4] = 200   // R
		rgba[i*4+1] = 150 // G
		rgba[i*4+2] = 100 // B
		rgba[i*4+3] = 200 // A
	}

	b.SetBytes(int64(halfW) * int64(halfH) * 2) // Cb + Cr bytes written
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for row := 0; row < height; row += 2 {
			rgbaStart := (row * width) * 4
			uvStart := (row / 2) * halfW
			alphaBlendRGBAChromaRow(&yuv[cbOffset+uvStart], &yuv[crOffset+uvStart], &rgba[rgbaStart], halfW, 256)
		}
	}
}

// BenchmarkAlphaBlendChromaRow_1080p_LowerThird benchmarks with 15% active overlay.
func BenchmarkAlphaBlendChromaRow_1080p_LowerThird(b *testing.B) {
	width, height := 1920, 1080
	halfW := width / 2
	halfH := height / 2
	yuv := makeYUV420(width, height, 128, 128, 128)
	ySize := width * height
	cbOffset := ySize
	crOffset := ySize + halfW*halfH

	rgba := make([]byte, width*height*4)
	cutoff := int(float64(height) * 0.85)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			idx := (row*width + col) * 4
			if row >= cutoff {
				rgba[idx] = 255
				rgba[idx+1] = 255
				rgba[idx+2] = 255
				rgba[idx+3] = 200
			}
		}
	}

	b.SetBytes(int64(halfW) * int64(halfH) * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for row := 0; row < height; row += 2 {
			rgbaStart := (row * width) * 4
			uvStart := (row / 2) * halfW
			alphaBlendRGBAChromaRow(&yuv[cbOffset+uvStart], &yuv[crOffset+uvStart], &rgba[rgbaStart], halfW, 256)
		}
	}
}
