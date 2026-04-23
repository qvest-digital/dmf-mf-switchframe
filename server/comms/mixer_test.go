package comms

import (
	"math"
	"testing"
)

func TestMixerSumExcludeSelf(t *testing.T) {
	m := newMixer()

	inputs := map[string][]int16{
		"opA": makeConstFrame(1000),
		"opB": makeConstFrame(2000),
		"opC": makeConstFrame(3000),
	}

	// opA should hear opB + opC = 2000 + 3000 = 5000
	outA := m.mixFor("opA", inputs)
	if len(outA) != FrameSize {
		t.Fatalf("expected %d samples, got %d", FrameSize, len(outA))
	}
	for i, v := range outA {
		if v != 5000 {
			t.Fatalf("outA[%d] = %d, want 5000", i, v)
		}
	}

	// opB should hear opA + opC = 1000 + 3000 = 4000
	outB := m.mixFor("opB", inputs)
	for i, v := range outB {
		if v != 4000 {
			t.Fatalf("outB[%d] = %d, want 4000", i, v)
		}
	}
}

func TestMixerSingleParticipant(t *testing.T) {
	m := newMixer()

	inputs := map[string][]int16{
		"solo": makeConstFrame(5000),
	}

	// Solo participant should get silence (no one else to hear)
	out := m.mixFor("solo", inputs)
	if len(out) != FrameSize {
		t.Fatalf("expected %d samples, got %d", FrameSize, len(out))
	}
	for i, v := range out {
		if v != 0 {
			t.Fatalf("out[%d] = %d, want 0 (silence)", i, v)
		}
	}
}

func TestMixerEmptyInputs(t *testing.T) {
	m := newMixer()

	inputs := map[string][]int16{}

	out := m.mixFor("anyone", inputs)
	if len(out) != FrameSize {
		t.Fatalf("expected %d samples, got %d", FrameSize, len(out))
	}
	for i, v := range out {
		if v != 0 {
			t.Fatalf("out[%d] = %d, want 0 (silence)", i, v)
		}
	}
}

// TestCommsMixer_SoftLimitsSimultaneousSpeakers reproduces the 3+ speaker
// hard-clamp distortion: 4 coherent 500 Hz sines at 0.6 × int16max each sum to
// 2.4× full-scale. The limiter must hold every output sample at or below
// -1 dBFS (29204) and keep the frame RMS within 1 dB of the natural sum's RMS
// after gain reduction.
func TestCommsMixer_SoftLimitsSimultaneousSpeakers(t *testing.T) {
	m := newMixer()

	const (
		amp  = 19660 // 0.6 × 32767
		freq = 500.0
	)
	sines := make(map[string][]int16, 4)
	for _, id := range []string{"A", "B", "C", "D"} {
		buf := make([]int16, FrameSize)
		for i := 0; i < FrameSize; i++ {
			buf[i] = int16(float64(amp) * math.Sin(2*math.Pi*freq*float64(i)/float64(SampleRate)))
		}
		sines[id] = buf
	}

	// Run several frames so the envelope settles to the 2.4× FS steady state.
	var out []int16
	for k := 0; k < 5; k++ {
		out = m.mixFor("listener", sines)
	}

	// Every sample must sit at or below -1 dBFS.
	const target = 29204
	for i, v := range out {
		a := int32(v)
		if a < 0 {
			a = -a
		}
		if a > target {
			t.Fatalf("out[%d] = %d exceeds ±%d (-1 dBFS)", i, v, target)
		}
	}

	// RMS sanity: 4 coherent 500 Hz sines at amp 19660 sum to peak 4×19660 = 78640.
	// Natural-sum RMS = 78640/√2 ≈ 55610. Gain reduction = 29204/78640 ≈ 0.371, so
	// expected post-limit RMS ≈ 20651. Allow ±1 dB.
	var sumSq float64
	for _, v := range out {
		sumSq += float64(v) * float64(v)
	}
	rms := math.Sqrt(sumSq / float64(FrameSize))
	expected := float64(target) / math.Sqrt2
	lowerBound := expected * math.Pow(10, -1.0/20)
	upperBound := expected * math.Pow(10, 1.0/20)
	if rms < lowerBound || rms > upperBound {
		t.Fatalf("frame RMS = %.1f, expected within [%.1f, %.1f] (±1 dB of %.1f)",
			rms, lowerBound, upperBound, expected)
	}
}

// TestCommsMixer_DoesNotLimitSingleSpeaker asserts that a single active source
// well below the -1 dBFS threshold passes through unchanged — the limiter must
// only act when the summed peak exceeds the threshold.
func TestCommsMixer_DoesNotLimitSingleSpeaker(t *testing.T) {
	m := newMixer()

	// -10 dBFS sine: amplitude = 32767 × 10^(-10/20) ≈ 10362.
	const amp = 10362
	buf := make([]int16, FrameSize)
	for i := 0; i < FrameSize; i++ {
		buf[i] = int16(float64(amp) * math.Sin(2*math.Pi*500*float64(i)/float64(SampleRate)))
	}

	inputs := map[string][]int16{
		"speaker":  buf,
		"listener": make([]int16, FrameSize),
	}
	out := m.mixFor("listener", inputs)

	for i, v := range out {
		if v != buf[i] {
			t.Fatalf("out[%d] = %d, want %d (no limiting below threshold)", i, v, buf[i])
		}
	}
}

// TestCommsMixer_EnvelopeResetsOnTopologyChange asserts that when the set of
// participants changes (join / leave), per-listener envelope state is cleared
// so a freshly arrived participant doesn't inherit stale limiting dynamics.
func TestCommsMixer_EnvelopeResetsOnTopologyChange(t *testing.T) {
	m := newMixer()

	// Warm up the "A" listener's envelope with loud coherent input from B + C.
	loud := map[string][]int16{
		"A": makeConstFrame(0),
		"B": makeConstFrame(25000),
		"C": makeConstFrame(25000),
	}
	for i := 0; i < 10; i++ {
		_ = m.mixFor("A", loud)
	}
	if env := m.envelopes["A"]; env <= 1000 {
		t.Fatalf("expected envelope for A to accumulate past 1000, got %v", env)
	}

	// Topology change: participant D joins. Feed silence so that if the reset
	// fires we can observe the envelope staying near zero through the frame.
	widened := map[string][]int16{
		"A": makeConstFrame(0),
		"B": makeConstFrame(0),
		"C": makeConstFrame(0),
		"D": makeConstFrame(0),
	}
	_ = m.mixFor("A", widened)
	if env := m.envelopes["A"]; env > 1e-9 {
		t.Fatalf("expected envelope for A to reset to 0 on topology change, got %v", env)
	}

	// Reverse direction: remove D, re-warm, then remove again to verify
	// join and leave both trigger a reset.
	for i := 0; i < 10; i++ {
		_ = m.mixFor("A", loud)
	}
	if env := m.envelopes["A"]; env <= 1000 {
		t.Fatalf("expected envelope for A to re-accumulate past 1000, got %v", env)
	}
	narrower := map[string][]int16{
		"A": makeConstFrame(0),
		"B": makeConstFrame(0),
	}
	_ = m.mixFor("A", narrower)
	if env := m.envelopes["A"]; env > 1e-9 {
		t.Fatalf("expected envelope reset on participant leave, got %v", env)
	}
}

// makeConstFrame returns a FrameSize slice filled with the given value.
func makeConstFrame(val int16) []int16 {
	buf := make([]int16, FrameSize)
	for i := range buf {
		buf[i] = val
	}
	return buf
}
