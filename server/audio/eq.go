package audio

import (
	"errors"
	"math"
	"sync/atomic"
)

// EQ band frequency ranges
var eqBandRanges = [3][2]float64{
	{80, 1000},    // Band 0 (Low)
	{200, 8000},   // Band 1 (Mid)
	{1000, 16000}, // Band 2 (High)
}

// EQ band default center frequencies
var eqBandDefaults = [3]float64{250, 1000, 4000}

// EQ parameter limits
const (
	eqMinGain = -12.0
	eqMaxGain = 12.0
	eqMinQ    = 0.5
	eqMaxQ    = 4.0
)

// EQ validation errors
var (
	ErrInvalidBand      = errors.New("audio: band index must be 0, 1, or 2")
	ErrInvalidFrequency = errors.New("audio: frequency out of range for band")
	ErrInvalidGain      = errors.New("audio: gain must be between -12 and +12 dB")
	ErrInvalidQ         = errors.New("audio: q must be between 0.5 and 4.0")
)

// BiquadFilter implements a Direct Form II Transposed biquad filter
// with coefficients from the RBJ Audio EQ Cookbook peakingEQ formula.
type BiquadFilter struct {
	// Normalized coefficients (divided by a0)
	b0, b1, b2 float64
	a1, a2     float64

	// Filter state (Direct Form II Transposed)
	s1, s2 float64
}

const denormalGuard = 1e-25

// Process applies the biquad filter to a single sample.
// A tiny DC offset is injected and removed to prevent filter state from
// decaying into denormal territory during silence (10-100x CPU spike on x86).
func (f *BiquadFilter) Process(x float64) float64 {
	x += denormalGuard
	y := f.b0*x + f.s1
	f.s1 = f.b1*x - f.a1*y + f.s2
	f.s2 = f.b2*x - f.a2*y
	return y - denormalGuard
}

// Reset clears the filter state.
func (f *BiquadFilter) Reset() {
	f.s1 = 0
	f.s2 = 0
}

// eqBandCoeffs holds the immutable biquad coefficients for one band.
type eqBandCoeffs struct {
	b0, b1, b2 float64
	a1, a2     float64
}

// eqBandParams holds the immutable parameters for one band.
type eqBandParams struct {
	frequency float64
	gain      float64
	q         float64
	enabled   bool
	coeffs    eqBandCoeffs
}

// eqParams is an immutable snapshot of all EQ band parameters and coefficients.
// Swapped atomically so Process() and IsBypassed() are lock-free.
type eqParams struct {
	bands    [3]eqBandParams
	channels int
}

// EQBandSettings holds the parameters for a single EQ band.
type EQBandSettings struct {
	Frequency float64
	Gain      float64
	Q         float64
	Enabled   bool
}

// EQ is a 3-band parametric equalizer using biquad filters.
// Each band uses an RBJ peakingEQ formula with configurable
// frequency, gain, and Q. Per-channel filter state prevents
// stereo crosstalk in interleaved audio.
//
// Parameters and coefficients are stored in an immutable eqParams snapshot
// swapped atomically. Filter state (s1, s2) is only accessed by Process()
// which is single-threaded (called from the mixer's processing goroutine).
// On coefficient change, filter state is NOT reset — the biquad naturally
// converges to the new response within ~10 samples, avoiding the step
// discontinuity (click) that zeroing s1/s2 would cause.
type EQ struct {
	params atomic.Pointer[eqParams]

	// Filter states: only written by Process() (single-writer, no lock needed).
	// Indexed as [band][channel].
	filterStates [3][]BiquadFilter

	sampleRate float64
}

// NewEQ creates a new 3-band parametric EQ with flat (0dB) defaults.
// channels specifies the number of interleaved audio channels (typically 2 for stereo).
func NewEQ(sampleRate, channels int) *EQ {
	if channels < 1 {
		channels = 2
	}
	eq := &EQ{
		sampleRate: float64(sampleRate),
	}

	p := &eqParams{channels: channels}
	for i := 0; i < 3; i++ {
		p.bands[i] = eqBandParams{
			frequency: eqBandDefaults[i],
			gain:      0,
			q:         1.0,
			enabled:   false,
			coeffs:    calcBandCoefficients(eqBandDefaults[i], 0, 1.0, float64(sampleRate)),
		}
		eq.filterStates[i] = make([]BiquadFilter, channels)
	}
	eq.params.Store(p)
	return eq
}

// calcBandCoefficients computes biquad coefficients using the RBJ Audio EQ
// Cookbook peakingEQ formula. Pure function — no side effects.
func calcBandCoefficients(freq, gain, q, sampleRate float64) eqBandCoeffs {
	A := math.Pow(10, gain/40.0)
	w0 := 2 * math.Pi * freq / sampleRate
	sinW0 := math.Sin(w0)
	cosW0 := math.Cos(w0)
	alpha := sinW0 / (2 * q)

	b0 := 1 + alpha*A
	b1 := -2 * cosW0
	b2 := 1 - alpha*A
	a0 := 1 + alpha/A
	a1 := -2 * cosW0
	a2 := 1 - alpha/A

	return eqBandCoeffs{
		b0: b0 / a0, b1: b1 / a0, b2: b2 / a0,
		a1: a1 / a0, a2: a2 / a0,
	}
}

// SetBand sets the parameters for a single EQ band and recalculates coefficients.
// band: 0 (Low), 1 (Mid), 2 (High)
// frequency: center frequency in Hz (must be within band range)
// gain: dB gain (-12 to +12)
// q: filter Q (0.5 to 4.0)
// enabled: whether the band is active
func (eq *EQ) SetBand(band int, frequency, gain, q float64, enabled bool) error {
	if band < 0 || band > 2 {
		return ErrInvalidBand
	}
	if frequency < eqBandRanges[band][0] || frequency > eqBandRanges[band][1] {
		return ErrInvalidFrequency
	}
	if gain < eqMinGain || gain > eqMaxGain {
		return ErrInvalidGain
	}
	if q < eqMinQ || q > eqMaxQ {
		return ErrInvalidQ
	}

	coeffs := calcBandCoefficients(frequency, gain, q, eq.sampleRate)

	for {
		old := eq.params.Load()
		newParams := *old
		newParams.bands[band] = eqBandParams{
			frequency: frequency,
			gain:      gain,
			q:         q,
			enabled:   enabled,
			coeffs:    coeffs,
		}
		if eq.params.CompareAndSwap(old, &newParams) {
			break
		}
	}
	return nil
}

// GetBands returns a snapshot of the current EQ band settings.
func (eq *EQ) GetBands() [3]EQBandSettings {
	p := eq.params.Load()
	var result [3]EQBandSettings
	for i := 0; i < 3; i++ {
		result[i] = EQBandSettings{
			Frequency: p.bands[i].frequency,
			Gain:      p.bands[i].gain,
			Q:         p.bands[i].q,
			Enabled:   p.bands[i].enabled,
		}
	}
	return result
}

// IsBypassed returns true when all bands are either at 0dB gain or disabled.
// Lock-free: reads the atomic params snapshot.
func (eq *EQ) IsBypassed() bool {
	p := eq.params.Load()
	for i := 0; i < 3; i++ {
		if p.bands[i].enabled && p.bands[i].gain != 0 {
			return false
		}
	}
	return true
}

// Process applies the enabled EQ bands in series to the input samples.
// samples must be interleaved with the given number of channels.
// Returns the processed samples (modifies and returns the input slice).
//
// Lock-free: reads coefficients from the atomic params snapshot.
// Filter states are only written here (single-writer from mixer goroutine).
func (eq *EQ) Process(samples []float32, channels int) []float32 {
	p := eq.params.Load()

	for i := 0; i < 3; i++ {
		band := &p.bands[i]
		if !band.enabled || band.gain == 0 {
			continue
		}
		coeffs := &band.coeffs
		filters := eq.filterStates[i]
		for j := 0; j < len(samples); j += channels {
			for ch := 0; ch < channels && ch < len(filters); ch++ {
				f := &filters[ch]
				x := float64(samples[j+ch]) + denormalGuard
				y := coeffs.b0*x + f.s1
				f.s1 = coeffs.b1*x - coeffs.a1*y + f.s2
				f.s2 = coeffs.b2*x - coeffs.a2*y
				samples[j+ch] = float32(y - denormalGuard)
			}
		}
	}
	return samples
}
