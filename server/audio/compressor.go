package audio

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
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
	ErrInvalidThreshold  = errors.New("audio: threshold must be between -40 and 0 dBFS")
	ErrInvalidRatio      = errors.New("audio: ratio must be between 1.0 and 20.0")
	ErrInvalidAttack     = errors.New("audio: attack must be between 0.1 and 100 ms")
	ErrInvalidRelease    = errors.New("audio: release must be between 10 and 1000 ms")
	ErrInvalidMakeupGain = errors.New("audio: makeup gain must be between 0 and 24 dB")
)

// compressorParams is an immutable snapshot of compressor configuration.
// Swapped atomically so Process() and IsBypassed() are lock-free.
type compressorParams struct {
	threshold  float64
	ratio      float64
	attackMs   float64
	releaseMs  float64
	makeupGain float64

	thresholdLinear float64
	attackCoeff     float64
	releaseCoeff    float64
	makeupLinear    float64

	gainTable [256]float32
}

// Compressor is a single-band dynamics compressor with envelope follower.
// Uses an exponential envelope detector with configurable threshold, ratio,
// attack, release, and makeup gain.
//
// Parameters are stored in an immutable compressorParams snapshot swapped
// atomically. Envelope state is only written by Process() (single-writer
// from the mixer's processing goroutine). GainReduction is an atomic float64
// for lock-free metering reads.
type Compressor struct {
	params atomic.Pointer[compressorParams]

	// Envelope state: only written by Process() (single-writer, no lock needed)
	envelope float64

	// Lock-free GR metering via atomic float64 encoding
	gainReductionBits atomic.Uint64

	// Reset flag: set by Reset(), cleared by Process()
	pendingReset atomic.Bool

	// Retained for tests that directly inspect envelope under lock
	mu sync.Mutex

	sampleRate float64
	channels   int
}

// newCompressorParams builds an immutable params snapshot.
func newCompressorParams(threshold, ratio, attackMs, releaseMs, makeupGain, sampleRate float64) *compressorParams {
	p := &compressorParams{
		threshold:       threshold,
		ratio:           ratio,
		attackMs:        attackMs,
		releaseMs:       releaseMs,
		makeupGain:      makeupGain,
		thresholdLinear: math.Pow(10, threshold/20.0),
		attackCoeff:     1 - math.Exp(-1.0/(sampleRate*attackMs/1000.0)),
		releaseCoeff:    1 - math.Exp(-1.0/(sampleRate*releaseMs/1000.0)),
		makeupLinear:    math.Pow(10, makeupGain/20.0),
	}
	for i := 0; i < len(p.gainTable); i++ {
		overDB := float64(i) * 0.25
		if overDB > 0 && ratio > 1.0 {
			reductionDB := overDB * (1 - 1/ratio)
			p.gainTable[i] = float32(math.Pow(10, -reductionDB/20.0))
		} else {
			p.gainTable[i] = 1.0
		}
	}
	return p
}

// NewCompressor creates a new compressor with default parameters (bypassed: ratio 1:1).
// channels specifies the interleaved channel count for linked stereo envelope tracking.
func NewCompressor(sampleRate, channels int) *Compressor {
	if channels < 1 {
		channels = 1
	}
	c := &Compressor{
		sampleRate: float64(sampleRate),
		channels:   channels,
	}
	c.params.Store(newCompressorParams(0, 1.0, 5.0, 100.0, 0, float64(sampleRate)))
	return c
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

	c.params.Store(newCompressorParams(threshold, ratio, attackMs, releaseMs, makeupGain, c.sampleRate))
	return nil
}

// GetParams returns the current compressor parameters.
func (c *Compressor) GetParams() (threshold, ratio, attackMs, releaseMs, makeupGain float64) {
	p := c.params.Load()
	return p.threshold, p.ratio, p.attackMs, p.releaseMs, p.makeupGain
}

// IsBypassed returns true when the compressor has no audible effect:
// ratio <= 1.0 (no compression) AND no makeup gain applied.
// Lock-free: reads the atomic params snapshot.
func (c *Compressor) IsBypassed() bool {
	p := c.params.Load()
	return p.ratio <= 1.0 && p.makeupGain == 0
}

// Reset clears the envelope and gain reduction state.
// Called when the program bus transitions to mute (FTB) so that stale
// envelope state does not briefly suppress audio on unmute.
func (c *Compressor) Reset() {
	c.gainReductionBits.Store(0)
	c.pendingReset.Store(true)
}

// GainReduction returns the current gain reduction in dB.
// 0 means no compression is active. Positive values indicate dB of reduction.
// Lock-free: reads the atomic float64.
func (c *Compressor) GainReduction() float64 {
	return math.Float64frombits(c.gainReductionBits.Load())
}

// Process applies compression to the samples in-place and returns the result.
// Lock-free: reads params atomically, writes envelope state (single-writer).
func (c *Compressor) Process(samples []float32) []float32 {
	if c.pendingReset.CompareAndSwap(true, false) {
		c.envelope = 0
		c.gainReductionBits.Store(0)
	}

	p := c.params.Load()

	threshold := p.thresholdLinear
	env := c.envelope
	makeupGain := float32(p.makeupLinear)
	attackCoeff := p.attackCoeff
	releaseCoeff := p.releaseCoeff
	ch := c.channels
	gainTable := &p.gainTable

	for i := 0; i < len(samples); i += ch {
		var peak float64
		for j := 0; j < ch && i+j < len(samples); j++ {
			abs := math.Abs(float64(samples[i+j]))
			if abs > peak {
				peak = abs
			}
		}

		if peak > env {
			env += attackCoeff * (peak - env)
		} else {
			env += releaseCoeff * (peak - env)
		}
		if env < 1e-15 {
			env = 0
		}

		var gain float32 = 1.0
		if env > threshold && threshold > 0 {
			overDB := 20 * math.Log10(env/threshold)
			idx := int(overDB * 4)
			if idx >= len(gainTable) {
				idx = len(gainTable) - 1
			}
			if idx < 0 {
				idx = 0
			}
			gain = gainTable[idx]
		}

		for j := 0; j < ch && i+j < len(samples); j++ {
			samples[i+j] = samples[i+j] * gain * makeupGain
		}
	}

	c.envelope = env

	var gr float64
	if env > threshold && threshold > 0 {
		overDB := 20 * math.Log10(env/threshold)
		gr = overDB * (1 - 1/p.ratio)
	}
	c.gainReductionBits.Store(math.Float64bits(gr))

	return samples
}
