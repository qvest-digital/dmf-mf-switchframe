package replay

import "math"

// WSOLATimeStretch performs Waveform Similarity Overlap-Add time-stretching.
// Preserves pitch while changing duration.
//
//   - input: interleaved PCM samples
//   - channels: number of audio channels (1 or 2)
//   - sampleRate: sample rate in Hz
//   - speed: playback speed (0.25-1.0)
//
// For speeds below 0.5x, uses cascaded stretching (two passes at sqrt(speed))
// to avoid the artifacts that single-pass extreme stretching produces.
//
// Returns the time-stretched output samples.
func WSOLATimeStretch(input []float32, channels, sampleRate int, speed float64) []float32 {
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

	// For extreme slow-down (< 0.5x), cascade two moderate stretches.
	// Each pass does sqrt(speed) stretch, e.g., 0.25x → two passes of 0.5x.
	// This produces much better quality than a single extreme stretch.
	if speed < 0.5 {
		intermediate := math.Sqrt(speed)
		pass1 := wsolaStretchSingle(input, channels, intermediate)
		if len(pass1) == 0 {
			return nil
		}
		return wsolaStretchSingle(pass1, channels, intermediate)
	}

	return wsolaStretchSingle(input, channels, speed)
}

// wsolaStretchSingle performs a single WSOLA pass. Speed should be in [0.5, 1.0)
// for best quality.
func wsolaStretchSingle(input []float32, channels int, speed float64) []float32 {
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

	totalSamples := len(input) / channels

	// Window size: 1024 samples (~21ms at 48kHz). This matches typical pitch
	// periods (50-500 Hz → 2-20ms) and produces clean overlap-add.
	windowSamples := 1024
	if windowSamples > totalSamples {
		windowSamples = totalSamples
	}

	// Search range: ±256 samples (~5.3ms). Enough to find matching pitch
	// periods without searching too far and finding false matches.
	searchRange := 256

	outputSamples := int(float64(totalSamples) / speed)
	output := make([]float32, outputSamples*channels)

	synthesisHop := windowSamples / 2
	analysisHop := int(float64(synthesisHop) * speed)
	if analysisHop < 1 {
		analysisHop = 1
	}

	// Periodic Hann window — has exact constant-overlap-add (COLA) property
	// at 50% hop, unlike the symmetric version which has slight ripple.
	hann := makePeriodicHannWindow(windowSamples)

	inputPos := 0
	outputPos := 0

	for outputPos+windowSamples*channels <= len(output) && inputPos+windowSamples <= totalSamples {
		bestOffset := 0
		if inputPos > 0 {
			bestOffset = findBestOverlap(input, output, inputPos, outputPos,
				windowSamples, channels, searchRange)
		}

		srcPos := inputPos + bestOffset
		if srcPos < 0 {
			srcPos = 0
		}
		if srcPos+windowSamples > totalSamples {
			srcPos = totalSamples - windowSamples
		}

		for i := 0; i < windowSamples; i++ {
			w := hann[i]
			for ch := 0; ch < channels; ch++ {
				srcIdx := (srcPos+i)*channels + ch
				dstIdx := (outputPos/channels+i)*channels + ch
				if srcIdx < len(input) && dstIdx < len(output) {
					output[dstIdx] += input[srcIdx] * float32(w)
				}
			}
		}

		inputPos += analysisHop
		outputPos += synthesisHop * channels
	}

	return output
}

// makePeriodicHannWindow creates a periodic Hann window of the given size.
// The periodic form has the exact COLA property at 50% hop:
//   sum of overlapping windows = 1.0 (constant)
func makePeriodicHannWindow(size int) []float64 {
	w := make([]float64, size)
	for i := 0; i < size; i++ {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size)))
	}
	return w
}

func findBestOverlap(input, output []float32, inputPos, outputPos, windowSize, channels, searchRange int) int {
	totalSamples := len(input) / channels
	bestCorr := -math.MaxFloat64
	bestOffset := 0

	// Correlate over the overlap region (synthesisHop = windowSize/2).
	// This gives the best match quality for the actual overlap.
	corrSamples := windowSize / 2
	if corrSamples > totalSamples {
		corrSamples = totalSamples
	}
	corrLen := corrSamples * channels

	for offset := -searchRange; offset <= searchRange; offset++ {
		pos := inputPos + offset
		if pos < 0 || pos+windowSize > totalSamples {
			continue
		}

		aStart := pos * channels
		bStart := outputPos
		if bStart+corrLen > len(output) || aStart+corrLen > len(input) {
			continue
		}

		corr32, normA32, normB32 := crossCorrFloat32(&input[aStart], &output[bStart], corrLen)
		corr := float64(corr32)
		normA := float64(normA32)
		normB := float64(normB32)

		if normA > 0 && normB > 0 {
			ncc := corr / math.Sqrt(normA*normB)
			if ncc > bestCorr {
				bestCorr = ncc
				bestOffset = offset
			}
		}
	}

	return bestOffset
}

// linearTimeStretch stretches audio by linear interpolation between samples.
// This changes pitch (lower at slow speeds) but guarantees continuous output
// with no gaps. Used as a fallback when WSOLA fails.
func linearTimeStretch(input []float32, channels int, speed float64) []float32 {
	if len(input) == 0 || speed <= 0 {
		return nil
	}
	totalSamples := len(input) / channels
	outputSamples := int(float64(totalSamples) / speed)
	if outputSamples <= 0 {
		return nil
	}
	output := make([]float32, outputSamples*channels)

	for i := 0; i < outputSamples; i++ {
		// Map output sample position back to input position.
		srcPos := float64(i) * speed
		srcIdx := int(srcPos)
		frac := float32(srcPos - float64(srcIdx))

		nextIdx := srcIdx + 1
		if nextIdx >= totalSamples {
			nextIdx = totalSamples - 1
		}
		if srcIdx >= totalSamples {
			srcIdx = totalSamples - 1
		}

		for ch := 0; ch < channels; ch++ {
			a := input[srcIdx*channels+ch]
			b := input[nextIdx*channels+ch]
			output[i*channels+ch] = a + (b-a)*frac
		}
	}
	return output
}
