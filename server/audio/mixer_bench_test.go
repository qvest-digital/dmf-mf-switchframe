package audio

import (
	"math"
	"testing"

	"github.com/zsiec/prism/media"
)

// BenchmarkMixerPassthrough benchmarks the passthrough path where a single
// active source at 0 dB bypasses decode/mix/encode entirely. This is the
// hot path during normal single-source operation.
func BenchmarkMixerPassthrough(b *testing.B) {
	var outputCount int
	mixer := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { outputCount++ },
	})
	defer mixer.Close()

	mixer.AddChannel("cam1")
	_ = mixer.SetAFV("cam1", true)
	mixer.OnProgramChange("cam1")

	// Simulate a realistic AAC frame (~128 bytes of encoded audio)
	frame := &media.AudioFrame{
		PTS:        0,
		Data:       make([]byte, 128),
		SampleRate: 48000,
		Channels:   2,
	}

	b.SetBytes(int64(len(frame.Data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		frame.PTS = int64(i) * 1024
		mixer.IngestFrame("cam1", frame)
	}
}

// BenchmarkDBToLinear benchmarks the dB-to-linear gain conversion, which is
// called on every frame in the mixing path for per-channel gain application.
func BenchmarkDBToLinear(b *testing.B) {
	// Test with a variety of dB values that appear in real usage
	dbValues := []float64{0, -6, -12, -24, -48, -96, 6, 12, math.Inf(-1)}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		DBToLinear(dbValues[i%len(dbValues)])
	}
}

// BenchmarkLinearToDBFS benchmarks the linear-to-dBFS conversion used in
// peak metering on every output frame.
func BenchmarkLinearToDBFS(b *testing.B) {
	// Test with realistic peak amplitude values
	values := []float64{0.0, 0.001, 0.01, 0.1, 0.5, 0.707, 1.0}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		LinearToDBFS(values[i%len(values)])
	}
}

// BenchmarkPeakLevel benchmarks peak level computation for stereo PCM,
// which runs on every audio frame for VU metering.
func BenchmarkPeakLevel_1024Samples(b *testing.B) {
	// 1024 stereo samples = one AAC frame at 48kHz
	pcm := make([]float32, 2048)
	for i := range pcm {
		// Simulate a sine wave
		pcm[i] = float32(math.Sin(float64(i) * 2 * math.Pi / 96))
	}

	b.SetBytes(int64(len(pcm) * 4)) // 4 bytes per float32
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		PeakLevel(pcm, 2)
	}
}

// BenchmarkEqualPowerCrossfade benchmarks the equal-power crossfade applied
// during audio cuts (one AAC frame, ~1024 stereo samples).
func BenchmarkEqualPowerCrossfade_1024Samples(b *testing.B) {
	n := 2048 // 1024 stereo samples
	oldPCM := make([]float32, n)
	newPCM := make([]float32, n)
	for i := range oldPCM {
		oldPCM[i] = float32(math.Sin(float64(i) * 2 * math.Pi / 96))
		newPCM[i] = float32(math.Sin(float64(i)*2*math.Pi/96 + math.Pi/4))
	}

	b.SetBytes(int64(n * 4)) // 4 bytes per float32
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		EqualPowerCrossfade(oldPCM, newPCM)
	}
}
