package replay

import "math"

const (
	pvFFTSize    = 4096            // FFT window size (samples)
	pvHopDivisor = 4              // analysis hop = FFT/4 = 1024 (75% overlap)
	pvNumBins    = pvFFTSize/2 + 1 // 2049 unique frequency bins
)

// phaseVocoder holds reusable state for STFT-based time-stretching.
type phaseVocoder struct {
	fftSize     int
	analysisHop int
	sampleRate  int
	fft         *fftState

	window   []float32 // Hann analysis/synthesis window
	windowSq []float32 // precomputed window^2 for COLA normalization

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
	peaksBuf   []int     // reusable buffer for spectral peak indices
	nearestBuf []int     // reusable nearest-peak lookup table
	magCopy    []float32 // reusable copy of magnitude per frame
	phaseCopy  []float32 // reusable copy of phase per frame
}

// newPhaseVocoder creates a new phase vocoder state.
func newPhaseVocoder(fftSize, hop, sampleRate int) *phaseVocoder {
	numBins := fftSize/2 + 1
	halfN := fftSize / 2

	window := makeHannWindowF32(fftSize)
	windowSq := make([]float32, fftSize)
	for i, w := range window {
		windowSq[i] = w * w
	}

	return &phaseVocoder{
		fftSize:     fftSize,
		analysisHop: hop,
		sampleRate:  sampleRate,
		fft:         newFFT(halfN), // C2C size = N/2
		window:      window,
		windowSq:    windowSq,
		prevPhase:   make([]float32, numBins),
		synthPhase:  make([]float32, numBins),
		windowed:    make([]float32, fftSize),
		freqBuf:     make([]float32, 2*numBins),
		magBuf:      make([]float32, numBins),
		phaseBuf:    make([]float32, numBins),
		reBuf:       make([]float32, numBins),
		imBuf:       make([]float32, numBins),
		synthFrame:  make([]float32, fftSize),
		peaksBuf:    make([]int, 0, numBins/4),
		nearestBuf:  make([]int, numBins),
		magCopy:     make([]float32, numBins),
		phaseCopy:   make([]float32, numBins),
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

	// Apply analysis window, zero-pad if frame is short
	n := pv.fftSize
	if len(frame) < n {
		n = len(frame)
	}
	for i := 0; i < n; i++ {
		pv.windowed[i] = frame[i] * pv.window[i]
	}
	for i := n; i < pv.fftSize; i++ {
		pv.windowed[i] = 0
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

// findSpectralPeaksInto identifies local maxima in the magnitude spectrum,
// writing results into the provided buffer to avoid allocation.
func findSpectralPeaksInto(mag []float32, buf []int) []int {
	n := len(mag)
	buf = buf[:0]
	if n < 3 {
		return buf
	}
	for k := 1; k < n-1; k++ {
		if mag[k] > mag[k-1] && mag[k] > mag[k+1] {
			buf = append(buf, k)
		}
	}
	return buf
}

// buildNearestPeakMap builds an O(n) lookup table mapping each bin to its
// nearest spectral peak. Replaces the O(n*p) nearestPeak linear scan.
func buildNearestPeakMap(peaks []int, numBins int, buf []int) []int {
	buf = buf[:numBins]
	if len(peaks) == 0 {
		for i := range buf {
			buf[i] = i
		}
		return buf
	}

	// Forward pass: assign each bin to closest peak seen so far
	pi := 0
	for k := 0; k < numBins; k++ {
		// Advance to next peak if it's closer
		for pi+1 < len(peaks) {
			distCur := peaks[pi] - k
			if distCur < 0 {
				distCur = -distCur
			}
			distNext := peaks[pi+1] - k
			if distNext < 0 {
				distNext = -distNext
			}
			if distNext < distCur {
				pi++
			} else {
				break
			}
		}
		buf[k] = peaks[pi]
	}
	return buf
}

// lockPhases applies identity phase locking. Non-peak bins inherit
// their nearest peak's phase advancement, preserving phase coherence
// and eliminating "phasiness" artifacts.
func lockPhases(mag, analysisPhase, synthPhase []float32, nearest []int, numBins int) {
	for k := 0; k < numBins; k++ {
		p := nearest[k]
		if p == k {
			continue // peak bins keep their own phase
		}
		// Non-peak bin: inherit peak's phase relationship
		synthPhase[k] = synthPhase[p] + (analysisPhase[k] - analysisPhase[p])
	}
}

// transientDetector tracks spectral flux to detect transient onsets.
type transientDetector struct {
	prevMag    []float32
	hasPrev    bool
	fluxRing   []float64 // circular buffer of recent flux values
	ringPos    int
	ringCount  int
	historyLen int
}

func newTransientDetector(numBins, historyLen int) *transientDetector {
	return &transientDetector{
		prevMag:    make([]float32, numBins),
		fluxRing:   make([]float64, historyLen),
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

	// Insert into ring buffer
	td.fluxRing[td.ringPos] = flux
	td.ringPos = (td.ringPos + 1) % td.historyLen
	if td.ringCount < td.historyLen {
		td.ringCount++
	}

	if td.ringCount < 4 {
		return false // need history to establish baseline
	}

	// Adaptive threshold: mean + 2*stddev of recent flux values
	var sum, sumSq float64
	for i := 0; i < td.ringCount; i++ {
		f := td.fluxRing[i]
		sum += f
		sumSq += f * f
	}
	n := float64(td.ringCount)
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

	// Measure input RMS before processing so we can match output level.
	// Phase vocoders attenuate broadband content due to incoherent phase
	// resynthesis — RMS matching compensates for this.
	inputRMS := rmsF32(input)

	var result []float32
	// For extreme slow-down (< 0.5x), cascade two passes at sqrt(speed).
	if speed < 0.5 {
		intermediate := math.Sqrt(speed)
		pass1 := phaseVocoderStretchSingle(input, channels, sampleRate, intermediate)
		if len(pass1) == 0 {
			return nil
		}
		result = phaseVocoderStretchSingle(pass1, channels, sampleRate, intermediate)
	} else {
		result = phaseVocoderStretchSingle(input, channels, sampleRate, speed)
	}

	if len(result) == 0 {
		return nil
	}

	matchRMS(result, inputRMS)
	return result
}

// rmsF32 computes the root-mean-square of a float32 slice.
func rmsF32(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// matchRMS scales samples so their RMS matches targetRMS, then hard-clips
// individual samples at ±0.95 to prevent clipping without rescaling the
// entire buffer (which would undo the RMS match).
func matchRMS(samples []float32, targetRMS float64) {
	if targetRMS < 1e-10 {
		return
	}
	outputRMS := rmsF32(samples)
	if outputRMS < 1e-10 {
		return
	}

	scale := float32(targetRMS / outputRMS)
	for i := range samples {
		s := samples[i] * scale
		if s > 0.95 {
			s = 0.95
		} else if s < -0.95 {
			s = -0.95
		}
		samples[i] = s
	}
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
		copy(pv.magCopy, mag)
		copy(pv.phaseCopy, analysisPhase)

		if firstFrame {
			// First frame: use analysis phases directly
			copy(synthPhase, pv.phaseCopy)
			copy(prevAnalysisPhase, pv.phaseCopy)
			firstFrame = false
		} else {
			// Detect transients
			isTransient := td.isTransient(pv.magCopy)

			if isTransient {
				// Reset synthesis phases to analysis phases
				copy(synthPhase, pv.phaseCopy)
			} else {
				// Phase accumulation with instantaneous frequency
				for k := 0; k < numBins; k++ {
					instFreq := pv.instantaneousFrequency(prevAnalysisPhase[k], pv.phaseCopy[k], k)
					// Accumulate synthesis phase, wrapping to [-pi, pi] to prevent
					// float32 precision degradation after many frames.
					phaseAdvance := 2 * math.Pi * instFreq * float64(synthHop) / float64(sampleRate)
					synthPhase[k] = float32(math.Remainder(float64(synthPhase[k])+phaseAdvance, 2*math.Pi))
				}

				// Phase locking via O(n) nearest-peak lookup
				peaks := findSpectralPeaksInto(pv.magCopy, pv.peaksBuf)
				pv.peaksBuf = peaks // retain buffer capacity
				if len(peaks) > 0 {
					nearest := buildNearestPeakMap(peaks, numBins, pv.nearestBuf)
					lockPhases(pv.magCopy, pv.phaseCopy, synthPhase, nearest, numBins)
				}
			}

			copy(prevAnalysisPhase, pv.phaseCopy)
		}

		// Synthesize with the modified phase
		synthFrame := pv.synthesizeFrame(pv.magCopy, synthPhase)

		// Overlap-add with precomputed window^2
		end := outPos + fftSize
		if end > len(output) {
			end = len(output)
		}
		for i := 0; i < end-outPos; i++ {
			output[outPos+i] += synthFrame[i]
			windowSum[outPos+i] += pv.windowSq[i]
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
