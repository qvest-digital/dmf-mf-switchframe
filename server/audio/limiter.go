package audio

import (
	"math"
	"sync"
)

// Limiter is a brickwall peak limiter at -1 dBFS for the program output bus.
// It uses a peak-following envelope with fast attack and slow release to prevent
// clipping without audible pumping. Always active on the program bus.
type Limiter struct {
	mu            sync.Mutex
	threshold     float64 // linear amplitude threshold (-1 dBFS)
	attackCoeff   float64 // envelope attack coefficient
	releaseCoeff  float64 // envelope release coefficient
	envelope      float64 // current envelope level (linear)
	gainReduction float64 // current gain reduction in dB (0 = no reduction)
	channels      int     // interleaved channel count for linked stereo
}

// NewLimiter creates a brickwall limiter at -1 dBFS.
// Attack time: 0.1ms (fast enough to catch transients).
// Release time: 50ms (slow enough to avoid pumping).
// channels specifies the interleaved channel count for linked stereo envelope tracking.
func NewLimiter(sampleRate, channels int) *Limiter {
	if channels < 1 {
		channels = 1
	}
	sr := float64(sampleRate)
	return &Limiter{
		threshold:    math.Pow(10, -1.0/20.0), // -1 dBFS ≈ 0.891
		attackCoeff:  1 - math.Exp(-1/(sr*0.0001)),  // 0.1ms
		releaseCoeff: 1 - math.Exp(-1/(sr*0.050)),   // 50ms
		channels:     channels,
	}
}

// Process applies brickwall limiting to the samples in-place.
// Returns the current gain reduction in dB (positive = gain being reduced).
//
// For multi-channel (stereo) audio, the envelope is linked: the peak across
// all channels in each sample group drives a single envelope, and the same
// gain reduction is applied to all channels. This prevents stereo image shift.
func (l *Limiter) Process(samples []float32) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	threshold := l.threshold
	env := l.envelope
	threshF32 := float32(threshold)
	ch := l.channels

	for i := 0; i < len(samples); i += ch {
		// Find peak across all channels in this group
		var peak float64
		for j := 0; j < ch && i+j < len(samples); j++ {
			abs := math.Abs(float64(samples[i+j]))
			if abs > peak {
				peak = abs
			}
		}

		// Peak-following envelope: fast attack, slow release
		if peak > env {
			env += l.attackCoeff * (peak - env)
		} else {
			env += l.releaseCoeff * (peak - env)
		}

		// Compute gain for this group
		var gain float32 = 1.0
		if env > threshold {
			gain = float32(threshold / env)
		}

		// Apply same gain to all channels + brickwall clamp
		for j := 0; j < ch && i+j < len(samples); j++ {
			samples[i+j] *= gain
			if samples[i+j] > threshF32 {
				samples[i+j] = threshF32
			} else if samples[i+j] < -threshF32 {
				samples[i+j] = -threshF32
			}
		}
	}

	l.envelope = env

	// Compute and store GR in dB
	if env > threshold {
		l.gainReduction = 20 * math.Log10(env/threshold)
	} else {
		l.gainReduction = 0
	}

	return l.gainReduction
}

// Reset clears the envelope and gain reduction state.
// Called when the program bus transitions to mute (FTB) so that stale
// envelope state does not briefly suppress audio on unmute.
func (l *Limiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.envelope = 0
	l.gainReduction = 0
}

// GainReduction returns the current gain reduction in dB.
// 0 means no limiting is active. Positive values indicate how many dB
// the signal is being reduced.
func (l *Limiter) GainReduction() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gainReduction
}
