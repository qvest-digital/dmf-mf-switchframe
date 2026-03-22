package audio

import (
	"fmt"
	"math"
	"sync/atomic"
)

const (
	hpfMinFreq = 20.0
	hpfMaxFreq = 500.0
	hpfQ       = 0.7071067811865476 // 1/sqrt(2), Butterworth
)

// hpfParams is an immutable snapshot of HPF configuration.
// Swapped atomically so Process() and IsBypassed() are lock-free.
type hpfParams struct {
	frequency          float64
	enabled            bool
	b0, b1, b2, a1, a2 float64
}

// HPF is a 2nd-order Butterworth high-pass filter (12 dB/octave).
// Variable frequency 20-500 Hz. Uses RBJ Audio EQ Cookbook high-pass
// coefficients computed for the BiquadFilter struct.
//
// Parameters are stored in an immutable hpfParams snapshot swapped
// atomically. Filter state (s1, s2) is only accessed by Process()
// which is single-threaded (called from the mixer's processing goroutine).
type HPF struct {
	params     atomic.Pointer[hpfParams]
	filterL    BiquadFilter
	filterR    BiquadFilter
	sampleRate float64
}

// NewHPF creates a new high-pass filter, starting bypassed at 80 Hz.
func NewHPF(sampleRate int) *HPF {
	h := &HPF{sampleRate: float64(sampleRate)}
	p := &hpfParams{frequency: 80, enabled: false}
	h.params.Store(p)
	return h
}

// IsBypassed returns true when the HPF is disabled or has no params.
// Lock-free: reads the atomic params snapshot.
func (h *HPF) IsBypassed() bool {
	p := h.params.Load()
	return p == nil || !p.enabled
}

// SetParams sets the HPF frequency and enabled state.
// Computes RBJ cookbook high-pass biquad coefficients.
func (h *HPF) SetParams(frequency float64, enabled bool) error {
	if frequency < hpfMinFreq || frequency > hpfMaxFreq {
		return fmt.Errorf("audio: HPF frequency must be between %.0f and %.0f Hz", hpfMinFreq, hpfMaxFreq)
	}
	p := &hpfParams{
		frequency: frequency,
		enabled:   enabled,
	}
	// RBJ cookbook high-pass coefficients
	w0 := 2.0 * math.Pi * frequency / h.sampleRate
	cosW0 := math.Cos(w0)
	alpha := math.Sin(w0) / (2.0 * hpfQ)
	a0 := 1.0 + alpha
	p.b0 = ((1.0 + cosW0) / 2.0) / a0
	p.b1 = -(1.0 + cosW0) / a0
	p.b2 = ((1.0 + cosW0) / 2.0) / a0
	p.a1 = (-2.0 * cosW0) / a0
	p.a2 = (1.0 - alpha) / a0
	h.params.Store(p)
	return nil
}

// Frequency returns the current HPF cutoff frequency.
func (h *HPF) Frequency() float64 {
	p := h.params.Load()
	if p == nil {
		return 80
	}
	return p.frequency
}

// Enabled returns whether the HPF is currently active.
func (h *HPF) Enabled() bool {
	p := h.params.Load()
	return p != nil && p.enabled
}

// Process applies the HPF to interleaved PCM in-place.
// Uses per-channel biquad filters to prevent stereo crosstalk.
func (h *HPF) Process(pcm []float32, channels int) {
	p := h.params.Load()
	if p == nil || !p.enabled {
		return
	}
	// Apply coefficients to the per-channel biquad filters
	h.filterL.b0, h.filterL.b1, h.filterL.b2 = p.b0, p.b1, p.b2
	h.filterL.a1, h.filterL.a2 = p.a1, p.a2
	h.filterR.b0, h.filterR.b1, h.filterR.b2 = p.b0, p.b1, p.b2
	h.filterR.a1, h.filterR.a2 = p.a1, p.a2

	if channels >= 2 {
		for i := 0; i < len(pcm)-1; i += 2 {
			pcm[i] = float32(h.filterL.Process(float64(pcm[i])))
			pcm[i+1] = float32(h.filterR.Process(float64(pcm[i+1])))
		}
	} else {
		for i := range pcm {
			pcm[i] = float32(h.filterL.Process(float64(pcm[i])))
		}
	}
}

// Reset clears filter state (call on mute/unmute to prevent transients).
func (h *HPF) Reset() {
	h.filterL.Reset()
	h.filterR.Reset()
}
