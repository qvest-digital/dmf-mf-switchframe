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
}

// NewLimiter creates a brickwall limiter at -1 dBFS.
// Attack time: 0.1ms (fast enough to catch transients).
// Release time: 50ms (slow enough to avoid pumping).
func NewLimiter(sampleRate int) *Limiter {
	sr := float64(sampleRate)
	return &Limiter{
		threshold:    math.Pow(10, -1.0/20.0), // -1 dBFS ≈ 0.891
		attackCoeff:  1 - math.Exp(-1/(sr*0.0001)),  // 0.1ms
		releaseCoeff: 1 - math.Exp(-1/(sr*0.050)),   // 50ms
	}
}

// Process applies brickwall limiting to the samples in-place.
// Returns the current gain reduction in dB (positive = gain being reduced).
func (l *Limiter) Process(samples []float32) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	threshold := l.threshold
	env := l.envelope

	threshF32 := float32(threshold)

	for i, s := range samples {
		abs := math.Abs(float64(s))

		// Peak-following envelope: fast attack, slow release
		if abs > env {
			env += l.attackCoeff * (abs - env)
		} else {
			env += l.releaseCoeff * (abs - env)
		}

		// Apply envelope-based gain reduction when envelope exceeds threshold
		if env > threshold {
			gain := float32(threshold / env)
			samples[i] = s * gain
		}

		// Brickwall hard clip: guarantee no sample exceeds threshold.
		// The envelope may lag on the first few samples of a transient,
		// so this final clamp ensures true brickwall behavior.
		if samples[i] > threshF32 {
			samples[i] = threshF32
		} else if samples[i] < -threshF32 {
			samples[i] = -threshF32
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

// GainReduction returns the current gain reduction in dB.
// 0 means no limiting is active. Positive values indicate how many dB
// the signal is being reduced.
func (l *Limiter) GainReduction() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gainReduction
}
