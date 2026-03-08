package mxl

import (
	"testing"
)

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

