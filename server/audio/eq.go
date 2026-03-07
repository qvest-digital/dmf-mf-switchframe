package audio

import (
	"errors"
	"math"
	"sync"
)

// EQ band frequency ranges
var eqBandRanges = [3][2]float64{
	{80, 1000},   // Band 0 (Low)
	{200, 8000},  // Band 1 (Mid)
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

// Process applies the biquad filter to a single sample.
func (f *BiquadFilter) Process(x float64) float64 {
	y := f.b0*x + f.s1
	f.s1 = f.b1*x - f.a1*y + f.s2
	f.s2 = f.b2*x - f.a2*y
	return y
}

// Reset clears the filter state.
func (f *BiquadFilter) Reset() {
	f.s1 = 0
	f.s2 = 0
}

// eqBand holds parameters and per-channel filters for a single EQ band.
type eqBand struct {
	frequency float64
	gain      float64
	q         float64
	enabled   bool
	filters   []BiquadFilter // one per channel to avoid stereo crosstalk
}

// EQ is a 3-band parametric equalizer using biquad filters.
// Each band uses an RBJ peakingEQ formula with configurable
// frequency, gain, and Q. Per-channel filter state prevents
// stereo crosstalk in interleaved audio.
type EQ struct {
	mu         sync.Mutex
	bands      [3]eqBand
	sampleRate float64
	channels   int
}

// NewEQ creates a new 3-band parametric EQ with flat (0dB) defaults.
// channels specifies the number of interleaved audio channels (typically 2 for stereo).
func NewEQ(sampleRate, channels int) *EQ {
	if channels < 1 {
		channels = 2
	}
	eq := &EQ{
		sampleRate: float64(sampleRate),
		channels:   channels,
	}
	// Initialize bands with default frequencies, 0dB gain, Q=1.0, disabled
	for i := 0; i < 3; i++ {
		eq.bands[i] = eqBand{
			frequency: eqBandDefaults[i],
			gain:      0,
			q:         1.0,
			enabled:   false,
			filters:   make([]BiquadFilter, channels),
		}
		eq.calcCoefficients(i)
	}
	return eq
}

// calcCoefficients computes the biquad coefficients for band i using
// the RBJ Audio EQ Cookbook peakingEQ formula.
// Caller must hold eq.mu.
func (eq *EQ) calcCoefficients(i int) {
	band := &eq.bands[i]
	gain := band.gain
	freq := band.frequency
	q := band.q

	// RBJ peakingEQ coefficients
	A := math.Pow(10, gain/40.0)
	w0 := 2 * math.Pi * freq / eq.sampleRate
	sinW0 := math.Sin(w0)
	cosW0 := math.Cos(w0)
	alpha := sinW0 / (2 * q)

	b0 := 1 + alpha*A
	b1 := -2 * cosW0
	b2 := 1 - alpha*A
	a0 := 1 + alpha/A
	a1 := -2 * cosW0
	a2 := 1 - alpha/A

	// Normalize by a0 and set on all per-channel filter instances
	// (same coefficients, independent state per channel)
	for ch := range band.filters {
		band.filters[ch].b0 = b0 / a0
		band.filters[ch].b1 = b1 / a0
		band.filters[ch].b2 = b2 / a0
		band.filters[ch].a1 = a1 / a0
		band.filters[ch].a2 = a2 / a0
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

	eq.mu.Lock()
	defer eq.mu.Unlock()

	eq.bands[band].frequency = frequency
	eq.bands[band].gain = gain
	eq.bands[band].q = q
	eq.bands[band].enabled = enabled
	for ch := range eq.bands[band].filters {
		eq.bands[band].filters[ch].Reset()
	}
	eq.calcCoefficients(band)
	return nil
}

// GetBands returns a snapshot of the current EQ band settings.
func (eq *EQ) GetBands() [3]EQBandSettings {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	var result [3]EQBandSettings
	for i := 0; i < 3; i++ {
		result[i] = EQBandSettings{
			Frequency: eq.bands[i].frequency,
			Gain:      eq.bands[i].gain,
			Q:         eq.bands[i].q,
			Enabled:   eq.bands[i].enabled,
		}
	}
	return result
}

// EQBandSettings holds the parameters for a single EQ band.
type EQBandSettings struct {
	Frequency float64
	Gain      float64
	Q         float64
	Enabled   bool
}

// IsBypassed returns true when all bands are either at 0dB gain or disabled.
func (eq *EQ) IsBypassed() bool {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	for i := 0; i < 3; i++ {
		if eq.bands[i].enabled && eq.bands[i].gain != 0 {
			return false
		}
	}
	return true
}

// Process applies the enabled EQ bands in series to the input samples.
// samples must be interleaved with the given number of channels.
// Returns the processed samples (modifies and returns the input slice).
func (eq *EQ) Process(samples []float32, channels int) []float32 {
	eq.mu.Lock()
	defer eq.mu.Unlock()

	for i := 0; i < 3; i++ {
		if !eq.bands[i].enabled || eq.bands[i].gain == 0 {
			continue
		}
		for j := 0; j < len(samples); j += channels {
			for ch := 0; ch < channels && ch < len(eq.bands[i].filters); ch++ {
				samples[j+ch] = float32(eq.bands[i].filters[ch].Process(float64(samples[j+ch])))
			}
		}
	}
	return samples
}
