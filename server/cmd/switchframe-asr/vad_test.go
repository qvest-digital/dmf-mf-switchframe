package main

import "testing"

func TestEnergyVAD_IdleToSpeaking(t *testing.T) {
	// Threshold at -26 dBFS. Amplitude 0.1 = -20 dBFS, well above threshold.
	v := newEnergyVAD(-26, 500, 16000)

	if v.state() != vadIdle {
		t.Fatalf("expected initial state idle, got %d", v.state())
	}
	if v.isSpeaking() {
		t.Fatal("expected not speaking initially")
	}

	// Feed a loud signal (amplitude 0.1 → RMS 0.1 = -20 dBFS > -26 dBFS threshold).
	loud := make([]float32, 1600) // 100ms at 16kHz
	for i := range loud {
		loud[i] = 0.1
	}
	v.process(loud)

	if v.state() != vadSpeaking {
		t.Fatalf("expected speaking after loud signal, got %d", v.state())
	}
	if !v.isSpeaking() {
		t.Fatal("expected isSpeaking() true after loud signal")
	}
}

func TestEnergyVAD_HangoverToIdle(t *testing.T) {
	// 500ms hangover at 16kHz = 8000 samples.
	v := newEnergyVAD(-26, 500, 16000)

	// Start speaking.
	loud := make([]float32, 1600)
	for i := range loud {
		loud[i] = 0.1
	}
	v.process(loud)
	if v.state() != vadSpeaking {
		t.Fatalf("expected speaking, got %d", v.state())
	}

	// Feed silence — should transition to trailing.
	silence := make([]float32, 1600) // 100ms
	v.process(silence)
	if v.state() != vadTrailing {
		t.Fatalf("expected trailing after silence, got %d", v.state())
	}
	if !v.isSpeaking() {
		t.Fatal("expected isSpeaking() true during trailing")
	}

	// Feed more silence — not enough to complete hangover (1600 + 3200 = 4800 < 8000).
	v.process(make([]float32, 3200))
	if v.state() != vadTrailing {
		t.Fatalf("expected still trailing, got %d", v.state())
	}

	// Feed enough silence to complete hangover (4800 + 4000 = 8800 >= 8000).
	v.process(make([]float32, 4000))
	if v.state() != vadIdle {
		t.Fatalf("expected idle after hangover, got %d", v.state())
	}
	if v.isSpeaking() {
		t.Fatal("expected isSpeaking() false after hangover")
	}
}

func TestEnergyVAD_TrailingToSpeaking(t *testing.T) {
	v := newEnergyVAD(-26, 500, 16000)

	// Start speaking.
	loud := make([]float32, 1600)
	for i := range loud {
		loud[i] = 0.1
	}
	v.process(loud)

	// Go to trailing.
	v.process(make([]float32, 1600))
	if v.state() != vadTrailing {
		t.Fatalf("expected trailing, got %d", v.state())
	}

	// Resume speech — should go back to speaking.
	v.process(loud)
	if v.state() != vadSpeaking {
		t.Fatalf("expected speaking after resume, got %d", v.state())
	}
}

func TestEnergyVAD_Reset(t *testing.T) {
	v := newEnergyVAD(-26, 500, 16000)

	// Get into speaking state.
	loud := make([]float32, 1600)
	for i := range loud {
		loud[i] = 0.1
	}
	v.process(loud)
	if v.state() != vadSpeaking {
		t.Fatalf("expected speaking, got %d", v.state())
	}

	// Reset should return to idle.
	v.reset()
	if v.state() != vadIdle {
		t.Fatalf("expected idle after reset, got %d", v.state())
	}
	if v.isSpeaking() {
		t.Fatal("expected not speaking after reset")
	}
}

func TestEnergyVAD_EmptyNoOp(t *testing.T) {
	v := newEnergyVAD(-26, 500, 16000)

	// Empty input should not change state.
	v.process(nil)
	if v.state() != vadIdle {
		t.Fatalf("expected idle after nil input, got %d", v.state())
	}

	v.process([]float32{})
	if v.state() != vadIdle {
		t.Fatalf("expected idle after empty input, got %d", v.state())
	}
}
