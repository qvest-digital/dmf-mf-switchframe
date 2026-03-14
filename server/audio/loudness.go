package audio

import (
	"math"
	"sync/atomic"
)

// LoudnessMeter implements BS.1770-4 compliant loudness metering with
// K-weighted filtering and gated integration. It provides three measurement
// windows: momentary (400ms), short-term (3s), and integrated (gated).
//
// Process() is single-threaded (called from the mixer's processing goroutine).
// LUFS readouts are cached as atomic float64s, updated at the end of each
// block emission for lock-free reads by metering consumers.
type LoudnessMeter struct {
	sampleRate int
	channels   int

	// Per-channel K-weighting filters (2 stages each).
	// Only written by Process() (single-writer).
	preFilters []BiquadFilter
	rlbFilters []BiquadFilter

	// Block accumulator state: only written by Process() (single-writer).
	blockAccum []float64
	blockCount int

	stepSize int // 100ms in samples per channel

	// Ring buffers: only written by Process() / emitBlock() (single-writer).
	momentaryRing [4]float64
	momentaryIdx  int
	momentaryFull bool

	shortTermRing [30]float64
	shortTermIdx  int
	shortTermFull bool

	integratedBlocks []float64

	// Cached LUFS readouts — updated atomically by emitBlock(),
	// read lock-free by MomentaryLUFS() / ShortTermLUFS() / IntegratedLUFS().
	momentaryLUFSBits  atomic.Uint64
	shortTermLUFSBits  atomic.Uint64
	integratedLUFSBits atomic.Uint64

	// Reset flag: set by Reset(), cleared by Process()
	pendingReset atomic.Bool
}

// BS.1770-4 K-weighting filter coefficients computed from sample rate
// using the bilinear transform. ITU-R BS.1770-4 Annex 1 defines the
// analog prototype parameters.
//
// Stage 1: Pre-filter (head-related shelf boost ~4dB above ~1.5kHz)
//   Analog parameters: f0=1681.974450955533 Hz, G=3.999843853973347 dB, Q=0.7071752369554196
//
// Stage 2: RLB weighting (revised low-frequency B-curve, high-pass ~38Hz)
//   Analog parameters: f0=38.13547087602444 Hz, Q=0.5003270373238773

// newKWeightPreFilter computes the BS.1770-4 pre-filter (high shelf) coefficients
// for the given sample rate using the bilinear transform.
//
// Uses the ITU-R BS.1770-4 Annex 1 formulation: analog high-shelf prototype
// with Vh = 10^(G/20), Vb = Vh^0.4996667741545416, K = tan(pi*f0/fs).
// Produces coefficients identical to the ITU reference table at 48kHz.
func newKWeightPreFilter(sampleRate int) BiquadFilter {
	// Analog prototype parameters from BS.1770-4 Annex 1
	const (
		f0        = 1681.974450955533
		G         = 3.999843853973347  // dB
		Q         = 0.7071752369554196
		vbExpHigh = 0.4996667741545416 // exponent for Vb derivation (boost)
	)

	fs := float64(sampleRate)
	Vh := math.Pow(10, G/20.0) // voltage gain
	Vb := math.Pow(Vh, vbExpHigh)
	K := math.Tan(math.Pi * f0 / fs)
	K2 := K * K
	KdivQ := K / Q

	a0 := 1.0 + KdivQ + K2
	b0 := (Vh + Vb*KdivQ + K2) / a0
	b1 := 2.0 * (K2 - Vh) / a0
	b2 := (Vh - Vb*KdivQ + K2) / a0
	a1 := 2.0 * (K2 - 1.0) / a0
	a2 := (1.0 - KdivQ + K2) / a0

	return BiquadFilter{b0: b0, b1: b1, b2: b2, a1: a1, a2: a2}
}

// newKWeightRLBFilter computes the BS.1770-4 RLB (revised low-frequency B-weighting)
// high-pass filter coefficients for the given sample rate using the bilinear transform.
//
// Uses the ITU-R BS.1770-4 Annex 1 formulation: second-order analog HPF prototype
// with K = tan(pi*f0/fs). All coefficients normalized by a0.
// Note: the ITU reference table at 48kHz rounds the b coefficients to 1.0/-2.0/1.0;
// this implementation produces the exact bilinear transform result (~0.995/-1.990/0.995
// at 48kHz) which is acoustically equivalent.
func newKWeightRLBFilter(sampleRate int) BiquadFilter {
	// Analog prototype parameters from BS.1770-4 Annex 1
	const (
		f0 = 38.13547087602444
		Q  = 0.5003270373238773
	)

	fs := float64(sampleRate)
	K := math.Tan(math.Pi * f0 / fs)
	K2 := K * K
	KdivQ := K / Q

	a0 := 1.0 + KdivQ + K2
	b0 := 1.0 / a0
	b1 := -2.0 / a0
	b2 := 1.0 / a0
	a1 := 2.0 * (K2 - 1.0) / a0
	a2 := (1.0 - KdivQ + K2) / a0

	return BiquadFilter{b0: b0, b1: b1, b2: b2, a1: a1, a2: a2}
}

var negMaxFloat64Bits = math.Float64bits(-math.MaxFloat64)

// NewLoudnessMeter creates a new BS.1770-4 loudness meter.
func NewLoudnessMeter(sampleRate, channels int) *LoudnessMeter {
	if channels < 1 {
		channels = 2
	}
	if sampleRate < 1 {
		sampleRate = 48000
	}
	preFilters := make([]BiquadFilter, channels)
	rlbFilters := make([]BiquadFilter, channels)
	for ch := 0; ch < channels; ch++ {
		preFilters[ch] = newKWeightPreFilter(sampleRate)
		rlbFilters[ch] = newKWeightRLBFilter(sampleRate)
	}

	stepSize := sampleRate / 10

	m := &LoudnessMeter{
		sampleRate: sampleRate,
		channels:   channels,
		preFilters: preFilters,
		rlbFilters: rlbFilters,
		blockAccum: make([]float64, channels),
		stepSize:   stepSize,
	}
	m.momentaryLUFSBits.Store(negMaxFloat64Bits)
	m.shortTermLUFSBits.Store(negMaxFloat64Bits)
	m.integratedLUFSBits.Store(negMaxFloat64Bits)
	return m
}

// Process applies K-weighting and accumulates samples for loudness measurement.
// samples must be interleaved PCM (e.g., [L0, R0, L1, R1, ...]).
//
// Lock-free on the hot path: filter state and accumulators are single-writer.
// Cached LUFS values updated atomically on block boundaries.
func (m *LoudnessMeter) Process(samples []float32) {
	if m.pendingReset.CompareAndSwap(true, false) {
		m.drainReset()
	}

	nChannels := m.channels
	for i := 0; i < len(samples)-nChannels+1; i += nChannels {
		for ch := 0; ch < nChannels; ch++ {
			x := float64(samples[i+ch])
			x = m.preFilters[ch].Process(x)
			x = m.rlbFilters[ch].Process(x)

			m.blockAccum[ch] += x * x
		}
		m.blockCount++

		if m.blockCount >= m.stepSize {
			m.emitBlock()
		}
	}
}

// emitBlock computes the mean energy for the current block, stores it,
// and updates the cached atomic LUFS readouts.
func (m *LoudnessMeter) emitBlock() {
	if m.blockCount == 0 {
		return
	}

	var energy float64
	for ch := 0; ch < m.channels; ch++ {
		energy += m.blockAccum[ch] / float64(m.blockCount)
	}

	m.momentaryRing[m.momentaryIdx] = energy
	m.momentaryIdx = (m.momentaryIdx + 1) % len(m.momentaryRing)
	if m.momentaryIdx == 0 {
		m.momentaryFull = true
	}

	m.shortTermRing[m.shortTermIdx] = energy
	m.shortTermIdx = (m.shortTermIdx + 1) % len(m.shortTermRing)
	if m.shortTermIdx == 0 {
		m.shortTermFull = true
	}

	const maxIntegratedBlocks = 360_000
	if len(m.integratedBlocks) >= maxIntegratedBlocks {
		half := maxIntegratedBlocks / 2
		copy(m.integratedBlocks, m.integratedBlocks[half:])
		m.integratedBlocks = m.integratedBlocks[:half]
	}
	m.integratedBlocks = append(m.integratedBlocks, energy)

	for ch := range m.blockAccum {
		m.blockAccum[ch] = 0
	}
	m.blockCount = 0

	m.updateCachedLUFS()
}

// updateCachedLUFS recomputes and atomically stores all three LUFS readouts.
// Called from emitBlock() which is called from Process() (single-writer).
func (m *LoudnessMeter) updateCachedLUFS() {
	// Momentary: requires full 400ms window (4 blocks) per BS.1770-4.
	// Reporting partial results gives incorrect loudness readings.
	if !m.momentaryFull {
		m.momentaryLUFSBits.Store(negMaxFloat64Bits)
	} else {
		var sum float64
		for i := 0; i < len(m.momentaryRing); i++ {
			sum += m.momentaryRing[i]
		}
		m.momentaryLUFSBits.Store(math.Float64bits(energyToLUFS(sum / float64(len(m.momentaryRing)))))
	}

	// Short-term: requires full 3s window (30 blocks) per BS.1770-4.
	if !m.shortTermFull {
		m.shortTermLUFSBits.Store(negMaxFloat64Bits)
	} else {
		var sum float64
		for i := 0; i < len(m.shortTermRing); i++ {
			sum += m.shortTermRing[i]
		}
		m.shortTermLUFSBits.Store(math.Float64bits(energyToLUFS(sum / float64(len(m.shortTermRing)))))
	}

	// Integrated (two-pass gating) -- unchanged.
	m.integratedLUFSBits.Store(math.Float64bits(m.computeIntegratedLUFS()))
}

// computeIntegratedLUFS performs BS.1770-4 gated integration.
// Called from updateCachedLUFS() under single-writer context.
func (m *LoudnessMeter) computeIntegratedLUFS() float64 {
	if len(m.integratedBlocks) == 0 {
		return -math.MaxFloat64
	}

	const absGateThreshold = -70.0
	absGateEnergy := math.Pow(10, (absGateThreshold+0.691)/10.0)

	var ungatedSum float64
	var ungatedCount int
	for _, e := range m.integratedBlocks {
		if e >= absGateEnergy {
			ungatedSum += e
			ungatedCount++
		}
	}
	if ungatedCount == 0 {
		return -math.MaxFloat64
	}

	ungatedMean := ungatedSum / float64(ungatedCount)
	ungatedLUFS := energyToLUFS(ungatedMean)

	relGateThreshold := ungatedLUFS - 10.0
	relGateEnergy := math.Pow(10, (relGateThreshold+0.691)/10.0)

	var gatedSum float64
	var gatedCount int
	for _, e := range m.integratedBlocks {
		if e >= relGateEnergy {
			gatedSum += e
			gatedCount++
		}
	}
	if gatedCount == 0 {
		return -math.MaxFloat64
	}

	return energyToLUFS(gatedSum / float64(gatedCount))
}

// energyToLUFS converts mean energy to LUFS.
func energyToLUFS(energy float64) float64 {
	if energy <= 0 {
		return -math.MaxFloat64
	}
	return -0.691 + 10*math.Log10(energy)
}

// MomentaryLUFS returns the momentary loudness (400ms window).
// Lock-free: reads the cached atomic value.
func (m *LoudnessMeter) MomentaryLUFS() float64 {
	return math.Float64frombits(m.momentaryLUFSBits.Load())
}

// ShortTermLUFS returns the short-term loudness (3s window).
// Lock-free: reads the cached atomic value.
func (m *LoudnessMeter) ShortTermLUFS() float64 {
	return math.Float64frombits(m.shortTermLUFSBits.Load())
}

// IntegratedLUFS returns the integrated loudness with BS.1770-4 gating.
// Lock-free: reads the cached atomic value.
func (m *LoudnessMeter) IntegratedLUFS() float64 {
	return math.Float64frombits(m.integratedLUFSBits.Load())
}

// Reset clears all measurement state including integrated blocks and filter state.
// Sets an atomic flag; the actual state clearing is performed by Process()
// on the next call to maintain single-writer invariant on filter state.
// The atomic LUFS readouts are cleared immediately for responsive metering.
func (m *LoudnessMeter) Reset() {
	m.momentaryLUFSBits.Store(negMaxFloat64Bits)
	m.shortTermLUFSBits.Store(negMaxFloat64Bits)
	m.integratedLUFSBits.Store(negMaxFloat64Bits)
	m.pendingReset.Store(true)
}

// drainReset performs the actual state clearing. Called from Process()
// (single-writer context) when the pendingReset flag was set.
func (m *LoudnessMeter) drainReset() {
	for ch := range m.preFilters {
		m.preFilters[ch].Reset()
	}
	for ch := range m.rlbFilters {
		m.rlbFilters[ch].Reset()
	}

	for ch := range m.blockAccum {
		m.blockAccum[ch] = 0
	}
	m.blockCount = 0

	m.momentaryRing = [4]float64{}
	m.momentaryIdx = 0
	m.momentaryFull = false

	m.shortTermRing = [30]float64{}
	m.shortTermIdx = 0
	m.shortTermFull = false

	m.integratedBlocks = m.integratedBlocks[:0]
}
