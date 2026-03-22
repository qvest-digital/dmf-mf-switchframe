package audio

import (
	"errors"
	"math"
	"sync/atomic"
)

// Gate parameter limits.
const (
	gateMinThreshold = -60.0
	gateMaxThreshold = 0.0
	gateMinRangeDB   = -80.0
	gateMaxRangeDB   = -20.0
	gateMinAttackMs  = 0.01
	gateMaxAttackMs  = 10.0
	gateMinReleaseMs = 10.0
	gateMaxReleaseMs = 500.0
	gateMinHoldMs    = 0.0
	gateMaxHoldMs    = 500.0
)

// Gate validation errors.
var (
	ErrInvalidGateThreshold = errors.New("audio: gate threshold must be between -60 and 0 dBFS")
	ErrInvalidGateRange     = errors.New("audio: gate range must be between -80 and -20 dB")
	ErrInvalidGateAttack    = errors.New("audio: gate attack must be between 0.01 and 10 ms")
	ErrInvalidGateRelease   = errors.New("audio: gate release must be between 10 and 500 ms")
	ErrInvalidGateHold      = errors.New("audio: gate hold must be between 0 and 500 ms")
)

// gateParams is an immutable snapshot of gate configuration.
// Swapped atomically so Process() and IsBypassed() are lock-free.
type gateParams struct {
	threshold float64
	rangeDB   float64
	attackMs  float64
	releaseMs float64
	holdMs    float64
	enabled   bool

	thresholdLinear float64 // 10^(threshold/20)
	rangeLinear     float64 // floor gain when gate is closed: 10^(rangeDB/20)
	attackCoeff     float64
	releaseCoeff    float64
	holdSamples     int
}

// Gate is a downward noise gate with hold time. Uses range (depth in dB)
// to control how much attenuation is applied when closed.
//
// Parameters are stored in an immutable gateParams snapshot swapped
// atomically. Envelope state is only written by Process() (single-writer
// from the mixer's processing goroutine). GainReduction is an atomic
// float64 for lock-free metering reads.
type Gate struct {
	params            atomic.Pointer[gateParams]
	envelope          float64 // current gate envelope [rangeLinear..1.0]
	holdCounter       int     // samples remaining in hold period
	gainReductionBits atomic.Uint64
	pendingReset      atomic.Bool
	sampleRate        float64
	channels          int
}

// NewGate creates a new noise gate, starting bypassed (disabled).
// channels specifies the interleaved channel count for linked stereo
// envelope tracking.
func NewGate(sampleRate, channels int) *Gate {
	g := &Gate{
		sampleRate: float64(sampleRate),
		channels:   channels,
		envelope:   1.0, // start open
	}
	// Default: bypassed
	p := newGateParams(-40, -60, 0.5, 100, 50, false, float64(sampleRate))
	g.params.Store(p)
	return g
}

// newGateParams builds an immutable params snapshot.
func newGateParams(threshold, rangeDB, attackMs, releaseMs, holdMs float64, enabled bool, sampleRate float64) *gateParams {
	return &gateParams{
		threshold:       threshold,
		rangeDB:         rangeDB,
		attackMs:        attackMs,
		releaseMs:       releaseMs,
		holdMs:          holdMs,
		enabled:         enabled,
		thresholdLinear: math.Pow(10, threshold/20.0),
		rangeLinear:     math.Pow(10, rangeDB/20.0),
		attackCoeff:     1 - math.Exp(-1/(sampleRate*attackMs/1000.0)),
		releaseCoeff:    1 - math.Exp(-1/(sampleRate*releaseMs/1000.0)),
		holdSamples:     int(sampleRate * holdMs / 1000.0),
	}
}

// SetParams sets all gate parameters at once. Validates all parameters
// and returns an error if any are out of range.
func (g *Gate) SetParams(threshold, rangeDB, attackMs, releaseMs, holdMs float64, enabled bool) error {
	if threshold < gateMinThreshold || threshold > gateMaxThreshold {
		return ErrInvalidGateThreshold
	}
	if rangeDB < gateMinRangeDB || rangeDB > gateMaxRangeDB {
		return ErrInvalidGateRange
	}
	if attackMs < gateMinAttackMs || attackMs > gateMaxAttackMs {
		return ErrInvalidGateAttack
	}
	if releaseMs < gateMinReleaseMs || releaseMs > gateMaxReleaseMs {
		return ErrInvalidGateRelease
	}
	if holdMs < gateMinHoldMs || holdMs > gateMaxHoldMs {
		return ErrInvalidGateHold
	}
	g.params.Store(newGateParams(threshold, rangeDB, attackMs, releaseMs, holdMs, enabled, g.sampleRate))
	return nil
}

// IsBypassed returns true when the gate is disabled or has no params.
// Lock-free: reads the atomic params snapshot.
func (g *Gate) IsBypassed() bool {
	p := g.params.Load()
	return p == nil || !p.enabled
}

// GainReduction returns the current gain reduction in dB.
// 0 means the gate is fully open. Positive values indicate dB of attenuation.
// Lock-free: reads the atomic float64.
func (g *Gate) GainReduction() float64 {
	bits := g.gainReductionBits.Load()
	return math.Float64frombits(bits)
}

// Reset clears the envelope state, restoring the gate to fully open.
// Called when the program bus transitions to mute (FTB) so that stale
// envelope state does not briefly gate audio on unmute.
func (g *Gate) Reset() {
	g.pendingReset.Store(true)
}

// Process applies the gate to interleaved PCM in-place.
// Lock-free: reads params atomically, writes envelope state (single-writer).
func (g *Gate) Process(pcm []float32) {
	p := g.params.Load()
	if p == nil || !p.enabled {
		return
	}
	if g.pendingReset.CompareAndSwap(true, false) {
		g.envelope = 1.0
		g.holdCounter = 0
		g.gainReductionBits.Store(0)
	}

	ch := g.channels
	if ch < 1 {
		ch = 1
	}

	for i := 0; i < len(pcm); i += ch {
		// Peak detect across channels (stereo-linked envelope)
		var peak float32
		for j := 0; j < ch && i+j < len(pcm); j++ {
			s := pcm[i+j]
			if s < 0 {
				s = -s
			}
			if s > peak {
				peak = s
			}
		}

		if float64(peak) >= p.thresholdLinear {
			// Above threshold: open gate (attack toward 1.0)
			g.envelope += p.attackCoeff * (1.0 - g.envelope)
			g.holdCounter = p.holdSamples
		} else {
			// Below threshold
			if g.holdCounter > 0 {
				g.holdCounter--
				// Hold: keep current envelope
			} else {
				// Release toward range floor
				target := p.rangeLinear
				g.envelope += p.releaseCoeff * (target - g.envelope)
			}
		}

		// Apply envelope gain
		gain := float32(g.envelope)
		for j := 0; j < ch && i+j < len(pcm); j++ {
			pcm[i+j] *= gain
		}
	}

	// Store GR for metering (dB, positive value = amount of reduction)
	gr := 0.0
	if g.envelope < 1.0 && g.envelope > 0 {
		gr = -20 * math.Log10(g.envelope)
	}
	g.gainReductionBits.Store(math.Float64bits(gr))
}

// Settings returns current gate parameters for state broadcast.
func (g *Gate) Settings() (threshold, rangeDB, attack, release, hold float64, enabled bool) {
	p := g.params.Load()
	if p == nil {
		return -40, -60, 0.5, 100, 50, false
	}
	return p.threshold, p.rangeDB, p.attackMs, p.releaseMs, p.holdMs, p.enabled
}
