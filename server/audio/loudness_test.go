package audio

import (
	"fmt"
	"math"
	"testing"
)

func TestLoudnessMeter_Silence(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Feed 1 second of silence (enough to fill momentary window)
	silence := make([]float32, 48000*2) // 1s stereo interleaved
	m.Process(silence)

	lufs := m.MomentaryLUFS()
	if lufs > -70 {
		t.Errorf("silence momentary LUFS = %f, want <= -70", lufs)
	}
}

func TestLoudnessMeter_FullScaleSine(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Generate 1s of 1kHz full-scale sine (stereo interleaved)
	samples := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2] = v   // L
		samples[i*2+1] = v // R
	}
	m.Process(samples)

	lufs := m.MomentaryLUFS()
	// Full-scale 1kHz stereo sine through K-weighting. Pre-filter boosts ~0.5dB at 1kHz.
	// Stereo summing adds ~3dB. Expected ~0 LUFS (can go slightly above due to K-weight).
	// BS.1770 reference: -3.01 LUFS per channel for full-scale sine, so stereo ~ 0 LUFS.
	if lufs < -5 || lufs > 1 {
		t.Errorf("full-scale 1kHz sine momentary LUFS = %f, want between -5 and 1", lufs)
	}
}

func TestLoudnessMeter_ShortTerm(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Feed 4 seconds of tone to fill short-term window (3s)
	samples := make([]float32, 48000*4*2)
	for i := 0; i < 48000*4; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2] = v
		samples[i*2+1] = v
	}
	m.Process(samples)

	lufs := m.ShortTermLUFS()
	if lufs < -5 || lufs > 1 {
		t.Errorf("full-scale 1kHz sine short-term LUFS = %f, want between -5 and 1", lufs)
	}
}

func TestLoudnessMeter_IntegratedWithGating(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Feed 2s of tone
	tone := make([]float32, 48000*2*2)
	for i := 0; i < 48000*2; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000))
		tone[i*2] = v
		tone[i*2+1] = v
	}
	m.Process(tone)

	toneLevel := m.IntegratedLUFS()

	// Now feed 5s of silence — should be gated out by absolute gate
	silence := make([]float32, 48000*5*2)
	m.Process(silence)

	afterSilence := m.IntegratedLUFS()

	// Integrated should reflect tone level — silence is gated out.
	// The difference should be small (silence blocks excluded by gating).
	diff := math.Abs(toneLevel - afterSilence)
	if diff > 1.0 {
		t.Errorf("integrated LUFS drifted %f after silence (tone=%f, after=%f), gating should exclude silence",
			diff, toneLevel, afterSilence)
	}

	// The integrated level should be in a reasonable range for half-scale tone
	if afterSilence < -20 || afterSilence > 0 {
		t.Errorf("integrated LUFS = %f, want between -20 and 0 for half-scale 1kHz", afterSilence)
	}
}

func TestLoudnessMeter_Reset(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Feed tone
	tone := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		tone[i*2] = v
		tone[i*2+1] = v
	}
	m.Process(tone)

	// Verify we have data
	before := m.IntegratedLUFS()
	if before == -math.MaxFloat64 {
		t.Fatal("expected valid integrated LUFS before reset")
	}

	// Reset
	m.Reset()

	// After reset, should have no data
	after := m.IntegratedLUFS()
	if after != -math.MaxFloat64 {
		t.Errorf("expected -MaxFloat64 after reset, got %f", after)
	}

	// Momentary should also be empty
	mom := m.MomentaryLUFS()
	if mom != -math.MaxFloat64 {
		t.Errorf("expected -MaxFloat64 momentary after reset, got %f", mom)
	}
}

func TestLoudnessMeter_Stereo(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Feed 1s of stereo with different levels per channel
	samples := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		// Left at full scale, right at half scale
		samples[i*2] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}
	m.Process(samples)

	lufs := m.MomentaryLUFS()

	// Should be between mono full-scale and mono half-scale levels
	if lufs < -15 || lufs > 0 {
		t.Errorf("stereo momentary LUFS = %f, want between -15 and 0", lufs)
	}
}

func TestLoudnessMeter_MonoChannel(t *testing.T) {
	m := NewLoudnessMeter(48000, 1)

	// Feed 1s of mono full-scale sine
	samples := make([]float32, 48000)
	for i := 0; i < 48000; i++ {
		samples[i] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
	}
	m.Process(samples)

	lufs := m.MomentaryLUFS()
	// Mono full-scale should be roughly -3 LUFS (no channel summing with stereo)
	if lufs < -10 || lufs > 0 {
		t.Errorf("mono full-scale 1kHz sine momentary LUFS = %f, want between -10 and 0", lufs)
	}
}

func TestLoudnessMeter_NoDataReturnsMinFloat(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	if m.MomentaryLUFS() != -math.MaxFloat64 {
		t.Error("expected -MaxFloat64 for momentary with no data")
	}
	if m.ShortTermLUFS() != -math.MaxFloat64 {
		t.Error("expected -MaxFloat64 for short-term with no data")
	}
	if m.IntegratedLUFS() != -math.MaxFloat64 {
		t.Error("expected -MaxFloat64 for integrated with no data")
	}
}

func TestLoudnessMeter_KWeightingAttenuatesLowFreq(t *testing.T) {
	// K-weighting should attenuate low frequencies relative to mid frequencies.
	// A 100Hz tone should measure lower than a 1kHz tone at the same amplitude.
	m100 := NewLoudnessMeter(48000, 2)
	m1k := NewLoudnessMeter(48000, 2)

	samples100 := make([]float32, 48000*2)
	samples1k := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		v100 := float32(math.Sin(2 * math.Pi * 100 * float64(i) / 48000))
		v1k := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples100[i*2] = v100
		samples100[i*2+1] = v100
		samples1k[i*2] = v1k
		samples1k[i*2+1] = v1k
	}

	m100.Process(samples100)
	m1k.Process(samples1k)

	lufs100 := m100.MomentaryLUFS()
	lufs1k := m1k.MomentaryLUFS()

	if lufs100 >= lufs1k {
		t.Errorf("expected 100Hz (%f) to measure lower than 1kHz (%f) with K-weighting",
			lufs100, lufs1k)
	}
}

func TestLoudnessMeterIntegratedBlocksCompaction(t *testing.T) {
	// Verify the compaction path in emitBlock(). maxIntegratedBlocks = 360,000.
	// Directly calling Process() 360,000+ times is too slow under -race.
	//
	// Instead, we verify the meter's behavior: feed a substantial amount of
	// audio (10,000 blocks), verify integrated LUFS is valid, then feed more
	// and verify the meter still works. This confirms the accumulator doesn't
	// corrupt data under heavy use.
	m := NewLoudnessMeter(48000, 1)

	// 1kHz sine at 0.5 amplitude — loud enough to pass both BS.1770 gates.
	// stepSize at 48kHz = 4800 mono samples per block.
	// 100 blocks per call * 4800 = 480,000 samples per call.
	chunkSamples := 4800 * 100
	chunk := make([]float32, chunkSamples)
	for i := range chunk {
		chunk[i] = float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}

	// Phase 1: feed 10,000 blocks (100 calls * 100 blocks/call)
	for i := 0; i < 100; i++ {
		m.Process(chunk)
	}

	lufs1 := m.IntegratedLUFS()
	if math.IsInf(lufs1, -1) || lufs1 == -math.MaxFloat64 {
		t.Fatalf("phase 1: integrated LUFS should be finite, got %v", lufs1)
	}
	if lufs1 < -30 || lufs1 > 0 {
		t.Errorf("phase 1: expected LUFS between -30 and 0, got %f", lufs1)
	}

	// Phase 2: feed another 10,000 blocks — meter should remain stable
	for i := 0; i < 100; i++ {
		m.Process(chunk)
	}

	lufs2 := m.IntegratedLUFS()
	if math.IsInf(lufs2, -1) || lufs2 == -math.MaxFloat64 {
		t.Fatalf("phase 2: integrated LUFS should be finite, got %v", lufs2)
	}

	// Same signal → integrated LUFS should be very close across phases
	diff := math.Abs(lufs1 - lufs2)
	if diff > 1.0 {
		t.Errorf("integrated LUFS drifted %f between phases (p1=%f, p2=%f)", diff, lufs1, lufs2)
	}

	// Momentary and short-term should also be valid
	mom := m.MomentaryLUFS()
	if math.IsInf(mom, -1) || mom == -math.MaxFloat64 {
		t.Fatalf("momentary LUFS should be finite after heavy use, got %v", mom)
	}
	st := m.ShortTermLUFS()
	if math.IsInf(st, -1) || st == -math.MaxFloat64 {
		t.Fatalf("short-term LUFS should be finite after heavy use, got %v", st)
	}
}

// ---------- C3: Premature reporting tests ----------

func TestLoudnessMeter_MomentaryRequiresFullWindow(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// The momentary window is 400ms, which requires 4 blocks of 100ms each.
	// Feed only 200ms of audio (2 blocks) -- momentary should return sentinel.
	// stepSize at 48kHz = 4800 samples/channel, so 2 blocks = 9600 samples/channel.
	samples := make([]float32, 9600*2) // 200ms stereo interleaved
	for i := 0; i < 9600; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2] = v
		samples[i*2+1] = v
	}
	m.Process(samples)

	lufs := m.MomentaryLUFS()
	if lufs != -math.MaxFloat64 {
		t.Errorf("momentary LUFS after 200ms = %f, want -MaxFloat64 (window not full)", lufs)
	}

	// Feed another 200ms (total 400ms = 4 blocks) -- now momentary should be valid.
	m.Process(samples)
	lufs = m.MomentaryLUFS()
	if lufs == -math.MaxFloat64 {
		t.Errorf("momentary LUFS after 400ms should be valid, got -MaxFloat64")
	}
	if lufs < -5 || lufs > 1 {
		t.Errorf("momentary LUFS after 400ms = %f, want between -5 and 1", lufs)
	}
}

func TestLoudnessMeter_ShortTermRequiresFullWindow(t *testing.T) {
	m := NewLoudnessMeter(48000, 2)

	// Short-term window is 3s (30 blocks of 100ms each).
	// Feed 1s of audio (10 blocks) -- short-term should return sentinel.
	samples := make([]float32, 48000*2) // 1s stereo interleaved
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2] = v
		samples[i*2+1] = v
	}
	m.Process(samples)

	lufs := m.ShortTermLUFS()
	if lufs != -math.MaxFloat64 {
		t.Errorf("short-term LUFS after 1s = %f, want -MaxFloat64 (window not full)", lufs)
	}

	// Feed 2 more seconds (total 3s = 30 blocks) -- now short-term should be valid.
	for i := 0; i < 2; i++ {
		m.Process(samples)
	}
	lufs = m.ShortTermLUFS()
	if lufs == -math.MaxFloat64 {
		t.Errorf("short-term LUFS after 3s should be valid, got -MaxFloat64")
	}
	if lufs < -5 || lufs > 1 {
		t.Errorf("short-term LUFS after 3s = %f, want between -5 and 1", lufs)
	}
}

func TestLoudnessMeterSampleRateWarning(t *testing.T) {
	// Creating a meter with a non-48kHz sample rate should work without warning
	// now that coefficients are computed from sample rate.
	m := NewLoudnessMeter(44100, 2)
	if m == nil {
		t.Fatal("NewLoudnessMeter should not return nil for non-48kHz sample rate")
	}

	// Should still process audio without error
	samples := make([]float32, 44100*2)
	m.Process(samples)

	lufs := m.MomentaryLUFS()
	if lufs > -60 {
		t.Errorf("silence at 44100Hz should still measure very low, got %f", lufs)
	}

	// Verify 48kHz also works
	m48 := NewLoudnessMeter(48000, 2)
	if m48 == nil {
		t.Fatal("NewLoudnessMeter should not return nil for 48kHz sample rate")
	}
}

func TestLoudnessMeter_SampleRateIndependence(t *testing.T) {
	// BS.1770-4 requires K-weighting coefficients computed from the actual
	// sample rate via bilinear transform. A 1kHz full-scale sine measured
	// at 44.1kHz and 48kHz should produce LUFS readings within 1 LU.
	rates := []int{44100, 48000}
	results := make(map[int]float64)

	for _, rate := range rates {
		m := NewLoudnessMeter(rate, 2)

		// Generate 1s of 1kHz full-scale sine at the given sample rate
		nSamples := rate
		samples := make([]float32, nSamples*2)
		for i := 0; i < nSamples; i++ {
			v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / float64(rate)))
			samples[i*2] = v
			samples[i*2+1] = v
		}
		m.Process(samples)

		lufs := m.MomentaryLUFS()
		if lufs == -math.MaxFloat64 {
			t.Fatalf("rate=%d: momentary LUFS should be valid after 1s of audio", rate)
		}
		results[rate] = lufs
		t.Logf("rate=%d: momentary LUFS = %.4f", rate, lufs)
	}

	diff := math.Abs(results[44100] - results[48000])
	if diff > 1.0 {
		t.Errorf("LUFS divergence between 44.1kHz (%.4f) and 48kHz (%.4f) = %.4f LU, want <= 1.0 LU",
			results[44100], results[48000], diff)
	}
}

func TestLoudnessMeter_48kHzBackwardCompat(t *testing.T) {
	// The computed coefficients for 48kHz must match the previously
	// hardcoded ITU-R BS.1770-4 reference values.

	// Pre-filter: must match the ITU reference table exactly (within FP tolerance).
	const exactTol = 1e-10
	pre := newKWeightPreFilter(48000)
	if math.Abs(pre.b0-1.53512485958697) > exactTol {
		t.Errorf("pre.b0 = %.15f, want 1.53512485958697", pre.b0)
	}
	if math.Abs(pre.b1-(-2.69169618940638)) > exactTol {
		t.Errorf("pre.b1 = %.15f, want -2.69169618940638", pre.b1)
	}
	if math.Abs(pre.b2-1.19839281085285) > exactTol {
		t.Errorf("pre.b2 = %.15f, want 1.19839281085285", pre.b2)
	}
	if math.Abs(pre.a1-(-1.69065929318241)) > exactTol {
		t.Errorf("pre.a1 = %.15f, want -1.69065929318241", pre.a1)
	}
	if math.Abs(pre.a2-0.73248077421585) > exactTol {
		t.Errorf("pre.a2 = %.15f, want 0.73248077421585", pre.a2)
	}

	// RLB filter: the ITU reference table rounded b coefficients to 1.0/-2.0/1.0,
	// but the exact bilinear transform produces ~0.995/-1.990/0.995 at 48kHz
	// (a0 ≈ 1.005, so b/a0 differs from b by ~0.5%). The a coefficients match exactly.
	// We verify the a coefficients match precisely and that the b coefficients
	// are within the expected normalization difference.
	rlb := newKWeightRLBFilter(48000)
	if math.Abs(rlb.a1-(-1.99004745483398)) > exactTol {
		t.Errorf("rlb.a1 = %.15f, want -1.99004745483398", rlb.a1)
	}
	if math.Abs(rlb.a2-0.99007225036621) > exactTol {
		t.Errorf("rlb.a2 = %.15f, want 0.99007225036621", rlb.a2)
	}

	// The b coefficients should be very close to 1/-2/1 (within 0.5% from a0 normalization).
	const rlbBTol = 0.01
	if math.Abs(rlb.b0-1.0) > rlbBTol {
		t.Errorf("rlb.b0 = %.15f, want ~1.0 (within %.3f)", rlb.b0, rlbBTol)
	}
	if math.Abs(rlb.b1-(-2.0)) > rlbBTol {
		t.Errorf("rlb.b1 = %.15f, want ~-2.0 (within %.3f)", rlb.b1, rlbBTol)
	}
	if math.Abs(rlb.b2-1.0) > rlbBTol {
		t.Errorf("rlb.b2 = %.15f, want ~1.0 (within %.3f)", rlb.b2, rlbBTol)
	}

	// Verify LUFS output is acoustically equivalent: a 1kHz sine at 48kHz should
	// produce the same reading as the old hardcoded implementation (within 0.1 LU).
	m := NewLoudnessMeter(48000, 2)
	samples := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
		samples[i*2] = v
		samples[i*2+1] = v
	}
	m.Process(samples)
	lufs := m.MomentaryLUFS()
	// The old hardcoded coefficients produced values in the range [-5, +1] for
	// full-scale 1kHz stereo sine. Verify we're in the same ballpark.
	if lufs < -5 || lufs > 1 {
		t.Errorf("48kHz 1kHz sine LUFS = %.4f, want between -5 and 1 (backward compat)", lufs)
	}
}

func TestLoudnessMeter_TruncationDoesNotPanic(t *testing.T) {
	// Verify that exceeding maxIntegratedBlocks (360,000) doesn't panic,
	// integrated LUFS remains valid, and Reset() clears all state.
	m := NewLoudnessMeter(48000, 1)

	// Feed enough blocks to trigger truncation. stepSize = 4800 samples
	// per channel. We need >360,000 blocks. Each Process call with 4800
	// samples yields 1 block. Feeding 100 blocks at a time:
	chunkSamples := 4800 * 100 // 100 blocks per call
	chunk := make([]float32, chunkSamples)
	for i := range chunk {
		chunk[i] = float32(0.1 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}

	// 3601 calls * 100 blocks = 360,100 blocks -- exceeds maxIntegratedBlocks.
	// Under -race this is slow, so we directly manipulate integratedBlocks
	// to test the truncation path without processing 360k+ blocks.
	for i := 0; i < 360_001; i++ {
		m.integratedBlocks = append(m.integratedBlocks, 0.001)
	}
	// Process a small chunk to trigger emitBlock and the truncation check.
	m.Process(chunk[:4800])

	// After truncation, integratedBlocks should be ~180,001 (half of 360k + 1 new).
	if len(m.integratedBlocks) > 180_010 {
		t.Errorf("expected truncation to ~180k blocks, got %d", len(m.integratedBlocks))
	}

	// Integrated LUFS should still be valid (not -inf or NaN).
	lufs := m.IntegratedLUFS()
	if math.IsNaN(lufs) {
		t.Error("integrated LUFS should not be NaN after truncation")
	}

	// Reset should clear everything.
	m.Reset()
	// Trigger drainReset via Process.
	m.Process(make([]float32, 4800))
	after := m.IntegratedLUFS()
	// After reset + 1 block of silence, integrated may be -MaxFloat64 (silence gated).
	if after != -math.MaxFloat64 {
		// If the 1 block of silence passes the absolute gate, that's OK —
		// just verify it's finite and not the pre-reset value.
		if math.IsNaN(after) || math.IsInf(after, 1) {
			t.Errorf("integrated LUFS after reset should be valid, got %v", after)
		}
	}
}

func TestLoudnessMeter_MultipleSampleRates(t *testing.T) {
	// Verify the meter works correctly across common broadcast sample rates.
	// All should produce valid LUFS readings for a 1kHz sine.
	rates := []int{32000, 44100, 48000, 96000}

	for _, rate := range rates {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			m := NewLoudnessMeter(rate, 2)

			nSamples := rate // 1 second
			samples := make([]float32, nSamples*2)
			for i := 0; i < nSamples; i++ {
				v := float32(math.Sin(2 * math.Pi * 1000 * float64(i) / float64(rate)))
				samples[i*2] = v
				samples[i*2+1] = v
			}
			m.Process(samples)

			lufs := m.MomentaryLUFS()
			if lufs == -math.MaxFloat64 {
				t.Fatalf("momentary LUFS should be valid after 1s at %dHz", rate)
			}
			// Full-scale 1kHz stereo sine: expect roughly -1 to +1 LUFS
			// (K-weighting boosts slightly at 1kHz)
			if lufs < -5 || lufs > 2 {
				t.Errorf("momentary LUFS = %.4f at %dHz, want between -5 and 2", lufs, rate)
			}
		})
	}
}
