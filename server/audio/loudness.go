package audio

import (
	"math"
	"sync"
)

// LoudnessMeter implements BS.1770-4 compliant loudness metering with
// K-weighted filtering and gated integration. It provides three measurement
// windows: momentary (400ms), short-term (3s), and integrated (gated).
type LoudnessMeter struct {
	mu         sync.Mutex
	sampleRate int
	channels   int

	// Per-channel K-weighting filters (2 stages each).
	preFilters []BiquadFilter // stage 1: head-related shelf boost
	rlbFilters []BiquadFilter // stage 2: revised low-frequency B-curve

	// Accumulate squared K-weighted samples for current block.
	blockAccum []float64 // per-channel running sum of squares
	blockCount int       // samples processed in current block (per channel)

	blockSize int // 400ms in samples per channel (e.g., 19200 at 48kHz)
	stepSize  int // 100ms in samples per channel (overlap step)

	// Momentary: latest 4 blocks (400ms window, 75% overlap at 100ms step)
	momentaryRing [4]float64
	momentaryIdx  int
	momentaryFull bool

	// Short-term: latest 30 blocks (3s window at 100ms step)
	shortTermRing [30]float64
	shortTermIdx  int
	shortTermFull bool

	// Integrated: all block energies for gating.
	integratedBlocks []float64
}

// BS.1770-4 K-weighting filter coefficients for 48kHz sample rate.
// Stage 1: Pre-filter (head-related shelf boost ~4dB above 1.5kHz)
// Stage 2: RLB weighting (revised low-frequency B-curve, high-pass ~100Hz)
// Coefficients are pre-normalized (a0 = 1.0).
func newKWeightPreFilter() BiquadFilter {
	return BiquadFilter{
		b0: 1.53512485958697,
		b1: -2.69169618940638,
		b2: 1.19839281085285,
		a1: -1.69065929318241,
		a2: 0.73248077421585,
	}
}

func newKWeightRLBFilter() BiquadFilter {
	return BiquadFilter{
		b0: 1.0,
		b1: -2.0,
		b2: 1.0,
		a1: -1.99004745483398,
		a2: 0.99007225036621,
	}
}

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
		preFilters[ch] = newKWeightPreFilter()
		rlbFilters[ch] = newKWeightRLBFilter()
	}

	stepSize := sampleRate / 10 // 100ms in samples per channel

	return &LoudnessMeter{
		sampleRate: sampleRate,
		channels:   channels,
		preFilters: preFilters,
		rlbFilters: rlbFilters,
		blockAccum: make([]float64, channels),
		blockSize:  sampleRate * 4 / 10, // 400ms
		stepSize:   stepSize,
	}
}

// Process applies K-weighting and accumulates samples for loudness measurement.
// samples must be interleaved PCM (e.g., [L0, R0, L1, R1, ...]).
func (m *LoudnessMeter) Process(samples []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	nChannels := m.channels
	for i := 0; i < len(samples)-nChannels+1; i += nChannels {
		for ch := 0; ch < nChannels; ch++ {
			// Apply K-weighting: stage 1 (pre-filter) then stage 2 (RLB)
			x := float64(samples[i+ch])
			x = m.preFilters[ch].Process(x)
			x = m.rlbFilters[ch].Process(x)

			// Accumulate squared K-weighted sample
			m.blockAccum[ch] += x * x
		}
		m.blockCount++

		// When we've accumulated stepSize samples, compute block energy
		if m.blockCount >= m.stepSize {
			m.emitBlock()
		}
	}
}

// emitBlock computes the mean energy for the current block and stores it.
// Caller must hold m.mu.
func (m *LoudnessMeter) emitBlock() {
	if m.blockCount == 0 {
		return
	}

	// Compute mean squared energy across all channels with equal weighting.
	// BS.1770-4: z_i = (1/N) * sum(w_ch * z_ch) where w_ch = 1.0 for L/R
	// For stereo with equal channel weights:
	var energy float64
	for ch := 0; ch < m.channels; ch++ {
		energy += m.blockAccum[ch] / float64(m.blockCount)
	}
	// Don't divide by channels — BS.1770-4 sums channel energies (with weights)
	// For L/R stereo, both channels have weight 1.0, so we just sum.

	// Store in momentary ring
	m.momentaryRing[m.momentaryIdx] = energy
	m.momentaryIdx = (m.momentaryIdx + 1) % len(m.momentaryRing)
	if m.momentaryIdx == 0 {
		m.momentaryFull = true
	}

	// Store in short-term ring
	m.shortTermRing[m.shortTermIdx] = energy
	m.shortTermIdx = (m.shortTermIdx + 1) % len(m.shortTermRing)
	if m.shortTermIdx == 0 {
		m.shortTermFull = true
	}

	// Store for integrated measurement
	m.integratedBlocks = append(m.integratedBlocks, energy)

	// Reset accumulator for next block
	for ch := range m.blockAccum {
		m.blockAccum[ch] = 0
	}
	m.blockCount = 0
}

// energyToLUFS converts mean energy to LUFS.
func energyToLUFS(energy float64) float64 {
	if energy <= 0 {
		return -math.MaxFloat64
	}
	return -0.691 + 10*math.Log10(energy)
}

// MomentaryLUFS returns the momentary loudness (400ms window).
func (m *LoudnessMeter) MomentaryLUFS() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := m.momentaryIdx
	if m.momentaryFull {
		count = len(m.momentaryRing)
	}
	if count == 0 {
		return -math.MaxFloat64
	}

	var sum float64
	for i := 0; i < count; i++ {
		sum += m.momentaryRing[i]
	}
	return energyToLUFS(sum / float64(count))
}

// ShortTermLUFS returns the short-term loudness (3s window).
func (m *LoudnessMeter) ShortTermLUFS() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := m.shortTermIdx
	if m.shortTermFull {
		count = len(m.shortTermRing)
	}
	if count == 0 {
		return -math.MaxFloat64
	}

	var sum float64
	for i := 0; i < count; i++ {
		sum += m.shortTermRing[i]
	}
	return energyToLUFS(sum / float64(count))
}

// IntegratedLUFS returns the integrated loudness with BS.1770-4 gating.
// Two-pass gating: absolute gate at -70 LUFS, then relative gate at -10 LU
// below the ungated mean.
func (m *LoudnessMeter) IntegratedLUFS() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.integratedBlocks) == 0 {
		return -math.MaxFloat64
	}

	// Absolute gate threshold: -70 LUFS
	const absGateThreshold = -70.0
	absGateEnergy := math.Pow(10, (absGateThreshold+0.691)/10.0)

	// Pass 1: compute ungated mean (excluding blocks below absolute gate)
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

	// Relative gate: -10 LU below the ungated mean
	relGateThreshold := ungatedLUFS - 10.0
	relGateEnergy := math.Pow(10, (relGateThreshold+0.691)/10.0)

	// Pass 2: compute gated mean (excluding blocks below relative gate)
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

// Reset clears all measurement state including integrated blocks and filter state.
func (m *LoudnessMeter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset filters
	for ch := range m.preFilters {
		m.preFilters[ch].Reset()
	}
	for ch := range m.rlbFilters {
		m.rlbFilters[ch].Reset()
	}

	// Reset accumulators
	for ch := range m.blockAccum {
		m.blockAccum[ch] = 0
	}
	m.blockCount = 0

	// Reset momentary ring
	m.momentaryRing = [4]float64{}
	m.momentaryIdx = 0
	m.momentaryFull = false

	// Reset short-term ring
	m.shortTermRing = [30]float64{}
	m.shortTermIdx = 0
	m.shortTermFull = false

	// Reset integrated
	m.integratedBlocks = m.integratedBlocks[:0]
}
