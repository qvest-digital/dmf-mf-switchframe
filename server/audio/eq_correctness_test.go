//go:build correctness

package audio

import (
	"math"
	"testing"
)

// TestBiquadCoefficients verifies that calcBandCoefficients produces the
// correct RBJ Audio EQ Cookbook peakingEQ coefficients for known parameters.
func TestBiquadCoefficients(t *testing.T) {
	tests := []struct {
		name       string
		freq       float64
		gain       float64
		q          float64
		sampleRate float64
	}{
		{"1kHz_+6dB_Q1_48k", 1000, 6.0, 1.0, 48000},
		{"250Hz_-3dB_Q0.7_48k", 250, -3.0, 0.707, 48000},
		{"4kHz_+12dB_Q2_44.1k", 4000, 12.0, 2.0, 44100},
		{"1kHz_0dB_Q1_48k", 1000, 0.0, 1.0, 48000}, // flat: should be passthrough
		{"500Hz_-12dB_Q0.5_96k", 500, -12.0, 0.5, 96000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calcBandCoefficients(tc.freq, tc.gain, tc.q, tc.sampleRate)

			// Recompute expected values from the RBJ cookbook formula
			A := math.Pow(10, tc.gain/40.0)
			w0 := 2 * math.Pi * tc.freq / tc.sampleRate
			sinW0 := math.Sin(w0)
			cosW0 := math.Cos(w0)
			alpha := sinW0 / (2 * tc.q)

			expB0 := (1 + alpha*A) / (1 + alpha/A)
			expB1 := (-2 * cosW0) / (1 + alpha/A)
			expB2 := (1 - alpha*A) / (1 + alpha/A)
			expA1 := (-2 * cosW0) / (1 + alpha/A)
			expA2 := (1 - alpha/A) / (1 + alpha/A)

			const tol = 1e-6
			check := func(name string, got, exp float64) {
				if math.Abs(got-exp) > tol {
					t.Errorf("%s: expected %.10f, got %.10f (diff=%.2e)", name, exp, got, math.Abs(got-exp))
				}
			}

			check("b0", got.b0, expB0)
			check("b1", got.b1, expB1)
			check("b2", got.b2, expB2)
			check("a1", got.a1, expA1)
			check("a2", got.a2, expA2)
		})
	}
}

// TestBiquadFlatGainPassthrough verifies that a 0dB gain biquad passes
// signal through unchanged (coefficients should be b0=1, b1=0, b2=0 / a0=1 form).
func TestBiquadFlatGainPassthrough(t *testing.T) {
	coeffs := calcBandCoefficients(1000, 0.0, 1.0, 48000)

	// At 0dB gain, A=1, so alpha*A = alpha/A = alpha
	// b0 = (1+alpha)/(1+alpha) = 1
	// b1 = -2cos(w0)/(1+alpha)
	// b2 = (1-alpha)/(1+alpha)
	// a1 = -2cos(w0)/(1+alpha) = b1
	// a2 = (1-alpha)/(1+alpha) = b2
	// This means b1=a1 and b2=a2, so the filter is an all-pass with unity gain.

	const tol = 1e-10
	if math.Abs(coeffs.b0-1.0) > tol {
		t.Errorf("b0 should be 1.0 at 0dB gain, got %.10f", coeffs.b0)
	}
	if math.Abs(coeffs.b1-coeffs.a1) > tol {
		t.Errorf("b1 should equal a1 at 0dB gain: b1=%.10f, a1=%.10f", coeffs.b1, coeffs.a1)
	}
	if math.Abs(coeffs.b2-coeffs.a2) > tol {
		t.Errorf("b2 should equal a2 at 0dB gain: b2=%.10f, a2=%.10f", coeffs.b2, coeffs.a2)
	}
}

// TestBiquadStability verifies that the filter poles are inside the unit circle
// for all valid parameter combinations (necessary for BIBO stability).
func TestBiquadStability(t *testing.T) {
	sampleRates := []float64{44100, 48000, 96000}
	freqs := []float64{100, 500, 1000, 4000, 10000}
	gains := []float64{-12, -6, 0, 6, 12}
	qs := []float64{0.5, 1.0, 2.0, 4.0}

	for _, sr := range sampleRates {
		for _, f := range freqs {
			if f >= sr/2 {
				continue // skip frequencies at or above Nyquist
			}
			for _, g := range gains {
				for _, q := range qs {
					coeffs := calcBandCoefficients(f, g, q, sr)

					// For a 2nd-order IIR filter y[n] = b0*x[n] + b1*x[n-1] + b2*x[n-2] - a1*y[n-1] - a2*y[n-2]
					// The characteristic polynomial is: z^2 + a1*z + a2 = 0
					// Stability requires both roots have magnitude < 1.
					// Necessary conditions: |a2| < 1 and |a1| < 1 + a2
					if math.Abs(coeffs.a2) >= 1.0 {
						t.Errorf("unstable: |a2|=%.6f >= 1 at sr=%.0f f=%.0f g=%.0f q=%.1f",
							math.Abs(coeffs.a2), sr, f, g, q)
					}
					if math.Abs(coeffs.a1) >= 1.0+coeffs.a2 {
						t.Errorf("unstable: |a1|=%.6f >= 1+a2=%.6f at sr=%.0f f=%.0f g=%.0f q=%.1f",
							math.Abs(coeffs.a1), 1.0+coeffs.a2, sr, f, g, q)
					}
				}
			}
		}
	}
}

// TestBiquadFrequencyResponse verifies that a +6dB peaking EQ at 1kHz produces
// approximately +6dB gain at the center frequency and 0dB at DC and Nyquist.
func TestBiquadFrequencyResponse(t *testing.T) {
	sr := 48000.0
	centerFreq := 1000.0
	gainDB := 6.0
	q := 1.0

	coeffs := calcBandCoefficients(centerFreq, gainDB, q, sr)

	// Evaluate transfer function H(z) = (b0 + b1*z^-1 + b2*z^-2) / (1 + a1*z^-1 + a2*z^-2)
	// at z = e^(j*omega) where omega = 2*pi*f/fs
	evalMagnitudeDB := func(f float64) float64 {
		omega := 2 * math.Pi * f / sr
		cosO := math.Cos(omega)
		cos2O := math.Cos(2 * omega)
		sinO := math.Sin(omega)
		sin2O := math.Sin(2 * omega)

		numRe := coeffs.b0 + coeffs.b1*cosO + coeffs.b2*cos2O
		numIm := -coeffs.b1*sinO - coeffs.b2*sin2O
		denRe := 1.0 + coeffs.a1*cosO + coeffs.a2*cos2O
		denIm := -coeffs.a1*sinO - coeffs.a2*sin2O

		numMag := math.Sqrt(numRe*numRe + numIm*numIm)
		denMag := math.Sqrt(denRe*denRe + denIm*denIm)
		return 20 * math.Log10(numMag/denMag)
	}

	// At center frequency: should be approximately +6dB
	atCenter := evalMagnitudeDB(centerFreq)
	if math.Abs(atCenter-gainDB) > 0.1 {
		t.Errorf("at center freq %.0f Hz: expected %.1f dB, got %.4f dB", centerFreq, gainDB, atCenter)
	}

	// At DC: should be approximately 0dB
	atDC := evalMagnitudeDB(1.0) // near-DC
	if math.Abs(atDC) > 0.01 {
		t.Errorf("at DC: expected ~0 dB, got %.4f dB", atDC)
	}

	// At Nyquist: should be approximately 0dB
	atNyquist := evalMagnitudeDB(sr/2 - 1)
	if math.Abs(atNyquist) > 0.1 {
		t.Errorf("at Nyquist: expected ~0 dB, got %.4f dB", atNyquist)
	}
}
