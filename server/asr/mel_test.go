package asr

import (
	"math"
	"testing"
)

func TestHannWindow(t *testing.T) {
	w := hannWindow(512)
	if len(w) != 512 {
		t.Fatalf("expected length 512, got %d", len(w))
	}

	// Hann window: w[0] = 0 (periodic), w[N/2] should be ~1.0.
	if w[0] != 0 {
		t.Errorf("expected w[0] = 0, got %f", w[0])
	}

	// Middle value should be close to 1.0.
	mid := w[256]
	if mid < 0.99 || mid > 1.01 {
		t.Errorf("expected w[256] ~ 1.0, got %f", mid)
	}

	// All values should be in [0, 1].
	for i, v := range w {
		if v < 0 || v > 1.001 {
			t.Errorf("w[%d] = %f, out of range [0, 1]", i, v)
		}
	}
}

func TestHzMelConversion(t *testing.T) {
	// Round-trip Hz -> Mel -> Hz should be identity.
	for _, hz := range []float64{0, 100, 440, 1000, 4000, 8000} {
		mel := hzToMel(hz)
		back := melToHz(mel)
		if math.Abs(back-hz) > 1e-6 {
			t.Errorf("round-trip failed for %.1f Hz: got %.6f", hz, back)
		}
	}

	// Mel of 0 Hz = 0.
	if hzToMel(0) != 0 {
		t.Errorf("expected mel(0) = 0, got %f", hzToMel(0))
	}

	// Mel should be monotonically increasing.
	prev := -1.0
	for hz := 0.0; hz <= 8000; hz += 100 {
		m := hzToMel(hz)
		if m < prev {
			t.Errorf("mel not monotonically increasing at %.0f Hz", hz)
		}
		prev = m
	}
}

func TestMelFilterBank(t *testing.T) {
	bank := melFilterBank(128, 512, 16000, 0, 8000)

	if len(bank) != 128 {
		t.Fatalf("expected 128 mel bins, got %d", len(bank))
	}

	nBins := 512/2 + 1
	for i, filter := range bank {
		if len(filter) != nBins {
			t.Fatalf("filter %d: expected %d freq bins, got %d", i, nBins, len(filter))
		}
	}

	// Most filters should have at least one non-zero value.
	// The very lowest filter(s) may be all zeros if the mel-spaced triangle
	// is narrower than one FFT bin — this is normal for low frequencies.
	zeroCount := 0
	for _, filter := range bank {
		hasNonZero := false
		for _, v := range filter {
			if v > 0 {
				hasNonZero = true
				break
			}
		}
		if !hasNonZero {
			zeroCount++
		}
	}
	if zeroCount > 3 {
		t.Errorf("too many all-zero filters: %d out of %d", zeroCount, len(bank))
	}

	// Filter values should be in [0, 1].
	for i, filter := range bank {
		for k, v := range filter {
			if v < 0 || v > 1.001 {
				t.Errorf("filter[%d][%d] = %f, out of range [0, 1]", i, k, v)
			}
		}
	}
}

func TestMelFFT_KnownSignal(t *testing.T) {
	// Test FFT with a pure cosine signal at a known frequency.
	n := 512
	fft := newMelFFT(n)

	// Generate a cosine at bin 10 (frequency = 10 * sampleRate / n).
	data := make([]float64, 2*n)
	for i := 0; i < n; i++ {
		data[2*i] = math.Cos(2 * math.Pi * 10 * float64(i) / float64(n))
		data[2*i+1] = 0
	}

	fft.forward(data)

	// The magnitude at bin 10 should be dominant.
	// For a cosine: FFT gives N/2 at the positive and negative frequency bins.
	mag10 := math.Sqrt(data[20]*data[20] + data[21]*data[21])

	// Check that bin 10 has significant energy.
	if mag10 < float64(n)/4 {
		t.Errorf("expected strong bin 10, got magnitude %f (expected ~%f)", mag10, float64(n)/2)
	}

	// All other bins (except mirror at N-10) should be much smaller.
	for k := 0; k < n; k++ {
		if k == 10 || k == n-10 {
			continue
		}
		mag := math.Sqrt(data[2*k]*data[2*k] + data[2*k+1]*data[2*k+1])
		if mag > 1.0 {
			t.Errorf("bin %d has unexpected magnitude %f", k, mag)
		}
	}
}

func TestMelSpectrogram_ShortAudio(t *testing.T) {
	m := NewMelSpectrogram()

	// Audio shorter than window size should return nil.
	short := make([]float32, 100)
	result, frames := m.Compute(short)
	if result != nil || frames != 0 {
		t.Errorf("expected nil/0 for short audio, got %d frames", frames)
	}
}

func TestMelSpectrogram_Silence(t *testing.T) {
	m := NewMelSpectrogram()

	// 1 second of silence at 16kHz.
	silence := make([]float32, 16000)
	result, frames := m.Compute(silence)
	if frames == 0 {
		t.Fatal("expected at least one frame for 16000 samples")
	}

	// Expected frames: (16000 - 512) / 160 + 1 = 96.875 => 97.
	expectedFrames := (16000-512)/160 + 1
	if frames != expectedFrames {
		t.Errorf("expected %d frames, got %d", expectedFrames, frames)
	}

	if len(result) != 128*frames {
		t.Errorf("expected %d values, got %d", 128*frames, len(result))
	}
}

func TestMelSpectrogram_Tone(t *testing.T) {
	m := NewMelSpectrogram()

	// Generate a 1kHz tone at 16kHz sample rate, 1 second.
	audio := make([]float32, 16000)
	for i := range audio {
		audio[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/16000))
	}

	result, frames := m.Compute(audio)
	if frames == 0 {
		t.Fatal("expected at least one frame")
	}
	if len(result) != 128*frames {
		t.Fatalf("expected %d values, got %d", 128*frames, len(result))
	}

	// After normalization, the mel bins corresponding to 1kHz should have
	// positive values (above average), while others should be below.
	// The exact bin depends on mel spacing, but 1kHz is around bin 40-50.
	// Just verify the output is finite and normalized (mean ~ 0 per feature).
	for f := 0; f < 128; f++ {
		var sum float64
		for fr := 0; fr < frames; fr++ {
			v := float64(result[f*frames+fr])
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("mel[%d][%d] is not finite: %f", f, fr, v)
			}
			sum += v
		}
		mean := sum / float64(frames)
		// After normalization, mean should be very close to 0.
		if math.Abs(mean) > 0.01 {
			t.Errorf("mel bin %d: mean = %f (expected ~0 after normalization)", f, mean)
		}
	}
}

func TestMelSpectrogram_OutputLayout(t *testing.T) {
	m := NewMelSpectrogram()

	// Generate exactly enough for 2 frames.
	// 2 frames: need windowSize + hopSize = 512 + 160 = 672 samples.
	audio := make([]float32, 672)
	for i := range audio {
		audio[i] = float32(0.3 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}

	result, frames := m.Compute(audio)
	if frames != 2 {
		t.Fatalf("expected 2 frames, got %d", frames)
	}
	if len(result) != 128*2 {
		t.Fatalf("expected 256 values, got %d", len(result))
	}

	// Verify layout: result[bin*numFrames + frame].
	// Access pattern should not panic.
	for bin := 0; bin < 128; bin++ {
		for fr := 0; fr < frames; fr++ {
			_ = result[bin*frames+fr]
		}
	}
}

func TestMelSpectrogram_DifferentSignals(t *testing.T) {
	// Test that the mel filter bank correctly places energy at different
	// frequency positions for different tones. We test the raw power spectrum
	// via the filter bank directly, avoiding normalization effects.
	bank := melFilterBank(128, 512, 16000, 0, 8000)

	// Compute the mel bin energy for a 500Hz and 4000Hz pure tone.
	// At 16kHz with FFT size 512, bin k corresponds to freq = k * 16000/512.
	// 500Hz -> bin 16, 4000Hz -> bin 128.
	nBins := 512/2 + 1

	// Create a power spectrum with a spike at 500Hz (bin 16).
	power500 := make([]float64, nBins)
	power500[16] = 1.0

	// Create a power spectrum with a spike at 4000Hz (bin 128).
	power4000 := make([]float64, nBins)
	power4000[128] = 1.0

	// Find peak mel bin for each.
	peakBin500 := 0
	peakEnergy500 := 0.0
	peakBin4000 := 0
	peakEnergy4000 := 0.0

	for bin := 0; bin < 128; bin++ {
		var e500, e4000 float64
		for k := 0; k < nBins; k++ {
			e500 += bank[bin][k] * power500[k]
			e4000 += bank[bin][k] * power4000[k]
		}
		if e500 > peakEnergy500 {
			peakEnergy500 = e500
			peakBin500 = bin
		}
		if e4000 > peakEnergy4000 {
			peakEnergy4000 = e4000
			peakBin4000 = bin
		}
	}

	// The peak bins should differ significantly.
	if peakBin500 >= peakBin4000 {
		t.Errorf("expected 500Hz peak (bin %d) < 4000Hz peak (bin %d)", peakBin500, peakBin4000)
	}

	// Both should have non-zero energy.
	if peakEnergy500 == 0 {
		t.Error("500Hz tone has zero mel energy")
	}
	if peakEnergy4000 == 0 {
		t.Error("4000Hz tone has zero mel energy")
	}

	t.Logf("500Hz peak at mel bin %d (energy %.4f), 4000Hz peak at mel bin %d (energy %.4f)",
		peakBin500, peakEnergy500, peakBin4000, peakEnergy4000)
}

func BenchmarkMelSpectrogram(b *testing.B) {
	m := NewMelSpectrogram()

	// 1 second of audio at 16kHz.
	audio := make([]float32, 16000)
	for i := range audio {
		audio[i] = float32(0.3 * math.Sin(2*math.Pi*1000*float64(i)/16000))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.Compute(audio)
	}
}
