package switcher

import (
	"math"
	"sync/atomic"
	"testing"
)

func TestTransitionAbortThreshold_Float64PackRoundTrip(t *testing.T) {
	var a atomic.Uint64
	cases := []float64{0.05, 0.1, 0.3, 0.499999, 0.5}
	for _, want := range cases {
		a.Store(math.Float64bits(want))
		got := math.Float64frombits(a.Load())
		if got != want {
			t.Errorf("round-trip mismatch: got %v, want %v", got, want)
		}
	}
}

func TestSwitcher_ScalarAtomics_InitializedFromDefaults(t *testing.T) {
	sw := New(nil)
	defer sw.Close()

	if got := sw.ftbDurationMsAtomic.Load(); got != int64(defaultFTBDurationMs) {
		t.Errorf("ftbDurationMsAtomic: got %d, want %d", got, defaultFTBDurationMs)
	}
	gotThresh := math.Float64frombits(sw.transitionAbortThresholdAtomic.Load())
	if gotThresh != defaultTransitionAbortThreshold {
		t.Errorf("transitionAbortThresholdAtomic: got %v, want %v", gotThresh, defaultTransitionAbortThreshold)
	}
	// pipeCodecsAtomic is allowed to be nil initially (New doesn't build codecs).
	// We only assert the field exists and loads without panic.
	_ = sw.pipeCodecsAtomic.Load()
}
