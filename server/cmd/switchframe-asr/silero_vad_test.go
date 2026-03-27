package main

import "testing"

func TestSileroVADState_AboveThreshold(t *testing.T) {
	s := newSileroVADState(0.5, 750)

	if s.speaking {
		t.Fatal("expected not speaking initially")
	}

	// Probability above threshold → speaking.
	s.updateState(0.8)
	if !s.speaking {
		t.Fatal("expected speaking after prob > threshold")
	}
	if s.hangover != 750 {
		t.Fatalf("expected hangover 750, got %d", s.hangover)
	}
}

func TestSileroVADState_HysteresisZone(t *testing.T) {
	s := newSileroVADState(0.5, 750)
	// negThreshold = max(0.5 - 0.15, 0.01) = 0.35

	// Start speaking.
	s.updateState(0.8)
	if !s.speaking {
		t.Fatal("expected speaking")
	}

	// Probability in hysteresis zone (between 0.35 and 0.5) → stay speaking, reset hangover.
	s.hangover = 300 // simulate some decay
	s.updateState(0.4)
	if !s.speaking {
		t.Fatal("expected still speaking in hysteresis zone")
	}
	if s.hangover != 750 {
		t.Fatalf("expected hangover reset to 750, got %d", s.hangover)
	}
}

func TestSileroVADState_BelowNegThreshold(t *testing.T) {
	s := newSileroVADState(0.5, 750)
	// negThreshold = 0.35

	// Start speaking.
	s.updateState(0.8)

	// Below negThreshold → countdown hangover.
	s.updateState(0.1)
	if !s.speaking {
		t.Fatal("expected still speaking during hangover countdown")
	}
	if s.hangover != 750-sileroChunkDurationMs {
		t.Fatalf("expected hangover %d, got %d", 750-sileroChunkDurationMs, s.hangover)
	}

	// Keep going below negThreshold until hangover exhausted.
	// 750ms / 32ms per chunk = 23.4 chunks. We already did 1.
	for i := 0; i < 22; i++ {
		s.updateState(0.1)
	}
	// After 23 total chunks: 750 - 23*32 = 750 - 736 = 14ms remaining.
	if !s.speaking {
		t.Fatal("expected still speaking, hangover not exhausted yet")
	}

	// One more should exhaust it.
	s.updateState(0.1)
	if s.speaking {
		t.Fatal("expected not speaking after hangover exhausted")
	}
	if s.hangover != 0 {
		t.Fatalf("expected hangover 0, got %d", s.hangover)
	}
}

func TestSileroVADState_NotSpeakingBelowThreshold(t *testing.T) {
	s := newSileroVADState(0.5, 750)

	// Below threshold while not speaking → stay not speaking.
	s.updateState(0.3)
	if s.speaking {
		t.Fatal("expected not speaking when below threshold from idle")
	}
}

func TestSileroVADState_DefaultValues(t *testing.T) {
	s := newSileroVADState(0, 0)
	if s.threshold != 0.5 {
		t.Fatalf("expected default threshold 0.5, got %f", s.threshold)
	}
	if s.hangoverMs != 750 {
		t.Fatalf("expected default hangoverMs 750, got %d", s.hangoverMs)
	}
}
