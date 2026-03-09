package audio

import (
	"math"

	"github.com/zsiec/switchframe/server/audio/vec"
)

// PeakLevel computes the peak absolute amplitude for each channel from
// interleaved float32 PCM samples. Returns linear values in [0, 1+].
// For stereo (channels=2), even indices are left, odd are right.
//
// For mono, delegates directly to vec.PeakAbsFloat32 (SIMD-accelerated).
// For stereo, deinterleaves into stack buffers and calls PeakAbsFloat32 twice.
func PeakLevel(pcm []float32, channels int) (peakL, peakR float64) {
	if len(pcm) == 0 {
		return
	}
	if channels == 1 {
		peakL = float64(vec.PeakAbsFloat32(&pcm[0], len(pcm)))
		return
	}
	if channels == 2 {
		if len(pcm) < 2 {
			return
		}
		l, r := vec.PeakAbsStereoFloat32(&pcm[0], len(pcm))
		peakL = float64(l)
		peakR = float64(r)
		return
	}
	// Fallback for channels > 2: original scalar path
	for i := 0; i < len(pcm); i += channels {
		v := math.Abs(float64(pcm[i]))
		if v > peakL {
			peakL = v
		}
		if channels >= 2 && i+1 < len(pcm) {
			v2 := math.Abs(float64(pcm[i+1]))
			if v2 > peakR {
				peakR = v2
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
