package asr

import (
	"math"
	"testing"
)

func TestMelSpectrogram_MatchesWhisperReference(t *testing.T) {
	mel := NewMelSpectrogram()

	// Same input as Python reference: 30s of 0.3 * sin(440Hz) at 16kHz
	audio := make([]float32, 16000*30)
	for i := range audio {
		audio[i] = float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}

	result := mel.Compute(audio)

	// Reference values from whisper.log_mel_spectrogram()
	refMin := float32(-0.6727)
	refMax := float32(1.3273)
	refMean := float32(-0.5404)
	refMel00 := float32(0.8724)

	// Find our min/max/mean
	var ourMin, ourMax, ourSum float32
	ourMin = result[0][0]
	ourMax = result[0][0]
	for _, row := range result {
		for _, v := range row {
			if v < ourMin { ourMin = v }
			if v > ourMax { ourMax = v }
			ourSum += v
		}
	}
	ourMean := ourSum / float32(80*3000)

	t.Logf("Reference: min=%.4f max=%.4f mean=%.4f mel[0][0]=%.4f", refMin, refMax, refMean, refMel00)
	t.Logf("Ours:      min=%.4f max=%.4f mean=%.4f mel[0][0]=%.4f", ourMin, ourMax, ourMean, result[0][0])
	t.Logf("mel[0][0:5]: %v", result[0][:5])

	tol := float32(0.05)
	if abs32(ourMin-refMin) > tol {
		t.Errorf("min mismatch: got %.4f want %.4f", ourMin, refMin)
	}
	if abs32(ourMax-refMax) > tol {
		t.Errorf("max mismatch: got %.4f want %.4f", ourMax, refMax)
	}
	if abs32(ourMean-refMean) > tol {
		t.Errorf("mean mismatch: got %.4f want %.4f", ourMean, refMean)
	}
	if abs32(result[0][0]-refMel00) > tol {
		t.Errorf("mel[0][0] mismatch: got %.4f want %.4f", result[0][0], refMel00)
	}
}

func abs32(x float32) float32 {
	if x < 0 { return -x }
	return x
}
