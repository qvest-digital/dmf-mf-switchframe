package colorgrade

import (
	"math"
	"testing"
)

func TestDefaultCorrectorState(t *testing.T) {
	d := DefaultCorrectorState()

	// Gamma should be [1,1,1].
	if d.Gamma != [3]float32{1, 1, 1} {
		t.Errorf("Gamma = %v, want [1,1,1]", d.Gamma)
	}
	// Gain should be [1,1,1].
	if d.Gain != [3]float32{1, 1, 1} {
		t.Errorf("Gain = %v, want [1,1,1]", d.Gain)
	}
	// Saturation should be 1.0.
	if d.Saturation != 1.0 {
		t.Errorf("Saturation = %f, want 1.0", d.Saturation)
	}
	// Lift should be zero.
	if d.Lift != [3]float32{0, 0, 0} {
		t.Errorf("Lift = %v, want [0,0,0]", d.Lift)
	}
	// Temperature, Tint, Contrast, Sharpness should be zero.
	if d.Temperature != 0 {
		t.Errorf("Temperature = %f, want 0", d.Temperature)
	}
	if d.Tint != 0 {
		t.Errorf("Tint = %f, want 0", d.Tint)
	}
	if d.Contrast != 0 {
		t.Errorf("Contrast = %f, want 0", d.Contrast)
	}
	if d.Sharpness != 0 {
		t.Errorf("Sharpness = %f, want 0", d.Sharpness)
	}
	// Curves should be nil.
	if d.CurveMaster != nil {
		t.Errorf("CurveMaster = %v, want nil", d.CurveMaster)
	}
}

func TestCorrectorState_IsDefault_True(t *testing.T) {
	d := DefaultCorrectorState()
	if !d.IsDefault() {
		t.Error("DefaultCorrectorState().IsDefault() = false, want true")
	}
}

func TestCorrectorState_IsDefault_False(t *testing.T) {
	tests := []struct {
		name   string
		modify func(c *CorrectorState)
	}{
		{"lift", func(c *CorrectorState) { c.Lift[0] = 0.1 }},
		{"gamma", func(c *CorrectorState) { c.Gamma[1] = 1.5 }},
		{"gain", func(c *CorrectorState) { c.Gain[2] = 0.8 }},
		{"temperature", func(c *CorrectorState) { c.Temperature = 50 }},
		{"tint", func(c *CorrectorState) { c.Tint = -30 }},
		{"saturation", func(c *CorrectorState) { c.Saturation = 1.2 }},
		{"contrast", func(c *CorrectorState) { c.Contrast = 0.1 }},
		{"shadows", func(c *CorrectorState) { c.Shadows[0] = 0.1 }},
		{"midtones", func(c *CorrectorState) { c.Midtones[1] = -0.2 }},
		{"highlights", func(c *CorrectorState) { c.Highlights[2] = 0.3 }},
		{"sharpness", func(c *CorrectorState) { c.Sharpness = 0.5 }},
		{"curveMaster", func(c *CorrectorState) {
			c.CurveMaster = []CurvePoint{{0, 0}, {1, 1}}
		}},
		{"curveR", func(c *CorrectorState) {
			c.CurveR = []CurvePoint{{0, 0}, {1, 0.8}}
		}},
		{"curveG", func(c *CorrectorState) {
			c.CurveG = []CurvePoint{{0, 0.1}, {1, 1}}
		}},
		{"curveB", func(c *CorrectorState) {
			c.CurveB = []CurvePoint{{0, 0}, {0.5, 0.6}, {1, 1}}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultCorrectorState()
			tt.modify(&c)
			if c.IsDefault() {
				t.Errorf("modified %s: IsDefault() = true, want false", tt.name)
			}
		})
	}
}

func TestCombineLUT_DefaultCorrector_NilLook(t *testing.T) {
	// Default corrector with no look should produce an identity LUT.
	c := DefaultCorrectorState()
	lut := CombineLUT(c, nil)

	identity := GenerateIdentityLUT()
	if !lutsEqual(lut, identity, 1e-5) {
		// Find the first difference for debugging.
		for i := range lut.Data {
			if math.Abs(float64(lut.Data[i]-identity.Data[i])) > 1e-5 {
				t.Fatalf("default corrector differs from identity at index %d: got %f, want %f",
					i, lut.Data[i], identity.Data[i])
			}
		}
	}
}

func TestCombineLUT_DefaultCorrector_WithLook(t *testing.T) {
	// Default corrector with a look should equal the look LUT itself.
	looks := BuiltinLooks()
	look := GenerateLook(looks[0]) // golden-hour

	c := DefaultCorrectorState()
	lut := CombineLUT(c, look)

	// The combined LUT should match the look (identity chained through look = look).
	if !lutsEqual(lut, look, 1e-4) {
		differences := 0
		maxDiff := float64(0)
		for i := range lut.Data {
			d := math.Abs(float64(lut.Data[i] - look.Data[i]))
			if d > 1e-4 {
				differences++
				if d > maxDiff {
					maxDiff = d
				}
			}
		}
		t.Fatalf("default corrector + look differs from look: %d values differ, max diff = %f",
			differences, maxDiff)
	}
}

func TestCombineLUT_CorrectorOnly(t *testing.T) {
	// Temperature=50 should produce a LUT that differs from identity.
	c := DefaultCorrectorState()
	c.Temperature = 50
	lut := CombineLUT(c, nil)

	identity := GenerateIdentityLUT()
	differences := 0
	for i := range lut.Data {
		if math.Abs(float64(lut.Data[i]-identity.Data[i])) > 1e-5 {
			differences++
		}
	}
	if differences == 0 {
		t.Error("corrector with temperature=50 is identical to identity")
	}

	// All values should remain in [0, 1].
	for i, v := range lut.Data {
		if v < 0 || v > 1 {
			t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
			break
		}
	}
}

func TestCombineLUT_Both(t *testing.T) {
	// Corrector + look should differ from look alone.
	looks := BuiltinLooks()
	look := GenerateLook(looks[0])

	c := DefaultCorrectorState()
	c.Temperature = 50
	c.Saturation = 0.8
	combined := CombineLUT(c, look)

	// Combined should differ from look-only.
	if lutsEqual(combined, look, 1e-4) {
		t.Error("corrector+look is identical to look alone (expected difference)")
	}

	// All values in range.
	for i, v := range combined.Data {
		if v < 0 || v > 1 {
			t.Errorf("data[%d] = %f, out of [0,1] range", i, v)
			break
		}
	}
}

func TestChainLUT_Identity(t *testing.T) {
	// Chaining a LUT through an identity look should not change it.
	looks := BuiltinLooks()
	original := GenerateLook(looks[0])

	// Make a copy of the original.
	lut := &LUT3D{
		Size: original.Size,
		Data: make([]float32, len(original.Data)),
	}
	copy(lut.Data, original.Data)

	identity := GenerateIdentityLUT()
	chainLUT(lut, identity)

	if !lutsEqual(lut, original, 1e-4) {
		differences := 0
		maxDiff := float64(0)
		for i := range lut.Data {
			d := math.Abs(float64(lut.Data[i] - original.Data[i]))
			if d > 1e-4 {
				differences++
				if d > maxDiff {
					maxDiff = d
				}
			}
		}
		t.Fatalf("chaining through identity changed LUT: %d values differ, max diff = %f",
			differences, maxDiff)
	}
}

// ─── Corrector transform function tests ─────────────────────────────────────

func TestApplyTemperature_Warm(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplyTemperature(lut, 100)

	// Midpoint should have boosted red and reduced blue.
	mid := (16*33*33 + 16*33 + 16) * 3
	r, b := lut.Data[mid], lut.Data[mid+2]
	if r <= 0.5 {
		t.Errorf("warm temperature: R at midpoint = %f, expected > 0.5", r)
	}
	if b >= 0.5 {
		t.Errorf("warm temperature: B at midpoint = %f, expected < 0.5", b)
	}
}

func TestApplyTemperature_Zero(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	ApplyTemperature(lut, 0)

	for i := range lut.Data {
		if lut.Data[i] != original[i] {
			t.Fatalf("zero temperature changed data at index %d", i)
		}
	}
}

func TestApplyTint_Magenta(t *testing.T) {
	lut := GenerateIdentityLUT()
	ApplyTint(lut, 100)

	// Midpoint should have reduced green.
	mid := (16*33*33 + 16*33 + 16) * 3
	g := lut.Data[mid+1]
	if g >= 0.5 {
		t.Errorf("magenta tint: G at midpoint = %f, expected < 0.5", g)
	}
}

func TestApplyShadowsMidtonesHighlights(t *testing.T) {
	lut := GenerateIdentityLUT()

	// Boost shadows red, cut highlights blue.
	shadows := [3]float32{1.0, 0, 0}
	midtones := [3]float32{0, 0, 0}
	highlights := [3]float32{0, 0, -1.0}
	ApplyShadowsMidtonesHighlights(lut, shadows, midtones, highlights)

	// Dark point (index 4,4,4): shadow region, red should be boosted.
	darkIdx := (4*33*33 + 4*33 + 4) * 3
	darkR := lut.Data[darkIdx]
	identity := float32(4) / 32.0
	if darkR <= identity {
		t.Errorf("shadow red boost: dark R = %f, expected > %f", darkR, identity)
	}

	// Bright point (index 28,28,28): highlight region, blue should be cut.
	brightIdx := (28*33*33 + 28*33 + 28) * 3
	brightB := lut.Data[brightIdx+2]
	identityB := float32(28) / 32.0
	if brightB >= identityB {
		t.Errorf("highlight blue cut: bright B = %f, expected < %f", brightB, identityB)
	}
}

func TestApplyShadowsMidtonesHighlights_AllZero(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	zero := [3]float32{0, 0, 0}
	ApplyShadowsMidtonesHighlights(lut, zero, zero, zero)

	for i := range lut.Data {
		if lut.Data[i] != original[i] {
			t.Fatalf("all-zero SMH changed data at index %d", i)
		}
	}
}

func TestApplyCurves_Master(t *testing.T) {
	lut := GenerateIdentityLUT()

	// S-curve: darken shadows, brighten highlights.
	master := []CurvePoint{{0, 0}, {0.25, 0.15}, {0.75, 0.85}, {1, 1}}
	ApplyCurves(lut, master, nil, nil, nil)

	// Midpoint should be approximately 0.5 (curve passes through it).
	mid := (16*33*33 + 16*33 + 16) * 3
	r := lut.Data[mid]
	if math.Abs(float64(r-0.5)) > 0.05 {
		t.Errorf("master curve midpoint R = %f, expected ~0.5", r)
	}

	// Dark quarter should be darkened.
	darkIdx := (8*33*33 + 8*33 + 8) * 3
	darkR := lut.Data[darkIdx]
	identityDark := float32(8) / 32.0
	if darkR >= identityDark {
		t.Errorf("master curve: dark R = %f, expected < %f", darkR, identityDark)
	}
}

func TestApplyCurves_PerChannel(t *testing.T) {
	lut := GenerateIdentityLUT()

	// Boost red channel curve.
	curveR := []CurvePoint{{0, 0.1}, {1, 1}}
	ApplyCurves(lut, nil, curveR, nil, nil)

	// Black point should have boosted red.
	if lut.Data[0] < 0.09 {
		t.Errorf("per-channel curve: black R = %f, expected >= 0.1", lut.Data[0])
	}
}

func TestApplyCurves_Empty(t *testing.T) {
	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	ApplyCurves(lut, nil, nil, nil, nil)

	for i := range lut.Data {
		if lut.Data[i] != original[i] {
			t.Fatalf("empty curves changed data at index %d", i)
		}
	}
}

func TestEvalCurve_Endpoints(t *testing.T) {
	pts := []CurvePoint{{0.2, 0.3}, {0.8, 0.9}}

	// Below range: clamp to first Y.
	if v := evalCurve(pts, 0.0); v != 0.3 {
		t.Errorf("below range: got %f, want 0.3", v)
	}
	// Above range: clamp to last Y.
	if v := evalCurve(pts, 1.0); v != 0.9 {
		t.Errorf("above range: got %f, want 0.9", v)
	}
	// At midpoint: linear interpolation.
	mid := evalCurve(pts, 0.5)
	expected := float32(0.6) // 0.3 + (0.5-0.2)/(0.8-0.2) * (0.9-0.3)
	if math.Abs(float64(mid-expected)) > 1e-5 {
		t.Errorf("midpoint: got %f, want %f", mid, expected)
	}
}

func TestLutsEqual(t *testing.T) {
	a := GenerateIdentityLUT()
	b := GenerateIdentityLUT()

	if !lutsEqual(a, b, 1e-6) {
		t.Error("two identity LUTs should be equal")
	}

	b.Data[0] = 0.5
	if lutsEqual(a, b, 1e-6) {
		t.Error("different LUTs should not be equal")
	}
}
