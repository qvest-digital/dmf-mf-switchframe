package asr

import "math"

// Whisper mel spectrogram parameters.
const (
	melSampleRate = 16000 // Input sample rate in Hz
	melFFTSize    = 400   // Window size (25ms at 16kHz)
	melHopLength  = 160   // Hop length (10ms at 16kHz)
	melNFFT       = 512   // FFT size (zero-padded)
	melNMels      = 80    // Number of mel filter banks
	melMaxFrames  = 3000  // Max frames (30s of audio)
	melMaxSamples = melSampleRate * 30 // 480000 samples for 30s
	melNBins      = melNFFT/2 + 1      // 257 FFT bins (DC to Nyquist)
)

// melFilterRange stores the non-zero range of a mel filter to skip zero
// multiplications in the filterbank dot product.
type melFilterRange struct {
	startBin int
	endBin   int // exclusive
	weights  []float32
}

// MelSpectrogram computes Whisper-compatible log-mel spectrograms.
// It holds precomputed Hann window, mel filterbank, and FFT twiddle factors
// so that repeated calls to Compute() avoid redundant setup work.
//
// MelSpectrogram is not goroutine-safe; callers must synchronize access
// or use one instance per goroutine.
type MelSpectrogram struct {
	hannWindow [melFFTSize]float32
	melFilters [melNMels]melFilterRange

	// FFT precomputed data (512-point radix-2 Cooley-Tukey)
	twiddleRe [melNFFT / 2]float32
	twiddleIm [melNFFT / 2]float32
	fftBitrev [melNFFT]uint16

	// Scratch buffers (reused across Compute calls)
	fftRe [melNFFT]float32
	fftIm [melNFFT]float32
}

// NewMelSpectrogram creates a new mel spectrogram computer with precomputed
// Hann window, mel filterbank, and FFT twiddle factors.
func NewMelSpectrogram() *MelSpectrogram {
	m := &MelSpectrogram{}
	m.initHannWindow()
	m.initMelFilterbank()
	m.initFFT()
	return m
}

// initHannWindow precomputes the Hann window: 0.5 * (1 - cos(2*pi*n/N))
func (m *MelSpectrogram) initHannWindow() {
	for n := 0; n < melFFTSize; n++ {
		m.hannWindow[n] = float32(0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(n)/float64(melFFTSize))))
	}
}

// hzToMel converts frequency in Hz to the mel scale.
func hzToMel(hz float64) float64 {
	return 2595.0 * math.Log10(1.0+hz/700.0)
}

// melToHz converts mel scale value back to Hz.
func melToHz(mel float64) float64 {
	return 700.0 * (math.Pow(10.0, mel/2595.0) - 1.0)
}

// initMelFilterbank precomputes 80 triangular mel filters spanning 0-8000Hz,
// storing only the non-zero weight ranges for each filter.
func (m *MelSpectrogram) initMelFilterbank() {
	fMin := 0.0
	fMax := float64(melSampleRate) / 2.0 // 8000 Hz (Nyquist)

	melMin := hzToMel(fMin)
	melMax := hzToMel(fMax)

	// 82 mel-spaced points (80 filters need 82 edges)
	nEdges := melNMels + 2
	melPoints := make([]float64, nEdges)
	for i := 0; i < nEdges; i++ {
		melPoints[i] = melMin + float64(i)*(melMax-melMin)/float64(nEdges-1)
	}

	// Convert mel points back to Hz, then to FFT bin indices
	binPoints := make([]float64, nEdges)
	for i := range binPoints {
		hz := melToHz(melPoints[i])
		binPoints[i] = hz * float64(melNFFT) / float64(melSampleRate)
	}

	// Build sparse triangular filters
	for f := 0; f < melNMels; f++ {
		left := binPoints[f]
		center := binPoints[f+1]
		right := binPoints[f+2]

		// Find the non-zero bin range
		startBin := int(math.Floor(left))
		if startBin < 0 {
			startBin = 0
		}
		endBin := int(math.Ceil(right)) + 1
		if endBin > melNBins {
			endBin = melNBins
		}

		weights := make([]float32, endBin-startBin)
		for bin := startBin; bin < endBin; bin++ {
			b := float64(bin)
			var w float64
			if b >= left && b < center {
				w = (b - left) / (center - left)
			} else if b >= center && b <= right {
				w = (right - b) / (right - center)
			}
			weights[bin-startBin] = float32(w)
		}

		m.melFilters[f] = melFilterRange{
			startBin: startBin,
			endBin:   endBin,
			weights:  weights,
		}
	}
}

// initFFT precomputes twiddle factors and bit-reversal permutation
// for a 512-point radix-2 Cooley-Tukey FFT.
func (m *MelSpectrogram) initFFT() {
	n := melNFFT
	halfN := n / 2

	// Precompute twiddle factors: W(k,N) = exp(-2*pi*i*k/N)
	// Only need first half (the butterfly only uses indices 0..N/2-1).
	for k := 0; k < halfN; k++ {
		angle := -2.0 * math.Pi * float64(k) / float64(n)
		m.twiddleRe[k] = float32(math.Cos(angle))
		m.twiddleIm[k] = float32(math.Sin(angle))
	}

	// Precompute bit-reversal permutation
	log2N := 9 // log2(512)
	for i := 0; i < n; i++ {
		rev := 0
		v := i
		for b := 0; b < log2N; b++ {
			rev = (rev << 1) | (v & 1)
			v >>= 1
		}
		m.fftBitrev[i] = uint16(rev)
	}
}

// fft512 computes a 512-point FFT in-place on m.fftRe and m.fftIm.
func (m *MelSpectrogram) fft512() {
	re := &m.fftRe
	im := &m.fftIm

	// Bit-reversal permutation
	for i := 0; i < melNFFT; i++ {
		j := int(m.fftBitrev[i])
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}

	// Iterative Cooley-Tukey butterfly stages (9 stages for N=512)
	for s := uint(1); s <= 9; s++ {
		groupSize := 1 << s
		halfGroup := groupSize >> 1
		twStride := melNFFT >> s // = N / groupSize

		for groupStart := 0; groupStart < melNFFT; groupStart += groupSize {
			for k := 0; k < halfGroup; k++ {
				twIdx := k * twStride
				twRe := m.twiddleRe[twIdx]
				twIm := m.twiddleIm[twIdx]

				evenIdx := groupStart + k
				oddIdx := evenIdx + halfGroup

				oRe := re[oddIdx]
				oIm := im[oddIdx]
				tRe := twRe*oRe - twIm*oIm
				tIm := twRe*oIm + twIm*oRe

				eRe := re[evenIdx]
				eIm := im[evenIdx]
				re[evenIdx] = eRe + tRe
				im[evenIdx] = eIm + tIm
				re[oddIdx] = eRe - tRe
				im[oddIdx] = eIm - tIm
			}
		}
	}
}

// Compute produces a Whisper-compatible [80][3000] log-mel spectrogram
// from the given PCM samples (mono float32 at 16kHz).
//
// The input is padded or truncated to exactly 480000 samples (30 seconds).
// Each of the 3000 output frames corresponds to a 10ms hop.
func (m *MelSpectrogram) Compute(samples []float32) [][]float32 {
	// Pad input to accommodate all STFT frames. The last frame starts at
	// (melMaxFrames-1)*melHopLength = 479840 and needs melFFTSize=400 samples,
	// so we need at least 480240 samples. Allocate with extra room and
	// zero-pad beyond the actual audio (Whisper zero-pads short audio).
	paddedLen := (melMaxFrames-1)*melHopLength + melFFTSize // 480240
	padded := make([]float32, paddedLen)
	n := len(samples)
	if n > melMaxSamples {
		n = melMaxSamples
	}
	copy(padded, samples[:n])
	// Remaining samples are zero (silence padding)

	// Allocate output: [melNMels][melMaxFrames]
	output := make([][]float32, melNMels)
	for i := range output {
		output[i] = make([]float32, melMaxFrames)
	}

	// Scratch for magnitude-squared power spectrum
	var power [melNBins]float32

	// Process each frame
	for frame := 0; frame < melMaxFrames; frame++ {
		offset := frame * melHopLength

		// Clear FFT buffers and apply windowed input in one pass.
		// First melFFTSize samples: window * input. Rest: zero.
		src := padded[offset : offset+melFFTSize]
		win := &m.hannWindow
		re := &m.fftRe
		im := &m.fftIm

		for i := 0; i < melFFTSize; i++ {
			re[i] = src[i] * win[i]
		}
		for i := melFFTSize; i < melNFFT; i++ {
			re[i] = 0
		}
		for i := range im {
			im[i] = 0
		}

		// Compute FFT
		m.fft512()

		// Compute magnitude squared of first 257 bins (DC to Nyquist)
		for bin := 0; bin < melNBins; bin++ {
			r := re[bin]
			i := im[bin]
			power[bin] = r*r + i*i
		}

		// Apply mel filterbank using sparse ranges and log scale
		for mel := 0; mel < melNMels; mel++ {
			filt := &m.melFilters[mel]
			energy := float32(0)
			pw := power[filt.startBin:filt.endBin]
			w := filt.weights
			for i, p := range pw {
				energy += p * w[i]
			}
			// Log scale with floor to prevent log(0)
			if energy < 1e-10 {
				energy = 1e-10
			}
			output[mel][frame] = float32(math.Log10(float64(energy)))
		}
	}

	// Normalize: find global max, clamp to [max-8, max], scale to Whisper range
	maxVal := float32(-math.MaxFloat32)
	for mel := 0; mel < melNMels; mel++ {
		row := output[mel]
		for _, v := range row {
			if v > maxVal {
				maxVal = v
			}
		}
	}

	// Clamp and scale: map [maxVal-8, maxVal] to [0, 1], then apply Whisper normalization
	clampMin := maxVal - 8.0
	invEight := float32(1.0 / 8.0)
	for mel := 0; mel < melNMels; mel++ {
		row := output[mel]
		for i, v := range row {
			if v < clampMin {
				v = clampMin
			}
			// Scale to [0, 1], then Whisper normalization
			row[i] = ((v-clampMin)*invEight + 4.0) * 0.25
		}
	}

	return output
}
