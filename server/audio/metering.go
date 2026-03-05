package audio

import "math"

// PeakLevel computes the peak absolute amplitude for each channel from
// interleaved float32 PCM samples. Returns linear values in [0, 1+].
// For stereo (channels=2), even indices are left, odd are right.
func PeakLevel(pcm []float32, channels int) (peakL, peakR float64) {
	for i := 0; i < len(pcm); i += channels {
		if channels >= 1 {
			v := math.Abs(float64(pcm[i]))
			if v > peakL {
				peakL = v
			}
		}
		if channels >= 2 && i+1 < len(pcm) {
			v := math.Abs(float64(pcm[i+1]))
			if v > peakR {
				peakR = v
			}
		}
	}
	return
}

// LinearToDBFS converts a linear amplitude (0..1) to dBFS.
// Returns -96 for silence (linear <= 0). Clamped to avoid -Inf
// which is not JSON-serializable.
func LinearToDBFS(linear float64) float64 {
	if linear <= 0 {
		return -96
	}
	db := 20 * math.Log10(linear)
	if db < -96 {
		return -96
	}
	return db
}
