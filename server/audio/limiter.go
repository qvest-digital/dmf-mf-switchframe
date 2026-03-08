package audio

import (
	"math"
	"sync"
	"sync/atomic"
)

// Limiter is a brickwall peak limiter at -1 dBFS for the program output bus.
// It uses a peak-following envelope with fast attack and slow release to prevent
// clipping without audible pumping. Always active on the program bus.
//
// Process() is single-threaded (called from the mixer's processing goroutine).
// GainReduction is stored as an atomic float64 for lock-free metering reads.
// Reset() sets an atomic flag; Process() performs the actual state clearing.
type Limiter struct {
	threshold    float64 // linear amplitude threshold (-1 dBFS)
	attackCoeff  float64 // envelope attack coefficient
	releaseCoeff float64 // envelope release coefficient
	channels     int     // interleaved channel count for linked stereo

	// Envelope state: only written by Process() (single-writer)
	envelope float64

	// Lock-free GR metering via atomic float64 encoding
	gainReductionBits atomic.Uint64

	// Reset flag: set by Reset(), cleared by Process()
	pendingReset atomic.Bool

	// Retained for tests that directly inspect envelope under lock
	mu sync.Mutex
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
		threshold:    math.Pow(10, -1.0/20.0),      // -1 dBFS ≈ 0.891
		attackCoeff:  1 - math.Exp(-1/(sr*0.0001)), // 0.1ms
		releaseCoeff: 1 - math.Exp(-1/(sr*0.050)),  // 50ms
		channels:     channels,
	}
}

// Process applies brickwall limiting to the samples in-place.
// Returns the current gain reduction in dB (positive = gain being reduced).
//
// Lock-free: envelope state is single-writer (mixer goroutine).
// GR stored atomically for concurrent metering reads.
func (l *Limiter) Process(samples []float32) float64 {
	if l.pendingReset.CompareAndSwap(true, false) {
		l.envelope = 0
		l.gainReductionBits.Store(0)
	}

	threshold := l.threshold
	env := l.envelope
	threshF32 := float32(threshold)
	ch := l.channels

	for i := 0; i < len(samples); i += ch {
		var peak float64
		for j := 0; j < ch && i+j < len(samples); j++ {
			abs := math.Abs(float64(samples[i+j]))
			if abs > peak {
				peak = abs
			}
		}

		if peak > env {
			env += l.attackCoeff * (peak - env)
		} else {
			env += l.releaseCoeff * (peak - env)
		}
		if env < 1e-15 {
			env = 0
		}

		var gain float32 = 1.0
		if env > threshold {
			gain = float32(threshold / env)
		}

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

	var gr float64
	if env > threshold {
		gr = 20 * math.Log10(env/threshold)
	}
	l.gainReductionBits.Store(math.Float64bits(gr))

	return gr
}

// Reset clears the envelope and gain reduction state.
// Called when the program bus transitions to mute (FTB) so that stale
// envelope state does not briefly suppress audio on unmute.
func (l *Limiter) Reset() {
	l.gainReductionBits.Store(0)
	l.pendingReset.Store(true)
}

// GainReduction returns the current gain reduction in dB.
// 0 means no limiting is active. Positive values indicate how many dB
// the signal is being reduced.
// Lock-free: reads the atomic float64.
func (l *Limiter) GainReduction() float64 {
	return math.Float64frombits(l.gainReductionBits.Load())
}
