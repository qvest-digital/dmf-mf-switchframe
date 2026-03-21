package asr

import (
	"math"
	"testing"
)

// sineWave generates a mono sine wave at the given frequency and sample rate.
func sineWave(freq float64, sampleRate int, numSamples int) []float32 {
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = float32(math.Sin(2.0 * math.Pi * freq * float64(i) / float64(sampleRate)))
	}
	return samples
}

func TestMelSpectrogram_OutputShape(t *testing.T) {
	mel := NewMelSpectrogram()

	// 30 seconds of 440Hz sine at 16kHz
	samples := sineWave(440, 16000, 16000*30)

	result := mel.Compute(samples)

	if len(result) != 80 {
		t.Fatalf("expected 80 mel bins, got %d", len(result))
	}
	for i, row := range result {
		if len(row) != 3000 {
			t.Fatalf("expected 3000 frames in mel bin %d, got %d", i, len(row))
		}
	}
}

func TestMelSpectrogram_NonSilentOutput(t *testing.T) {
	mel := NewMelSpectrogram()

	// 30 seconds of 440Hz sine at 16kHz
	samples := sineWave(440, 16000, 16000*30)

	result := mel.Compute(samples)

	// A 440Hz tone should produce non-zero energy in several mel bins.
	// After Whisper normalization, values should not all be the same.
	allSame := true
	firstVal := result[0][0]
	for _, row := range result {
		for _, v := range row {
			if v != firstVal {
				allSame = false
				break
			}
		}
		if !allSame {
			break
		}
	}

	if allSame {
		t.Error("expected varying output for tonal input, but all values are identical")
	}

	// Check that there is some energy (not all zeros)
	hasNonZero := false
	for _, row := range result {
		for _, v := range row {
			if v != 0 {
				hasNonZero = true
				break
			}
		}
		if hasNonZero {
			break
		}
	}

	if !hasNonZero {
		t.Error("expected non-zero output for 440Hz sine wave")
	}
}

func TestMelSpectrogram_SilenceProducesLowEnergy(t *testing.T) {
	mel := NewMelSpectrogram()

	// 30 seconds of silence
	samples := make([]float32, 16000*30)

	result := mel.Compute(samples)

	// After log10 and normalization, silence should produce uniform values.
	// With Whisper normalization:
	//   log10(1e-10) = -10, max for silence is -10
	//   clamped = max(-10, -10 - 8) = -10
	//   scaled = (-10 - (-18)) / 8 = 8/8 = 1.0
	//   final = (1.0 + 4.0) / 4.0 = 1.25
	// All values should be 1.25 (uniform silence at the max of the dynamic range).
	expected := float32(1.25)
	for i, row := range result {
		for j, v := range row {
			if v < expected-0.01 || v > expected+0.01 {
				t.Errorf("silence value at [%d][%d] = %f, expected ~%f", i, j, v, expected)
				return
			}
		}
	}
}

func TestMelSpectrogram_ShortAudioPadsToFull(t *testing.T) {
	mel := NewMelSpectrogram()

	// Only 5 seconds of audio (should be zero-padded to 30s)
	samples := sineWave(440, 16000, 16000*5)

	result := mel.Compute(samples)

	if len(result) != 80 {
		t.Fatalf("expected 80 mel bins, got %d", len(result))
	}
	for i, row := range result {
		if len(row) != 3000 {
			t.Fatalf("expected 3000 frames in mel bin %d, got %d", i, len(row))
		}
	}

	// The first ~500 frames should have 440Hz energy, while later frames
	// (from padded silence) should have lower energy. Verify they differ.
	earlyEnergy := float64(0)
	for mel := 0; mel < 80; mel++ {
		earlyEnergy += float64(result[mel][100]) // frame in tonal region
	}

	lateEnergy := float64(0)
	for mel := 0; mel < 80; mel++ {
		lateEnergy += float64(result[mel][2500]) // frame in padded-silence region
	}

	if earlyEnergy == lateEnergy {
		t.Error("expected tonal region and padded-silence region to differ")
	}
}

func TestMelSpectrogram_DeterministicOutput(t *testing.T) {
	mel := NewMelSpectrogram()

	samples := sineWave(440, 16000, 16000*30)

	result1 := mel.Compute(samples)
	result2 := mel.Compute(samples)

	for i := range result1 {
		for j := range result1[i] {
			if result1[i][j] != result2[i][j] {
				t.Fatalf("non-deterministic output at [%d][%d]: %f vs %f",
					i, j, result1[i][j], result2[i][j])
			}
		}
	}
}

func TestMelSpectrogram_FrequencyLocalization(t *testing.T) {
	mel := NewMelSpectrogram()

	// A low-frequency tone (200Hz) should produce energy in low mel bins,
	// while a high-frequency tone (4000Hz) should produce energy in high mel bins.
	lowTone := sineWave(200, 16000, 16000*30)
	highTone := sineWave(4000, 16000, 16000*30)

	lowResult := mel.Compute(lowTone)
	highResult := mel.Compute(highTone)

	// Sum energy across time for the lowest 10 mel bins and highest 10 mel bins
	lowBinsLowTone := float64(0)
	highBinsLowTone := float64(0)
	lowBinsHighTone := float64(0)
	highBinsHighTone := float64(0)

	for frame := 0; frame < 3000; frame++ {
		for m := 0; m < 10; m++ {
			lowBinsLowTone += float64(lowResult[m][frame])
			lowBinsHighTone += float64(highResult[m][frame])
		}
		for m := 70; m < 80; m++ {
			highBinsLowTone += float64(lowResult[m][frame])
			highBinsHighTone += float64(highResult[m][frame])
		}
	}

	// Low tone should have more energy in low bins than high tone does
	if lowBinsLowTone <= lowBinsHighTone {
		t.Errorf("expected low tone to have more energy in low mel bins: low=%f high=%f",
			lowBinsLowTone, lowBinsHighTone)
	}

	// High tone should have more energy in high bins than low tone does
	if highBinsHighTone <= highBinsLowTone {
		t.Errorf("expected high tone to have more energy in high mel bins: high=%f low=%f",
			highBinsHighTone, highBinsLowTone)
	}
}

func BenchmarkMelSpectrogram_Compute(b *testing.B) {
	mel := NewMelSpectrogram()
	samples := sineWave(440, 16000, 16000*30)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mel.Compute(samples)
	}
}
