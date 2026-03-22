package colorgrade

import (
	"math"
	"testing"
)

// ---------- helpers ----------

// lutMidGray returns the index into lut.Data for the mid-gray entry (16,16,16) in a 33^3 LUT.
func lutMidGray(lut *LUT3D) int {
	return (16*lut.Size*lut.Size + 16*lut.Size + 16) * 3
}

// assertLUTUnchanged checks that every value in lut.Data matches orig within tol.
func assertLUTUnchanged(t *testing.T, lut *LUT3D, orig []float32, tol float64) {
	t.Helper()
	for i := range lut.Data {
		if math.Abs(float64(lut.Data[i]-orig[i])) > tol {
			t.Errorf("data[%d] changed: got %f, want %f (tol %e)", i, lut.Data[i], orig[i], tol)
			return
		}
	}
}

// assertLUTInRange checks that every value in lut.Data is in [0, 1].
func assertLUTInRange(t *testing.T, lut *LUT3D) {
	t.Helper()
	for i, v := range lut.Data {
		if v < 0 || v > 1 {
			t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
			return
		}
	}
}

func cloneLUT(lut *LUT3D) []float32 {
	orig := make([]float32, len(lut.Data))
	copy(orig, lut.Data)
	return orig
}

// ---------- Temperature ----------

func TestApplyTemperature_Zero_Identity(t *testing.T) {
	lut := GenerateIdentityLUT()
	orig := cloneLUT(lut)
	ApplyTemperature(lut, 0)
	assertLUTUnchanged(t, lut, orig, 1e-6)
}

func TestApplyTemperature_Warm(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)
	origR := lut.Data[mid]
	origB := lut.Data[mid+2]

	ApplyTemperature(lut, 100)

	if lut.Data[mid] <= origR {
		t.Errorf("warm: red at mid-gray should increase, got %f (was %f)", lut.Data[mid], origR)
	}
	if lut.Data[mid+2] >= origB {
		t.Errorf("warm: blue at mid-gray should decrease, got %f (was %f)", lut.Data[mid+2], origB)
	}
	assertLUTInRange(t, lut)
}

func TestApplyTemperature_Cool(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)
	origR := lut.Data[mid]
	origB := lut.Data[mid+2]

	ApplyTemperature(lut, -100)

	if lut.Data[mid] >= origR {
		t.Errorf("cool: red at mid-gray should decrease, got %f (was %f)", lut.Data[mid], origR)
	}
	if lut.Data[mid+2] <= origB {
		t.Errorf("cool: blue at mid-gray should increase, got %f (was %f)", lut.Data[mid+2], origB)
	}
	assertLUTInRange(t, lut)
}

func TestApplyTemperature_ExtremeClamp(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplyTemperature(lut, 100)
	assertLUTInRange(t, lut)

	lut2 := GenerateIdentityLUT()
	ApplyTemperature(lut2, -100)
	assertLUTInRange(t, lut2)
}

// ---------- Tint ----------

func TestApplyTint_Zero_Identity(t *testing.T) {
	lut := GenerateIdentityLUT()
	orig := cloneLUT(lut)
	ApplyTint(lut, 0)
	assertLUTUnchanged(t, lut, orig, 1e-6)
}

func TestApplyTint_Green(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)
	origG := lut.Data[mid+1]

	ApplyTint(lut, -100)

	if lut.Data[mid+1] <= origG {
		t.Errorf("green tint: green at mid-gray should increase, got %f (was %f)", lut.Data[mid+1], origG)
	}
	assertLUTInRange(t, lut)
}

func TestApplyTint_Magenta(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)
	origG := lut.Data[mid+1]

	ApplyTint(lut, 100)

	if lut.Data[mid+1] >= origG {
		t.Errorf("magenta tint: green at mid-gray should decrease, got %f (was %f)", lut.Data[mid+1], origG)
	}
	assertLUTInRange(t, lut)
}

func TestApplyTint_ExtremeClamp(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplyTint(lut, 100)
	assertLUTInRange(t, lut)

	lut2 := GenerateIdentityLUT()
	ApplyTint(lut2, -100)
	assertLUTInRange(t, lut2)
}

// ---------- Shadows / Midtones / Highlights ----------

func TestApplyShadowsMidtonesHighlights_Zero_Identity(t *testing.T) {
	lut := GenerateIdentityLUT()
	orig := cloneLUT(lut)

	zero := [3]float32{0, 0, 0}
	ApplyShadowsMidtonesHighlights(lut, zero, zero, zero)

	assertLUTUnchanged(t, lut, orig, 1e-6)
}

func TestApplyShadowsMidtonesHighlights_WarmShadows(t *testing.T) {
	lut := GenerateIdentityLUT()

	// Warm shadows: boost red in shadows.
	shadows := [3]float32{1.0, 0, 0}
	zero := [3]float32{0, 0, 0}
	ApplyShadowsMidtonesHighlights(lut, shadows, zero, zero)

	// Check a low-luminance entry. (0,0,0) is pure black and has 0 luminance,
	// but the shadow zone weight is smooth, so check a dark-ish entry.
	// Entry (4,4,4) — low luminance, in shadow zone.
	darkIdx := (4*33*33 + 4*33 + 4) * 3
	darkR := lut.Data[darkIdx]
	darkG := lut.Data[darkIdx+1]
	darkB := lut.Data[darkIdx+2]

	if darkR <= darkG || darkR <= darkB {
		t.Errorf("warm shadows: expected red boost in dark entry, got R=%f G=%f B=%f", darkR, darkG, darkB)
	}

	// Highlight entry (28,28,28) should be mostly unaffected.
	brightIdx := (28*33*33 + 28*33 + 28) * 3
	brightR := lut.Data[brightIdx]
	brightG := lut.Data[brightIdx+1]
	brightB := lut.Data[brightIdx+2]

	// The red boost in highlights should be much smaller than in shadows.
	darkRedShift := darkR - float32(4)/32.0
	brightRedShift := brightR - float32(28)/32.0
	if brightRedShift >= darkRedShift {
		t.Errorf("warm shadows leaked into highlights: dark shift=%f, bright shift=%f", darkRedShift, brightRedShift)
	}
	_ = brightG
	_ = brightB

	assertLUTInRange(t, lut)
}

func TestApplyShadowsMidtonesHighlights_ExtremeClamp(t *testing.T) {
	lut := GenerateIdentityLUT()
	extreme := [3]float32{1, 1, 1}
	ApplyShadowsMidtonesHighlights(lut, extreme, extreme, extreme)
	assertLUTInRange(t, lut)

	lut2 := GenerateIdentityLUT()
	negExtreme := [3]float32{-1, -1, -1}
	ApplyShadowsMidtonesHighlights(lut2, negExtreme, negExtreme, negExtreme)
	assertLUTInRange(t, lut2)
}

// ---------- Curves ----------

func TestApplyCurves_Empty_Identity(t *testing.T) {
	lut := GenerateIdentityLUT()
	orig := cloneLUT(lut)

	ApplyCurves(lut, nil, nil, nil, nil)
	assertLUTUnchanged(t, lut, orig, 1e-6)
}

func TestApplyCurves_LinearIdentity(t *testing.T) {
	lut := GenerateIdentityLUT()
	orig := cloneLUT(lut)

	identity := []CurvePoint{{0, 0}, {1, 1}}
	ApplyCurves(lut, identity, identity, identity, identity)

	assertLUTUnchanged(t, lut, orig, 1e-4)
}

func TestApplyCurves_Brighten(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)
	origR := lut.Data[mid]

	// Curve above diagonal: brightens.
	bright := []CurvePoint{{0, 0}, {0.5, 0.7}, {1, 1}}
	ApplyCurves(lut, bright, nil, nil, nil)

	if lut.Data[mid] <= origR {
		t.Errorf("brighten curve: mid-gray R should increase, got %f (was %f)", lut.Data[mid], origR)
	}
	assertLUTInRange(t, lut)
}

func TestApplyCurves_MasterAffectsAll(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)
	origR := lut.Data[mid]
	origG := lut.Data[mid+1]
	origB := lut.Data[mid+2]

	// Master curve above diagonal.
	bright := []CurvePoint{{0, 0}, {0.5, 0.7}, {1, 1}}
	ApplyCurves(lut, bright, nil, nil, nil)

	if lut.Data[mid] <= origR {
		t.Errorf("master curve: R at mid-gray should increase, got %f (was %f)", lut.Data[mid], origR)
	}
	if lut.Data[mid+1] <= origG {
		t.Errorf("master curve: G at mid-gray should increase, got %f (was %f)", lut.Data[mid+1], origG)
	}
	if lut.Data[mid+2] <= origB {
		t.Errorf("master curve: B at mid-gray should increase, got %f (was %f)", lut.Data[mid+2], origB)
	}
}

func TestApplyCurves_PerChannel(t *testing.T) {
	lut := GenerateIdentityLUT()
	mid := lutMidGray(lut)

	// Only boost red channel.
	bright := []CurvePoint{{0, 0}, {0.5, 0.7}, {1, 1}}
	identity := []CurvePoint{{0, 0}, {1, 1}}
	ApplyCurves(lut, nil, bright, identity, identity)

	origMid := float32(16) / 32.0 // 0.5
	if lut.Data[mid] <= origMid {
		t.Errorf("per-channel: R should increase, got %f", lut.Data[mid])
	}
	// Green and blue should be approximately unchanged.
	if math.Abs(float64(lut.Data[mid+1]-origMid)) > 0.01 {
		t.Errorf("per-channel: G should be ~unchanged, got %f (want ~%f)", lut.Data[mid+1], origMid)
	}
	if math.Abs(float64(lut.Data[mid+2]-origMid)) > 0.01 {
		t.Errorf("per-channel: B should be ~unchanged, got %f (want ~%f)", lut.Data[mid+2], origMid)
	}
}

func TestApplyCurves_ExtremeClamp(t *testing.T) {
	lut := GenerateIdentityLUT()
	// Curve that pushes everything to 1.
	push := []CurvePoint{{0, 1}, {1, 1}}
	ApplyCurves(lut, push, nil, nil, nil)
	assertLUTInRange(t, lut)
}

// ---------- Smoothstep helper ----------

func TestSmoothstep(t *testing.T) {
	// At edge0, result should be 0.
	if v := smoothstep(0.2, 0.8, 0.2); math.Abs(float64(v)) > 1e-6 {
		t.Errorf("smoothstep at edge0 = %f, want 0", v)
	}
	// At edge1, result should be 1.
	if v := smoothstep(0.2, 0.8, 0.8); math.Abs(float64(v)-1) > 1e-6 {
		t.Errorf("smoothstep at edge1 = %f, want 1", v)
	}
	// At midpoint, result should be 0.5.
	if v := smoothstep(0.0, 1.0, 0.5); math.Abs(float64(v)-0.5) > 1e-6 {
		t.Errorf("smoothstep at midpoint = %f, want 0.5", v)
	}
	// Below edge0, clamped to 0.
	if v := smoothstep(0.2, 0.8, 0.0); v != 0 {
		t.Errorf("smoothstep below edge0 = %f, want 0", v)
	}
	// Above edge1, clamped to 1.
	if v := smoothstep(0.2, 0.8, 1.0); v != 1 {
		t.Errorf("smoothstep above edge1 = %f, want 1", v)
	}
}

// ---------- buildCurveLUT helper ----------

func TestBuildCurveLUT_Identity(t *testing.T) {
	pts := []CurvePoint{{0, 0}, {1, 1}}
	table := buildCurveLUT(pts)
	if len(table) != 256 {
		t.Fatalf("table length = %d, want 256", len(table))
	}
	for i, v := range table {
		expected := float32(i) / 255.0
		if math.Abs(float64(v-expected)) > 1e-4 {
			t.Errorf("table[%d] = %f, want %f", i, v, expected)
			break
		}
	}
}

func TestBuildCurveLUT_Clamped(t *testing.T) {
	// Points that would extrapolate beyond [0,1].
	pts := []CurvePoint{{0, 0.5}, {1, 1.5}}
	table := buildCurveLUT(pts)
	for i, v := range table {
		if v < 0 || v > 1 {
			t.Errorf("table[%d] = %f, out of [0,1] range", i, v)
			break
		}
	}
}
