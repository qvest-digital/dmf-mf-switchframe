package replay

import (
	"math"
	"testing"
)

// BenchmarkFindBestOverlap_Mono benchmarks the cross-correlation search for mono audio.
func BenchmarkFindBestOverlap_Mono(b *testing.B) {
	// 48kHz mono, 1 second of 440Hz sine
	n := 48000
	input := make([]float32, n)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := make([]float32, n*2)
	copy(output, input)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		findBestOverlap(input, output, 512, 512, 1024, 1, 256)
	}
}

// BenchmarkFindBestOverlap_Stereo benchmarks the cross-correlation search for stereo audio.
func BenchmarkFindBestOverlap_Stereo(b *testing.B) {
	// 48kHz stereo, 1 second
	n := 48000 * 2
	input := make([]float32, n)
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
		input[i*2] = v
		input[i*2+1] = v * 0.5
	}
	output := make([]float32, n*2)
	copy(output, input)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		findBestOverlap(input, output, 512, 512*2, 1024, 2, 256)
	}
}
