package main

import "math"

const (
	melSampleRate = 16000
	melWindowSize = 400  // 25ms at 16kHz
	melHopSize    = 160  // 10ms at 16kHz
	melFFTSize    = 512
	melNumBins    = 80
	melFMin       = 0.0
	melFMax       = 8000.0 // Nyquist
	melLogOffset  = 1e-7
)

// melFFT implements a radix-2 Cooley-Tukey FFT for real-valued input.
type melFFT struct {
	n      int
	log2N  int
	bitrev []int
}

func newMelFFT(n int) *melFFT {
	if n < 2 || n&(n-1) != 0 {
		panic("mel fft: size must be a power of 2")
	}
	log2N := 0
	for v := n; v > 1; v >>= 1 {
		log2N++
	}

	// Precompute bit-reversal permutation.
	bitrev := make([]int, n)
	for i := 0; i < n; i++ {
		rev := 0
		v := i
		for b := 0; b < log2N; b++ {
			rev = (rev << 1) | (v & 1)
			v >>= 1
		}
		bitrev[i] = rev
	}

	return &melFFT{n: n, log2N: log2N, bitrev: bitrev}
}

// Forward computes the FFT of a real-valued input of length n.
// Returns n/2+1 complex frequency bins.
func (f *melFFT) Forward(input []float64) []complex128 {
	n := f.n

	// Pack into complex array with zero imaginary parts.
	data := make([]complex128, n)
	for i := 0; i < n && i < len(input); i++ {
		data[i] = complex(input[i], 0)
	}

	// Bit-reversal permutation.
	for i := 0; i < n; i++ {
		j := f.bitrev[i]
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}

	// Cooley-Tukey butterfly stages.
	for s := 1; s <= f.log2N; s++ {
		m := 1 << s
		halfM := m / 2
		wm := complex(math.Cos(-2.0*math.Pi/float64(m)), math.Sin(-2.0*math.Pi/float64(m)))

		for groupStart := 0; groupStart < n; groupStart += m {
			w := complex(1, 0)
			for j := 0; j < halfM; j++ {
				t := w * data[groupStart+j+halfM]
				u := data[groupStart+j]
				data[groupStart+j] = u + t
				data[groupStart+j+halfM] = u - t
				w *= wm
			}
		}
	}

	// Return first n/2+1 bins (DC through Nyquist).
	output := make([]complex128, n/2+1)
	copy(output, data[:n/2+1])
	return output
}

// melFrontend computes 80-bin log-mel spectrograms from 16kHz audio.
type melFrontend struct {
	window     []float64     // Hann window [melWindowSize]
	filterbank [][]float64   // mel filterbank [melNumBins][melFFTSize/2 + 1]
	fft        *melFFT       // FFT engine
	fftBuf     []float64     // scratch for windowed frame [melFFTSize]
}

func newMelFrontend() *melFrontend {
	// Build Hann window.
	window := make([]float64, melWindowSize)
	for i := 0; i < melWindowSize; i++ {
		window[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(melWindowSize)))
	}

	// Build mel filterbank.
	filterbank := buildMelFilterbank(melNumBins, melFFTSize, melSampleRate, melFMin, melFMax)

	return &melFrontend{
		window:     window,
		filterbank: filterbank,
		fft:        newMelFFT(melFFTSize),
		fftBuf:     make([]float64, melFFTSize),
	}
}

// Compute returns log-mel features as a flat float32 slice in [mel_bins, time] layout.
// Returns (features, numFrames).
func (m *melFrontend) Compute(audio []float32) ([]float32, int) {
	if len(audio) < melWindowSize {
		return nil, 0
	}
	numFrames := (len(audio)-melWindowSize)/melHopSize + 1

	numBins := melFFTSize/2 + 1
	features := make([]float32, melNumBins*numFrames)

	for frame := 0; frame < numFrames; frame++ {
		offset := frame * melHopSize

		// Apply Hann window and zero-pad to FFT size.
		for i := 0; i < melFFTSize; i++ {
			if i < melWindowSize {
				m.fftBuf[i] = float64(audio[offset+i]) * m.window[i]
			} else {
				m.fftBuf[i] = 0
			}
		}

		// Compute FFT.
		spectrum := m.fft.Forward(m.fftBuf)

		// Compute power spectrum |FFT[k]|^2.
		power := make([]float64, numBins)
		for k := 0; k < numBins; k++ {
			re := real(spectrum[k])
			im := imag(spectrum[k])
			power[k] = re*re + im*im
		}

		// Apply mel filterbank and log.
		for bin := 0; bin < melNumBins; bin++ {
			var melEnergy float64
			for k := 0; k < numBins; k++ {
				melEnergy += m.filterbank[bin][k] * power[k]
			}
			// log(mel + offset) — column-major layout [bin, frame]
			features[bin*numFrames+frame] = float32(math.Log(melEnergy + melLogOffset))
		}
	}

	// Per-feature normalization: zero-mean, unit-variance for each mel bin.
	// NeMo's AudioToMelSpectrogramPreprocessor uses normalize="per_feature"
	// which normalizes per-utterance (not global stats).
	for bin := 0; bin < melNumBins; bin++ {
		// Compute mean.
		var sum float64
		for f := 0; f < numFrames; f++ {
			sum += float64(features[bin*numFrames+f])
		}
		mean := sum / float64(numFrames)

		// Compute variance.
		var varSum float64
		for f := 0; f < numFrames; f++ {
			d := float64(features[bin*numFrames+f]) - mean
			varSum += d * d
		}
		stddev := math.Sqrt(varSum/float64(numFrames) + 1e-5)

		// Normalize.
		for f := 0; f < numFrames; f++ {
			features[bin*numFrames+f] = float32((float64(features[bin*numFrames+f]) - mean) / stddev)
		}
	}

	return features, numFrames
}

// hzToMel converts frequency in Hz to mel scale (HTK formula).
func hzToMel(hz float64) float64 {
	return 2595.0 * math.Log10(1.0+hz/700.0)
}

// melToHz converts mel scale to frequency in Hz (HTK formula).
func melToHz(mel float64) float64 {
	return 700.0 * (math.Pow(10.0, mel/2595.0) - 1.0)
}

// buildMelFilterbank creates numMels triangular filters spanning fMin to fMax,
// each mapping numFFT/2+1 FFT bins to one mel coefficient.
func buildMelFilterbank(numMels, numFFT, sampleRate int, fMin, fMax float64) [][]float64 {
	numBins := numFFT/2 + 1
	melMin := hzToMel(fMin)
	melMax := hzToMel(fMax)

	// numMels + 2 evenly spaced points in mel scale (includes lower and upper edges).
	melPoints := make([]float64, numMels+2)
	for i := 0; i < numMels+2; i++ {
		melPoints[i] = melMin + float64(i)*(melMax-melMin)/float64(numMels+1)
	}

	// Convert mel points back to Hz, then to FFT bin indices.
	binFreqs := make([]float64, numMels+2)
	for i := range melPoints {
		binFreqs[i] = melToHz(melPoints[i]) * float64(numFFT) / float64(sampleRate)
	}

	// Build triangular filters.
	filterbank := make([][]float64, numMels)
	for i := 0; i < numMels; i++ {
		filterbank[i] = make([]float64, numBins)
		lower := binFreqs[i]
		center := binFreqs[i+1]
		upper := binFreqs[i+2]

		for k := 0; k < numBins; k++ {
			fk := float64(k)
			if fk >= lower && fk < center {
				// Rising slope.
				if center > lower {
					filterbank[i][k] = (fk - lower) / (center - lower)
				}
			} else if fk >= center && fk <= upper {
				// Falling slope.
				if upper > center {
					filterbank[i][k] = (upper - fk) / (upper - center)
				}
			}
		}
	}

	return filterbank
}
