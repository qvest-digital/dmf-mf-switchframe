//go:build correctness

package replay

import (
	"math"
	"math/rand"
	"testing"
)

// TestFFTKnownSine runs an FFT on a sine wave whose frequency lands exactly
// on a bin (no spectral leakage), and verifies the peak is at the correct bin
// with all other bins at least 40 dB below.
func TestFFTKnownSine(t *testing.T) {
	const N = 1024 // real samples (R2C FFT size)
	const sampleRate = 48000

	halfN := N / 2
	fft := newFFT(halfN) // R2C uses half-size C2C internally

	// Choose a frequency that lands exactly on a bin to avoid spectral leakage.
	// bin k corresponds to frequency k * sampleRate / N.
	// We pick bin 20: freq = 20 * 48000 / 1024 = 937.5 Hz
	const targetBin = 20
	freq := float64(targetBin) * float64(sampleRate) / float64(N)

	input := make([]float32, N)
	for i := 0; i < N; i++ {
		input[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / float64(sampleRate)))
	}

	// Perform R2C FFT
	output := make([]float32, 2*(halfN+1))
	fft.r2c(input, output)

	// Find the bin with maximum magnitude
	maxMag := float64(0)
	maxBin := 0
	numBins := halfN + 1

	for k := 0; k < numBins; k++ {
		re := float64(output[2*k])
		im := float64(output[2*k+1])
		mag := math.Sqrt(re*re + im*im)
		if mag > maxMag {
			maxMag = mag
			maxBin = k
		}
	}

	if maxBin != targetBin {
		t.Fatalf("peak at bin %d, expected %d (freq %.1f Hz)", maxBin, targetBin, freq)
	}

	t.Logf("peak at bin %d (freq %.1f Hz), magnitude = %.2f", maxBin, freq, maxMag)

	// With exact-bin frequency, other bins should be essentially zero
	// (limited only by float32 precision). Use 40 dB threshold.
	peakDB := 20 * math.Log10(maxMag+1e-30)
	for k := 0; k < numBins; k++ {
		if k == maxBin {
			continue
		}
		re := float64(output[2*k])
		im := float64(output[2*k+1])
		mag := math.Sqrt(re*re + im*im)
		binDB := 20 * math.Log10(mag+1e-30)
		if binDB > peakDB-40 {
			t.Errorf("bin %d magnitude %.2f dB is not at least 40 dB below peak %.2f dB",
				k, binDB, peakDB)
		}
	}
}

// TestFFTRoundTrip verifies that IFFT(FFT(x)) = x within ±1e-4 for random input.
// Uses R2C forward and C2R inverse.
func TestFFTRoundTrip(t *testing.T) {
	const N = 2048 // real samples
	halfN := N / 2
	fft := newFFT(halfN)

	rng := rand.New(rand.NewSource(42))

	input := make([]float32, N)
	for i := range input {
		input[i] = float32(rng.Float64()*2 - 1)
	}

	// Forward R2C
	freq := make([]float32, 2*(halfN+1))
	fft.r2c(input, freq)

	// Inverse C2R
	recovered := make([]float32, N)
	fft.c2r(freq, recovered)

	// Compare: c2r includes 1/N scaling via the inverse C2C
	var maxErr float64
	for i := 0; i < N; i++ {
		diff := math.Abs(float64(recovered[i]) - float64(input[i]))
		if diff > maxErr {
			maxErr = diff
		}
	}

	t.Logf("FFT round-trip max error: %e", maxErr)
	if maxErr > 1e-4 {
		t.Fatalf("FFT round-trip max error %e exceeds 1e-4", maxErr)
	}
}

// TestFFTComplexRoundTrip verifies that inverse(forward(x)) = x for the
// complex C2C FFT path.
func TestFFTComplexRoundTrip(t *testing.T) {
	const N = 512
	fft := newFFT(N)

	rng := rand.New(rand.NewSource(123))

	// Generate random complex data
	data := make([]float32, 2*N)
	original := make([]float32, 2*N)
	for i := range data {
		data[i] = float32(rng.Float64()*2 - 1)
		original[i] = data[i]
	}

	// Forward
	fft.forward(data)

	// Inverse
	fft.inverse(data)

	// Compare
	var maxErr float64
	for i := 0; i < 2*N; i++ {
		diff := math.Abs(float64(data[i]) - float64(original[i]))
		if diff > maxErr {
			maxErr = diff
		}
	}

	t.Logf("C2C round-trip max error: %e", maxErr)
	if maxErr > 1e-4 {
		t.Fatalf("C2C round-trip max error %e exceeds 1e-4", maxErr)
	}
}

// TestFFTParseval verifies Parseval's theorem: the energy in time domain
// equals the energy in frequency domain (scaled by N).
func TestFFTParseval(t *testing.T) {
	const N = 1024
	halfN := N / 2
	fft := newFFT(halfN)

	rng := rand.New(rand.NewSource(99))

	input := make([]float32, N)
	var timeEnergy float64
	for i := range input {
		input[i] = float32(rng.Float64()*2 - 1)
		timeEnergy += float64(input[i]) * float64(input[i])
	}

	freq := make([]float32, 2*(halfN+1))
	fft.r2c(input, freq)

	// Frequency domain energy: sum of |X[k]|^2
	// For R2C: DC and Nyquist bins count once, others count twice (conjugate symmetry)
	var freqEnergy float64
	numBins := halfN + 1
	for k := 0; k < numBins; k++ {
		re := float64(freq[2*k])
		im := float64(freq[2*k+1])
		mag2 := re*re + im*im
		if k == 0 || k == halfN {
			freqEnergy += mag2
		} else {
			freqEnergy += 2 * mag2 // conjugate pair
		}
	}
	freqEnergy /= float64(N) // Parseval normalization

	relErr := math.Abs(freqEnergy-timeEnergy) / timeEnergy
	t.Logf("Parseval: time=%.6f, freq=%.6f, relErr=%e", timeEnergy, freqEnergy, relErr)

	if relErr > 1e-3 {
		t.Fatalf("Parseval violation: relative error %e > 1e-3", relErr)
	}
}

func absDiffInt(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
