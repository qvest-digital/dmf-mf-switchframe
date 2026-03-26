package asr

import "math"

// MelSpectrogram computes a 128-bin log mel spectrogram from 16kHz mono float32 audio.
// This matches the NeMo Parakeet TDT preprocessing pipeline:
//   - Window size: 512 samples (32ms at 16kHz)
//   - Hop size: 160 samples (10ms)
//   - FFT size: 512
//   - Mel filter bank: 128 bins, freq range 0-8000 Hz
//   - Output: log(mel_energy + 1e-10) (natural log, then per-feature normalization)
//
// Returns a flat float32 slice in [128, numFrames] layout (feature-major, as expected
// by the ONNX encoder input "audio_signal").
type MelSpectrogram struct {
	nFFT       int // FFT size (512)
	hopSize    int // hop between frames (160)
	windowSize int // analysis window size (512)
	numMelBins int // number of mel bins (128)
	sampleRate int // expected sample rate (16000)

	// Precomputed data
	window    []float64   // Hann window [windowSize]
	melBank   [][]float64 // mel filter bank [numMelBins][nFFT/2+1]
	fftState  *melFFT     // FFT engine
	fftBuf    []float64   // reusable complex FFT buffer [2*nFFT]
	powerBuf  []float64   // reusable power spectrum [nFFT/2+1]
	windowBuf []float64   // reusable windowed frame buffer [nFFT]
}

// NewMelSpectrogram creates a mel spectrogram extractor with standard NeMo parameters.
func NewMelSpectrogram() *MelSpectrogram {
	const (
		nFFT       = 512
		hopSize    = 160
		windowSize = 512
		numMelBins = 128
		sampleRate = 16000
		fMin       = 0.0
		fMax       = 8000.0
	)

	m := &MelSpectrogram{
		nFFT:       nFFT,
		hopSize:    hopSize,
		windowSize: windowSize,
		numMelBins: numMelBins,
		sampleRate: sampleRate,
		window:     hannWindow(windowSize),
		melBank:    melFilterBank(numMelBins, nFFT, sampleRate, fMin, fMax),
		fftState:   newMelFFT(nFFT),
		fftBuf:     make([]float64, 2*nFFT),
		powerBuf:   make([]float64, nFFT/2+1),
		windowBuf:  make([]float64, nFFT),
	}
	return m
}

// Compute extracts the mel spectrogram from 16kHz mono float32 audio.
// Returns the spectrogram as a flat float32 slice in [numMelBins, numFrames] layout
// and the number of time frames. The output is log-scaled and per-feature normalized.
//
// Not safe for concurrent use — caller must serialize (NemotronBackend
// serializes via the inferring mutex).
func (m *MelSpectrogram) Compute(audio []float32) ([]float32, int) {
	if len(audio) < m.windowSize {
		return nil, 0
	}

	numFrames := 0
	for start := 0; start+m.windowSize <= len(audio); start += m.hopSize {
		numFrames++
	}
	if numFrames == 0 {
		return nil, 0
	}

	// Allocate output in [numMelBins, numFrames] layout.
	out := make([]float32, m.numMelBins*numFrames)

	nBins := m.nFFT/2 + 1

	for t := 0; t < numFrames; t++ {
		start := t * m.hopSize

		// Apply Hann window and zero-pad to FFT size.
		for i := 0; i < m.windowSize; i++ {
			m.windowBuf[i] = float64(audio[start+i]) * m.window[i]
		}
		for i := m.windowSize; i < m.nFFT; i++ {
			m.windowBuf[i] = 0
		}

		// Pack real data into interleaved complex format.
		for i := 0; i < m.nFFT; i++ {
			m.fftBuf[2*i] = m.windowBuf[i]
			m.fftBuf[2*i+1] = 0
		}

		// Forward FFT.
		m.fftState.forward(m.fftBuf)

		// Power spectrum: |X[k]|^2.
		for k := 0; k < nBins; k++ {
			re := m.fftBuf[2*k]
			im := m.fftBuf[2*k+1]
			m.powerBuf[k] = re*re + im*im
		}

		// Apply mel filter bank and take log.
		for bin := 0; bin < m.numMelBins; bin++ {
			var energy float64
			for k := 0; k < nBins; k++ {
				energy += m.melBank[bin][k] * m.powerBuf[k]
			}
			// Log mel energy with small epsilon to avoid log(0).
			out[bin*numFrames+t] = float32(math.Log(energy + 1e-10))
		}
	}

	// Per-feature normalization (zero mean, unit variance).
	m.normalize(out, m.numMelBins, numFrames)

	return out, numFrames
}

// normalize applies per-feature zero-mean, unit-variance normalization.
// data is in [numFeatures, numFrames] layout.
func (m *MelSpectrogram) normalize(data []float32, numFeatures, numFrames int) {
	if numFrames <= 1 {
		return
	}

	for f := 0; f < numFeatures; f++ {
		offset := f * numFrames

		// Compute mean.
		var sum float64
		for t := 0; t < numFrames; t++ {
			sum += float64(data[offset+t])
		}
		mean := sum / float64(numFrames)

		// Compute variance.
		var varSum float64
		for t := 0; t < numFrames; t++ {
			d := float64(data[offset+t]) - mean
			varSum += d * d
		}
		std := math.Sqrt(varSum / float64(numFrames))
		if std < 1e-10 {
			std = 1e-10
		}

		// Normalize.
		invStd := 1.0 / std
		for t := 0; t < numFrames; t++ {
			data[offset+t] = float32((float64(data[offset+t]) - mean) * invStd)
		}
	}
}

// hannWindow generates a periodic Hann window of the given size.
func hannWindow(size int) []float64 {
	w := make([]float64, size)
	for i := 0; i < size; i++ {
		w[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(size)))
	}
	return w
}

// hzToMel converts frequency in Hz to mel scale.
func hzToMel(hz float64) float64 {
	return 2595.0 * math.Log10(1.0+hz/700.0)
}

// melToHz converts mel scale to frequency in Hz.
func melToHz(mel float64) float64 {
	return 700.0 * (math.Pow(10.0, mel/2595.0) - 1.0)
}

// melFilterBank creates a mel filter bank matrix [numMelBins][nFFT/2+1].
// Each row is a triangular filter in the frequency domain.
func melFilterBank(numMelBins, nFFT, sampleRate int, fMin, fMax float64) [][]float64 {
	nBins := nFFT/2 + 1

	// Compute mel-spaced center frequencies.
	melMin := hzToMel(fMin)
	melMax := hzToMel(fMax)
	melPoints := make([]float64, numMelBins+2) // includes lower and upper edges
	for i := 0; i < numMelBins+2; i++ {
		melPoints[i] = melMin + float64(i)*(melMax-melMin)/float64(numMelBins+1)
	}

	// Convert mel points back to Hz and then to FFT bin indices.
	binFreqs := make([]float64, numMelBins+2)
	for i := 0; i < numMelBins+2; i++ {
		binFreqs[i] = melToHz(melPoints[i]) * float64(nFFT) / float64(sampleRate)
	}

	// Build triangular filters.
	bank := make([][]float64, numMelBins)
	for i := 0; i < numMelBins; i++ {
		bank[i] = make([]float64, nBins)
		left := binFreqs[i]
		center := binFreqs[i+1]
		right := binFreqs[i+2]

		for k := 0; k < nBins; k++ {
			freq := float64(k)
			if freq >= left && freq <= center && center > left {
				bank[i][k] = (freq - left) / (center - left)
			} else if freq > center && freq <= right && right > center {
				bank[i][k] = (right - freq) / (right - center)
			}
		}
	}
	return bank
}

// melFFT is a minimal radix-2 Cooley-Tukey FFT for mel spectrogram computation.
// Uses float64 for numerical precision in the spectral analysis path.
type melFFT struct {
	n      int       // FFT size (must be power of 2)
	log2N  int       // log2(n)
	bitrev []int     // bit-reversal permutation
	twRe   []float64 // precomputed twiddle cosines
	twIm   []float64 // precomputed twiddle sines
}

// newMelFFT creates a new FFT engine for the given size (must be power of 2).
func newMelFFT(n int) *melFFT {
	if n < 2 || n&(n-1) != 0 {
		panic("melFFT: size must be a power of 2")
	}

	log2N := 0
	for v := n; v > 1; v >>= 1 {
		log2N++
	}

	// Bit-reversal permutation.
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

	// Precompute twiddle factors: W(k,N) = exp(-2*pi*i*k/N).
	twRe := make([]float64, n)
	twIm := make([]float64, n)
	for k := 0; k < n; k++ {
		angle := -2.0 * math.Pi * float64(k) / float64(n)
		twRe[k] = math.Cos(angle)
		twIm[k] = math.Sin(angle)
	}

	return &melFFT{
		n:      n,
		log2N:  log2N,
		bitrev: bitrev,
		twRe:   twRe,
		twIm:   twIm,
	}
}

// forward performs an in-place forward FFT on interleaved complex data.
// data has length 2*n (n complex values as [re0, im0, re1, im1, ...]).
func (f *melFFT) forward(data []float64) {
	n := f.n

	// Bit-reversal permutation.
	for i := 0; i < n; i++ {
		j := f.bitrev[i]
		if i < j {
			data[2*i], data[2*j] = data[2*j], data[2*i]
			data[2*i+1], data[2*j+1] = data[2*j+1], data[2*i+1]
		}
	}

	// Iterative Cooley-Tukey butterfly.
	for s := 1; s <= f.log2N; s++ {
		m := 1 << s
		halfM := m / 2
		twStride := n / m

		for groupStart := 0; groupStart < n; groupStart += m {
			for k := 0; k < halfM; k++ {
				twIdx := k * twStride
				wRe := f.twRe[twIdx]
				wIm := f.twIm[twIdx]

				evenIdx := (groupStart + k) * 2
				oddIdx := (groupStart + k + halfM) * 2

				oddRe := data[oddIdx]
				oddIm := data[oddIdx+1]

				tRe := wRe*oddRe - wIm*oddIm
				tIm := wRe*oddIm + wIm*oddRe

				eRe := data[evenIdx]
				eIm := data[evenIdx+1]

				data[evenIdx] = eRe + tRe
				data[evenIdx+1] = eIm + tIm
				data[oddIdx] = eRe - tRe
				data[oddIdx+1] = eIm - tIm
			}
		}
	}
}
