package asr

import (
	"math"
	"testing"
)

// helper: generate a buffer of constant-amplitude samples
func constantTone(amplitude float32, count int) []float32 {
	buf := make([]float32, count)
	for i := range buf {
		buf[i] = amplitude
	}
	return buf
}

// helper: generate silence
func silence(count int) []float32 {
	return make([]float32, count)
}

func TestVAD_SilenceStaysIdle(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	// Feed several chunks of silence
	for i := 0; i < 10; i++ {
		v.Process(silence(160)) // 10ms at 16kHz
	}

	if v.State() != VADIdle {
		t.Errorf("expected VADIdle after silence, got %v", v.State())
	}
}

func TestVAD_LoudSignalTriggersSpeaking(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	// 0.5 amplitude is well above -35 dBFS threshold
	// RMS of constant 0.5 = 0.5, which is -6 dBFS
	v.Process(constantTone(0.5, 160))

	if v.State() != VADSpeaking {
		t.Errorf("expected VADSpeaking after loud signal, got %v", v.State())
	}
}

func TestVAD_HangoverDelayBeforeIdle(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	// Hangover = 16000 * 200 / 1000 = 3200 samples

	// Start speaking
	v.Process(constantTone(0.5, 160))
	if v.State() != VADSpeaking {
		t.Fatalf("expected VADSpeaking, got %v", v.State())
	}

	// Go silent - should transition to trailing
	v.Process(silence(160))
	if v.State() != VADTrailing {
		t.Errorf("expected VADTrailing after first silence chunk, got %v", v.State())
	}

	// Feed silence but not enough to exceed hangover (3200 samples)
	// We've fed 160 silent samples so far; feed 2880 more (= 3040 total, still < 3200)
	for i := 0; i < 18; i++ { // 18 * 160 = 2880
		v.Process(silence(160))
	}
	if v.State() != VADTrailing {
		t.Errorf("expected VADTrailing during hangover period, got %v", v.State())
	}

	// Feed enough to exceed hangover: need 160 more to reach 3200
	v.Process(silence(160))
	if v.State() != VADIdle {
		t.Errorf("expected VADIdle after hangover expired, got %v", v.State())
	}
}

func TestVAD_SpeechDuringTrailingResetsHangover(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	// Start speaking
	v.Process(constantTone(0.5, 160))
	if v.State() != VADSpeaking {
		t.Fatalf("expected VADSpeaking, got %v", v.State())
	}

	// Go silent -> trailing
	v.Process(silence(160))
	if v.State() != VADTrailing {
		t.Fatalf("expected VADTrailing, got %v", v.State())
	}

	// Feed more silence but stay in hangover
	for i := 0; i < 5; i++ {
		v.Process(silence(160))
	}
	if v.State() != VADTrailing {
		t.Fatalf("expected still VADTrailing, got %v", v.State())
	}

	// Speak again -> should go back to speaking, resetting hangover
	v.Process(constantTone(0.5, 160))
	if v.State() != VADSpeaking {
		t.Errorf("expected VADSpeaking after speech during trailing, got %v", v.State())
	}

	// Now go silent again - should need full hangover period
	v.Process(silence(160))
	if v.State() != VADTrailing {
		t.Errorf("expected VADTrailing after new silence, got %v", v.State())
	}
}

func TestVAD_EmptyBufferNoOp(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	v.Process([]float32{})
	if v.State() != VADIdle {
		t.Errorf("expected VADIdle after empty buffer, got %v", v.State())
	}

	// Also verify no panic on nil
	v.Process(nil)
	if v.State() != VADIdle {
		t.Errorf("expected VADIdle after nil buffer, got %v", v.State())
	}
}

func TestVAD_Reset(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	// Get into speaking state
	v.Process(constantTone(0.5, 160))
	if v.State() != VADSpeaking {
		t.Fatalf("expected VADSpeaking, got %v", v.State())
	}

	v.Reset()
	if v.State() != VADIdle {
		t.Errorf("expected VADIdle after reset, got %v", v.State())
	}
}

func TestVAD_ThresholdBoundary(t *testing.T) {
	v := NewVAD(VADConfig{
		ThresholdDB: -35,
		HangoverMs:  200,
		SampleRate:  16000,
	})

	// -35 dBFS linear threshold = 10^(-35/20) ~= 0.01778
	thresholdLinear := math.Pow(10, -35.0/20.0)

	// Signal just below threshold should not trigger
	belowThreshold := float32(thresholdLinear * 0.9)
	v.Process(constantTone(belowThreshold, 160))
	if v.State() != VADIdle {
		t.Errorf("expected VADIdle for signal below threshold, got %v", v.State())
	}

	// Signal just above threshold should trigger
	aboveThreshold := float32(thresholdLinear * 1.1)
	v.Process(constantTone(aboveThreshold, 160))
	if v.State() != VADSpeaking {
		t.Errorf("expected VADSpeaking for signal above threshold, got %v", v.State())
	}
}

func TestVADState_String(t *testing.T) {
	tests := []struct {
		state VADState
		want  string
	}{
		{VADIdle, "idle"},
		{VADSpeaking, "speaking"},
		{VADTrailing, "trailing"},
		{VADState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("VADState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
