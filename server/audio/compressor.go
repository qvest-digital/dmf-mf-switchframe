package audio

import (
	"errors"
	"math"
	"sync"
)

// Compressor parameter limits
const (
	compMinThreshold  = -40.0
	compMaxThreshold  = 0.0
	compMinRatio      = 1.0
	compMaxRatio      = 20.0
	compMinAttackMs   = 0.1
	compMaxAttackMs   = 100.0
	compMinReleaseMs  = 10.0
	compMaxReleaseMs  = 1000.0
	compMinMakeupGain = 0.0
	compMaxMakeupGain = 24.0
)

// Compressor validation errors
var (
	ErrInvalidThreshold  = errors.New("threshold must be between -40 and 0 dBFS")
	ErrInvalidRatio      = errors.New("ratio must be between 1.0 and 20.0")
	ErrInvalidAttack     = errors.New("attack must be between 0.1 and 100 ms")
	ErrInvalidRelease    = errors.New("release must be between 10 and 1000 ms")
	ErrInvalidMakeupGain = errors.New("makeup gain must be between 0 and 24 dB")
)

// Compressor is a single-band dynamics compressor with envelope follower.
// Uses an exponential envelope detector (same pattern as limiter.go) with
// configurable threshold, ratio, attack, release, and makeup gain.
type Compressor struct {
	mu sync.Mutex

	// Parameters
	threshold  float64 // dBFS (-40 to 0)
	ratio      float64 // 1:1 to 20:1
	attackMs   float64 // ms (0.1 to 100)
	releaseMs  float64 // ms (10 to 1000)
	makeupGain float64 // dB (0 to 24)

	// Derived coefficients
	thresholdLinear float64 // linear amplitude of threshold
	attackCoeff     float64 // envelope attack coefficient
	releaseCoeff    float64 // envelope release coefficient
	makeupLinear    float64 // linear gain for makeup

	// State
	envelope      float64 // current envelope level (linear)
	gainReduction float64 // current GR in dB

	sampleRate float64
}

// NewCompressor creates a new compressor with default parameters (bypassed: ratio 1:1).
func NewCompressor(sampleRate int) *Compressor {
	c := &Compressor{
		threshold:  0,
		ratio:      1.0,
		attackMs:   5.0,
		releaseMs:  100.0,
		makeupGain: 0,
		sampleRate: float64(sampleRate),
	}
	c.recalcCoefficients()
	return c
}

// recalcCoefficients updates derived values from the current parameters.
// Caller must hold c.mu or be in the constructor.
func (c *Compressor) recalcCoefficients() {
	sr := c.sampleRate
	c.thresholdLinear = math.Pow(10, c.threshold/20.0)
	c.attackCoeff = 1 - math.Exp(-1.0/(sr*c.attackMs/1000.0))
	c.releaseCoeff = 1 - math.Exp(-1.0/(sr*c.releaseMs/1000.0))
	c.makeupLinear = math.Pow(10, c.makeupGain/20.0)
}

// SetParams sets all compressor parameters at once. Validates all parameters
// and returns an error if any are out of range.
func (c *Compressor) SetParams(threshold, ratio, attackMs, releaseMs, makeupGain float64) error {
	if threshold < compMinThreshold || threshold > compMaxThreshold {
		return ErrInvalidThreshold
	}
	if ratio < compMinRatio || ratio > compMaxRatio {
		return ErrInvalidRatio
	}
	if attackMs < compMinAttackMs || attackMs > compMaxAttackMs {
		return ErrInvalidAttack
	}
	if releaseMs < compMinReleaseMs || releaseMs > compMaxReleaseMs {
		return ErrInvalidRelease
	}
	if makeupGain < compMinMakeupGain || makeupGain > compMaxMakeupGain {
		return ErrInvalidMakeupGain
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.threshold = threshold
	c.ratio = ratio
	c.attackMs = attackMs
	c.releaseMs = releaseMs
	c.makeupGain = makeupGain
	c.recalcCoefficients()
	return nil
}

// GetParams returns the current compressor parameters.
func (c *Compressor) GetParams() (threshold, ratio, attackMs, releaseMs, makeupGain float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.threshold, c.ratio, c.attackMs, c.releaseMs, c.makeupGain
}

// IsBypassed returns true when the compressor has no audible effect:
// ratio <= 1.0 (no compression) AND no makeup gain applied.
func (c *Compressor) IsBypassed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ratio <= 1.0 && c.makeupGain == 0
}

// GainReduction returns the current gain reduction in dB.
// 0 means no compression is active. Positive values indicate dB of reduction.
func (c *Compressor) GainReduction() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gainReduction
}

// Process applies compression to the samples in-place and returns the result.
// The compression algorithm:
// 1. Envelope follower tracks the signal level (fast attack, slow release)
// 2. When envelope exceeds threshold, gain reduction is applied based on ratio
// 3. Makeup gain is applied to the entire signal
func (c *Compressor) Process(samples []float32) []float32 {
	c.mu.Lock()
	defer c.mu.Unlock()

	threshold := c.thresholdLinear
	ratio := c.ratio
	env := c.envelope
	makeupGain := float32(c.makeupLinear)
	attackCoeff := c.attackCoeff
	releaseCoeff := c.releaseCoeff

	for i, s := range samples {
		abs := math.Abs(float64(s))

		// Peak-following envelope: fast attack, slow release
		if abs > env {
			env += attackCoeff * (abs - env)
		} else {
			env += releaseCoeff * (abs - env)
		}

		// Compute gain reduction when envelope exceeds threshold
		if env > threshold && threshold > 0 {
			// How many dB above threshold
			overDB := 20 * math.Log10(env/threshold)
			// Desired reduction: overDB - overDB/ratio = overDB * (1 - 1/ratio)
			reductionDB := overDB * (1 - 1/ratio)
			// Convert to linear gain
			gain := float32(math.Pow(10, -reductionDB/20.0))
			samples[i] = s * gain
		}

		// Apply makeup gain
		samples[i] *= makeupGain
	}

	c.envelope = env

	// Compute and store GR in dB for metering
	if env > threshold && threshold > 0 {
		overDB := 20 * math.Log10(env/threshold)
		c.gainReduction = overDB * (1 - 1/ratio)
	} else {
		c.gainReduction = 0
	}

	return samples
}
