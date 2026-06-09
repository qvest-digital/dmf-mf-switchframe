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

// TestApplyCurves_SplineSetupHoisted guards against rebuilding the Hermite
// spline (slice allocations + Fritsch-Carlson tangent computation) for every
// one of the 35937 LUT entries. The spline setup depends only on the control
// points, so it must be computed once per curve per ApplyCurves call — this
// runs lazily under the SourceCorrectorManager lock on the render path, where
// ~860k allocations per rebuild blow the frame budget.
func TestApplyCurves_SplineSetupHoisted(t *testing.T) {
	curve := []CurvePoint{{0, 0}, {0.25, 0.2}, {0.5, 0.5}, {0.75, 0.8}, {1, 1}}
	lut := GenerateIdentityLUT()

	allocs := testing.AllocsPerRun(2, func() {
		ApplyCurves(lut, curve, curve, curve, curve)
	})

	// A handful of allocations per curve (spline setup) is fine; per-sample
	// allocation is not.
	if allocs > 64 {
		t.Fatalf("ApplyCurves allocated %.0f times; spline setup is being rebuilt per LUT sample", allocs)
	}
}

// TestApplyCurves_MatchesEvalCurve verifies that the LUT produced by
// ApplyCurves agrees with per-sample evalCurve composition (master then
// per-channel), so hoisting the spline setup cannot change the output.
func TestApplyCurves_MatchesEvalCurve(t *testing.T) {
	master := []CurvePoint{{0, 0.05}, {0.4, 0.3}, {0.6, 0.7}, {1, 0.95}}
	curveR := []CurvePoint{{0, 0}, {0.5, 0.6}, {1, 1}}
	curveG := []CurvePoint{{0, 0.1}, {1, 0.9}}

	lut := GenerateIdentityLUT()
	original := make([]float32, len(lut.Data))
	copy(original, lut.Data)

	ApplyCurves(lut, master, curveR, curveG, nil)

	for i := 0; i < len(lut.Data); i += 3 {
		r := evalCurve(curveR, evalCurve(master, original[i]))
		g := evalCurve(curveG, evalCurve(master, original[i+1]))
		b := evalCurve(master, original[i+2])

		wantR := sanitizeCurveOutput(r, original[i])
		wantG := sanitizeCurveOutput(g, original[i+1])
		wantB := sanitizeCurveOutput(b, original[i+2])

		if math.Abs(float64(lut.Data[i]-wantR)) > 1e-6 ||
			math.Abs(float64(lut.Data[i+1]-wantG)) > 1e-6 ||
			math.Abs(float64(lut.Data[i+2]-wantB)) > 1e-6 {
			t.Fatalf("entry %d: got [%f %f %f], want [%f %f %f]", i/3,
				lut.Data[i], lut.Data[i+1], lut.Data[i+2], wantR, wantG, wantB)
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
	// At midpoint: linear interpolation (2-point curves are linear).
	mid := evalCurve(pts, 0.5)
	expected := float32(0.6) // 0.3 + (0.5-0.2)/(0.8-0.2) * (0.9-0.3)
	if math.Abs(float64(mid-expected)) > 1e-5 {
		t.Errorf("midpoint: got %f, want %f", mid, expected)
	}
}

func TestEvalCurve_MonotoneCubicNotLinear(t *testing.T) {
	// With 3+ control points, the cubic spline between two control points
	// should NOT equal simple piecewise-linear interpolation.
	// Use an asymmetric curve where the tangent influence is visible.
	pts := []CurvePoint{{0, 0}, {0.3, 0.5}, {0.7, 0.6}, {1, 1}}

	// Evaluate at x=0.5 (midpoint of the middle segment).
	// Piecewise-linear would give: 0.5 + (0.5-0.3)/(0.7-0.3) * (0.6-0.5) = 0.55
	linearMid := float32(0.55)
	cubicMid := evalCurve(pts, 0.5)

	// The cubic result should differ from linear by a meaningful amount.
	diff := math.Abs(float64(cubicMid - linearMid))
	if diff < 0.001 {
		t.Errorf("cubic midpoint = %f, same as linear %f (diff=%f); expected cubic to differ",
			cubicMid, linearMid, diff)
	}

	// The curve should still be smooth and in a reasonable range.
	if cubicMid < 0 || cubicMid > 1 {
		t.Errorf("cubic midpoint = %f, out of [0,1] range", cubicMid)
	}
}

func TestEvalCurve_MonotoneCubicSmoothness(t *testing.T) {
	// Test that the cubic interpolation produces a smooth curve by checking
	// that intermediate samples don't have discontinuities.
	pts := []CurvePoint{{0, 0}, {0.3, 0.5}, {0.7, 0.6}, {1, 1}}

	const numSamples = 100
	prev := evalCurve(pts, 0)
	maxJump := float64(0)
	for i := 1; i <= numSamples; i++ {
		x := float32(i) / float32(numSamples)
		y := evalCurve(pts, x)
		jump := math.Abs(float64(y - prev))
		if jump > maxJump {
			maxJump = jump
		}
		prev = y
	}

	// With smooth cubic interpolation, the maximum jump between adjacent
	// samples (1/100 apart) should be small. Linear interpolation on this
	// curve would have max jump ~0.016; cubic should be similar or smaller.
	if maxJump > 0.05 {
		t.Errorf("max jump between adjacent samples = %f, too large for smooth curve", maxJump)
	}
}

func TestEvalCurve_MonotoneCubicPassesThroughControlPoints(t *testing.T) {
	// The spline must pass exactly through every control point.
	pts := []CurvePoint{{0, 0}, {0.25, 0.4}, {0.5, 0.3}, {0.75, 0.8}, {1, 1}}

	for _, pt := range pts {
		got := evalCurve(pts, pt.X)
		if math.Abs(float64(got-pt.Y)) > 1e-5 {
			t.Errorf("evalCurve(pts, %f) = %f, want %f (control point)", pt.X, got, pt.Y)
		}
	}
}

func TestEvalCurve_MonotoneCubicIdentityCurve(t *testing.T) {
	// Identity curve with interior points should be close to y=x.
	pts := []CurvePoint{{0, 0}, {0.25, 0.25}, {0.5, 0.5}, {0.75, 0.75}, {1, 1}}

	for i := 0; i <= 20; i++ {
		x := float32(i) / 20.0
		y := evalCurve(pts, x)
		if math.Abs(float64(y-x)) > 0.01 {
			t.Errorf("identity curve at x=%f: got %f, expected ~%f", x, y, x)
		}
	}
}

func TestEvalCurve_EmptyAndSingle(t *testing.T) {
	// Empty: passthrough.
	if v := evalCurve(nil, 0.5); v != 0.5 {
		t.Errorf("nil curve: got %f, want 0.5", v)
	}

	// Single point: clamp.
	single := []CurvePoint{{0.5, 0.7}}
	if v := evalCurve(single, 0.3); v != 0.7 {
		t.Errorf("single point below: got %f, want 0.7", v)
	}
	if v := evalCurve(single, 0.8); v != 0.7 {
		t.Errorf("single point above: got %f, want 0.7", v)
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

// TestEvalCurve_DuplicateXNoNaN verifies the defensive h==0 guard: a curve with
// a zero-width segment (duplicate X) must not yield NaN/Inf, which clampf would
// not remove and would corrupt the GPU-uploaded LUT.
func TestEvalCurve_DuplicateXNoNaN(t *testing.T) {
	// Duplicate X at an interior segment that contains the sampled x.
	pts := []CurvePoint{{0, 0}, {0.5, 0.4}, {0.5, 0.6}, {1, 1}}

	for i := 0; i <= 100; i++ {
		x := float32(i) / 100.0
		y := evalCurve(pts, x)
		if math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) {
			t.Fatalf("evalCurve(dupX, %f) = %v, want finite value", x, y)
		}
	}
}

// TestApplyCurves_MalformedCurveNoNaN verifies that even if a malformed curve
// reaches ApplyCurves, the resulting LUT contains no NaN/Inf entries.
func TestApplyCurves_MalformedCurveNoNaN(t *testing.T) {
	lut := GenerateIdentityLUT()
	// Duplicate X (zero-width segment) on the master curve.
	master := []CurvePoint{{0, 0}, {0.5, 0.2}, {0.5, 0.8}, {1, 1}}
	ApplyCurves(lut, master, nil, nil, nil)

	for i, v := range lut.Data {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("LUT entry %d is non-finite: %v", i, v)
		}
		if v < 0 || v > 1 {
			t.Fatalf("LUT entry %d out of [0,1]: %v", i, v)
		}
	}
}

func TestMaxCurvePoints(t *testing.T) {
	if MaxCurvePoints <= 1 {
		t.Errorf("MaxCurvePoints must be > 1, got %d", MaxCurvePoints)
	}
}
