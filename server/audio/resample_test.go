package audio

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// ============================================================================
// Core resampler tests
// ============================================================================

func TestResamplerIdentity(t *testing.T) {
	t.Parallel()
	r := NewResampler(48000, 48000, 1)
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	output := r.Resample(input)
	require.Equal(t, input, output)
}

func TestResamplerOutputLength(t *testing.T) {
	t.Parallel()
	r := NewResampler(44100, 48000, 1)

	const frames = 1000
	const samplesPerFrame = 1024
	totalIn, totalOut := 0, 0

	expectedPerFrame := float64(samplesPerFrame) * 48000.0 / 44100.0
	lo := int(math.Floor(expectedPerFrame))
	hi := int(math.Ceil(expectedPerFrame))

	for i := 0; i < frames; i++ {
		output := r.Resample(make([]float32, samplesPerFrame))
		totalIn += samplesPerFrame
		totalOut += len(output)
		require.True(t, len(output) >= lo && len(output) <= hi,
			"frame %d: expected %d-%d, got %d", i, lo, hi, len(output))
	}

	expectedTotal := float64(totalIn) * 48000.0 / 44100.0
	require.InDelta(t, expectedTotal, float64(totalOut), 1.0)
}

func TestResamplerDCPreservation(t *testing.T) {
	t.Parallel()
	r := NewResampler(44100, 48000, 1)
	dc := float32(0.75)

	warmup := make([]float32, 4096)
	for i := range warmup {
		warmup[i] = dc
	}
	_ = r.Resample(warmup)

	input := make([]float32, 1024)
	for i := range input {
		input[i] = dc
	}
	for _, v := range r.Resample(input) {
		require.InDelta(t, dc, v, 1e-4)
	}
}

func TestResamplerZeroSignal(t *testing.T) {
	t.Parallel()
	r := NewResampler(44100, 48000, 1)
	for _, v := range r.Resample(make([]float32, 2048)) {
		require.Equal(t, float32(0), v)
	}
}

func TestResamplerEmptyInput(t *testing.T) {
	t.Parallel()
	r := NewResampler(44100, 48000, 1)
	require.Empty(t, r.Resample([]float32{}))
}

func TestResamplerReset(t *testing.T) {
	t.Parallel()
	signal := makeSine(2048, 1000, 44100)

	r := NewResampler(44100, 48000, 1)
	_ = r.Resample(signal)
	r.Reset()
	out1 := r.Resample(signal)

	fresh := NewResampler(44100, 48000, 1)
	out2 := fresh.Resample(signal)

	require.Equal(t, len(out1), len(out2))
	for i := range out1 {
		require.InDelta(t, out1[i], out2[i], 1e-7, "sample %d", i)
	}
}

// ============================================================================
// Signal quality tests (time-domain — fast, no DFT)
// ============================================================================

// TestResamplerSinusoidSNR measures SNR by fitting a reference sine to the
// resampled output. This is a time-domain measurement — no DFT needed.
func TestResamplerSinusoidSNR(t *testing.T) {
	t.Parallel()

	signal := makeSine(16384, 1000, 44100)
	r := NewResampler(44100, 48000, 1)
	output := r.Resample(signal)

	snr := timeDomainSNR(output, 1000, 48000)
	t.Logf("1kHz sinusoid SNR: %.1f dB", snr)
	require.Greater(t, snr, 50.0)
}

func TestResamplerRoundTrip(t *testing.T) {
	t.Parallel()

	signal := makeSine(16384, 1000, 44100)

	rUp := NewResampler(44100, 48000, 1)
	upsampled := rUp.Resample(signal)

	rDown := NewResampler(48000, 44100, 1)
	roundTrip := rDown.Resample(upsampled)

	snr := timeDomainSNR(roundTrip, 1000, 44100)
	t.Logf("Round-trip SNR: %.1f dB", snr)
	require.Greater(t, snr, 40.0)
}

func TestResamplerEnergyConservation(t *testing.T) {
	t.Parallel()

	input := make([]float32, 4096)
	for i := range input {
		x := float64(i) / 44100.0
		input[i] = float32(0.3*math.Sin(2*math.Pi*440*x) + 0.2*math.Sin(2*math.Pi*1000*x) +
			0.15*math.Sin(2*math.Pi*5000*x) + 0.1*math.Sin(2*math.Pi*10000*x))
	}

	r := NewResampler(44100, 48000, 1)
	_ = r.Resample(input) // warmup
	output := r.Resample(input)

	var inE, outE float64
	for _, v := range input {
		inE += float64(v) * float64(v)
	}
	for _, v := range output {
		outE += float64(v) * float64(v)
	}

	require.InDelta(t, 48000.0/44100.0, outE/inE, 0.02)
}

func TestResamplerLinearity(t *testing.T) {
	t.Parallel()
	const n = 2048
	const a, b = float32(0.7), float32(0.3)

	x := makeSine(n, 440, 44100)
	y := makeSine(n, 2000, 44100)
	combined := make([]float32, n)
	for i := range combined {
		combined[i] = a*x[i] + b*y[i]
	}

	outC := NewResampler(44100, 48000, 1).Resample(combined)
	outX := NewResampler(44100, 48000, 1).Resample(x)
	outY := NewResampler(44100, 48000, 1).Resample(y)

	for i := 0; i < min(len(outC), min(len(outX), len(outY))); i++ {
		require.InDelta(t, a*outX[i]+b*outY[i], outC[i], 1e-5, "sample %d", i)
	}
}

// ============================================================================
// Streaming consistency
// ============================================================================

func TestResamplerStreamingConsistency(t *testing.T) {
	t.Parallel()
	signal := makeSine(10240, 1000, 44100)

	mono := NewResampler(44100, 48000, 1).Resample(signal)

	block := NewResampler(44100, 48000, 1)
	var blockOut []float32
	for i := 0; i < len(signal); i += 1024 {
		end := min(i+1024, len(signal))
		blockOut = append(blockOut, block.Resample(signal[i:end])...)
	}

	require.Equal(t, len(mono), len(blockOut))
	for i := range mono {
		require.InDelta(t, mono[i], blockOut[i], 1e-7, "sample %d", i)
	}
}

// ============================================================================
// Multi-rate and multi-channel
// ============================================================================

func TestResamplerMultipleRatePairs(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, dst int }{
		{44100, 48000}, {48000, 44100}, {96000, 48000}, {32000, 48000}, {22050, 48000},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d→%d", tc.src, tc.dst), func(t *testing.T) {
			t.Parallel()
			r := NewResampler(tc.src, tc.dst, 1)
			totalIn, totalOut := 0, 0
			for i := 0; i < 500; i++ {
				totalOut += len(r.Resample(make([]float32, 1024)))
				totalIn += 1024
			}
			expected := float64(totalIn) * float64(tc.dst) / float64(tc.src)
			require.InDelta(t, expected, float64(totalOut), 1.0)
		})
	}
}

func TestResamplerStereo(t *testing.T) {
	t.Parallel()
	r := NewResampler(44100, 48000, 2)

	input := make([]float32, 4096*2)
	for i := 0; i < 4096; i++ {
		input[i*2] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 44100))
		input[i*2+1] = 0
	}

	output := r.Resample(input)
	require.True(t, len(output)%2 == 0)

	// Right channel: no crosstalk
	var maxR float32
	for i := 1; i < len(output); i += 2 {
		if v := float32(math.Abs(float64(output[i]))); v > maxR {
			maxR = v
		}
	}
	require.Less(t, maxR, float32(1e-6))

	// Left channel: signal present
	var maxL float32
	for i := 0; i < len(output); i += 2 {
		if v := float32(math.Abs(float64(output[i]))); v > maxL {
			maxL = v
		}
	}
	require.Greater(t, maxL, float32(0.5))
}

func TestResamplerDownsampling(t *testing.T) {
	t.Parallel()
	signal := makeSine(16384, 1000, 48000)
	r := NewResampler(48000, 44100, 1)
	output := r.Resample(signal)

	snr := timeDomainSNR(output, 1000, 44100)
	t.Logf("Downsampling 1kHz SNR: %.1f dB", snr)
	require.Greater(t, snr, 50.0)
}

// ============================================================================
// Math unit tests
// ============================================================================

func TestResamplerGCD(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, dst, L, M int }{
		{44100, 48000, 160, 147}, {48000, 44100, 147, 160},
		{96000, 48000, 1, 2}, {32000, 48000, 3, 2}, {22050, 48000, 320, 147},
	}
	for _, tc := range cases {
		r := NewResampler(tc.src, tc.dst, 1)
		require.Equal(t, tc.L, r.upFactor)
		require.Equal(t, tc.M, r.downFactor)
	}
}

func TestResamplerBesselI0(t *testing.T) {
	t.Parallel()
	cases := []struct{ x, want float64 }{
		{0, 1.0}, {1, 1.2660658}, {5, 27.2398718}, {10, 2815.71662},
	}
	for _, tc := range cases {
		require.InDelta(t, tc.want, besselI0(tc.x), tc.want*1e-6)
	}
}

func TestResamplerKaiserWindow(t *testing.T) {
	t.Parallel()
	require.InDelta(t, kaiserWindow(0, 100, 10), kaiserWindow(100, 100, 10), 1e-10)
	require.InDelta(t, 1.0, kaiserWindow(50, 100, 10), 1e-10)
	require.Less(t, kaiserWindow(0, 100, 10), 0.01)
}

// ============================================================================
// Compliance tests adapted from libsamplerate / AES17
// ============================================================================

// TestResamplerAES17GainAccuracy tests gain at 997 Hz (AES17 reference).
func TestResamplerAES17GainAccuracy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ src, dst int }{
		{44100, 48000}, {48000, 44100}, {96000, 48000}, {32000, 48000},
	} {
		t.Run(fmt.Sprintf("%d→%d", tc.src, tc.dst), func(t *testing.T) {
			t.Parallel()
			signal := makeSine(32768, 997, tc.src)
			for i := range signal {
				signal[i] *= 0.5 // -6 dBFS
			}
			output := NewResampler(tc.src, tc.dst, 1).Resample(signal)

			var maxAmp float32
			for i := len(output) / 2; i < len(output); i++ {
				if v := float32(math.Abs(float64(output[i]))); v > maxAmp {
					maxAmp = v
				}
			}
			gainDB := 20 * math.Log10(float64(maxAmp)/0.5)
			t.Logf("gain at 997 Hz: %.3f dB", gainDB)
			require.InDelta(t, 0.0, gainDB, 0.5) // AES17 allows ±1.1, we target ±0.5
		})
	}
}

// TestResamplerPassbandRipple sweeps 100-18000 Hz, measures max deviation.
func TestResamplerPassbandRipple(t *testing.T) {
	t.Parallel()
	freqs := []float64{100, 200, 500, 997, 2000, 4000, 8000, 12000, 15000, 18000}
	var gains []float64

	for _, freq := range freqs {
		signal := makeSine(32768, freq, 44100)
		output := NewResampler(44100, 48000, 1).Resample(signal)

		var maxAmp float32
		for i := len(output) / 2; i < len(output); i++ {
			if v := float32(math.Abs(float64(output[i]))); v > maxAmp {
				maxAmp = v
			}
		}
		gains = append(gains, 20*math.Log10(float64(maxAmp)))
	}

	maxG, minG := gains[0], gains[0]
	for _, g := range gains {
		if g > maxG {
			maxG = g
		}
		if g < minG {
			minG = g
		}
	}
	ripple := (maxG - minG) / 2
	t.Logf("Passband ripple: %.4f dB", ripple)
	require.Less(t, ripple, 0.5)
}

// TestResamplerVariableBlockStreaming uses libsamplerate's block pattern.
func TestResamplerVariableBlockStreaming(t *testing.T) {
	t.Parallel()
	blocks := []int{5, 400, 10, 300, 20, 200, 50, 100, 70}
	total := 0
	for _, b := range blocks {
		total += b
	}

	for _, tc := range []struct{ src, dst int }{
		{44100, 13230}, {44100, 39690}, {44100, 48510}, {44100, 132300},
	} {
		t.Run(fmt.Sprintf("%d→%d", tc.src, tc.dst), func(t *testing.T) {
			t.Parallel()
			signal := makeSine(total, 490, tc.src)

			batch := NewResampler(tc.src, tc.dst, 1).Resample(signal)

			stream := NewResampler(tc.src, tc.dst, 1)
			var streamOut []float32
			off := 0
			for _, bs := range blocks {
				streamOut = append(streamOut, stream.Resample(signal[off:off+bs])...)
				off += bs
			}

			require.Equal(t, len(batch), len(streamOut))
			for i := range batch {
				require.InDelta(t, batch[i], streamOut[i], 1e-6, "sample %d", i)
			}
		})
	}
}

// TestResamplerResetClean: process ones, reset, process zeros → must output zeros.
func TestResamplerResetClean(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ src, dst int }{
		{44100, 48000}, {48000, 44100}, {44100, 132300},
	} {
		t.Run(fmt.Sprintf("%d→%d", tc.src, tc.dst), func(t *testing.T) {
			t.Parallel()
			r := NewResampler(tc.src, tc.dst, 1)
			ones := make([]float32, 2048)
			for i := range ones {
				ones[i] = 1.0
			}
			_ = r.Resample(ones)
			r.Reset()
			for _, v := range r.Resample(make([]float32, 2048)) {
				require.Equal(t, float32(0), v)
			}
		})
	}
}

// TestResamplerMultiChannelSNR: different freq per channel, all must pass SNR.
func TestResamplerMultiChannelSNR(t *testing.T) {
	t.Parallel()
	const channels = 4
	const numFrames = 16384
	freqs := [channels]float64{490, 2000, 8000, 14000}

	signal := make([]float32, numFrames*channels)
	for i := 0; i < numFrames; i++ {
		for ch := 0; ch < channels; ch++ {
			signal[i*channels+ch] = float32(math.Sin(2 * math.Pi * freqs[ch] * float64(i) / 44100))
		}
	}

	output := NewResampler(44100, 48000, channels).Resample(signal)
	outFrames := len(output) / channels

	for ch := 0; ch < channels; ch++ {
		chanData := make([]float32, outFrames)
		for i := 0; i < outFrames; i++ {
			chanData[i] = output[i*channels+ch]
		}
		snr := timeDomainSNR(chanData, freqs[ch], 48000)
		t.Logf("Channel %d (%.0f Hz): SNR=%.1f dB", ch, freqs[ch], snr)
		require.Greater(t, snr, 40.0, "channel %d SNR too low", ch)
	}
}

// TestResamplerMultiFreqSNR tests SNR across several frequencies and ratios.
func TestResamplerMultiFreqSNR(t *testing.T) {
	t.Parallel()

	cases := []struct {
		srcRate int
		dstRate int
		freq    float64
		minSNR  float64
	}{
		{44100, 48000, 490, 50},
		{44100, 48000, 1000, 50},
		{44100, 48000, 8000, 50},
		{44100, 48000, 15000, 40},
		{48000, 44100, 1000, 50},
		{48000, 44100, 8000, 50},
		{96000, 48000, 1000, 50},
		{32000, 48000, 1000, 50},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("%d→%d_%.0fHz", tc.srcRate, tc.dstRate, tc.freq)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			signal := makeSine(16384, tc.freq, tc.srcRate)
			output := NewResampler(tc.srcRate, tc.dstRate, 1).Resample(signal)
			snr := timeDomainSNR(output, tc.freq, tc.dstRate)
			t.Logf("SNR: %.1f dB", snr)
			require.Greater(t, snr, tc.minSNR)
		})
	}
}

// ============================================================================
// Frame-aligned resampling (AAC compatibility)
// ============================================================================

// TestResamplerFrameAligned verifies that ResampleFrameAligned always returns
// exactly frameSize*channels samples per call, buffering internally to handle
// the non-integer ratio between input and output sample counts.
func TestResamplerFrameAligned(t *testing.T) {
	t.Parallel()

	const frameSize = 1024
	r := NewResampler(44100, 48000, 2)

	// Process 500 frames — every output must be exactly 1024*2 = 2048 samples
	for i := 0; i < 500; i++ {
		input := makeSine(frameSize*2, 1000, 44100) // 1024 stereo samples
		output := r.ResampleFrameAligned(input, frameSize)
		require.Equal(t, frameSize*2, len(output),
			"frame %d: expected %d samples, got %d", i, frameSize*2, len(output))
	}
}

// TestResamplerFrameAlignedIdentity verifies the identity fast path produces
// exact frame-sized output.
func TestResamplerFrameAlignedIdentity(t *testing.T) {
	t.Parallel()

	r := NewResampler(48000, 48000, 2)
	input := makeSine(1024*2, 1000, 48000)
	output := r.ResampleFrameAligned(input, 1024)
	require.Equal(t, 1024*2, len(output))
}

// TestResamplerFrameAlignedDCPreservation verifies that frame-aligned output
// preserves DC after the transient settles.
func TestResamplerFrameAlignedDCPreservation(t *testing.T) {
	t.Parallel()

	r := NewResampler(44100, 48000, 1)
	dc := float32(0.75)
	input := make([]float32, 1024)
	for i := range input {
		input[i] = dc
	}

	// Warmup — fill internal buffers
	for i := 0; i < 10; i++ {
		_ = r.ResampleFrameAligned(input, 1024)
	}

	output := r.ResampleFrameAligned(input, 1024)
	require.Equal(t, 1024, len(output))
	for i, v := range output {
		require.InDelta(t, dc, v, 1e-3, "sample %d", i)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func makeSine(n int, freqHz float64, sampleRate int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate)))
	}
	return out
}

// timeDomainSNR fits a reference sine to the output and measures SNR.
// Uses the latter half of the signal to avoid filter transients.
func timeDomainSNR(output []float32, freqHz float64, sampleRate int) float64 {
	if len(output) < 200 {
		return 0
	}

	start := len(output) / 2
	end := len(output) - 50

	// Search for best phase alignment (1000 steps over 2π)
	bestPhase := 0.0
	bestCorr := -math.MaxFloat64
	for p := 0; p < 1000; p++ {
		phase := float64(p) / 1000.0 * 2 * math.Pi
		var corr float64
		for i := start; i < end; i++ {
			corr += float64(output[i]) * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate)+phase)
		}
		if corr > bestCorr {
			bestCorr = corr
			bestPhase = phase
		}
	}

	var sigPow, errPow float64
	for i := start; i < end; i++ {
		ref := math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate) + bestPhase)
		e := float64(output[i]) - ref
		sigPow += ref * ref
		errPow += e * e
	}
	if errPow == 0 {
		return 200
	}
	return 10 * math.Log10(sigPow/errPow)
}

// ============================================================================
// Benchmarks
// ============================================================================

// BenchmarkResample44100to48000Mono benchmarks the primary use case:
// one AAC frame (1024 samples) of mono audio, 44100→48000 Hz.
func BenchmarkResample44100to48000Mono(b *testing.B) {
	r := NewResampler(44100, 48000, 1)
	input := makeSine(1024, 1000, 44100)
	// Warmup to fill history and allocate output buffer
	_ = r.Resample(input)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(input) * 4)) // 4 bytes per float32
	for i := 0; i < b.N; i++ {
		_ = r.Resample(input)
	}
}

// BenchmarkResample44100to48000Stereo benchmarks stereo (the common case).
func BenchmarkResample44100to48000Stereo(b *testing.B) {
	r := NewResampler(44100, 48000, 2)
	input := make([]float32, 1024*2)
	for i := 0; i < 1024; i++ {
		input[i*2] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 44100))
		input[i*2+1] = float32(math.Sin(2 * math.Pi * 2000 * float64(i) / 44100))
	}
	_ = r.Resample(input)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(input) * 4))
	for i := 0; i < b.N; i++ {
		_ = r.Resample(input)
	}
}

// BenchmarkResample48000to44100Stereo benchmarks the downsampling case.
func BenchmarkResample48000to44100Stereo(b *testing.B) {
	r := NewResampler(48000, 44100, 2)
	input := make([]float32, 1024*2)
	for i := 0; i < 1024; i++ {
		input[i*2] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		input[i*2+1] = float32(math.Sin(2 * math.Pi * 2000 * float64(i) / 48000))
	}
	_ = r.Resample(input)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(input) * 4))
	for i := 0; i < b.N; i++ {
		_ = r.Resample(input)
	}
}

// BenchmarkResampleIdentity benchmarks the identity (same-rate) fast path.
func BenchmarkResampleIdentity(b *testing.B) {
	r := NewResampler(48000, 48000, 2)
	input := make([]float32, 1024*2)

	b.ResetTimer()
	b.ReportAllocs()
	b.SetBytes(int64(len(input) * 4))
	for i := 0; i < b.N; i++ {
		_ = r.Resample(input)
	}
}

// BenchmarkNewResampler44100to48000 benchmarks resampler creation (one-time cost).
func BenchmarkNewResampler44100to48000(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewResampler(44100, 48000, 2)
	}
}
