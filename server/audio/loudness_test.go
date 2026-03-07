package audio

import (
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
		samples[i*2+1] = float32(0.5 * math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
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
