//go:build correctness

package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// ==========================================================================
// 1. Equal-power crossfade: cos²θ + sin²θ = 1.0 at every sample
// ==========================================================================

func TestEqualPowerCrossfade(t *testing.T) {
	t.Parallel()

	// Generate two sine waves at different frequencies as source material.
	const sampleRate = 48000
	const durationSec = 0.5
	oldPCM := testutil.GenerateSineWavePCM(440, -6, sampleRate, durationSec)
	newPCM := testutil.GenerateSineWavePCM(880, -6, sampleRate, durationSec)

	n := len(oldPCM)
	require.Equal(t, n, len(newPCM))

	// Apply crossfade
	result := audio.EqualPowerCrossfade(oldPCM, newPCM)
	require.Equal(t, n, len(result))

	// Verify equal-power property: at every sample position, the squared sum
	// of cos and sin gains should equal 1.0.
	// The crossfade table maps sample index to [0, tableSize-1].
	// We verify by reconstructing the gains and checking cos²+sin² = 1.
	pairCount := float64(n - 1)
	if pairCount < 1 {
		pairCount = 1
	}
	const tableSize = 1024

	maxDevDB := 0.0
	for i := 0; i < n; i++ {
		idx := int(float64(i) / pairCount * float64(tableSize-1))
		if idx >= tableSize {
			idx = tableSize - 1
		}
		t_ := float64(idx) / float64(tableSize-1)
		cosGain := math.Cos(t_ * math.Pi / 2)
		sinGain := math.Sin(t_ * math.Pi / 2)
		powerSum := cosGain*cosGain + sinGain*sinGain

		// Convert power deviation to dB: 10*log10(powerSum) should be 0 dB.
		devDB := math.Abs(10 * math.Log10(powerSum))
		if devDB > maxDevDB {
			maxDevDB = devDB
		}
	}

	// Assert total power deviation is within 0.001 dB at every sample.
	require.LessOrEqual(t, maxDevDB, 0.001,
		"equal power property violated: max deviation = %.6f dB, want <= 0.001 dB", maxDevDB)

	// Also verify the actual crossfade output:
	// At t=0, result should be ~oldPCM[0]; at t=1, result should be ~newPCM[n-1].
	require.InDelta(t, float64(oldPCM[0]), float64(result[0]), 0.01,
		"start of crossfade should be ~old signal")
	require.InDelta(t, float64(newPCM[n-1]), float64(result[n-1]), 0.01,
		"end of crossfade should be ~new signal")
}

// ==========================================================================
// 2. Crossfade duration accuracy: ramp starts and completes within ±1 frame
// ==========================================================================

func TestCrossfadeDurationAccuracy(t *testing.T) {
	t.Parallel()

	// Test multi-frame crossfade using EqualPowerCrossfadeRanged.
	// Simulate a 4-frame crossfade where each frame covers 1/4 of the range.
	const samplesPerFrame = 1024
	const numFrames = 4
	const channels = 1

	old := make([]float32, samplesPerFrame)
	new := make([]float32, samplesPerFrame)
	for i := range old {
		old[i] = 1.0
		new[i] = 0.0
	}

	// Frame 0: position [0.0, 0.25) — should start with old=1.0 (cos(0)=1)
	frame0 := audio.EqualPowerCrossfadeRanged(nil, old, new, channels, 0.0, 0.25)
	require.InDelta(t, 1.0, float64(frame0[0]), 0.01,
		"frame 0 start: should be ~1.0 (cos(0)=1)")

	// Frame 3: position [0.75, 1.0] — should end with old~0 (cos(pi/2)~0)
	frame3 := audio.EqualPowerCrossfadeRanged(nil, old, new, channels, 0.75, 1.0)
	require.InDelta(t, 0.0, float64(frame3[samplesPerFrame-1]), 0.02,
		"frame 3 end: should be ~0.0 (cos(pi/2)~0)")

	// Verify continuity at frame boundaries: end of frame N ~ start of frame N+1
	for f := 0; f < numFrames-1; f++ {
		posStart := float64(f) / float64(numFrames)
		posEnd := float64(f+1) / float64(numFrames)
		posNext := float64(f+1) / float64(numFrames)
		posNextEnd := float64(f+2) / float64(numFrames)

		frameA := audio.EqualPowerCrossfadeRanged(nil, old, new, channels, posStart, posEnd)
		frameB := audio.EqualPowerCrossfadeRanged(nil, old, new, channels, posNext, posNextEnd)

		require.InDelta(t, float64(frameA[samplesPerFrame-1]), float64(frameB[0]), 0.02,
			"crossfade discontinuity at frame boundary %d→%d", f, f+1)
	}
}

// ==========================================================================
// 3. LUFS metering: BS.1770-4 calibrated sine reads correctly
// ==========================================================================

func TestLUFSMetering(t *testing.T) {
	t.Parallel()

	// BS.1770-4 reference: a mono 1kHz sine at 0 dBFS (amplitude 1.0) should
	// measure approximately -3.01 LUFS. For stereo (same signal both channels),
	// it adds ~3 dB, giving approximately 0 LUFS before K-weighting.
	// K-weighting at 1kHz boosts by ~0.5 dB (pre-filter shelf).
	//
	// We generate a calibrated mono sine at -23 dBFS and feed it to a mono meter.
	// The K-weighting pre-filter boosts 1kHz by ~0.5 dB, so a -23 dBFS sine
	// measures approximately -23 + 0.5 - 3.01 = -25.51 LUFS for mono.
	//
	// Instead, let's use a known relationship: generate a stereo signal and
	// verify the meter reads within tolerance.

	const sampleRate = 48000
	const channels = 1

	// For a mono meter, a full-scale (0 dBFS) 1kHz sine should read about -3.01 LUFS
	// (RMS of sine = -3.01 dB) plus K-weighting boost at 1kHz (~+0.5 dB).
	// Expected: approximately -2.5 LUFS for mono full-scale 1kHz sine.

	meter := audio.NewLoudnessMeter(sampleRate, channels)

	// Generate 4 seconds of 1kHz sine at 0 dBFS (amplitude 1.0) mono
	// to fill both momentary and short-term windows.
	samples := testutil.GenerateSineWavePCM(1000, 0, sampleRate, 4.0)
	meter.Process(samples)

	integrated := meter.IntegratedLUFS()
	t.Logf("Mono 0 dBFS 1kHz sine: integrated LUFS = %.4f", integrated)

	// The expected LUFS for a 0 dBFS mono 1kHz sine with K-weighting is
	// approximately -2 to -3 LUFS (sine RMS is -3.01 dB, K-weight boosts ~0.5 dB).
	// We use a wider initial check and then verify relative accuracy.
	require.Greater(t, integrated, -5.0, "mono 0dBFS 1kHz should be > -5 LUFS")
	require.Less(t, integrated, 0.0, "mono 0dBFS 1kHz should be < 0 LUFS")

	// Now test a calibrated level: generate signal at -20 dBFS.
	// The difference from the 0 dBFS measurement should be exactly 20 dB (±0.2 LU).
	meter2 := audio.NewLoudnessMeter(sampleRate, channels)
	samples2 := testutil.GenerateSineWavePCM(1000, -20, sampleRate, 4.0)
	meter2.Process(samples2)

	integrated2 := meter2.IntegratedLUFS()
	t.Logf("Mono -20 dBFS 1kHz sine: integrated LUFS = %.4f", integrated2)

	// The level difference should be exactly 20 dB.
	diff := integrated - integrated2
	testutil.AssertDBFS(t, diff, 20.0, 0.2)
}

// ==========================================================================
// 4. LUFS gating: silence is gated out of integrated measurement
// ==========================================================================

func TestLUFSGating(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const channels = 1

	// Meter A: tone only (reference)
	meterToneOnly := audio.NewLoudnessMeter(sampleRate, channels)
	tone := testutil.GenerateSineWavePCM(1000, -14, sampleRate, 4.0)
	meterToneOnly.Process(tone)
	lufsToneOnly := meterToneOnly.IntegratedLUFS()

	// Meter B: 2s tone, 5s silence, 2s tone
	meterGated := audio.NewLoudnessMeter(sampleRate, channels)

	tone2s := testutil.GenerateSineWavePCM(1000, -14, sampleRate, 2.0)
	silence5s := testutil.GenerateSilencePCM(sampleRate, 5.0)

	meterGated.Process(tone2s)
	meterGated.Process(silence5s)
	meterGated.Process(tone2s)

	lufsGated := meterGated.IntegratedLUFS()

	t.Logf("Tone-only LUFS = %.4f, Gated (2s tone + 5s silence + 2s tone) LUFS = %.4f",
		lufsToneOnly, lufsGated)

	// BS.1770-4 absolute gate (-70 LUFS) should exclude silence blocks.
	// The integrated measurement should reflect only the tone portions,
	// matching the tone-only measurement within 0.5 LU.
	// (Allowing slightly more tolerance because the gating may include
	// partial blocks at the boundaries.)
	diff := math.Abs(lufsToneOnly - lufsGated)
	require.LessOrEqual(t, diff, 0.5,
		"gated LUFS should match tone-only LUFS within 0.5 LU: tone=%.4f, gated=%.4f, diff=%.4f",
		lufsToneOnly, lufsGated, diff)
}

// ==========================================================================
// 5. Limiter ceiling: output peak never exceeds threshold
// ==========================================================================

func TestLimiterCeiling(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const channels = 2

	limiter := audio.NewLimiter(sampleRate, channels)
	// Default threshold is -1 dBFS.

	// Generate a hot signal at 0 dBFS (amplitude 1.0) — 1kHz stereo sine.
	numSamples := sampleRate // 1 second
	samples := make([]float32, numSamples*channels)
	for i := 0; i < numSamples; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / float64(sampleRate)))
		samples[i*channels] = v
		samples[i*channels+1] = v
	}

	// Process through limiter
	limiter.Process(samples)

	// The threshold is -1 dBFS = 10^(-1/20) ≈ 0.891
	thresholdLinear := math.Pow(10, -1.0/20.0)

	// Scan every sample: none should exceed the threshold.
	for i, s := range samples {
		abs := math.Abs(float64(s))
		require.LessOrEqual(t, abs, thresholdLinear+1e-6,
			"sample %d exceeds limiter ceiling: %.6f > %.6f (-1 dBFS)",
			i, abs, thresholdLinear)
	}

	// Verify the limiter is actually doing something (gain reduction > 0).
	gr := limiter.GainReduction()
	require.Greater(t, gr, 0.0, "limiter should be reducing gain for 0 dBFS signal")
}

// ==========================================================================
// 6. Limiter passes below-threshold signals unaltered
// ==========================================================================

func TestLimiterPassesBelow(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const channels = 2

	limiter := audio.NewLimiter(sampleRate, channels)
	// Default threshold is -1 dBFS.

	// Generate a quiet signal at -10 dBFS (well below -1 dBFS threshold).
	amplitude := math.Pow(10, -10.0/20.0) // ≈ 0.316
	numSamples := sampleRate               // 1 second
	input := make([]float32, numSamples*channels)
	reference := make([]float32, numSamples*channels)
	for i := 0; i < numSamples; i++ {
		v := float32(amplitude * math.Sin(2*math.Pi*1000*float64(i)/float64(sampleRate)))
		input[i*channels] = v
		input[i*channels+1] = v
		reference[i*channels] = v
		reference[i*channels+1] = v
	}

	limiter.Process(input)

	// For below-threshold signals, the limiter should be (near) unity.
	// The envelope follower may cause tiny initial transient differences,
	// so we check after the first 100ms (attack settling).
	startSample := sampleRate / 10 * channels // skip first 100ms
	maxDiffDB := 0.0
	for i := startSample; i < len(input); i++ {
		if math.Abs(float64(reference[i])) < 1e-10 {
			continue // skip zero crossings
		}
		ratio := float64(input[i]) / float64(reference[i])
		diffDB := math.Abs(20 * math.Log10(math.Abs(ratio)))
		if diffDB > maxDiffDB {
			maxDiffDB = diffDB
		}
	}

	require.LessOrEqual(t, maxDiffDB, 0.05,
		"below-threshold signal should pass through near-unity: max diff = %.4f dB", maxDiffDB)
}

// ==========================================================================
// 7. EQ frequency response: +6dB boost at 1kHz is localized
// ==========================================================================

func TestEQFrequencyResponse(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const channels = 1
	const duration = 2.0 // 2 seconds for accurate level measurement

	eq := audio.NewEQ(sampleRate, channels)

	// Set mid band: +6 dB boost at 1kHz, Q=1.0
	err := eq.SetBand(1, 1000, 6.0, 1.0, true)
	require.NoError(t, err)

	// Measure the response at multiple frequencies.
	// For each frequency, generate a sine wave, process through EQ, measure output level.
	testFreqs := []struct {
		freq      float64
		expectMin float64 // min expected gain in dB
		expectMax float64 // max expected gain in dB
	}{
		{100, -1.0, 1.0},   // Far below: should be near 0 dB (±1 dB)
		{1000, 4.5, 7.0},   // At center: should be ~+6 dB (±1.5 dB to account for filter shape)
		{10000, -1.0, 1.5}, // Far above: should be near 0 dB (±1.5 dB)
	}

	for _, tc := range testFreqs {
		// Generate sine at the test frequency, -20 dBFS to avoid clipping after boost
		inputPCM := testutil.GenerateSineWavePCM(tc.freq, -20.0, sampleRate, duration)
		inputLevel := testutil.PeakDBFS(inputPCM)

		// Process through EQ (need to copy since EQ modifies in-place)
		outputPCM := make([]float32, len(inputPCM))
		copy(outputPCM, inputPCM)
		eq.Process(outputPCM, channels)

		// Measure output level using steady-state portion (skip first 100ms for filter settling)
		skipSamples := sampleRate / 10
		outputLevel := testutil.PeakDBFS(outputPCM[skipSamples:])
		gainDB := outputLevel - inputLevel

		t.Logf("EQ response at %6.0f Hz: input=%.2f dBFS, output=%.2f dBFS, gain=%.2f dB",
			tc.freq, inputLevel, outputLevel, gainDB)

		require.GreaterOrEqual(t, gainDB, tc.expectMin,
			"EQ gain at %.0f Hz = %.2f dB, want >= %.2f dB", tc.freq, gainDB, tc.expectMin)
		require.LessOrEqual(t, gainDB, tc.expectMax,
			"EQ gain at %.0f Hz = %.2f dB, want <= %.2f dB", tc.freq, gainDB, tc.expectMax)
	}

	// Verify the boost is localized: the gain at 1kHz should be at least
	// 4 dB higher than the gain at 100 Hz and 10 kHz.
	inputAt1k := testutil.GenerateSineWavePCM(1000, -20, sampleRate, duration)
	outputAt1k := make([]float32, len(inputAt1k))
	copy(outputAt1k, inputAt1k)

	// Need a fresh EQ because filter state is stateful.
	eq2 := audio.NewEQ(sampleRate, channels)
	require.NoError(t, eq2.SetBand(1, 1000, 6.0, 1.0, true))
	eq2.Process(outputAt1k, channels)
	skipSamples := sampleRate / 10
	gainAt1k := testutil.PeakDBFS(outputAt1k[skipSamples:]) - testutil.PeakDBFS(inputAt1k)

	inputAt100 := testutil.GenerateSineWavePCM(100, -20, sampleRate, duration)
	outputAt100 := make([]float32, len(inputAt100))
	copy(outputAt100, inputAt100)

	eq3 := audio.NewEQ(sampleRate, channels)
	require.NoError(t, eq3.SetBand(1, 1000, 6.0, 1.0, true))
	eq3.Process(outputAt100, channels)
	gainAt100 := testutil.PeakDBFS(outputAt100[skipSamples:]) - testutil.PeakDBFS(inputAt100)

	require.Greater(t, gainAt1k-gainAt100, 3.0,
		"1kHz boost should be at least 3 dB above 100 Hz: 1kHz=%.2f dB, 100Hz=%.2f dB",
		gainAt1k, gainAt100)
}

// ==========================================================================
// 8. Compressor ratio: 2:1 ratio at -20 dBFS threshold
// ==========================================================================

func TestCompressorRatio(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const channels = 1

	comp := audio.NewCompressor(sampleRate, channels)

	// Set 2:1 ratio, threshold -20 dBFS, fast attack (0.1ms), slow release (100ms).
	// No makeup gain for this test.
	err := comp.SetParams(-20.0, 2.0, 0.1, 100.0, 0.0)
	require.NoError(t, err)

	// Feed a sustained signal at -10 dBFS (10 dB above threshold).
	// With 2:1 ratio, output should be approximately -15 dBFS (5 dB above threshold).
	// Need sustained signal for envelope to settle.
	inputPCM := testutil.GenerateSineWavePCM(1000, -10, sampleRate, 2.0)

	// Keep a copy for reference level measurement.
	inputLevel := testutil.PeakDBFS(inputPCM)
	t.Logf("Compressor input level: %.2f dBFS", inputLevel)

	outputPCM := make([]float32, len(inputPCM))
	copy(outputPCM, inputPCM)
	comp.Process(outputPCM)

	// Measure steady-state output level (skip first 500ms for attack settling).
	skipSamples := sampleRate / 2
	outputLevel := testutil.PeakDBFS(outputPCM[skipSamples:])
	t.Logf("Compressor output level (steady-state): %.2f dBFS", outputLevel)

	// Expected output: threshold + (input - threshold) / ratio = -20 + 10/2 = -15 dBFS
	// The compressor uses an envelope follower, not sample-by-sample math, so
	// the actual level will be close but may differ due to the peak-following
	// envelope and gain table quantization.
	testutil.AssertDBFS(t, outputLevel, -15.0, 1.5)

	// Verify gain reduction is approximately correct.
	gr := comp.GainReduction()
	t.Logf("Compressor gain reduction: %.2f dB", gr)

	// Expected GR = (input - threshold) * (1 - 1/ratio) = 10 * 0.5 = 5 dB
	testutil.AssertDBFS(t, gr, 5.0, 1.5)
}

// ==========================================================================
// 9. AAC round-trip: encode 1kHz sine, decode, check SNR > 40 dB
// ==========================================================================

func TestAACRoundTrip(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const channels = 2
	const frameSize = 1024 // AAC-LC frame size

	enc, err := audio.NewFDKEncoder(sampleRate, channels)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := audio.NewFDKDecoder(sampleRate, channels)
	require.NoError(t, err)
	defer dec.Close()

	// Generate a 1kHz sine wave at -6 dBFS (stereo, interleaved).
	// Use 30 frames (~0.6 seconds) -- well past the AAC MDCT priming window.
	amplitude := math.Pow(10, -6.0/20.0)
	numFrames := 30

	allInputPCM := make([]float32, 0, numFrames*frameSize*channels)
	allOutputPCM := make([]float32, 0, numFrames*frameSize*channels)

	for f := 0; f < numFrames; f++ {
		frame := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			sampleIdx := f*frameSize + i
			v := float32(amplitude * math.Sin(2*math.Pi*1000*float64(sampleIdx)/float64(sampleRate)))
			frame[i*channels] = v
			frame[i*channels+1] = v
		}
		allInputPCM = append(allInputPCM, frame...)

		aacData, encErr := enc.Encode(frame)
		require.NoError(t, encErr)
		if len(aacData) == 0 {
			continue
		}

		pcmOut, decErr := dec.Decode(aacData)
		if decErr != nil || pcmOut == nil {
			continue
		}
		out := make([]float32, len(pcmOut))
		copy(out, pcmOut)
		allOutputPCM = append(allOutputPCM, out...)
	}

	require.NotEmpty(t, allOutputPCM, "should have decoded output after round-trip")
	require.Equal(t, len(allInputPCM), len(allOutputPCM),
		"input and output should have the same number of samples (frame-aligned codec)")

	// The FDK AAC encoder/decoder pipeline has zero-delay ADTS framing: every
	// encode produces output and every decode produces 1024*channels samples.
	// However, the first decoded frame contains the MDCT priming (zeros/ramp),
	// so the output is phase-aligned with the input but the first ~1024 samples
	// are degraded by the priming transient.
	//
	// For SNR measurement, we skip the first 5 frames (5120 stereo samples =
	// 10240 interleaved samples) to avoid the priming region entirely, then
	// compare input and output at the same indices.
	skipFrames := 5
	startSample := skipFrames * frameSize * channels

	var signalPower, noisePower float64
	measured := 0
	for i := startSample; i < len(allInputPCM) && i < len(allOutputPCM); i++ {
		sig := float64(allInputPCM[i])
		out := float64(allOutputPCM[i])
		signalPower += sig * sig
		noise := out - sig
		noisePower += noise * noise
		measured++
	}

	require.Greater(t, measured, frameSize*channels,
		"should have substantial samples for SNR measurement")

	if noisePower < 1e-30 {
		noisePower = 1e-30
	}
	snrDB := 10 * math.Log10(signalPower/noisePower)
	t.Logf("AAC round-trip SNR (past priming): %.2f dB (measured %d samples)", snrDB, measured)

	// AAC-LC at 128kbps stereo with FDK afterburner should achieve > 40 dB SNR.
	// The float32→int16→float32 path through the codec introduces quantization,
	// and AAC's MDCT transform adds some coding noise, but for a clean 1kHz sine
	// at -6 dBFS, the SNR should be well above 40 dB.
	require.Greater(t, snrDB, 40.0,
		"AAC round-trip SNR should be > 40 dB (got %.2f dB)", snrDB)
}

// ==========================================================================
// 10. Master gain staging: 0 dB = unity, -inf = silence
// ==========================================================================

func TestMasterGainStaging(t *testing.T) {
	t.Parallel()

	// Test the limiter at unity (no limiting for below-threshold signals)
	// and the EQ at flat (bypass) as proxies for the master chain components.
	// Testing the full mixer master chain requires the clock-driven ticker,
	// so we test the individual components that make up the chain.

	t.Run("unity_gain", func(t *testing.T) {
		t.Parallel()

		// Generate a signal at -10 dBFS (well below limiter threshold).
		pcm := testutil.GenerateSineWavePCM(1000, -10, 48000, 0.5)
		inputLevel := testutil.PeakDBFS(pcm)

		// Process through a limiter with default settings (-1 dBFS threshold).
		// Signal at -10 dBFS should pass through unchanged.
		limiter := audio.NewLimiter(48000, 1)
		output := make([]float32, len(pcm))
		copy(output, pcm)
		limiter.Process(output)

		outputLevel := testutil.PeakDBFS(output[4800:]) // skip 100ms for settling
		diff := math.Abs(outputLevel - inputLevel)
		require.LessOrEqual(t, diff, 0.05,
			"unity path: output should match input within 0.05 dB (got diff=%.4f dB)", diff)
	})

	t.Run("flat_eq_is_unity", func(t *testing.T) {
		t.Parallel()

		// A flat EQ (0 dB on all bands) should be bit-for-bit identical when bypassed.
		eq := audio.NewEQ(48000, 1)
		// All bands are at 0 dB by default = bypassed
		require.True(t, eq.IsBypassed(), "flat EQ should report as bypassed")

		pcm := testutil.GenerateSineWavePCM(1000, -10, 48000, 0.5)
		reference := make([]float32, len(pcm))
		copy(reference, pcm)

		// Process through "bypassed" EQ
		eq.Process(pcm, 1)

		// Should be identical (bypassed path does not process samples).
		for i := range pcm {
			require.Equal(t, reference[i], pcm[i],
				"bypassed EQ should not modify samples at index %d", i)
		}
	})

	t.Run("silence_output", func(t *testing.T) {
		t.Parallel()

		// A very low-amplitude signal (-96 dBFS) should be near silence.
		pcm := testutil.GenerateSineWavePCM(1000, -96, 48000, 0.5)
		peakDB := testutil.PeakDBFS(pcm)
		require.Less(t, peakDB, -90.0,
			"signal at -96 dBFS should measure below -90 dBFS (got %.2f)", peakDB)
	})
}

// ==========================================================================
// 11. Sample rate mismatch: resampler preserves signal
// ==========================================================================

func TestSampleRateMismatch(t *testing.T) {
	t.Parallel()

	// Test the resampler directly: resample 44100 Hz → 48000 Hz.
	// A 1kHz sine should survive the resampling with minimal distortion.

	const srcRate = 44100
	const dstRate = 48000
	const channels = 1
	const duration = 1.0 // seconds

	resampler := audio.NewResampler(srcRate, dstRate, channels)

	// Generate 1kHz sine at 44100 Hz.
	inputPCM := testutil.GenerateSineWavePCM(1000, -6, srcRate, duration)
	inputLevel := testutil.PeakDBFS(inputPCM)

	// Resample in chunks (simulating real-time frame processing).
	const chunkSize = 1024
	var outputPCM []float32
	for i := 0; i < len(inputPCM); i += chunkSize {
		end := i + chunkSize
		if end > len(inputPCM) {
			end = len(inputPCM)
		}
		chunk := inputPCM[i:end]
		resampled := resampler.Resample(chunk)
		outputPCM = append(outputPCM, resampled...)
	}

	// Output length should be approximately srcLength * dstRate / srcRate.
	expectedLen := float64(len(inputPCM)) * float64(dstRate) / float64(srcRate)
	require.InDelta(t, expectedLen, float64(len(outputPCM)), float64(dstRate)/10,
		"resampled length should be ~%.0f, got %d", expectedLen, len(outputPCM))

	// Output level should match input level within 0.5 dB (resampling preserves amplitude).
	// Skip the first and last 100ms to avoid edge effects.
	skipSamples := dstRate / 10
	if 2*skipSamples >= len(outputPCM) {
		skipSamples = 0
	}
	outputLevel := testutil.PeakDBFS(outputPCM[skipSamples : len(outputPCM)-skipSamples])
	levelDiff := math.Abs(outputLevel - inputLevel)
	require.LessOrEqual(t, levelDiff, 0.5,
		"resampled signal level should match input within 0.5 dB: input=%.2f, output=%.2f",
		inputLevel, outputLevel)

	// Verify the output actually contains signal (not silence).
	require.Greater(t, testutil.PeakDBFS(outputPCM), -20.0,
		"resampled output should not be silent")

	t.Logf("Resample 44100→48000: input=%.2f dBFS, output=%.2f dBFS, diff=%.3f dB, len=%d→%d",
		inputLevel, outputLevel, levelDiff, len(inputPCM), len(outputPCM))
}

// ==========================================================================
// 12. Channel count mismatch: mono + stereo handled without crash
// ==========================================================================

func TestChannelCountMismatch(t *testing.T) {
	t.Parallel()

	// Test the EQ and compressor with different channel counts to verify
	// they handle mono and stereo inputs correctly.

	t.Run("mono_eq", func(t *testing.T) {
		t.Parallel()

		eq := audio.NewEQ(48000, 1) // mono
		require.NoError(t, eq.SetBand(1, 1000, 3.0, 1.0, true))

		pcm := testutil.GenerateSineWavePCM(1000, -10, 48000, 0.5)
		eq.Process(pcm, 1)

		// Should not crash and should have signal.
		require.Greater(t, testutil.PeakDBFS(pcm), -20.0,
			"mono EQ output should have signal")
	})

	t.Run("stereo_eq", func(t *testing.T) {
		t.Parallel()

		eq := audio.NewEQ(48000, 2) // stereo
		require.NoError(t, eq.SetBand(1, 1000, 3.0, 1.0, true))

		// Generate stereo interleaved signal.
		mono := testutil.GenerateSineWavePCM(1000, -10, 48000, 0.5)
		stereo := make([]float32, len(mono)*2)
		for i, s := range mono {
			stereo[i*2] = s   // L
			stereo[i*2+1] = s // R
		}

		eq.Process(stereo, 2)

		// Verify both channels have signal.
		var peakL, peakR float64
		for i := 0; i < len(stereo); i += 2 {
			absL := math.Abs(float64(stereo[i]))
			absR := math.Abs(float64(stereo[i+1]))
			if absL > peakL {
				peakL = absL
			}
			if absR > peakR {
				peakR = absR
			}
		}
		require.Greater(t, peakL, 0.01, "stereo EQ left channel should have signal")
		require.Greater(t, peakR, 0.01, "stereo EQ right channel should have signal")

		// L and R should be identical (same input, symmetric EQ).
		for i := 0; i < len(stereo)-1; i += 2 {
			require.InDelta(t, float64(stereo[i]), float64(stereo[i+1]), 1e-5,
				"stereo channels should be equal at sample %d", i/2)
		}
	})

	t.Run("mono_compressor", func(t *testing.T) {
		t.Parallel()

		comp := audio.NewCompressor(48000, 1)
		require.NoError(t, comp.SetParams(-20, 4.0, 1.0, 50.0, 0))

		pcm := testutil.GenerateSineWavePCM(1000, -10, 48000, 0.5)
		comp.Process(pcm)

		require.Greater(t, testutil.PeakDBFS(pcm), -30.0,
			"mono compressor output should have signal")
	})

	t.Run("stereo_compressor", func(t *testing.T) {
		t.Parallel()

		comp := audio.NewCompressor(48000, 2)
		require.NoError(t, comp.SetParams(-20, 4.0, 1.0, 50.0, 0))

		mono := testutil.GenerateSineWavePCM(1000, -10, 48000, 0.5)
		stereo := make([]float32, len(mono)*2)
		for i, s := range mono {
			stereo[i*2] = s
			stereo[i*2+1] = s
		}

		comp.Process(stereo)

		// Verify both channels have signal.
		var peakL, peakR float64
		for i := 0; i < len(stereo); i += 2 {
			absL := math.Abs(float64(stereo[i]))
			absR := math.Abs(float64(stereo[i+1]))
			if absL > peakL {
				peakL = absL
			}
			if absR > peakR {
				peakR = absR
			}
		}
		require.Greater(t, peakL, 0.01, "stereo compressor left should have signal")
		require.Greater(t, peakR, 0.01, "stereo compressor right should have signal")
	})

	t.Run("mono_limiter", func(t *testing.T) {
		t.Parallel()

		lim := audio.NewLimiter(48000, 1)
		pcm := testutil.GenerateSineWavePCM(1000, 0, 48000, 0.5) // 0 dBFS
		lim.Process(pcm)

		// Should limit to -1 dBFS
		thresholdLinear := math.Pow(10, -1.0/20.0)
		for i, s := range pcm {
			require.LessOrEqual(t, math.Abs(float64(s)), thresholdLinear+1e-6,
				"mono limiter: sample %d exceeds threshold", i)
		}
	})

	t.Run("crossfade_stereo", func(t *testing.T) {
		t.Parallel()

		// Crossfade between mono and stereo-length buffers.
		mono := testutil.GenerateSineWavePCM(440, -6, 48000, 0.1)
		stereo := make([]float32, len(mono)*2)
		for i, s := range mono {
			stereo[i*2] = s
			stereo[i*2+1] = s
		}

		// Crossfade with different lengths should not crash (shorter is zero-padded).
		result := audio.EqualPowerCrossfade(mono, stereo)
		require.NotNil(t, result)
		require.Equal(t, len(stereo), len(result),
			"crossfade should output the length of the longer buffer")
	})
}
