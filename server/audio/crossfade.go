package audio

import "math"

// EqualPowerCrossfade applies an equal-power crossfade between oldPCM and newPCM.
// It uses cos/sin curves so total power remains constant through the transition:
//
//	cos²(t·π/2) + sin²(t·π/2) = 1 for all t ∈ [0,1]
//
// At t=0 the result is purely old; at t=1 the result is purely new.
// The output length is max(len(oldPCM), len(newPCM)); the shorter buffer is zero-padded.
func EqualPowerCrossfade(oldPCM, newPCM []float32) []float32 {
	n := len(oldPCM)
	if len(newPCM) > n {
		n = len(newPCM)
	}
	if n == 0 {
		return nil
	}

	result := make([]float32, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(n)
		cosGain := float32(math.Cos(t * math.Pi / 2))
		sinGain := float32(math.Sin(t * math.Pi / 2))
		var oldSample, newSample float32
		if i < len(oldPCM) {
			oldSample = oldPCM[i]
		}
		if i < len(newPCM) {
			newSample = newPCM[i]
		}
		result[i] = oldSample*cosGain + newSample*sinGain
	}
	return result
}
