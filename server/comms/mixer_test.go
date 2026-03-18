package comms

import (
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

func TestMixerClipping(t *testing.T) {
	m := newMixer()

	inputs := map[string][]int16{
		"opA": makeConstFrame(30000),
		"opB": makeConstFrame(30000),
		"opC": makeConstFrame(100), // excluded
	}

	// opC hears opA + opB = 60000, should clamp to 32767
	out := m.mixFor("opC", inputs)
	for i, v := range out {
		if v != 32767 {
			t.Fatalf("out[%d] = %d, want 32767 (positive clamp)", i, v)
		}
	}

	// Test negative clipping
	inputsNeg := map[string][]int16{
		"opA": makeConstFrame(-30000),
		"opB": makeConstFrame(-30000),
		"opC": makeConstFrame(100),
	}

	outNeg := m.mixFor("opC", inputsNeg)
	for i, v := range outNeg {
		if v != -32768 {
			t.Fatalf("outNeg[%d] = %d, want -32768 (negative clamp)", i, v)
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

// makeConstFrame returns a FrameSize slice filled with the given value.
func makeConstFrame(val int16) []int16 {
	buf := make([]int16, FrameSize)
	for i := range buf {
		buf[i] = val
	}
	return buf
}
