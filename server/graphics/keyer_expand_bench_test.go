package graphics

import "testing"

// BenchmarkExpandChromaMask_1080p benchmarks the chroma→luma mask expansion
// using the new per-row kernel + copy approach.
func BenchmarkExpandChromaMask_1080p(b *testing.B) {
	width, height := 1920, 1080
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight

	chromaMask := make([]byte, uvSize)
	mask := make([]byte, width*height)

	for i := range chromaMask {
		chromaMask[i] = byte(i % 256)
	}

	b.SetBytes(int64(width * height))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for chromaRow := 0; chromaRow < uvHeight; chromaRow++ {
			srcOff := chromaRow * uvWidth
			dstOff := (chromaRow * 2) * width
			expandChromaMaskRow(&mask[dstOff], &chromaMask[srcOff], uvWidth)
			copy(mask[dstOff+width:dstOff+2*width], mask[dstOff:dstOff+width])
		}
	}
}

// BenchmarkExpandChromaMask_1080p_GoBaseline benchmarks the original Go double-loop.
func BenchmarkExpandChromaMask_1080p_GoBaseline(b *testing.B) {
	width, height := 1920, 1080
	uvWidth := width / 2
	uvSize := uvWidth * (height / 2)

	chromaMask := make([]byte, uvSize)
	mask := make([]byte, width*height)

	for i := range chromaMask {
		chromaMask[i] = byte(i % 256)
	}

	b.SetBytes(int64(width * height))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for row := 0; row < height; row++ {
			for col := 0; col < width; col++ {
				uvIdx := (row/2)*uvWidth + (col / 2)
				mask[row*width+col] = chromaMask[uvIdx]
			}
		}
	}
}
