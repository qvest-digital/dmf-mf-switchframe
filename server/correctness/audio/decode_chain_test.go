//go:build correctness

package audio

import (
	"math"
	"math/cmplx"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// ==========================================================================
// TestAACEncodeDecodeRoundTrip: encode 1kHz sine → decode → measure SNR
// ==========================================================================

func TestAACEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	const (
		sampleRate = 48000
		channels   = 1
		freq       = 1000.0
		levelDBFS  = -6.0
		frameSize  = 1024
		numFrames  = 50 // ~1 second
	)

	// Create encoder and decoder.
	enc, err := audio.NewFDKEncoder(sampleRate, channels)
	require.NoError(t, err, "failed to create FDK encoder")
	defer enc.Close()

	dec, err := audio.NewFDKDecoder(sampleRate, channels)
	require.NoError(t, err, "failed to create FDK decoder")
	defer dec.Close()

	// Generate 1kHz sine wave and encode/decode frame-by-frame.
	// This preserves alignment between input and output arrays.
	amplitude := math.Pow(10.0, levelDBFS/20.0)
	var allInput []float32
	var allOutput []float32

	for f := 0; f < numFrames; f++ {
		frame := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			sampleIdx := f*frameSize + i
			frame[i] = float32(amplitude * math.Sin(2*math.Pi*freq*float64(sampleIdx)/float64(sampleRate)))
		}
		allInput = append(allInput, frame...)

		aacData, encErr := enc.Encode(frame)
		require.NoError(t, encErr, "encode error at frame %d", f)
		if len(aacData) == 0 {
			// Encoder priming: produce zeros as placeholder to keep alignment.
			allOutput = append(allOutput, make([]float32, frameSize*channels)...)
			continue
		}

		pcm, decErr := dec.Decode(aacData)
		if decErr != nil || pcm == nil {
			// Decoder priming: produce zeros as placeholder.
			allOutput = append(allOutput, make([]float32, frameSize*channels)...)
			continue
		}
		pcmCopy := make([]float32, len(pcm))
		copy(pcmCopy, pcm)
		allOutput = append(allOutput, pcmCopy...)
	}

	t.Logf("MEASURE: total input samples = %d, total output samples = %d", len(allInput), len(allOutput))
	require.Equal(t, len(allInput), len(allOutput),
		"frame-by-frame encode/decode should produce same number of samples")

	// Skip priming region (first 5 frames = 5120 samples) for SNR measurement.
	skipSamples := 5 * frameSize * channels
	require.Greater(t, len(allInput), skipSamples, "need more frames than skip region")

	var signalPower, noisePower float64
	measured := 0
	for i := skipSamples; i < len(allInput); i++ {
		sig := float64(allInput[i])
		noise := float64(allOutput[i]) - sig
		signalPower += sig * sig
		noisePower += noise * noise
		measured++
	}

	if noisePower < 1e-30 {
		noisePower = 1e-30
	}
	snr := 10.0 * math.Log10(signalPower/noisePower)
	t.Logf("MEASURE: SNR = %.1f dB (measured %d samples, skipped %d priming samples)",
		snr, measured, skipSamples)

	// AAC-LC at 64kbps mono should achieve > 40 dB SNR for a 1kHz tone.
	assert.Greater(t, snr, 40.0, "SNR should be > 40 dB for AAC encode/decode round-trip")

	// Also verify total decoded count is sane.
	assert.Equal(t, numFrames*frameSize*channels, len(allOutput),
		"output sample count should exactly match input (frame-aligned codec)")
}

// ==========================================================================
// TestAACDecodeChainSampleRate: encode at 44100 → decode → resample to 48000 → verify
// ==========================================================================

func TestAACDecodeChainSampleRate(t *testing.T) {
	t.Parallel()

	const (
		srcRate     = 44100
		dstRate     = 48000
		channels    = 1
		durationSec = 1.0
		freq        = 1000.0
		levelDBFS   = -6.0
		frameSize   = 1024
	)

	// Generate sine at 44100 Hz sample rate.
	original := testutil.GenerateSineWavePCM(freq, levelDBFS, srcRate, durationSec)
	t.Logf("MEASURE: generated %d samples at %d Hz", len(original), srcRate)

	// Encode at 44100 Hz.
	enc, err := audio.NewFDKEncoder(srcRate, channels)
	require.NoError(t, err)
	defer enc.Close()

	var aacFrames [][]byte
	for offset := 0; offset+frameSize <= len(original); offset += frameSize {
		aacData, encErr := enc.Encode(original[offset : offset+frameSize])
		require.NoError(t, encErr)
		if len(aacData) > 0 {
			aacFrames = append(aacFrames, aacData)
		}
	}
	t.Logf("MEASURE: encoded %d AAC frames at %d Hz", len(aacFrames), srcRate)

	// Decode at 44100 Hz.
	dec, err := audio.NewFDKDecoder(srcRate, channels)
	require.NoError(t, err)
	defer dec.Close()

	var decoded []float32
	for _, frame := range aacFrames {
		pcm, decErr := dec.Decode(frame)
		if decErr != nil || pcm == nil {
			continue
		}
		pcmCopy := make([]float32, len(pcm))
		copy(pcmCopy, pcm)
		decoded = append(decoded, pcmCopy...)
	}
	t.Logf("MEASURE: decoded %d samples at %d Hz", len(decoded), srcRate)

	// Verify decoded sample count matches expectation.
	expectedDecoded := len(original)
	decodedDiff := int(math.Abs(float64(len(decoded) - expectedDecoded)))
	t.Logf("MEASURE: decoded sample count diff from input = %d", decodedDiff)
	assert.LessOrEqual(t, decodedDiff, 2048, "decoded count should be within ±2048 of input")

	// Resample 44100 → 48000.
	resampler := audio.NewResampler(srcRate, dstRate, channels)
	resampled := resampler.Resample(decoded)
	t.Logf("MEASURE: resampled output length = %d (expected ~%d)", len(resampled),
		int(float64(len(decoded))*float64(dstRate)/float64(srcRate)))

	expectedResampled := int(float64(len(decoded)) * float64(dstRate) / float64(srcRate))
	resampledDiff := int(math.Abs(float64(len(resampled) - expectedResampled)))
	t.Logf("MEASURE: resampled length diff from expected = %d", resampledDiff)
	// Allow some tolerance for resampler filter delay.
	assert.LessOrEqual(t, resampledDiff, 256,
		"resampled length should be close to expected ratio")

	// FFT the resampled output to verify 1kHz peak is present.
	// Skip ahead past the AAC priming region (first ~5000 samples) to avoid
	// codec transient artifacts that would pollute the spectrum.
	fftLen := 4096
	fftSkip := 8192 // skip priming + settling
	if len(resampled) < fftSkip+fftLen {
		t.Skipf("not enough resampled samples for FFT (%d < %d)", len(resampled), fftSkip+fftLen)
	}

	// Window the signal (Hanning) and compute FFT.
	fftInput := make([]complex128, fftLen)
	for i := 0; i < fftLen; i++ {
		window := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(fftLen-1)))
		fftInput[i] = complex(float64(resampled[fftSkip+i])*window, 0)
	}
	fftOutput := fft(fftInput)

	// Find peak bin.
	maxMag := 0.0
	peakBin := 0
	for i := 1; i < fftLen/2; i++ {
		mag := cmplx.Abs(fftOutput[i])
		if mag > maxMag {
			maxMag = mag
			peakBin = i
		}
	}
	peakFreq := float64(peakBin) * float64(dstRate) / float64(fftLen)
	t.Logf("MEASURE: FFT peak at bin %d = %.1f Hz (expected ~%.0f Hz)", peakBin, peakFreq, freq)

	// Peak should be within +/- one bin of 1kHz.
	binWidth := float64(dstRate) / float64(fftLen)
	assert.InDelta(t, freq, peakFreq, binWidth*1.5,
		"FFT peak should be at ~1kHz after resampling")

	// Check for resampler aliasing artifacts at known aliasing frequencies.
	// For 44100->48000, the aliasing image of 1kHz would be at 44100-1000=43100 Hz
	// (folded into 48000-43100=4900 Hz in the output spectrum). However, AAC codec
	// noise is broadband at ~-60 dB, so instead of checking all bins, we verify:
	// 1. The peak is at 1kHz (done above)
	// 2. The RMS noise floor is below -30 dB relative to peak (accounts for AAC noise)
	//
	// Compute average noise floor excluding the peak region.
	expectedBin := int(math.Round(freq * float64(fftLen) / float64(dstRate)))
	var noiseSum float64
	noiseBins := 0
	for i := 1; i < fftLen/2; i++ {
		// Skip bins near the expected peak (+/- 5 bins).
		if i >= expectedBin-5 && i <= expectedBin+5 {
			continue
		}
		mag := cmplx.Abs(fftOutput[i])
		noiseSum += mag * mag
		noiseBins++
	}
	rmsNoiseRatio := math.Sqrt(noiseSum/float64(noiseBins)) / maxMag
	rmsNoiseDB := 20.0 * math.Log10(rmsNoiseRatio+1e-15)
	t.Logf("MEASURE: RMS noise floor relative to peak = %.1f dB (over %d bins)", rmsNoiseDB, noiseBins)
	assert.Less(t, rmsNoiseDB, -30.0,
		"RMS noise floor should be at least 30 dB below peak after resampling")

	// Also check for the specific aliasing frequency (4900 Hz for 44100->48000 with 1kHz input).
	// If the resampler has aliasing, this bin would be anomalously high.
	aliasingFreq := float64(srcRate) - freq // 43100 Hz, folds to 48000-43100=4900 Hz
	foldedFreq := float64(dstRate) - aliasingFreq
	aliasingBin := int(math.Round(foldedFreq * float64(fftLen) / float64(dstRate)))
	if aliasingBin > 0 && aliasingBin < fftLen/2 {
		aliasingMag := cmplx.Abs(fftOutput[aliasingBin])
		aliasingDB := 20.0 * math.Log10(aliasingMag/maxMag+1e-15)
		t.Logf("MEASURE: aliasing at %.0f Hz (bin %d) = %.1f dB below peak", foldedFreq, aliasingBin, aliasingDB)
		assert.Less(t, aliasingDB, -40.0,
			"resampler aliasing artifact at %.0f Hz should be at least 40 dB below peak", foldedFreq)
	}
}

// ==========================================================================
// TestMixerIngestAACFrame: feed AAC frames through mixer, verify non-silent mixed PCM
// ==========================================================================

func TestMixerIngestAACFrame(t *testing.T) {
	t.Parallel()

	const (
		sampleRate  = 48000
		channels    = 2
		durationSec = 0.5
		freq        = 1000.0
		levelDBFS   = -6.0
		frameSize   = 1024
	)

	// Generate stereo 1kHz sine.
	monoSine := testutil.GenerateSineWavePCM(freq, levelDBFS, sampleRate, durationSec)
	stereoSine := make([]float32, len(monoSine)*channels)
	for i, s := range monoSine {
		for ch := 0; ch < channels; ch++ {
			stereoSine[i*channels+ch] = s
		}
	}

	// Encode to AAC.
	enc, err := audio.NewFDKEncoder(sampleRate, channels)
	require.NoError(t, err)
	defer enc.Close()

	var aacFrames [][]byte
	for offset := 0; offset+frameSize*channels <= len(stereoSine); offset += frameSize * channels {
		chunk := stereoSine[offset : offset+frameSize*channels]
		aacData, encErr := enc.Encode(chunk)
		require.NoError(t, encErr)
		if len(aacData) > 0 {
			aacFrames = append(aacFrames, aacData)
		}
	}
	t.Logf("MEASURE: prepared %d AAC frames for mixer ingest", len(aacFrames))

	decoderFactory := func(sr, ch int) (audio.Decoder, error) {
		return audio.NewFDKDecoder(sr, ch)
	}

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate:     sampleRate,
		Channels:       channels,
		DecoderFactory: decoderFactory,
	})
	defer mixer.Close()

	// Add channel and make it active.
	sourceKey := "test-source-1"
	mixer.AddChannel(sourceKey)
	mixer.SetActive(sourceKey, true)

	// The mixer stamps AAC with its arrival. Each frame is fed with a PTS
	// of the source's own timeline and the program period that ends at
	// its arrival is read right after, as the reference clock reads.
	var output []float32
	for k, aacData := range aacFrames {
		mixer.IngestFrame(sourceKey, &media.AudioFrame{
			PTS:        int64(k * frameSize * 90000 / sampleRate),
			Data:       aacData,
			SampleRate: sampleRate,
			Channels:   channels,
		})
		idx := nowIndex() - frameSize
		output = append(output, mixer.ProduceSamples(idx, frameSize, clock.IndexPTS(rate48k, idx))...)
	}
	require.Len(t, output, len(aacFrames)*frameSize*channels)

	var maxAbsVal float32
	var rmsAccum float64
	var rmsCount int
	for _, s := range output {
		absVal := float32(math.Abs(float64(s)))
		if absVal > maxAbsVal {
			maxAbsVal = absVal
		}
		rmsAccum += float64(s) * float64(s)
		rmsCount++
	}
	totalSamples := len(output)

	rms := float32(0)
	if rmsCount > 0 {
		rms = float32(math.Sqrt(rmsAccum / float64(rmsCount)))
	}
	t.Logf("MEASURE: output RMS = %.4f, max abs = %.4f, total samples = %d, expected > 0.01",
		rms, maxAbsVal, totalSamples)

	// The mixer should produce non-silent output if it ingested non-silent AAC.
	assert.Greater(t, float64(rms), 0.01,
		"mixer output RMS should be > 0.01 (not silence) after ingesting 1kHz tone")
}

// ==========================================================================
// TestAACFrameBoundaryAlignment: test codec behavior at exact and non-aligned boundaries
// ==========================================================================

func TestAACFrameBoundaryAlignment(t *testing.T) {
	t.Parallel()

	const (
		sampleRate = 48000
		channels   = 1
		freq       = 1000.0
		levelDBFS  = -6.0
		frameSize  = 1024
	)

	t.Run("ExactFrameSize", func(t *testing.T) {
		// Generate exactly 1024 samples.
		sine := testutil.GenerateSineWavePCM(freq, levelDBFS, sampleRate,
			float64(frameSize)/float64(sampleRate))
		require.Equal(t, frameSize, len(sine))

		enc, err := audio.NewFDKEncoder(sampleRate, channels)
		require.NoError(t, err)
		defer enc.Close()

		aacData, err := enc.Encode(sine)
		require.NoError(t, err)
		t.Logf("MEASURE: encoded 1024 samples to %d bytes AAC", len(aacData))

		if len(aacData) == 0 {
			t.Log("MEASURE: first frame produced 0 bytes (encoder priming)")
			// Try a second frame to get actual output.
			aacData2, err2 := enc.Encode(sine)
			require.NoError(t, err2)
			t.Logf("MEASURE: second frame produced %d bytes", len(aacData2))
			aacData = aacData2
		}

		// Decode back.
		dec, err := audio.NewFDKDecoder(sampleRate, channels)
		require.NoError(t, err)
		defer dec.Close()

		pcm, err := dec.Decode(aacData)
		if err != nil {
			t.Logf("MEASURE: decode error (may need priming): %v", err)
		}
		if pcm == nil {
			t.Log("MEASURE: decoded sample count = 0 from 1024 input (priming frame)")
		} else {
			t.Logf("MEASURE: decoded sample count = %d from 1024 input", len(pcm))
		}
	})

	t.Run("SubFrameInput_1023", func(t *testing.T) {
		// 1023 samples (one less than frame size) — encoder should reject.
		sine := testutil.GenerateSineWavePCM(freq, levelDBFS, sampleRate,
			1023.0/float64(sampleRate))
		require.Equal(t, 1023, len(sine))

		enc, err := audio.NewFDKEncoder(sampleRate, channels)
		require.NoError(t, err)
		defer enc.Close()

		_, encErr := enc.Encode(sine)
		t.Logf("MEASURE: encode 1023 samples error: %v", encErr)
		// The encoder requires exactly 1024*channels samples; 1023 should fail.
		assert.Error(t, encErr,
			"encoder should reject input that is not exactly frameSize*channels samples")
	})

	t.Run("SuperFrameInput_1025", func(t *testing.T) {
		// 1025 samples (one more than frame size) — encoder should reject.
		sine := testutil.GenerateSineWavePCM(freq, levelDBFS, sampleRate,
			1025.0/float64(sampleRate))
		require.Equal(t, 1025, len(sine))

		enc, err := audio.NewFDKEncoder(sampleRate, channels)
		require.NoError(t, err)
		defer enc.Close()

		_, encErr := enc.Encode(sine)
		t.Logf("MEASURE: encode 1025 samples error: %v", encErr)
		assert.Error(t, encErr,
			"encoder should reject input that is not exactly frameSize*channels samples")
	})
}

// ==========================================================================
// TestAACEncoderDecoderConsistency: deterministic codec output
// ==========================================================================

func TestAACEncoderDecoderConsistency(t *testing.T) {
	t.Parallel()

	const (
		sampleRate  = 48000
		channels    = 1
		durationSec = 0.5
		freq        = 1000.0
		levelDBFS   = -6.0
		frameSize   = 1024
		numRuns     = 3
	)

	// Generate source signal.
	original := testutil.GenerateSineWavePCM(freq, levelDBFS, sampleRate, durationSec)

	type roundTripResult struct {
		aacFrames [][]byte
		decoded   []float32
	}

	results := make([]roundTripResult, numRuns)

	for run := 0; run < numRuns; run++ {
		// Fresh encoder.
		enc, err := audio.NewFDKEncoder(sampleRate, channels)
		require.NoError(t, err)

		var aacFrames [][]byte
		for offset := 0; offset+frameSize <= len(original); offset += frameSize {
			aacData, encErr := enc.Encode(original[offset : offset+frameSize])
			require.NoError(t, encErr)
			if len(aacData) > 0 {
				frameCopy := make([]byte, len(aacData))
				copy(frameCopy, aacData)
				aacFrames = append(aacFrames, frameCopy)
			}
		}
		enc.Close()

		// Fresh decoder.
		dec, err := audio.NewFDKDecoder(sampleRate, channels)
		require.NoError(t, err)

		var decoded []float32
		for _, frame := range aacFrames {
			pcm, decErr := dec.Decode(frame)
			if decErr != nil || pcm == nil {
				continue
			}
			pcmCopy := make([]float32, len(pcm))
			copy(pcmCopy, pcm)
			decoded = append(decoded, pcmCopy...)
		}
		dec.Close()

		results[run] = roundTripResult{aacFrames: aacFrames, decoded: decoded}
		t.Logf("MEASURE: run %d: %d AAC frames, %d decoded samples",
			run, len(aacFrames), len(decoded))
	}

	// Compare AAC frame counts across runs.
	for i := 1; i < numRuns; i++ {
		assert.Equal(t, len(results[0].aacFrames), len(results[i].aacFrames),
			"AAC frame count should be consistent across runs")
	}

	// Compare AAC encoded bytes for determinism.
	aacByteExact := true
	for i := 1; i < numRuns; i++ {
		minFrames := len(results[0].aacFrames)
		if len(results[i].aacFrames) < minFrames {
			minFrames = len(results[i].aacFrames)
		}
		for j := 0; j < minFrames; j++ {
			if len(results[0].aacFrames[j]) != len(results[i].aacFrames[j]) {
				aacByteExact = false
				t.Logf("MEASURE: AAC frame %d size mismatch: run0=%d vs run%d=%d",
					j, len(results[0].aacFrames[j]), i, len(results[i].aacFrames[j]))
				break
			}
			for k := range results[0].aacFrames[j] {
				if results[0].aacFrames[j][k] != results[i].aacFrames[j][k] {
					aacByteExact = false
					t.Logf("MEASURE: AAC frame %d byte %d differs: run0=0x%02x vs run%d=0x%02x",
						j, k, results[0].aacFrames[j][k], i, results[i].aacFrames[j][k])
					break
				}
			}
			if !aacByteExact {
				break
			}
		}
	}
	t.Logf("MEASURE: AAC encoded bytes are byte-exact across %d runs: %v", numRuns, aacByteExact)

	// Compare decoded PCM for determinism.
	pcmExact := true
	for i := 1; i < numRuns; i++ {
		assert.Equal(t, len(results[0].decoded), len(results[i].decoded),
			"decoded sample count should be consistent across runs")
		minLen := len(results[0].decoded)
		if len(results[i].decoded) < minLen {
			minLen = len(results[i].decoded)
		}
		diffs := 0
		maxDiff := float32(0)
		for j := 0; j < minLen; j++ {
			d := float32(math.Abs(float64(results[0].decoded[j] - results[i].decoded[j])))
			if d > 0 {
				diffs++
				if d > maxDiff {
					maxDiff = d
				}
				pcmExact = false
			}
		}
		if diffs > 0 {
			t.Logf("MEASURE: run0 vs run%d: %d sample diffs, max diff = %.8f", i, diffs, maxDiff)
		}
	}
	t.Logf("MEASURE: decoded PCM is byte-exact across %d runs: %v", numRuns, pcmExact)

	// If codec is deterministic, both should be exact.
	assert.True(t, aacByteExact, "AAC encoder should be deterministic")
	assert.True(t, pcmExact, "AAC decode of identical input should be deterministic")
}

// ==========================================================================
// Helpers
// ==========================================================================

// fft computes the discrete Fourier transform using Cooley-Tukey radix-2.
// Input length must be a power of 2.
func fft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}
	if n&(n-1) != 0 {
		panic("fft: length must be power of 2")
	}

	// Bit-reversal permutation.
	result := make([]complex128, n)
	copy(result, x)
	bits := 0
	for v := n; v > 1; v >>= 1 {
		bits++
	}
	for i := 0; i < n; i++ {
		j := reverseBits(i, bits)
		if j > i {
			result[i], result[j] = result[j], result[i]
		}
	}

	// Cooley-Tukey butterfly.
	for size := 2; size <= n; size *= 2 {
		half := size / 2
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wn := complex(1, 0)
			for k := 0; k < half; k++ {
				t := wn * result[start+k+half]
				u := result[start+k]
				result[start+k] = u + t
				result[start+k+half] = u - t
				wn *= w
			}
		}
	}

	return result
}

func reverseBits(v, bits int) int {
	r := 0
	for i := 0; i < bits; i++ {
		r = (r << 1) | (v & 1)
		v >>= 1
	}
	return r
}
