//go:build correctness

package audio

import (
	"math"
	"testing"
)

// TestResamplerImpulseResponse sends a unit impulse through the resampler and
// verifies the output shape matches the expected sinc-like response.
// The output should resemble a windowed sinc function centered at the impulse.
func TestResamplerImpulseResponse(t *testing.T) {
	// 44100 -> 48000 mono
	r := NewResampler(44100, 48000, 1)

	// Feed a long buffer of silence, then impulse, then silence.
	// The impulse must be far enough from the start to allow the filter
	// history to be filled.
	preLen := r.TapsPerPhase() * 4
	postLen := r.TapsPerPhase() * 4
	impulsePos := preLen

	input := make([]float32, preLen+1+postLen)
	input[impulsePos] = 1.0

	output := r.Resample(input)
	if len(output) == 0 {
		t.Fatal("resampler produced no output")
	}

	// Find the peak of the impulse response
	peakIdx := 0
	peakVal := float32(0)
	for i, v := range output {
		if v > peakVal {
			peakVal = v
			peakIdx = i
		}
	}

	if peakVal < 0.5 {
		t.Fatalf("impulse response peak too low: %f at index %d", peakVal, peakIdx)
	}

	// The impulse response energy should be nonzero and have a clear peak.
	// For a polyphase resampler, the exact energy depends on the filter design;
	// we just verify it is well-formed (peaked, decaying) rather than testing
	// an exact energy value.
	var energy float64
	for _, v := range output {
		energy += float64(v) * float64(v)
	}

	if energy < 0.1 {
		t.Fatalf("impulse response energy too low: %f", energy)
	}
	if energy > 5.0 {
		t.Fatalf("impulse response energy unexpectedly high: %f (possible gain error)", energy)
	}
	t.Logf("impulse response energy: %.6f, peak: %.6f at index %d", energy, peakVal, peakIdx)

	// Verify the response decays: samples far from the peak should be near zero
	farIdx := peakIdx + r.TapsPerPhase()*2
	if farIdx < len(output) {
		if math.Abs(float64(output[farIdx])) > 0.01 {
			t.Fatalf("impulse response not decaying: output[%d] = %f", farIdx, output[farIdx])
		}
	}
}

// TestResamplerSineWave resamples a 1kHz sine from 44100 to 48000 Hz and
// verifies the frequency is preserved and no aliasing artifacts appear.
func TestResamplerSineWave(t *testing.T) {
	srcRate := 44100
	dstRate := 48000
	freq := 1000.0

	r := NewResampler(srcRate, dstRate, 1)

	// Generate several periods of 1kHz sine at 44100 Hz
	numSamples := srcRate // 1 second
	input := make([]float32, numSamples)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / float64(srcRate)))
	}

	output := r.Resample(input)

	// Expected output length: approximately numSamples * dstRate / srcRate
	expectedLen := float64(numSamples) * float64(dstRate) / float64(srcRate)
	if math.Abs(float64(len(output))-expectedLen) > float64(r.TapsPerPhase())*2 {
		t.Fatalf("output length %d far from expected %.0f", len(output), expectedLen)
	}

	// Skip the transient region (filter startup) and analyze steady state.
	// The transient is tapsPerPhase input samples long, which maps to
	// approximately tapsPerPhase * L/M output samples.
	skipSamples := int(float64(r.TapsPerPhase())*float64(dstRate)/float64(srcRate)) * 2
	if skipSamples >= len(output)-dstRate/2 {
		t.Fatalf("not enough output samples to analyze after transient skip")
	}
	analyzeRegion := output[skipSamples : len(output)-skipSamples]

	// Find zero crossings to measure frequency
	crossings := 0
	for i := 1; i < len(analyzeRegion); i++ {
		if (analyzeRegion[i-1] >= 0 && analyzeRegion[i] < 0) ||
			(analyzeRegion[i-1] < 0 && analyzeRegion[i] >= 0) {
			crossings++
		}
	}

	// Each full cycle has 2 zero crossings
	measuredFreq := float64(crossings) / 2.0 / (float64(len(analyzeRegion)) / float64(dstRate))
	freqError := math.Abs(measuredFreq-freq) / freq * 100
	if freqError > 0.5 { // 0.5% tolerance
		t.Fatalf("frequency not preserved: expected %.0f Hz, measured %.1f Hz (%.2f%% error)",
			freq, measuredFreq, freqError)
	}

	// Verify amplitude is preserved (should be close to 1.0 in steady state)
	var maxAmp float32
	for _, v := range analyzeRegion {
		if v > maxAmp {
			maxAmp = v
		}
		if -v > maxAmp {
			maxAmp = -v
		}
	}

	ampErrDB := 20 * math.Log10(float64(maxAmp))
	if math.Abs(ampErrDB) > 0.1 { // ±0.1 dB
		t.Fatalf("amplitude error: %.4f dB (peak=%.6f)", ampErrDB, maxAmp)
	}

	// Verify RMS level matches expected sine RMS (1/sqrt(2) ≈ 0.707)
	var rmsSum float64
	for _, v := range analyzeRegion {
		rmsSum += float64(v) * float64(v)
	}
	measuredRMS := math.Sqrt(rmsSum / float64(len(analyzeRegion)))

	expectedRMS := 1.0 / math.Sqrt(2)
	relErr := math.Abs(measuredRMS-expectedRMS) / expectedRMS
	if relErr > 0.01 { // 1% tolerance
		t.Fatalf("RMS error: measured=%.6f, expected=%.6f, relative error=%.4f%%",
			measuredRMS, expectedRMS, relErr*100)
	}
	t.Logf("RMS: measured=%.6f, expected=%.6f, relative error=%.6f%%", measuredRMS, expectedRMS, relErr*100)
}

// TestResamplerIdentityPassthrough verifies that same-rate resampling returns input unchanged.
func TestResamplerIdentityPassthrough(t *testing.T) {
	r := NewResampler(48000, 48000, 2)

	input := make([]float32, 2048)
	for i := range input {
		input[i] = float32(i) * 0.001
	}

	output := r.Resample(input)
	if len(output) != len(input) {
		t.Fatalf("identity resampler changed length: %d -> %d", len(input), len(output))
	}

	for i, v := range output {
		if v != input[i] {
			t.Fatalf("identity resampler changed sample %d: %f -> %f", i, input[i], v)
		}
	}
}

// TestPolyphaseCoeffSum verifies that the polyphase coefficients for each phase
// sum to approximately 1.0 (unity DC gain per sub-filter).
func TestPolyphaseCoeffSum(t *testing.T) {
	// 44100 -> 48000
	g := gcd(44100, 48000)
	upFactor := 48000 / g
	downFactor := 44100 / g

	tapsPerPhase := 16
	coeffs := computePolyphaseCoeffs(upFactor, downFactor, tapsPerPhase, resamplerDefaultBeta)

	for p := 0; p < upFactor; p++ {
		var sum float64
		for t := 0; t < tapsPerPhase; t++ {
			sum += float64(coeffs[p*tapsPerPhase+t])
		}
		errDB := 20 * math.Log10(math.Abs(sum-1.0)+1e-15)
		// Each sub-filter should have DC gain of approximately 1.0
		// float32 precision limits us to about -40 dB error
		if math.Abs(sum-1.0) > 0.01 {
			t.Errorf("phase %d: coefficient sum = %.6f, error = %.2f dB", p, sum, errDB)
		}
	}
}
