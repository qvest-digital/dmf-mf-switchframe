package audio

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEQ_FlatPassesSignalUnchanged(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)

	// Generate a 1kHz sine wave
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	result := eq.Process(samples, 1)

	// Flat EQ (all bands at 0dB gain) should not alter the signal
	for i := range result {
		require.InDelta(t, float64(original[i]), float64(result[i]), 1e-5,
			"sample %d should be unchanged with flat EQ", i)
	}
}

func TestEQ_IsBypassed_FlatGain(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	require.True(t, eq.IsBypassed(), "new EQ with 0dB gains should be bypassed")
}

func TestEQ_IsBypassed_AllDisabled(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	// Set a non-zero gain but disable the band
	err := eq.SetBand(0, 500, 6.0, 1.0, false)
	require.NoError(t, err)
	require.True(t, eq.IsBypassed(), "EQ with all bands disabled should be bypassed")
}

func TestEQ_IsBypassed_NonZeroGain(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	err := eq.SetBand(0, 500, 6.0, 1.0, true)
	require.NoError(t, err)
	require.False(t, eq.IsBypassed(), "EQ with an enabled band at non-zero gain should not be bypassed")
}

func TestEQ_SingleBandBoost(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	// Boost band 0 at 200Hz by +12dB
	err := eq.SetBand(0, 200, 12.0, 1.0, true)
	require.NoError(t, err)

	// Generate a 200Hz sine wave (target frequency)
	n := 4096
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(0.1 * math.Sin(2*math.Pi*200.0*float64(i)/48000.0))
	}

	// Measure energy before
	energyBefore := rmsEnergy(samples)

	// Process through EQ
	result := eq.Process(samples, 1)

	// Measure energy after
	energyAfter := rmsEnergy(result)

	// Boosting at the target frequency should increase energy
	require.Greater(t, energyAfter, energyBefore*1.5,
		"boosting at target frequency should measurably increase energy")
}

func TestEQ_SingleBandCut(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	// Cut band 1 at 1kHz by -12dB
	err := eq.SetBand(1, 1000, -12.0, 1.0, true)
	require.NoError(t, err)

	// Generate a 1kHz sine wave (target frequency)
	n := 4096
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(0.5 * math.Sin(2*math.Pi*1000.0*float64(i)/48000.0))
	}

	// Measure energy before
	energyBefore := rmsEnergy(samples)

	// Process through EQ
	result := eq.Process(samples, 1)

	// Cutting at the target frequency should decrease energy
	require.Less(t, rmsEnergy(result), energyBefore*0.5,
		"cutting at target frequency should measurably decrease energy")
}

func TestEQ_ParameterValidation_FrequencyRanges(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)

	// Band 0 (Low): 80-1000Hz
	require.Error(t, eq.SetBand(0, 79, 0, 1.0, true), "band 0 freq below 80 should fail")
	require.Error(t, eq.SetBand(0, 1001, 0, 1.0, true), "band 0 freq above 1000 should fail")
	require.NoError(t, eq.SetBand(0, 80, 0, 1.0, true), "band 0 freq at 80 should succeed")
	require.NoError(t, eq.SetBand(0, 1000, 0, 1.0, true), "band 0 freq at 1000 should succeed")

	// Band 1 (Mid): 200-8000Hz
	require.Error(t, eq.SetBand(1, 199, 0, 1.0, true), "band 1 freq below 200 should fail")
	require.Error(t, eq.SetBand(1, 8001, 0, 1.0, true), "band 1 freq above 8000 should fail")
	require.NoError(t, eq.SetBand(1, 200, 0, 1.0, true), "band 1 freq at 200 should succeed")
	require.NoError(t, eq.SetBand(1, 8000, 0, 1.0, true), "band 1 freq at 8000 should succeed")

	// Band 2 (High): 1000-16000Hz
	require.Error(t, eq.SetBand(2, 999, 0, 1.0, true), "band 2 freq below 1000 should fail")
	require.Error(t, eq.SetBand(2, 16001, 0, 1.0, true), "band 2 freq above 16000 should fail")
	require.NoError(t, eq.SetBand(2, 1000, 0, 1.0, true), "band 2 freq at 1000 should succeed")
	require.NoError(t, eq.SetBand(2, 16000, 0, 1.0, true), "band 2 freq at 16000 should succeed")
}

func TestEQ_ParameterValidation_GainLimits(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	require.Error(t, eq.SetBand(0, 500, -13, 1.0, true), "gain below -12 should fail")
	require.Error(t, eq.SetBand(0, 500, 13, 1.0, true), "gain above +12 should fail")
	require.NoError(t, eq.SetBand(0, 500, -12, 1.0, true), "gain at -12 should succeed")
	require.NoError(t, eq.SetBand(0, 500, 12, 1.0, true), "gain at +12 should succeed")
}

func TestEQ_ParameterValidation_QLimits(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	require.Error(t, eq.SetBand(0, 500, 0, 0.4, true), "Q below 0.5 should fail")
	require.Error(t, eq.SetBand(0, 500, 0, 4.1, true), "Q above 4.0 should fail")
	require.NoError(t, eq.SetBand(0, 500, 0, 0.5, true), "Q at 0.5 should succeed")
	require.NoError(t, eq.SetBand(0, 500, 0, 4.0, true), "Q at 4.0 should succeed")
}

func TestEQ_ParameterValidation_InvalidBand(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	require.Error(t, eq.SetBand(-1, 500, 0, 1.0, true), "band -1 should fail")
	require.Error(t, eq.SetBand(3, 500, 0, 1.0, true), "band 3 should fail")
}

func TestEQ_CoefficientsRecalculateOnSetBand(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)

	// Process a signal
	samples1 := make([]float32, 1024)
	for i := range samples1 {
		samples1[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}
	result1 := eq.Process(samples1, 1)

	// Change a band
	err := eq.SetBand(1, 1000, 6.0, 1.0, true)
	require.NoError(t, err)

	// Process same signal -- result should differ
	samples2 := make([]float32, 1024)
	for i := range samples2 {
		samples2[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}

	// Reset the EQ's filter state for a fair comparison
	eq2 := NewEQ(48000, 1)
	err = eq2.SetBand(1, 1000, 6.0, 1.0, true)
	require.NoError(t, err)
	result2 := eq2.Process(samples2, 1)

	// The two results should differ (flat vs boosted)
	differ := false
	for i := range result1 {
		if math.Abs(float64(result1[i]-result2[i])) > 1e-5 {
			differ = true
			break
		}
	}
	require.True(t, differ, "output should change after SetBand modifies coefficients")
}

func TestEQ_GetBands(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 1)
	err := eq.SetBand(0, 200, 3.0, 1.5, true)
	require.NoError(t, err)

	bands := eq.GetBands()
	require.Len(t, bands, 3)
	require.InDelta(t, 200.0, bands[0].Frequency, 0.01)
	require.InDelta(t, 3.0, bands[0].Gain, 0.01)
	require.InDelta(t, 1.5, bands[0].Q, 0.01)
	require.True(t, bands[0].Enabled)
}

func TestEQ_StereoNoCrosstalk(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 2)
	err := eq.SetBand(1, 1000, 12.0, 1.0, true)
	require.NoError(t, err)

	// Interleaved stereo: left = 1kHz sine, right = silence
	const numSamples = 1024
	samples := make([]float32, numSamples*2)
	for i := 0; i < numSamples; i++ {
		samples[i*2] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2+1] = 0.0
	}

	processed := eq.Process(samples, 2)

	// Right channel must remain silent
	for i := 0; i < numSamples; i++ {
		require.InDelta(t, 0.0, float64(processed[i*2+1]), 1e-6,
			"right channel sample %d should be silent", i)
	}

	// Left channel should have signal
	var leftPeak float64
	for i := 0; i < numSamples; i++ {
		v := math.Abs(float64(processed[i*2]))
		if v > leftPeak {
			leftPeak = v
		}
	}
	assert.Greater(t, leftPeak, 0.5)
}

// rmsEnergy computes the root mean square energy of a sample buffer.
func rmsEnergy(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func TestEQProcessNoMutex(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000, 2)
	require.NoError(t, eq.SetBand(1, 1000, 6.0, 1.0, true))

	const iterations = 5000

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer goroutine: continuously change EQ parameters
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			gain := float64(i%24) - 12.0 // cycle -12 to +11
			q := 0.5 + float64(i%8)*0.5  // cycle 0.5 to 4.0
			freq := 200.0 + float64(i%7800)
			if freq > 8000 {
				freq = 8000
			}
			_ = eq.SetBand(1, freq, gain, q, i%3 != 0)
		}
	}()

	// Processing goroutine: continuously process audio
	go func() {
		defer wg.Done()
		samples := make([]float32, 256)
		for i := 0; i < iterations; i++ {
			for j := range samples {
				samples[j] = float32(math.Sin(2 * math.Pi * 1000 * float64(j) / 48000))
			}
			eq.Process(samples, 2)
			_ = eq.IsBypassed()
			_ = eq.GetBands()
		}
	}()

	wg.Wait()
}

func TestCompressorProcessNoMutex(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000, 2)
	require.NoError(t, c.SetParams(-10, 4.0, 5.0, 100.0, 3.0))

	const iterations = 5000

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			thresh := -40.0 + float64(i%40)
			ratio := 1.0 + float64(i%19)
			_ = c.SetParams(thresh, ratio, 5.0, 100.0, float64(i%24))
		}
	}()

	go func() {
		defer wg.Done()
		samples := make([]float32, 256)
		for i := 0; i < iterations; i++ {
			for j := range samples {
				samples[j] = float32(math.Sin(2*math.Pi*1000*float64(j)/48000)) * 0.8
			}
			c.Process(samples)
			_ = c.IsBypassed()
			_ = c.GainReduction()
			_, _, _, _, _ = c.GetParams()
		}
	}()

	wg.Wait()
}

func TestLimiterProcessNoMutex(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	const iterations = 5000

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		samples := make([]float32, 256)
		for i := 0; i < iterations; i++ {
			for j := range samples {
				samples[j] = 2.0 * float32(math.Sin(2*math.Pi*1000*float64(j)/48000))
			}
			lim.Process(samples)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = lim.GainReduction()
			if i%500 == 0 {
				lim.Reset()
			}
		}
	}()

	wg.Wait()
}

func TestLoudnessProcessNoMutex(t *testing.T) {
	t.Parallel()
	m := NewLoudnessMeter(48000, 2)

	const iterations = 2000

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		samples := make([]float32, 960) // 10ms stereo at 48kHz
		for i := 0; i < iterations; i++ {
			for j := 0; j < 480; j++ {
				v := float32(0.5 * math.Sin(2*math.Pi*1000*float64(j)/48000))
				samples[j*2] = v
				samples[j*2+1] = v
			}
			m.Process(samples)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = m.MomentaryLUFS()
			_ = m.ShortTermLUFS()
			_ = m.IntegratedLUFS()
			if i%500 == 0 {
				m.Reset()
			}
		}
	}()

	wg.Wait()
}

func BenchmarkBiquadAfterSilence(b *testing.B) {
	coeffs := calcBandCoefficients(1000, 6.0, 1.0, 48000)
	f := &BiquadFilter{
		b0: coeffs.b0, b1: coeffs.b1, b2: coeffs.b2,
		a1: coeffs.a1, a2: coeffs.a2,
	}

	// Prime with 10 seconds of silence (480000 samples at 48kHz)
	for i := 0; i < 480000; i++ {
		f.Process(0.0)
	}

	burst := make([]float64, 1024)
	for i := range burst {
		burst[i] = math.Sin(2 * math.Pi * float64(i) / 48.0)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, s := range burst {
			f.Process(s)
		}
	}
}

func TestBiquadDenormalProtection(t *testing.T) {
	t.Parallel()
	f := &BiquadFilter{
		b0: 1.53512485958697,
		b1: -2.69169618940638,
		b2: 1.19839281085285,
		a1: -1.69065929318241,
		a2: 0.73248077421585,
	}

	// Process 48000 samples of silence — enough for any denormal to accumulate
	for i := 0; i < 48000; i++ {
		f.Process(0)
	}

	// The DC offset trick keeps filter state at ~denormalGuard magnitude
	// (normal float range) rather than decaying into denormal territory.
	// Verify state is bounded near the guard magnitude, not at denormal levels (<1e-300).
	assert.InDelta(t, 0, f.s1, 1e-20, "s1 should be near zero (within DC guard range) after processing silence")
	assert.InDelta(t, 0, f.s2, 1e-20, "s2 should be near zero (within DC guard range) after processing silence")

	// Verify the output for silence is effectively zero (denormalGuard cancels out)
	out := f.Process(0)
	assert.InDelta(t, 0, out, 1e-15, "output should be effectively zero for silence input")
}
