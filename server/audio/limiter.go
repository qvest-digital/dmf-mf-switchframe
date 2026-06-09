package audio

import (
	"math"
	"sync"
	"sync/atomic"
)

// Limiter is a true-peak limiter for the program output bus. It uses a
// peak-following envelope with fast attack and slow release driven by an
// ITU-R BS.1770-4 Annex 2 4× polyphase FIR true-peak detector, so inter-sample
// peaks are caught before they reach a downstream DAC. Always active on the
// program bus.
//
// Ceiling guarantee — steady state only. This is a feed-forward design with
// zero lookahead and a finite attack coefficient (0.1ms). The dBTP ceiling is
// only guaranteed once the envelope has settled: on a fast transient ONSET the
// envelope needs several samples to rise, so the first samples are
// under-attenuated and the reconstructed true-peak can briefly exceed the
// configured ceiling (measured ~1 dB of overshoot on a worst-case
// silence→inter-sample-peak step; see TestLimiter_TransientOnsetOvershoot).
// The sample-domain hard clamp below clamps the SAMPLE value to ±target, not
// the reconstructed true-peak, so it does not close this gap. Making the
// ceiling truly brickwall on transients requires a short (1-2ms) lookahead
// delay so gain reduction is applied before the peak it must attenuate; that is
// a follow-up because it adds output latency and changes the audio path.
//
// Process() is single-threaded (called from the mixer's processing goroutine).
// GainReduction and TruePeak are stored as atomic float64 for lock-free
// metering reads.  Reset() sets an atomic flag; Process() performs the
// actual state clearing.
type Limiter struct {
	threshold    float64 // linear amplitude threshold (default -1 dBTP)
	attackCoeff  float64 // envelope attack coefficient
	releaseCoeff float64 // envelope release coefficient
	channels     int     // interleaved channel count for linked stereo
	ceiling      float64 // linear amplitude ceiling (default 1.0 = 0 dBTP)
	ceilingDB    float64 // dBTP readback cache (default 0)

	// Envelope state: only written by Process() (single-writer)
	envelope float64

	// ITU-R BS.1770-4 Annex 2 true-peak detector state.
	// tpHistory[ch] is a 12-sample history ring buffer per channel, newest-first.
	// tpIdx is the write index (shared — all channels advance in lockstep).
	tpHistory [][12]float64

	// Lock-free GR + TP metering via atomic float64 encoding
	gainReductionBits atomic.Uint64
	truePeakBits      atomic.Uint64

	// Reset flag: set by Reset(), cleared by Process()
	pendingReset atomic.Bool

	// Retained for tests that directly inspect envelope under lock
	mu sync.Mutex
}

// truePeakFIRCoefficients returns the 4-phase 12-tap polyphase FIR filter
// coefficients from ITU-R BS.1770-4 Annex 2 for 4× oversampling true-peak
// estimation.  Phase 0 is the (near-)identity phase; phases 1-3 interpolate
// at 1/4, 2/4, 3/4 sample offsets.
func truePeakFIRCoefficients() [4][12]float64 {
	return [4][12]float64{
		{
			0.0017089843750, 0.0109863281250, -0.0196533203125, 0.0332031250000,
			-0.0594482421875, 0.1373291015625, 0.9721679687500, -0.1022949218750,
			0.0476074218750, -0.0266113281250, 0.0148925781250, -0.0083007812500,
		},
		{
			-0.0291748046875, 0.0292968750000, -0.0517578125000, 0.0891113281250,
			-0.1665039062500, 0.4650878906250, 0.7797851562500, -0.2003173828125,
			0.1015625000000, -0.0582275390625, 0.0330810546875, -0.0189208984375,
		},
		{
			-0.0189208984375, 0.0330810546875, -0.0582275390625, 0.1015625000000,
			-0.2003173828125, 0.7797851562500, 0.4650878906250, -0.1665039062500,
			0.0891113281250, -0.0517578125000, 0.0292968750000, -0.0291748046875,
		},
		{
			-0.0083007812500, 0.0148925781250, -0.0266113281250, 0.0476074218750,
			-0.1022949218750, 0.9721679687500, 0.1373291015625, -0.0594482421875,
			0.0332031250000, -0.0196533203125, 0.0109863281250, 0.0017089843750,
		},
	}
}

var tpCoeffs = truePeakFIRCoefficients()

// NewLimiter creates a brickwall true-peak limiter at -1 dBTP (EBU R128 s1).
// Attack time: 0.1 ms (fast enough to catch transients).
// Release time: 50 ms (slow enough to avoid pumping).
// channels specifies the interleaved channel count for linked stereo envelope tracking.
//
// Threshold and ceiling values are in dBTP (decibels relative to full scale,
// true-peak estimate) rather than sample-peak dBFS.  For broadcast compliance
// (ATSC A/85) operators should tighten the ceiling to -2 dBTP via SetCeiling.
func NewLimiter(sampleRate, channels int) *Limiter {
	if channels < 1 {
		channels = 1
	}
	sr := float64(sampleRate)
	l := &Limiter{
		threshold:    math.Pow(10, -1.0/20.0),      // -1 dBTP ≈ 0.891
		attackCoeff:  1 - math.Exp(-1/(sr*0.0001)), // 0.1ms
		releaseCoeff: 1 - math.Exp(-1/(sr*0.050)),  // 50ms
		channels:     channels,
		ceiling:      1.0, // 0 dBTP (no ceiling reduction)
		ceilingDB:    0,
		tpHistory:    make([][12]float64, channels),
	}
	l.truePeakBits.Store(math.Float64bits(math.Inf(-1)))
	return l
}

// Process applies true-peak limiting to the samples in-place and returns the
// current gain reduction in dB (positive = gain being reduced).
//
// The peak detector runs an ITU-R BS.1770-4 Annex 2 4× polyphase FIR per
// channel to estimate the reconstructed (analog) true-peak.  The envelope
// follower tracks that true-peak (linked across channels) and applies a
// single gain to all channels together.  The audio path itself is unchanged
// — the oversampled reconstruction is used only for detection.
//
// Lock-free: envelope / TP / FIR state is single-writer (mixer goroutine).
// GR and last-block TP are stored atomically for concurrent metering reads.
func (l *Limiter) Process(samples []float32) float64 {
	if l.pendingReset.CompareAndSwap(true, false) {
		l.envelope = 0
		l.gainReductionBits.Store(0)
		l.truePeakBits.Store(math.Float64bits(math.Inf(-1)))
		for ch := range l.tpHistory {
			for i := range l.tpHistory[ch] {
				l.tpHistory[ch][i] = 0
			}
		}
	}

	// Brickwall target is whichever of threshold/ceiling is lower; gain is applied once against it.
	target := l.threshold
	if l.ceiling < target {
		target = l.ceiling
	}
	env := l.envelope
	targetF32 := float32(target)
	ch := l.channels

	var blockMaxTP float64

	for i := 0; i < len(samples); i += ch {
		// True-peak detection: push current sample into each channel's
		// history, then evaluate all 4 FIR phases.  Linked-stereo envelope
		// tracks max across channels and phases.
		var framePeak float64
		for j := 0; j < ch && i+j < len(samples); j++ {
			hist := &l.tpHistory[j]
			for k := 11; k > 0; k-- {
				hist[k] = hist[k-1]
			}
			hist[0] = float64(samples[i+j])
			for phase := 0; phase < 4; phase++ {
				var acc float64
				for k := 0; k < 12; k++ {
					acc += tpCoeffs[phase][k] * hist[k]
				}
				if abs := math.Abs(acc); abs > framePeak {
					framePeak = abs
				}
			}
		}
		if framePeak > blockMaxTP {
			blockMaxTP = framePeak
		}

		if framePeak > env {
			env += l.attackCoeff * (framePeak - env)
		} else {
			env += l.releaseCoeff * (framePeak - env)
		}
		if env < 1e-15 {
			env = 0
		}

		var gain float32 = 1.0
		if env > target {
			gain = float32(target / env)
		}

		for j := 0; j < ch && i+j < len(samples); j++ {
			samples[i+j] *= gain
			// Sample-domain hard clamp. NOTE: this bounds the SAMPLE value to
			// ±target, not the reconstructed inter-sample (true) peak, which can
			// sit up to ~+3 dB above the sample peak. It is a backstop against
			// gross overshoot, not a true-peak ceiling guarantee on transients.
			if samples[i+j] > targetF32 {
				samples[i+j] = targetF32
			} else if samples[i+j] < -targetF32 {
				samples[i+j] = -targetF32
			}
		}
	}

	l.envelope = env

	var gr float64
	if env > target {
		gr = 20 * math.Log10(env/target)
	}
	l.gainReductionBits.Store(math.Float64bits(gr))

	tpDB := math.Inf(-1)
	if blockMaxTP > 0 {
		tpDB = 20 * math.Log10(blockMaxTP)
	}
	l.truePeakBits.Store(math.Float64bits(tpDB))

	return gr
}

// SetThreshold changes the limiter threshold.
// thresholdDB: -12 to 0 dBTP (interpreted against the reconstructed true-peak,
// not sample-peak, since commit that introduced BS.1770-4 true-peak detection).
func (l *Limiter) SetThreshold(thresholdDB float64) {
	l.threshold = math.Pow(10, thresholdDB/20.0)
}

// ThresholdDB returns the current threshold in dBTP.
func (l *Limiter) ThresholdDB() float64 {
	return 20 * math.Log10(l.threshold)
}

// SetCeiling changes the output ceiling.
// ceilingDB: -3 to 0 dBTP (true-peak).  EBU R128 s1 specifies -1 dBTP for
// streaming delivery; ATSC A/85 §6.5 specifies -2 dBTP for broadcast.
func (l *Limiter) SetCeiling(ceilingDB float64) {
	l.ceilingDB = ceilingDB
	l.ceiling = math.Pow(10, ceilingDB/20.0)
}

// CeilingDB returns the current ceiling in dBTP.
func (l *Limiter) CeilingDB() float64 {
	return l.ceilingDB
}

// Reset clears the envelope, gain reduction, true-peak, and FIR state.
// Called when the program bus transitions to mute (FTB) so that stale
// envelope state does not briefly suppress audio on unmute.
func (l *Limiter) Reset() {
	l.gainReductionBits.Store(0)
	l.truePeakBits.Store(math.Float64bits(math.Inf(-1)))
	l.pendingReset.Store(true)
}

// GainReduction returns the current gain reduction in dB.
// 0 means no limiting is active. Positive values indicate how many dB
// the signal is being reduced.
// Lock-free: reads the atomic float64.
func (l *Limiter) GainReduction() float64 {
	return math.Float64frombits(l.gainReductionBits.Load())
}

// TruePeakDBTP returns the maximum ITU-R BS.1770-4 Annex 2 true-peak
// measured over the most recent Process() call, in dBTP.  Returns -Inf
// when no block has been processed since the last Reset.  Lock-free.
func (l *Limiter) TruePeakDBTP() float64 {
	return math.Float64frombits(l.truePeakBits.Load())
}
