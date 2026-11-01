package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// measureTruePeakDBTP reconstructs the true-peak of a processed buffer
// using the same ITU-R BS.1770-4 Annex 2 4× polyphase FIR used by the limiter.
// The first 12 input frames are used to pre-warm the FIR history so the
// measurement reflects steady-state reconstruction rather than the initial
// step-response transient caused by zero-padded history.
// Returns dBTP (peak relative to 0 dBFS); -Inf if the buffer is shorter
// than the warm-up window.
func measureTruePeakDBTP(samples []float32, channels int) float64 {
	const warmup = 12
	if len(samples) < channels*(warmup+1) {
		return math.Inf(-1)
	}
	coeffs := truePeakFIRCoefficients()
	hist := make([][12]float64, channels)
	push := func(i int) {
		for ch := 0; ch < channels; ch++ {
			for k := 11; k > 0; k-- {
				hist[ch][k] = hist[ch][k-1]
			}
			hist[ch][0] = float64(samples[i+ch])
		}
	}
	i := 0
	for f := 0; f < warmup && i+channels <= len(samples); f++ {
		push(i)
		i += channels
	}
	var maxTP float64
	for ; i+channels <= len(samples); i += channels {
		push(i)
		for ch := 0; ch < channels; ch++ {
			for phase := 0; phase < 4; phase++ {
				var acc float64
				for k := 0; k < 12; k++ {
					acc += coeffs[phase][k] * hist[ch][k]
				}
				if a := math.Abs(acc); a > maxTP {
					maxTP = a
				}
			}
		}
	}
	if maxTP <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(maxTP)
}

// TestLimiter_TruePeakCatchesISP reproduces the canonical ITU test case:
// a 12 kHz sine at 48 kHz sampled with phase π/4 has sample values at ±0.707
// but reconstructs (band-limited) to a ±1.0 peak — an inter-sample peak of
// +3.01 dBTP above the sample-peak.
//
// With a -1 dBTP ceiling, the pre-fix sample-peak limiter takes no action
// (sample-peak 0.707 is below the -1 dBFS threshold), so the reconstructed
// true-peak exceeds 0 dBTP — a hard DAC clip.  Post-fix, the limiter must
// pull the true-peak down to -1 dBTP.
func TestLimiter_TruePeakCatchesISP(t *testing.T) {
	t.Parallel()
	const (
		sr   = 48000.0
		freq = 12000.0 // exactly Fs/4 so samples land at cos(π/4 + k·π/2) = ±0.707
		amp  = 1.0     // true amplitude — sample-peak comes out ≈ 0.707
	)
	lim := NewLimiter(int(sr), 1)
	lim.SetCeiling(-1.0)
	lim.SetThreshold(-1.0)

	const n = 4800 // 100 ms
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		samples[i] = float32(amp * math.Cos(2*math.Pi*freq*float64(i)/sr+math.Pi/4))
	}
	lim.Process(samples)

	// The output true-peak, measured by an independent 4× oversampling detector,
	// must respect the -1 dBTP ceiling.  A sample-peak limiter with this input
	// leaves the output at ~0 dBTP reconstructed; a true-peak limiter pulls it
	// back to -1 dBTP.
	outTP := measureTruePeakDBTP(samples[n/2:], 1)
	require.LessOrEqual(t, outTP, -1.0+0.1,
		"post-fix output true-peak %.3f dBTP must not exceed -1 dBTP ceiling", outTP)
	require.GreaterOrEqual(t, outTP, -1.0-0.5,
		"output true-peak %.3f dBTP should sit near -1 dBTP (not over-compressed)", outTP)

	// Sanity: the limiter must report a positive gain reduction.
	require.Greater(t, lim.GainReduction(), 0.0,
		"gain reduction must be active when true-peak exceeds ceiling")
}

// TestLimiter_TransientOnsetOvershoot characterizes a KNOWN limitation of the
// zero-lookahead feed-forward design: on a fast transient onset (silence jumping
// straight to a full-amplitude inter-sample-peak tone) the envelope takes several
// samples to rise, so the first samples of the transient are under-attenuated and
// the reconstructed true-peak briefly exceeds the configured ceiling.
//
// This is the transient counterpart to TestLimiter_TruePeakCatchesISP, which only
// measures the SETTLED back half of a steady tone. The fix for a truly brickwall
// transient ceiling is a short lookahead delay (see Limiter doc comment); until
// then this test pins the magnitude of the overshoot so a regression that made it
// dramatically worse would fail, while documenting that the ceiling holds in
// steady state.
func TestLimiter_TransientOnsetOvershoot(t *testing.T) {
	t.Parallel()
	const (
		sr      = 48000.0
		freq    = 12000.0 // Fs/4, phase π/4 → reconstructs ~+3 dBTP above sample-peak
		ceiling = -1.0
	)
	lim := NewLimiter(int(sr), 1)
	lim.SetCeiling(ceiling)
	lim.SetThreshold(ceiling)

	const n = 4800 // 100 ms
	samples := make([]float32, n)
	// First half silence, then an abrupt onset of the ISP tone — worst case for
	// a limiter with no lookahead.
	for i := n / 2; i < n; i++ {
		samples[i] = float32(math.Cos(2*math.Pi*freq*float64(i)/sr + math.Pi/4))
	}
	lim.Process(samples)

	// Steady state (settled, far from the onset) MUST respect the ceiling: that
	// is the guarantee the limiter actually makes.
	settledTP := measureTruePeakDBTP(samples[n-600:], 1)
	require.LessOrEqual(t, settledTP, ceiling+0.1,
		"settled true-peak %.3f dBTP must respect the %.1f dBTP ceiling", settledTP, ceiling)

	// Onset region: the true-peak overshoots the ceiling because the envelope
	// has not risen yet. This documents the limitation. We bound the overshoot
	// so the test still fails if it ever blows past the sample-peak headroom
	// (~+3 dBTP of inter-sample peak), which would indicate a real regression.
	onsetTP := measureTruePeakDBTP(samples[n/2:n/2+200], 1)
	require.Greater(t, onsetTP, settledTP+0.3,
		"expected the transient onset (%.3f dBTP) to overshoot the settled value "+
			"(%.3f dBTP) given the zero-lookahead design", onsetTP, settledTP)
	require.LessOrEqual(t, onsetTP, ceiling+3.1,
		"transient overshoot %.3f dBTP should stay within the inter-sample-peak "+
			"headroom above the %.1f dBTP ceiling; a larger value is a regression",
		onsetTP, ceiling)
}

// TestLimiter_TruePeakOnCorrelatedStereo feeds an identical 12 kHz ISP signal
// on L and R at -0.5 dBFS sample-peak.  The true-peak detector must report
// the reconstructed peak (which for Fs/4 phase-π/4 sits ~3 dB above the
// sample-peak), and gain reduction must engage — the sample-peak detector
// would see only -3.5 dBFS and stay silent under a -1 dBFS threshold.
func TestLimiter_TruePeakOnCorrelatedStereo(t *testing.T) {
	t.Parallel()
	const (
		sr   = 48000.0
		freq = 12000.0
		amp  = 0.5 // yields sample-peak 0.354 (~-9 dBFS), true-peak 0.5 (~-6 dBTP)
	)
	lim := NewLimiter(int(sr), 2)
	lim.SetCeiling(-10.0)
	lim.SetThreshold(-10.0) // below sample-peak (-9 dBFS) — both detectors should fire,
	// but the TP detector fires earlier and harder.

	const n = 9600 // 100 ms stereo
	samples := make([]float32, n)
	for i := 0; i+2 <= n; i += 2 {
		v := float32(amp * math.Cos(2*math.Pi*freq*float64(i/2)/sr+math.Pi/4))
		samples[i] = v
		samples[i+1] = v
	}
	lim.Process(samples)

	tp := lim.TruePeakDBTP()
	// Sample-peak of the input was 0.354 (-9 dBFS); true-peak reconstructs to
	// 0.5 (-6 dBTP).  The reported TruePeakDBTP must reflect the reconstructed
	// peak, not the sample-peak — that is, it must sit well above -9 dBFS.
	require.Greater(t, tp, -7.5,
		"TruePeakDBTP %.3f should report reconstructed peak (~-6 dBTP), not sample-peak (-9 dBFS)", tp)

	// Output must respect the ceiling via true-peak reconstruction.
	outTP := measureTruePeakDBTP(samples[n/2:], 2)
	require.LessOrEqual(t, outTP, -10.0+0.2,
		"post-fix stereo output true-peak %.3f dBTP must not exceed -10 dBTP ceiling", outTP)
}

// TestLimiter_TruePeakApproxSamplePeakOnBandlimited verifies that on
// well-behaved bandlimited content (a clean 1 kHz sine, densely sampled at
// 48 kHz), the true-peak reading tracks the sample-peak within a small
// margin.  This pins the "no regression" half of the AC: broadcast-quality
// program material with mid-band energy must not be over-limited.
func TestLimiter_TruePeakApproxSamplePeakOnBandlimited(t *testing.T) {
	t.Parallel()
	const (
		sr   = 48000.0
		freq = 1000.0
		amp  = 0.5 // -6 dBFS, well below any threshold
	)
	lim := NewLimiter(int(sr), 1)

	const n = 9600 // 200 ms
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		samples[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/sr))
	}
	lim.Process(samples)

	tp := lim.TruePeakDBTP()
	spDB := 20 * math.Log10(float64(amp))
	require.InDelta(t, spDB, tp, 0.5,
		"on a clean 1 kHz sine, TruePeakDBTP %.3f should sit within 0.5 dB of sample-peak %.3f", tp, spDB)

	// No gain reduction should fire — sample-peak is -6 dBFS, true-peak is
	// essentially the same, default threshold is -1 dBFS.
	require.InDelta(t, 0.0, lim.GainReduction(), 0.01,
		"no GR on -6 dBFS sine with -1 dBFS threshold")
}

// TestLimiter_TruePeakReportedOnLastBlock verifies that TruePeakDBTP() reflects
// the true-peak measured over the most recent Process() call, not a cumulative
// maximum across all calls.  Reset() clears the FIR history so the quiet block
// is not contaminated by residual loud samples in the 12-tap window.
func TestLimiter_TruePeakReportedOnLastBlock(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)

	loud := make([]float32, 1024)
	for i := range loud {
		loud[i] = 0.9
	}
	lim.Process(loud)
	tpLoud := lim.TruePeakDBTP()
	require.Greater(t, tpLoud, -2.0, "loud block should report TP near 0 dBFS, got %.3f", tpLoud)

	lim.Reset()

	quiet := make([]float32, 1024)
	for i := range quiet {
		quiet[i] = 0.1
	}
	lim.Process(quiet)
	tpQuiet := lim.TruePeakDBTP()
	require.Less(t, tpQuiet, tpLoud-10,
		"quiet block should report a much lower TP (got %.3f, loud was %.3f)", tpQuiet, tpLoud)
}

// TestLimiter_TruePeakReset clears the TP readback alongside GR on Reset().
func TestLimiter_TruePeakReset(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)

	loud := make([]float32, 1024)
	for i := range loud {
		loud[i] = 0.9
	}
	lim.Process(loud)
	require.Greater(t, lim.TruePeakDBTP(), -6.0)

	lim.Reset()
	require.Less(t, lim.TruePeakDBTP(), -100.0,
		"after Reset, TP should be reported as -inf or far-negative")
}

// TestLimiter_NoRegression_SamplePeakCase pins the no-regression half of the
// bug fix: on non-pathological program material (a 1 kHz sine densely sampled
// at 48 kHz), true-peak ≈ sample-peak, and the limiter's brickwall behavior
// with threshold = ceiling = -1 dB matches the pre-fix contract.  The sister
// TestLimiter_SineAt0dBFSWithMinusOneCeiling in limiter_single_gain_test.go
// covers the same invariant from the sample-peak angle.
func TestLimiter_NoRegression_SamplePeakCase(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)
	lim.SetThreshold(-1.0)
	lim.SetCeiling(-1.0)

	const (
		sr   = 48000.0
		freq = 1000.0
	)
	samples := make([]float32, 4800)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / sr))
	}
	lim.Process(samples)

	// Output sample-peak must respect the -1 dBFS target (≈ -1 dB envelope).
	var peak float32
	for i := 2400; i < len(samples); i++ {
		if a := float32(math.Abs(float64(samples[i]))); a > peak {
			peak = a
		}
	}
	peakDB := 20 * math.Log10(float64(peak))
	require.InDelta(t, -1.0, peakDB, 0.1,
		"1 kHz sine into -1 dB threshold: sample-peak %.3f dBFS should sit at -1 dBFS", peakDB)

	// And steady-state true-peak ≈ sample-peak for a clean 1 kHz sine.
	outTP := measureTruePeakDBTP(samples[2400:], 1)
	require.InDelta(t, peakDB, outTP, 0.2,
		"1 kHz sine: output TP %.3f should track sample-peak %.3f within 0.2 dB", outTP, peakDB)
}

// TestMixer_LimiterSettings_JSONSafeAtColdStart pins that LimiterSettings
// returns a JSON-encodable TruePeak on a fresh mixer (before any block has
// been processed).  encoding/json rejects ±Inf and NaN, so the idle sentinel
// must be a finite floor value.
func TestMixer_LimiterSettings_JSONSafeAtColdStart(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	_, _, _, tp := m.LimiterSettings()
	require.False(t, math.IsInf(tp, 0), "idle TruePeak must be finite (got %v)", tp)
	require.False(t, math.IsNaN(tp), "idle TruePeak must not be NaN")
	require.Less(t, tp, -100.0, "idle TruePeak should be a deep floor (got %v)", tp)
}

// TestMixer_LimiterSettings_IncludesTruePeak exercises the 4-tuple return
// contract: the mixer's LimiterSettings must surface the true-peak alongside
// threshold/ceiling/GR.
func TestMixer_LimiterSettings_IncludesTruePeak(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	// Drive the internal limiter directly so we don't depend on the mixer tick.
	loud := make([]float32, 1024)
	for i := range loud {
		loud[i] = 0.9
	}
	m.limiter.Process(loud)

	threshold, ceiling, gr, tp := m.LimiterSettings()
	require.InDelta(t, -1.0, threshold, 0.01)
	require.InDelta(t, 0.0, ceiling, 0.01)
	require.Greater(t, gr, 0.0, "GR should fire at 0.9 DC into -1 dBFS threshold")
	require.Greater(t, tp, -2.0, "TP should report near 0 dBFS for 0.9 DC input")
}
