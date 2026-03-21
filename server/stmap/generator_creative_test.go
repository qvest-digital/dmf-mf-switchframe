package stmap

import (
	"math"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateAnimated_HeatShimmer(t *testing.T) {
	const (
		w          = 64
		h          = 64
		frameCount = 30
	)

	anim, err := GenerateAnimated("heat_shimmer", nil, w, h, frameCount)
	require.NoError(t, err)
	require.Len(t, anim.Frames, frameCount)

	// Each frame should have the correct dimensions.
	for i, f := range anim.Frames {
		require.Equal(t, w, f.Width, "frame %d width", i)
		require.Equal(t, h, f.Height, "frame %d height", i)
		require.Len(t, f.S, w*h, "frame %d S len", i)
		require.Len(t, f.T, w*h, "frame %d T len", i)
	}

	// Frames should differ from each other (animated effect).
	differ := false
	for i := 1; i < frameCount; i++ {
		for j := 0; j < w*h; j++ {
			if anim.Frames[0].T[j] != anim.Frames[i].T[j] {
				differ = true
				break
			}
		}
		if differ {
			break
		}
	}
	require.True(t, differ, "heat shimmer frames should differ from each other")

	// S channel should remain identity (no horizontal displacement).
	identity := Identity(w, h)
	for i, f := range anim.Frames {
		for j := 0; j < w*h; j++ {
			require.InDelta(t, identity.S[j], f.S[j], 1e-6,
				"frame %d pixel %d: S should be identity (no horizontal displacement)", i, j)
		}
	}
}

func TestGenerateAnimated_Dream(t *testing.T) {
	const (
		w          = 64
		h          = 64
		frameCount = 24
	)

	anim, err := GenerateAnimated("dream", nil, w, h, frameCount)
	require.NoError(t, err)
	require.Len(t, anim.Frames, frameCount)

	// Each frame should have correct dimensions.
	for i, f := range anim.Frames {
		require.Equal(t, w, f.Width, "frame %d width", i)
		require.Equal(t, h, f.Height, "frame %d height", i)
	}

	// All coordinates should be in a reasonable range (not wildly out of bounds).
	for i, f := range anim.Frames {
		for j := 0; j < w*h; j++ {
			require.True(t, f.S[j] >= -0.5 && f.S[j] <= 1.5,
				"frame %d pixel %d: S=%f out of reasonable range", i, j, f.S[j])
			require.True(t, f.T[j] >= -0.5 && f.T[j] <= 1.5,
				"frame %d pixel %d: T=%f out of reasonable range", i, j, f.T[j])
		}
	}
}

func TestGenerateAnimated_Ripple(t *testing.T) {
	const (
		w          = 64
		h          = 64
		frameCount = 30
	)

	anim, err := GenerateAnimated("ripple", nil, w, h, frameCount)
	require.NoError(t, err)
	require.Len(t, anim.Frames, frameCount)

	// Each frame should have correct dimensions.
	for i, f := range anim.Frames {
		require.Equal(t, w, f.Width, "frame %d width", i)
		require.Equal(t, h, f.Height, "frame %d height", i)
	}

	// Frames should differ from each other (animated ripple).
	differ := false
	for i := 1; i < frameCount; i++ {
		for j := 0; j < w*h; j++ {
			if anim.Frames[0].S[j] != anim.Frames[i].S[j] {
				differ = true
				break
			}
		}
		if differ {
			break
		}
	}
	require.True(t, differ, "ripple frames should differ from each other")
}

func TestGenerateAnimated_LensBreathe(t *testing.T) {
	const (
		w          = 64
		h          = 64
		frameCount = 30
	)

	anim, err := GenerateAnimated("lens_breathe", nil, w, h, frameCount)
	require.NoError(t, err)
	require.Len(t, anim.Frames, frameCount)

	// Center pixel should stay near center across all frames.
	// The center pixel is at (w/2, h/2), which in identity has
	// S = (w/2 + 0.5) / w and T = (h/2 + 0.5) / h.
	centerIdx := (h/2)*w + (w / 2)
	identityCenterS := (float64(w/2) + 0.5) / float64(w)
	identityCenterT := (float64(h/2) + 0.5) / float64(h)

	for i, f := range anim.Frames {
		// lens_breathe scales from center, so the center pixel should map
		// very close to itself (within a tolerance accounting for the scaling).
		deltaS := math.Abs(float64(f.S[centerIdx]) - identityCenterS)
		deltaT := math.Abs(float64(f.T[centerIdx]) - identityCenterT)
		require.Less(t, deltaS, 0.01,
			"frame %d: center S should be near identity (delta=%f)", i, deltaS)
		require.Less(t, deltaT, 0.01,
			"frame %d: center T should be near identity (delta=%f)", i, deltaT)
	}
}

func TestGenerateAnimated_Vortex(t *testing.T) {
	const (
		w          = 64
		h          = 64
		frameCount = 30
	)

	anim, err := GenerateAnimated("vortex", nil, w, h, frameCount)
	require.NoError(t, err)
	require.Len(t, anim.Frames, frameCount)

	// Center pixel (at (0.5, 0.5) in normalized coords) should remain
	// fixed across all frames since r=0 means angle=0.
	centerIdx := (h/2)*w + (w / 2)

	for i, f := range anim.Frames {
		require.InDelta(t, 0.5, float64(f.S[centerIdx]), 0.02,
			"frame %d: center S should be ~0.5", i)
		require.InDelta(t, 0.5, float64(f.T[centerIdx]), 0.02,
			"frame %d: center T should be ~0.5", i)
	}
}

func TestGenerateAnimated_UnknownType(t *testing.T) {
	_, err := GenerateAnimated("nonexistent_effect", nil, 64, 64, 30)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown animated generator type")
}

func TestListAnimatedGenerators(t *testing.T) {
	names := ListAnimatedGenerators()

	// Should contain all 5 creative generators.
	expected := []string{"dream", "heat_shimmer", "lens_breathe", "ripple", "vortex"}
	sort.Strings(names)

	for _, exp := range expected {
		require.Contains(t, names, exp, "missing animated generator %q", exp)
	}
}

func TestGenerateAnimated_HeatShimmer_CustomParams(t *testing.T) {
	const (
		w          = 32
		h          = 32
		frameCount = 10
	)

	// Higher intensity should produce larger displacement.
	animLow, err := GenerateAnimated("heat_shimmer", map[string]float64{
		"intensity": 0.1,
	}, w, h, frameCount)
	require.NoError(t, err)

	animHigh, err := GenerateAnimated("heat_shimmer", map[string]float64{
		"intensity": 0.9,
	}, w, h, frameCount)
	require.NoError(t, err)

	// Measure max T displacement from identity.
	identity := Identity(w, h)
	maxDispLow := float32(0)
	maxDispHigh := float32(0)
	for _, f := range animLow.Frames {
		for j := 0; j < w*h; j++ {
			d := abs32(f.T[j] - identity.T[j])
			if d > maxDispLow {
				maxDispLow = d
			}
		}
	}
	for _, f := range animHigh.Frames {
		for j := 0; j < w*h; j++ {
			d := abs32(f.T[j] - identity.T[j])
			if d > maxDispHigh {
				maxDispHigh = d
			}
		}
	}

	require.Greater(t, maxDispHigh, maxDispLow,
		"higher intensity should produce larger displacement")
}

func TestGenerateAnimated_Ripple_CustomCenter(t *testing.T) {
	const (
		w          = 64
		h          = 64
		frameCount = 10
	)

	// Ripple centered at top-left corner.
	anim, err := GenerateAnimated("ripple", map[string]float64{
		"cx": 0.0,
		"cy": 0.0,
	}, w, h, frameCount)
	require.NoError(t, err)
	require.Len(t, anim.Frames, frameCount)

	// The pixel at (0,0) has r=distance to center (0,0), so
	// (0+0.5 - 0*64)^2 = 0.25, small r, minimal displacement.
	// Just verify it produces valid output.
	for i, f := range anim.Frames {
		for j := 0; j < w*h; j++ {
			require.False(t, math.IsNaN(float64(f.S[j])),
				"frame %d pixel %d: S is NaN", i, j)
			require.False(t, math.IsNaN(float64(f.T[j])),
				"frame %d pixel %d: T is NaN", i, j)
		}
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
