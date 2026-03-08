package audio

import "math"

const crossfadeTableSize = 1024

var crossfadeCosTable [crossfadeTableSize]float32
var crossfadeSinTable [crossfadeTableSize]float32

func init() {
	for i := range crossfadeCosTable {
		t := float64(i) / float64(crossfadeTableSize-1)
		crossfadeCosTable[i] = float32(math.Cos(t * math.Pi / 2))
		crossfadeSinTable[i] = float32(math.Sin(t * math.Pi / 2))
	}
}

// EqualPowerCrossfade applies an equal-power crossfade between oldPCM and newPCM.
// Assumes mono (1 channel). For stereo/multi-channel, use EqualPowerCrossfadeStereo.
func EqualPowerCrossfade(oldPCM, newPCM []float32) []float32 {
	return EqualPowerCrossfadeStereoInto(nil, oldPCM, newPCM, 1)
}

// EqualPowerCrossfadeInto is like EqualPowerCrossfade but writes into dst.
// If dst has insufficient capacity, it is grown. Returns the result slice.
func EqualPowerCrossfadeInto(dst, oldPCM, newPCM []float32) []float32 {
	return EqualPowerCrossfadeStereoInto(dst, oldPCM, newPCM, 1)
}

// EqualPowerCrossfadeStereo applies an equal-power crossfade between oldPCM and newPCM,
// using cos/sin curves so total power remains constant through the transition:
//
//	cos²(t·π/2) + sin²(t·π/2) = 1 for all t ∈ [0,1]
//
// At t=0 the result is purely old; at t=1 the result is purely new.
// The output length is max(len(oldPCM), len(newPCM)); the shorter buffer is zero-padded.
//
// channels specifies the interleaved channel count. The crossfade position advances
// per sample-pair (not per individual sample) so all channels at the same time
// instant receive identical gain, preventing L/R phase skew.
func EqualPowerCrossfadeStereo(oldPCM, newPCM []float32, channels int) []float32 {
	return EqualPowerCrossfadeStereoInto(nil, oldPCM, newPCM, channels)
}

// EqualPowerCrossfadeStereoInto is like EqualPowerCrossfadeStereo but writes into dst.
// If dst has insufficient capacity, it is grown. Returns the result slice.
func EqualPowerCrossfadeStereoInto(dst, oldPCM, newPCM []float32, channels int) []float32 {
	if channels < 1 {
		channels = 1
	}
	n := len(oldPCM)
	if len(newPCM) > n {
		n = len(newPCM)
	}
	if n == 0 {
		return nil
	}

	pairCount := float64(n / channels)
	if pairCount < 1 {
		pairCount = 1
	}

	if cap(dst) >= n {
		dst = dst[:n]
	} else {
		dst = make([]float32, n)
	}
	for i := 0; i < n; i++ {
		idx := int(float64(i/channels) / pairCount * float64(crossfadeTableSize-1))
		if idx >= crossfadeTableSize {
			idx = crossfadeTableSize - 1
		}
		cosGain := crossfadeCosTable[idx]
		sinGain := crossfadeSinTable[idx]
		var oldSample, newSample float32
		if i < len(oldPCM) {
			oldSample = oldPCM[i]
		}
		if i < len(newPCM) {
			newSample = newPCM[i]
		}
		dst[i] = oldSample*cosGain + newSample*sinGain
	}
	return dst
}
