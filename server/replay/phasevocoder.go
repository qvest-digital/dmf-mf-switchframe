package replay

import "math"

const (
	pvFFTSize    = 4096        // FFT window size (samples)
	pvHopDivisor = 4           // analysis hop = FFT/4 = 1024 (75% overlap)
	pvNumBins    = pvFFTSize/2 + 1 // 2049 unique frequency bins
)

// phaseVocoder holds reusable state for STFT-based time-stretching.
type phaseVocoder struct {
	fftSize     int
	analysisHop int
	sampleRate  int
	fft         *fftState

	window     []float32 // Hann analysis/synthesis window
	prevPhase  []float32 // previous frame analysis phase, per bin
	synthPhase []float32 // accumulated synthesis phase, per bin

	// Reusable buffers
	windowed   []float32 // windowed input frame
	freqBuf    []float32 // R2C output (2*(N/2+1) floats)
	magBuf     []float32 // magnitude per bin
	phaseBuf   []float32 // phase per bin
	reBuf      []float32 // real part per bin (for synthesis)
	imBuf      []float32 // imag part per bin (for synthesis)
	synthFrame []float32 // IFFT output frame
}

// newPhaseVocoder creates a new phase vocoder state.
func newPhaseVocoder(fftSize, hop, sampleRate int) *phaseVocoder {
	numBins := fftSize/2 + 1
	halfN := fftSize / 2

	window := makeHannWindowF32(fftSize)

	return &phaseVocoder{
		fftSize:     fftSize,
		analysisHop: hop,
		sampleRate:  sampleRate,
		fft:         newFFT(halfN), // C2C size = N/2
		window:      window,
		prevPhase:   make([]float32, numBins),
		synthPhase:  make([]float32, numBins),
		windowed:    make([]float32, fftSize),
		freqBuf:     make([]float32, 2*numBins),
		magBuf:      make([]float32, numBins),
		phaseBuf:    make([]float32, numBins),
		reBuf:       make([]float32, numBins),
		imBuf:       make([]float32, numBins),
		synthFrame:  make([]float32, fftSize),
	}
}

// makeHannWindowF32 creates a periodic Hann window as float32.
func makeHannWindowF32(size int) []float32 {
	w := make([]float32, size)
	for i := 0; i < size; i++ {
		w[i] = float32(0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size))))
	}
	return w
}

// cartesianToPolar converts interleaved complex bins to magnitude and phase.
func cartesianToPolar(re, im, mag, phase []float32, n int) {
	for i := 0; i < n; i++ {
		r, j := float64(re[i]), float64(im[i])
		mag[i] = float32(math.Sqrt(r*r + j*j))
		phase[i] = float32(math.Atan2(j, r))
	}
}

// polarToCartesian converts magnitude and phase to real and imaginary.
func polarToCartesian(mag, phase, re, im []float32, n int) {
	for i := 0; i < n; i++ {
		m, p := float64(mag[i]), float64(phase[i])
		re[i] = float32(m * math.Cos(p))
		im[i] = float32(m * math.Sin(p))
	}
}

// analyzeFrame applies the analysis window and computes the STFT of a single frame.
// Returns magnitude and phase arrays (references internal buffers — valid until next call).
func (pv *phaseVocoder) analyzeFrame(frame []float32) (mag, phase []float32) {
	numBins := pv.fftSize/2 + 1

	// Apply analysis window
	for i := 0; i < pv.fftSize && i < len(frame); i++ {
		pv.windowed[i] = frame[i] * pv.window[i]
	}

	// R2C FFT
	pv.fft.r2c(pv.windowed, pv.freqBuf)

	// Split interleaved complex into separate re/im for polar conversion
	for k := 0; k < numBins; k++ {
		pv.reBuf[k] = pv.freqBuf[2*k]
		pv.imBuf[k] = pv.freqBuf[2*k+1]
	}

	// Convert to polar
	cartesianToPolar(pv.reBuf, pv.imBuf, pv.magBuf, pv.phaseBuf, numBins)

	return pv.magBuf, pv.phaseBuf
}

// synthesizeFrame converts magnitude and phase back to a time-domain frame.
// Returns the windowed time-domain frame (references internal buffer).
func (pv *phaseVocoder) synthesizeFrame(mag, phase []float32) []float32 {
	numBins := pv.fftSize/2 + 1

	// Polar to cartesian
	polarToCartesian(mag, phase, pv.reBuf, pv.imBuf, numBins)

	// Pack into interleaved complex
	for k := 0; k < numBins; k++ {
		pv.freqBuf[2*k] = pv.reBuf[k]
		pv.freqBuf[2*k+1] = pv.imBuf[k]
	}

	// C2R inverse FFT
	pv.fft.c2r(pv.freqBuf, pv.synthFrame)

	// Apply synthesis window
	for i := 0; i < pv.fftSize; i++ {
		pv.synthFrame[i] *= pv.window[i]
	}

	return pv.synthFrame
}

// instantaneousFrequency estimates the true frequency of bin k from the
// phase difference between consecutive analysis frames.
func (pv *phaseVocoder) instantaneousFrequency(prevPhase, curPhase float32, binIdx int) float64 {
	// Expected phase advance for this bin at the analysis hop
	expectedAdvance := 2.0 * math.Pi * float64(binIdx) * float64(pv.analysisHop) / float64(pv.fftSize)

	// Actual phase difference
	phaseDiff := float64(curPhase - prevPhase)

	// Phase deviation from expected
	deviation := phaseDiff - expectedAdvance

	// Wrap deviation to [-π, π]
	deviation = deviation - 2*math.Pi*math.Round(deviation/(2*math.Pi))

	// True frequency = bin center frequency + deviation-based correction
	binFreq := float64(binIdx) * float64(pv.sampleRate) / float64(pv.fftSize)
	freqCorrection := deviation * float64(pv.sampleRate) / (2 * math.Pi * float64(pv.analysisHop))

	return binFreq + freqCorrection
}

// findSpectralPeaks identifies local maxima in the magnitude spectrum.
// A bin k is a peak if mag[k] > mag[k-1] and mag[k] > mag[k+1].
func findSpectralPeaks(mag []float32) []int {
	n := len(mag)
	if n < 3 {
		return nil
	}
	var peaks []int
	for k := 1; k < n-1; k++ {
		if mag[k] > mag[k-1] && mag[k] > mag[k+1] {
			peaks = append(peaks, k)
		}
	}
	return peaks
}

// nearestPeak returns the peak index closest to bin k.
func nearestPeak(peaks []int, k int) int {
	if len(peaks) == 0 {
		return k // no peaks, return self
	}
	best := peaks[0]
	bestDist := abs(k - best)
	for _, p := range peaks[1:] {
		d := abs(k - p)
		if d < bestDist {
			bestDist = d
			best = p
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// lockPhases applies identity phase locking. Non-peak bins inherit
// their nearest peak's phase advancement, preserving phase coherence
// and eliminating "phasiness" artifacts.
func lockPhases(mag, analysisPhase, synthPhase []float32, prevAnalysisPhase []float32, peaks []int, numBins int) {
	if len(peaks) == 0 {
		return // no peaks, leave phases as-is
	}

	for k := 0; k < numBins; k++ {
		// Find nearest peak
		p := nearestPeak(peaks, k)
		if p == k {
			continue // peak bins keep their own phase
		}
		// Non-peak bin: inherit peak's phase relationship
		// synthPhase[k] = synthPhase[peak] + (analysisPhase[k] - analysisPhase[peak])
		synthPhase[k] = synthPhase[p] + (analysisPhase[k] - analysisPhase[p])
	}
}
