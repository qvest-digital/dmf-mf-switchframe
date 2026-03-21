package stmap

import (
	"math"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateIdentity(t *testing.T) {
	m, err := Generate("identity", nil, 8, 8)
	require.NoError(t, err)
	require.Equal(t, 8, m.Width)
	require.Equal(t, 8, m.Height)

	// Should produce the same values as Identity().
	ref := Identity(8, 8)
	for i := range m.S {
		require.InDelta(t, float64(ref.S[i]), float64(m.S[i]), 1e-6,
			"S[%d] mismatch", i)
		require.InDelta(t, float64(ref.T[i]), float64(m.T[i]), 1e-6,
			"T[%d] mismatch", i)
	}
}

func TestGenerateBarrel(t *testing.T) {
	const w, h = 64, 64
	m, err := Generate("barrel", map[string]float64{"k1": -0.3, "k2": 0.0}, w, h)
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	cx := float64(w) / 2.0
	cy := float64(h) / 2.0

	// Center pixel should be unchanged (maps to itself).
	centerIdx := int(cy)*w + int(cx)
	centerS := float64(m.S[centerIdx])
	centerT := float64(m.T[centerIdx])
	expectedS := (cx + 0.5) / float64(w)
	expectedT := (cy + 0.5) / float64(h)
	require.InDelta(t, expectedS, centerS, 1e-4, "center S should be near identity")
	require.InDelta(t, expectedT, centerT, 1e-4, "center T should be near identity")

	// Corners should be displaced from identity values (barrel pushes corners outward).
	ref := Identity(w, h)
	corners := []int{0, w - 1, (h - 1) * w, (h-1)*w + w - 1}
	for _, idx := range corners {
		dS := math.Abs(float64(m.S[idx]) - float64(ref.S[idx]))
		dT := math.Abs(float64(m.T[idx]) - float64(ref.T[idx]))
		require.Greater(t, dS+dT, 0.001,
			"corner pixel %d should be displaced from identity", idx)
	}
}

func TestGenerateBarrel_DefaultParams(t *testing.T) {
	const w, h = 32, 32
	// nil params should use defaults and not error.
	m, err := Generate("barrel", nil, w, h)
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)
	require.Len(t, m.S, w*h)
	require.Len(t, m.T, w*h)

	// All values should be finite.
	for i := range m.S {
		require.False(t, math.IsNaN(float64(m.S[i])), "S[%d] is NaN", i)
		require.False(t, math.IsInf(float64(m.S[i]), 0), "S[%d] is Inf", i)
		require.False(t, math.IsNaN(float64(m.T[i])), "T[%d] is NaN", i)
		require.False(t, math.IsInf(float64(m.T[i]), 0), "T[%d] is Inf", i)
	}
}

func TestGeneratePincushion(t *testing.T) {
	const w, h = 64, 64
	m, err := Generate("pincushion", map[string]float64{"k1": 0.3}, w, h)
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	// Center should be approximately identity.
	cx := float64(w) / 2.0
	cy := float64(h) / 2.0
	centerIdx := int(cy)*w + int(cx)
	expectedS := (cx + 0.5) / float64(w)
	expectedT := (cy + 0.5) / float64(h)
	require.InDelta(t, expectedS, float64(m.S[centerIdx]), 1e-4)
	require.InDelta(t, expectedT, float64(m.T[centerIdx]), 1e-4)

	// Corners should be displaced.
	ref := Identity(w, h)
	idx := 0 // top-left corner
	dS := math.Abs(float64(m.S[idx]) - float64(ref.S[idx]))
	dT := math.Abs(float64(m.T[idx]) - float64(ref.T[idx]))
	require.Greater(t, dS+dT, 0.001, "corner should be displaced")

	// All values should be finite.
	for i := range m.S {
		require.False(t, math.IsNaN(float64(m.S[i])), "S[%d] is NaN", i)
		require.False(t, math.IsInf(float64(m.S[i]), 0), "S[%d] is Inf", i)
	}
}

func TestGenerateFisheye(t *testing.T) {
	const w, h = 64, 64
	m, err := Generate("fisheye_to_rectilinear", map[string]float64{"fov": 180}, w, h)
	require.NoError(t, err)
	require.Equal(t, w, m.Width)
	require.Equal(t, h, m.Height)

	// Center should be approximately identity.
	cx := float64(w) / 2.0
	cy := float64(h) / 2.0
	centerIdx := int(cy)*w + int(cx)
	expectedS := (cx + 0.5) / float64(w)
	expectedT := (cy + 0.5) / float64(h)
	require.InDelta(t, expectedS, float64(m.S[centerIdx]), 1e-4)
	require.InDelta(t, expectedT, float64(m.T[centerIdx]), 1e-4)

	// All values should be finite.
	for i := range m.S {
		require.False(t, math.IsNaN(float64(m.S[i])), "S[%d] is NaN", i)
		require.False(t, math.IsInf(float64(m.S[i]), 0), "S[%d] is Inf", i)
		require.False(t, math.IsNaN(float64(m.T[i])), "T[%d] is NaN", i)
		require.False(t, math.IsInf(float64(m.T[i]), 0), "T[%d] is Inf", i)
	}
}

func TestGenerateCornerPin_Identity(t *testing.T) {
	const w, h = 64, 64
	// Corners at the actual image corners should produce approximately identity.
	params := map[string]float64{
		"tl_x": 0, "tl_y": 0,
		"tr_x": 1, "tr_y": 0,
		"bl_x": 0, "bl_y": 1,
		"br_x": 1, "br_y": 1,
	}
	m, err := Generate("corner_pin", params, w, h)
	require.NoError(t, err)

	// Every pixel should map to itself (within tolerance for bilinear approx).
	ref := Identity(w, h)
	maxDiff := float64(0)
	for i := range m.S {
		dS := math.Abs(float64(m.S[i]) - float64(ref.S[i]))
		dT := math.Abs(float64(m.T[i]) - float64(ref.T[i]))
		if dS > maxDiff {
			maxDiff = dS
		}
		if dT > maxDiff {
			maxDiff = dT
		}
	}
	// Corner pin at actual corners differs from pixel-center identity in the formula:
	// corner_pin uses u/(w-1) normalization while identity uses (x+0.5)/w.
	// The difference is largest at edges, so we allow a tolerance per pixel.
	require.Less(t, maxDiff, 0.02, "corner pin at identity corners should approximate identity")
}

func TestGenerateCornerPin_Shift(t *testing.T) {
	const w, h = 32, 32
	// Shift the top-right corner inward — this should produce a non-identity map.
	params := map[string]float64{
		"tl_x": 0, "tl_y": 0,
		"tr_x": 0.8, "tr_y": 0.1,
		"bl_x": 0, "bl_y": 1,
		"br_x": 1, "br_y": 1,
	}
	m, err := Generate("corner_pin", params, w, h)
	require.NoError(t, err)

	ref := Identity(w, h)

	// Top-right region should be noticeably shifted.
	trIdx := w - 1 // top-right pixel
	dS := math.Abs(float64(m.S[trIdx]) - float64(ref.S[trIdx]))
	dT := math.Abs(float64(m.T[trIdx]) - float64(ref.T[trIdx]))
	require.Greater(t, dS+dT, 0.05, "shifted corner should produce shifted output")

	// Bottom-left should be mostly unchanged (corner not shifted).
	blIdx := (h - 1) * w
	dS = math.Abs(float64(m.S[blIdx]) - float64(ref.S[blIdx]))
	dT = math.Abs(float64(m.T[blIdx]) - float64(ref.T[blIdx]))
	require.Less(t, dS+dT, 0.05, "un-shifted corner should be near identity")
}

func TestGenerateUnknownType(t *testing.T) {
	_, err := Generate("nonexistent", nil, 8, 8)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown generator")
}

func TestListGenerators(t *testing.T) {
	names := ListGenerators()
	require.GreaterOrEqual(t, len(names), 5, "should have at least 5 generators")

	// Verify all 5 correction generators are present.
	expected := []string{"identity", "barrel", "pincushion", "fisheye_to_rectilinear", "corner_pin"}
	for _, name := range expected {
		require.Contains(t, names, name, "missing generator: %s", name)
	}

	// Should be sorted.
	require.True(t, sort.StringsAreSorted(names), "generator names should be sorted")
}

func TestGeneratorInfoList(t *testing.T) {
	infos := GeneratorInfoList()
	require.GreaterOrEqual(t, len(infos), 5)

	// Check that infos are sorted by name.
	for i := 1; i < len(infos); i++ {
		require.True(t, infos[i-1].Name <= infos[i].Name,
			"infos not sorted: %s > %s", infos[i-1].Name, infos[i].Name)
	}

	// Verify barrel has expected param schema.
	var barrelInfo *GeneratorInfo
	for i := range infos {
		if infos[i].Name == "barrel" {
			barrelInfo = &infos[i]
			break
		}
	}
	require.NotNil(t, barrelInfo, "barrel info should exist")
	require.Equal(t, "static", barrelInfo.Type)
	require.Contains(t, barrelInfo.Params, "k1")
	require.Contains(t, barrelInfo.Params, "k2")
	require.Equal(t, -0.3, barrelInfo.Params["k1"].Default)
	require.Equal(t, -1.0, barrelInfo.Params["k1"].Min)
	require.Equal(t, 0.0, barrelInfo.Params["k1"].Max)
}

func TestParamOr(t *testing.T) {
	params := map[string]float64{"k1": -0.5}

	// Existing key returns value.
	require.Equal(t, -0.5, paramOr(params, "k1", -0.3))

	// Missing key returns default.
	require.Equal(t, -0.3, paramOr(params, "k2", -0.3))

	// Nil map returns default.
	require.Equal(t, 42.0, paramOr(nil, "k1", 42.0))
}
