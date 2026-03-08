package replay

import "math"

const (
	wsolaWindowSize  = 1024 // ~21.3ms at 48kHz
	wsolaSearchRange = 256  // +/-5.3ms search range
)

// WSOLATimeStretch performs Waveform Similarity Overlap-Add time-stretching.
// Preserves pitch while changing duration.
//
//   - input: interleaved PCM samples
//   - channels: number of audio channels (1 or 2)
//   - sampleRate: sample rate in Hz
//   - speed: playback speed (0.25-1.0)
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

	totalSamples := len(input) / channels
	windowSamples := wsolaWindowSize
	if windowSamples > totalSamples {
		windowSamples = totalSamples
	}

	outputSamples := int(float64(totalSamples) / speed)
	output := make([]float32, outputSamples*channels)

	synthesisHop := windowSamples / 2
	analysisHop := int(float64(synthesisHop) * speed)
	if analysisHop < 1 {
		analysisHop = 1
	}

	hann := makeHannWindow(windowSamples)

	inputPos := 0
	outputPos := 0

	for outputPos+windowSamples*channels <= len(output) && inputPos+windowSamples <= totalSamples {
		bestOffset := 0
		if inputPos > 0 {
			bestOffset = findBestOverlap(input, output, inputPos, outputPos,
				windowSamples, channels, wsolaSearchRange)
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

func makeHannWindow(size int) []float64 {
	w := make([]float64, size)
	for i := 0; i < size; i++ {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size-1)))
	}
	return w
}

func findBestOverlap(input, output []float32, inputPos, outputPos, windowSize, channels, searchRange int) int {
	totalSamples := len(input) / channels
	bestCorr := -math.MaxFloat64
	bestOffset := 0

	for offset := -searchRange; offset <= searchRange; offset++ {
		pos := inputPos + offset
		if pos < 0 || pos+windowSize > totalSamples {
			continue
		}

		var corr, normA, normB float64
		for i := 0; i < windowSize && i < searchRange; i++ {
			for ch := 0; ch < channels; ch++ {
				a := float64(input[(pos+i)*channels+ch])
				outIdx := (outputPos/channels+i)*channels + ch
				if outIdx >= len(output) {
					continue
				}
				b := float64(output[outIdx])
				corr += a * b
				normA += a * a
				normB += b * b
			}
		}

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
