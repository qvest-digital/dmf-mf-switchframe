package replay

import (
	"math"
	"testing"
)

func generateSineWave(sampleRate, durationMs int, freqHz float64, channels int) []float32 {
	totalSamples := sampleRate * durationMs / 1000
	out := make([]float32, totalSamples*channels)
	for i := 0; i < totalSamples; i++ {
		v := float32(math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate)))
		for ch := 0; ch < channels; ch++ {
			out[i*channels+ch] = v
		}
	}
	return out
}

func BenchmarkFFT_Forward_4096(b *testing.B) {
	fft := newFFT(2048) // C2C size for 4096-point R2C
	data := make([]float32, 2*2048)
	for i := range data {
		data[i] = float32(i) / float32(len(data))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fft.forward(data)
	}
}

func BenchmarkR2C_4096(b *testing.B) {
	fft := newFFT(2048)
	input := make([]float32, 4096)
	output := make([]float32, 2*2049)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fft.r2c(input, output)
	}
}

func BenchmarkCartesianToPolar_2049(b *testing.B) {
	n := 2049
	re := make([]float32, n)
	im := make([]float32, n)
	mag := make([]float32, n)
	phase := make([]float32, n)
	for i := 0; i < n; i++ {
		re[i] = float32(i) / float32(n)
		im[i] = float32(n-i) / float32(n)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cartesianToPolar(re, im, mag, phase, n)
	}
}

func BenchmarkPhaseVocoder_Mono_HalfSpeed(b *testing.B) {
	// 1 second of mono 48kHz audio
	input := generateSineWave(48000, 1000, 440, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PhaseVocoderTimeStretch(input, 1, 48000, 0.5)
	}
}

func BenchmarkPhaseVocoder_Stereo_HalfSpeed(b *testing.B) {
	// 1 second of stereo 48kHz audio
	input := generateSineWave(48000, 1000, 440, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PhaseVocoderTimeStretch(input, 2, 48000, 0.5)
	}
}

func BenchmarkPhaseVocoder_Mono_QuarterSpeed(b *testing.B) {
	// 1 second of mono 48kHz audio at 0.25x (cascaded)
	input := generateSineWave(48000, 1000, 440, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PhaseVocoderTimeStretch(input, 1, 48000, 0.25)
	}
}

func BenchmarkWSOLA_Mono_HalfSpeed(b *testing.B) {
	// 1 second of mono 48kHz audio — for comparison
	input := generateSineWave(48000, 1000, 440, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		WSOLATimeStretch(input, 1, 48000, 0.5)
	}
}

func BenchmarkPhaseVocoder_10s_Stereo(b *testing.B) {
	// 10 seconds of stereo — target < 2 seconds
	input := generateSineWave(48000, 10000, 440, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PhaseVocoderTimeStretch(input, 2, 48000, 0.5)
	}
}

func BenchmarkWSOLA_vs_PhaseVocoder(b *testing.B) {
	// 10-second stereo clip — head-to-head comparison
	input := generateSineWave(48000, 10000, 440, 2)

	b.Run("WSOLA", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			WSOLATimeStretch(input, 2, 48000, 0.5)
		}
	})
	b.Run("PhaseVocoder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			PhaseVocoderTimeStretch(input, 2, 48000, 0.5)
		}
	})
}
