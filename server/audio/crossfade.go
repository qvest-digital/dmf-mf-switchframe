package audio

import (
	"math"
	"sync"

	"github.com/zsiec/switchframe/server/audio/vec"
)

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

// crossfadeGainPool recycles gain buffers used to expand cos/sin tables
// for SIMD crossfade. Typical audio frame is 2048 samples (1024 stereo pairs),
// so most buffers are 2*2048 = 4096 float32s.
var crossfadeGainPool = sync.Pool{
	New: func() any {
		// Start with a reasonable default; growBuf will expand if needed.
		return make([]float32, 0, 2048)
	},
}

// crossfadePadPool recycles zero-padded input buffers for length-mismatched crossfade.
var crossfadePadPool = sync.Pool{
	New: func() any {
		return make([]float32, 0, 2048)
	},
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
//
// Uses SIMD-accelerated vec.MulAddFloat32 kernel for the multiply-add loop,
// with pre-expanded gain arrays from the lookup tables.
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

	// Pre-expand gain arrays from lookup tables.
	// Borrow from pool to avoid per-call allocation.
	cosGains := crossfadeGainPool.Get().([]float32)
	sinGains := crossfadeGainPool.Get().([]float32)
	cosGains = growBuf(cosGains, n)
	sinGains = growBuf(sinGains, n)

	for i := 0; i < n; i++ {
		idx := int(float64(i/channels) / pairCount * float64(crossfadeTableSize-1))
		if idx >= crossfadeTableSize {
			idx = crossfadeTableSize - 1
		}
		cosGains[i] = crossfadeCosTable[idx]
		sinGains[i] = crossfadeSinTable[idx]
	}

	// Ensure both input slices are at least n elements for the SIMD kernel.
	// If they differ in length, zero-pad the shorter one via a pooled buffer.
	oldBuf := oldPCM
	newBuf := newPCM
	var padded []float32 // track which buffer came from pool, if any
	if len(oldPCM) < n {
		padded = crossfadePadPool.Get().([]float32)
		padded = growBuf(padded, n)
		copy(padded[:len(oldPCM)], oldPCM)
		for i := len(oldPCM); i < n; i++ {
			padded[i] = 0
		}
		oldBuf = padded
	} else if len(newPCM) < n {
		padded = crossfadePadPool.Get().([]float32)
		padded = growBuf(padded, n)
		copy(padded[:len(newPCM)], newPCM)
		for i := len(newPCM); i < n; i++ {
			padded[i] = 0
		}
		newBuf = padded
	}

	// SIMD kernel: dst[i] = oldBuf[i]*cosGains[i] + newBuf[i]*sinGains[i]
	vec.MulAddFloat32(&dst[0], &oldBuf[0], &cosGains[0], &newBuf[0], &sinGains[0], n)

	// Return buffers to pools.
	crossfadeGainPool.Put(cosGains)
	crossfadeGainPool.Put(sinGains)
	if padded != nil {
		crossfadePadPool.Put(padded)
	}

	return dst
}
