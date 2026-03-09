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

// transientDetector tracks spectral flux to detect transient onsets.
type transientDetector struct {
	prevMag     []float32
	hasPrev     bool
	fluxHistory []float64
	historyLen  int
}

func newTransientDetector(numBins, historyLen int) *transientDetector {
	return &transientDetector{
		prevMag:    make([]float32, numBins),
		historyLen: historyLen,
	}
}

// spectralFlux computes the half-wave rectified spectral flux between
// consecutive magnitude spectra. Only positive increases count.
func spectralFlux(prevMag, curMag []float32) float64 {
	var flux float64
	for i := range curMag {
		diff := float64(curMag[i]) - float64(prevMag[i])
		if diff > 0 {
			flux += diff
		}
	}
	return flux
}

// isTransient returns true if the current magnitude spectrum represents
// a transient onset (spectral flux exceeds adaptive threshold).
func (td *transientDetector) isTransient(mag []float32) bool {
	if !td.hasPrev {
		copy(td.prevMag, mag)
		td.hasPrev = true
		return false
	}

	flux := spectralFlux(td.prevMag, mag)
	copy(td.prevMag, mag)

	// Adaptive threshold: mean + 2*stddev of recent flux values
	td.fluxHistory = append(td.fluxHistory, flux)
	if len(td.fluxHistory) > td.historyLen {
		td.fluxHistory = td.fluxHistory[len(td.fluxHistory)-td.historyLen:]
	}

	if len(td.fluxHistory) < 4 {
		return false // need history to establish baseline
	}

	var sum, sumSq float64
	for _, f := range td.fluxHistory {
		sum += f
		sumSq += f * f
	}
	n := float64(len(td.fluxHistory))
	mean := sum / n
	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0
	}
	stddev := math.Sqrt(variance)

	return flux > mean+2*stddev
}

// PhaseVocoderTimeStretch performs high-quality pitch-preserved time-stretching
// using an STFT-based phase vocoder with identity phase locking and transient
// detection.
//
//   - input: interleaved PCM samples
//   - channels: number of audio channels (1 or 2)
//   - sampleRate: sample rate in Hz
//   - speed: playback speed (0.1-1.0)
//
// Returns the time-stretched output samples.
func PhaseVocoderTimeStretch(input []float32, channels, sampleRate int, speed float64) []float32 {
	if len(input) == 0 {
		return nil
	}
	if speed >= 1.0 {
		out := make([]float32, len(input))
		copy(out, input)
		return out
	}
	if speed < 0.1 {
		speed = 0.1
	}

	// For extreme slow-down (< 0.5x), cascade two passes at sqrt(speed).
	if speed < 0.5 {
		intermediate := math.Sqrt(speed)
		pass1 := phaseVocoderStretchSingle(input, channels, sampleRate, intermediate)
		if len(pass1) == 0 {
			return nil
		}
		result := phaseVocoderStretchSingle(pass1, channels, sampleRate, intermediate)
		normalizePeak(result)
		return result
	}

	result := phaseVocoderStretchSingle(input, channels, sampleRate, speed)
	normalizePeak(result)
	return result
}

// phaseVocoderStretchSingle performs a single phase vocoder pass.
func phaseVocoderStretchSingle(input []float32, channels, sampleRate int, speed float64) []float32 {
	if len(input) == 0 {
		return nil
	}

	totalSamples := len(input) / channels
	fftSize := pvFFTSize

	// If input is shorter than FFT window, fall back
	if totalSamples < fftSize {
		return nil
	}

	analysisHop := fftSize / pvHopDivisor
	synthHop := int(float64(analysisHop) / speed)
	if synthHop < 1 {
		synthHop = 1
	}

	// Deinterleave channels
	channelData := make([][]float32, channels)
	for ch := 0; ch < channels; ch++ {
		channelData[ch] = make([]float32, totalSamples)
		for i := 0; i < totalSamples; i++ {
			channelData[ch][i] = input[i*channels+ch]
		}
	}

	// Process each channel independently
	outputLen := int(float64(totalSamples) / speed)
	channelOut := make([][]float32, channels)
	for ch := 0; ch < channels; ch++ {
		channelOut[ch] = stretchChannel(channelData[ch], fftSize, analysisHop, synthHop, sampleRate)
		// Trim or pad to expected output length
		if len(channelOut[ch]) > outputLen {
			channelOut[ch] = channelOut[ch][:outputLen]
		}
	}

	// Find minimum output length across channels
	minLen := outputLen
	for ch := 0; ch < channels; ch++ {
		if len(channelOut[ch]) < minLen {
			minLen = len(channelOut[ch])
		}
	}
	if minLen <= 0 {
		return nil
	}

	// Reinterleave
	result := make([]float32, minLen*channels)
	for i := 0; i < minLen; i++ {
		for ch := 0; ch < channels; ch++ {
			result[i*channels+ch] = channelOut[ch][i]
		}
	}

	return result
}

// stretchChannel processes a single channel through the phase vocoder.
func stretchChannel(data []float32, fftSize, analysisHop, synthHop, sampleRate int) []float32 {
	numBins := fftSize/2 + 1
	pv := newPhaseVocoder(fftSize, analysisHop, sampleRate)
	td := newTransientDetector(numBins, 20)

	// Calculate number of analysis frames
	numFrames := 0
	for pos := 0; pos+fftSize <= len(data); pos += analysisHop {
		numFrames++
	}
	if numFrames == 0 {
		return nil
	}

	// Estimate output length
	outputLen := (numFrames-1)*synthHop + fftSize
	output := make([]float32, outputLen)
	windowSum := make([]float32, outputLen) // for COLA normalization

	// Phase accumulation state
	prevAnalysisPhase := make([]float32, numBins)
	synthPhase := make([]float32, numBins)
	firstFrame := true

	outPos := 0
	for inPos := 0; inPos+fftSize <= len(data); inPos += analysisHop {
		frame := data[inPos : inPos+fftSize]

		mag, analysisPhase := pv.analyzeFrame(frame)

		// Copy analysis results (they reference internal buffers)
		magCopy := make([]float32, numBins)
		phaseCopy := make([]float32, numBins)
		copy(magCopy, mag)
		copy(phaseCopy, analysisPhase)

		if firstFrame {
			// First frame: use analysis phases directly
			copy(synthPhase, phaseCopy)
			copy(prevAnalysisPhase, phaseCopy)
			firstFrame = false
		} else {
			// Detect transients
			isTransient := td.isTransient(magCopy)

			if isTransient {
				// Reset synthesis phases to analysis phases
				copy(synthPhase, phaseCopy)
			} else {
				// Phase accumulation with instantaneous frequency
				for k := 0; k < numBins; k++ {
					instFreq := pv.instantaneousFrequency(prevAnalysisPhase[k], phaseCopy[k], k)
					// Accumulate synthesis phase
					phaseAdvance := 2 * math.Pi * instFreq * float64(synthHop) / float64(sampleRate)
					synthPhase[k] += float32(phaseAdvance)
				}

				// Phase locking
				peaks := findSpectralPeaks(magCopy)
				if len(peaks) > 0 {
					lockPhases(magCopy, phaseCopy, synthPhase, prevAnalysisPhase, peaks, numBins)
				}
			}

			copy(prevAnalysisPhase, phaseCopy)
		}

		// Synthesize with the modified phase
		synthFrame := pv.synthesizeFrame(magCopy, synthPhase)

		// Overlap-add
		for i := 0; i < fftSize; i++ {
			idx := outPos + i
			if idx < len(output) {
				output[idx] += synthFrame[i]
				windowSum[idx] += pv.window[i] * pv.window[i] // Hann^2 for COLA normalization
			}
		}

		outPos += synthHop
	}

	// Normalize by window sum (COLA normalization)
	for i := range output {
		if windowSum[i] > 1e-6 {
			output[i] /= windowSum[i]
		}
	}

	return output
}
