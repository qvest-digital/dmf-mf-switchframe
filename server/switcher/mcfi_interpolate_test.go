package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCFIInterpolateInto_ZeroAllocs(t *testing.T) {
	w, h := 32, 32
	frameSize := w * h * 3 / 2
	frameA := make([]byte, frameSize)
	frameB := make([]byte, frameSize)
	dst := make([]byte, frameSize)
	for i := range frameA {
		frameA[i] = 100
		frameB[i] = 200
	}

	mcfi := NewMCFIState()

	// InterpolateInto should not allocate (for near-threshold paths).
	allocs := testing.AllocsPerRun(10, func() {
		mcfi.InterpolateInto(dst, frameA, frameB, w, h, 0.01)
	})
	if allocs > 0 {
		t.Errorf("InterpolateInto near-threshold: got %v allocs, want 0", allocs)
	}
}

func TestMCFIInterpolate_NearThresholdReturnsCopy(t *testing.T) {
	// Bug: Interpolate() returns frameA/frameB directly for near-threshold
	// alpha values, violating the documented "freshly allocated copy" contract.
	// Callers that mutate the returned slice would corrupt the source frame.
	//
	// The fix should return a copy for both the low and high threshold paths.
	w, h := 32, 32
	frameSize := w * h * 3 / 2

	// Create two distinct frames.
	frameA := make([]byte, frameSize)
	for i := range frameA {
		frameA[i] = 100
	}
	frameB := make([]byte, frameSize)
	for i := range frameB {
		frameB[i] = 200
	}

	// Save original frameA data.
	origA := make([]byte, len(frameA))
	copy(origA, frameA)
	origB := make([]byte, len(frameB))
	copy(origB, frameB)

	mcfi := NewMCFIState()

	tests := []struct {
		name      string
		alpha     float64
		origFrame []byte // expected source frame content
		origCopy  []byte // saved copy of original
	}{
		{
			name:      "alpha below low threshold returns copy of frameA",
			alpha:     0.01, // well below frcNearThresholdLow (0.05)
			origFrame: frameA,
			origCopy:  origA,
		},
		{
			name:      "alpha above high threshold returns copy of frameB",
			alpha:     0.99, // well above frcNearThresholdHigh (0.95)
			origFrame: frameB,
			origCopy:  origB,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mcfi.Interpolate(frameA, frameB, w, h, tc.alpha)
			require.NotNil(t, result, "Interpolate should return non-nil")
			require.Equal(t, frameSize, len(result), "result should be full frame size")

			// Mutate the returned slice.
			for i := range result {
				result[i] = 0
			}

			// Original frame must be unchanged (proves result was a copy, not alias).
			require.Equal(t, tc.origCopy, tc.origFrame,
				"original frame must be unchanged after mutating Interpolate result")
		})
	}
}
