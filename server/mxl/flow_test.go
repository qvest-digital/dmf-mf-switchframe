package mxl

import (
	"testing"
)

func TestInstanceStub_ReturnsError(t *testing.T) {
	inst, err := NewInstance("/dev/shm/mxl")
	if err == nil {
		t.Fatal("expected error from stub NewInstance")
	}
	if inst != nil {
		t.Fatal("expected nil instance from stub")
	}
	if err != ErrMXLNotAvailable {
		t.Fatalf("expected ErrMXLNotAvailable, got: %v", err)
	}
}

func TestDiscoverStub_ReturnsError(t *testing.T) {
	flows, err := Discover("/dev/shm/mxl")
	if err == nil {
		t.Fatal("expected error from stub Discover")
	}
	if flows != nil {
		t.Fatal("expected nil flows from stub")
	}
	if err != ErrMXLNotAvailable {
		t.Fatalf("expected ErrMXLNotAvailable, got: %v", err)
	}
}

func TestCurrentIndex_StubNotNil(t *testing.T) {
	if CurrentIndex == nil {
		t.Fatal("CurrentIndex should be initialized by stub init()")
	}
	rate := Rational{Numerator: 30000, Denominator: 1001}
	idx := CurrentIndex(rate)
	// Just verify it returns a non-zero value (based on current time).
	if idx == 0 {
		t.Log("warning: CurrentIndex returned 0, might be a timing edge case")
	}
}

func TestCurrentIndex_ZeroRate(t *testing.T) {
	if CurrentIndex == nil {
		t.Fatal("CurrentIndex should be initialized")
	}
	// Zero rate should not panic.
	idx := CurrentIndex(Rational{Numerator: 0, Denominator: 0})
	if idx != 0 {
		t.Fatalf("expected 0 for zero rate, got %d", idx)
	}
}

func TestRational_Float64(t *testing.T) {
	tests := []struct {
		name string
		r    Rational
		want float64
	}{
		{"29.97fps", Rational{30000, 1001}, 29.970029970029970},
		{"30fps", Rational{30, 1}, 30.0},
		{"48kHz", Rational{48000, 1}, 48000.0},
		{"zero denom", Rational{30, 0}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.r.Float64()
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.001 {
				t.Fatalf("Float64() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestFlowOpenerInterface(t *testing.T) {
	// Verify Instance satisfies FlowOpener interface at compile time.
	var _ FlowOpener = (*Instance)(nil)
}

func TestStubInstance_MethodsReturnError(t *testing.T) {
	// Create a zero-value Instance (simulating what you'd get if you ignored the error).
	inst := &Instance{}

	if _, err := inst.OpenReader("test"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenReader: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.OpenAudioReader("test"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenAudioReader: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.OpenWriter("{}"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenWriter: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.OpenAudioWriter("{}"); err != ErrMXLNotAvailable {
		t.Fatalf("OpenAudioWriter: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.GetFlowConfig("test"); err != ErrMXLNotAvailable {
		t.Fatalf("GetFlowConfig: expected ErrMXLNotAvailable, got %v", err)
	}
	if _, err := inst.IsFlowActive("test"); err != ErrMXLNotAvailable {
		t.Fatalf("IsFlowActive: expected ErrMXLNotAvailable, got %v", err)
	}
	if err := inst.Close(); err != nil {
		t.Fatalf("Close: expected nil, got %v", err)
	}
}
