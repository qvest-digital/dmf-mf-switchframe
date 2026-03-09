package audio

import (
	"math"
	"testing"
)

func BenchmarkPeakLevel_Stereo2048(b *testing.B) {
	// Typical mix cycle: 2048 interleaved stereo samples (1024 per channel)
	n := 2048
	pcm := make([]float32, n)
	for i := range pcm {
		pcm[i] = float32(math.Sin(float64(i) * 0.01))
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PeakLevel(pcm, 2)
	}
}

func BenchmarkPeakLevel_Mono2048(b *testing.B) {
	n := 2048
	pcm := make([]float32, n)
	for i := range pcm {
		pcm[i] = float32(math.Sin(float64(i) * 0.01))
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PeakLevel(pcm, 1)
	}
}
