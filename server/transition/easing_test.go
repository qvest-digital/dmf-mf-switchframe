package transition

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// allPresets returns every non-custom EasingType for table-driven tests.
func allPresets() []EasingType {
	return []EasingType{
		EasingLinear,
		EasingEase,
		EasingEaseIn,
		EasingEaseOut,
		EasingEaseInOut,
		EasingSmoothstep,
	}
}

// --- 1. Endpoint invariants ---

func TestEasingEndpoints(t *testing.T) {
	// Every preset and a custom curve must satisfy Ease(0)==0, Ease(1)==1.
	presets := allPresets()
	for _, et := range presets {
		t.Run(string(et), func(t *testing.T) {
			c := NewEasingCurve(et)
			require.NotNil(t, c)
			require.InDelta(t, 0.0, c.Ease(0.0), 1e-9, "Ease(0) should be 0")
			require.InDelta(t, 1.0, c.Ease(1.0), 1e-9, "Ease(1) should be 1")
		})
	}

	t.Run("custom", func(t *testing.T) {
		c, err := NewCustomEasingCurve(0.25, 0.1, 0.25, 1.0)
		require.NoError(t, err)
		require.InDelta(t, 0.0, c.Ease(0.0), 1e-9, "Ease(0) should be 0")
		require.InDelta(t, 1.0, c.Ease(1.0), 1e-9, "Ease(1) should be 1")
	})
}

// --- 2. Linear identity ---

func TestEasingLinearIdentity(t *testing.T) {
	c := NewEasingCurve(EasingLinear)
	for i := 0; i <= 100; i++ {
		tt := float64(i) / 100.0
		got := c.Ease(tt)
		require.InDelta(t, tt, got, 1e-12, "Linear Ease(%v) should be %v", tt, tt)
	}
}

// --- 3. Smoothstep backward compatibility ---

func TestEasingSmoothstepBackwardCompat(t *testing.T) {
	c := NewEasingCurve(EasingSmoothstep)
	for i := 0; i <= 1000; i++ {
		tt := float64(i) / 1000.0
		expected := tt * tt * (3.0 - 2.0*tt)
		got := c.Ease(tt)
		require.InDelta(t, expected, got, 1e-9,
			"Smoothstep Ease(%v): expected %v, got %v", tt, expected, got)
	}
}

// --- 4. Monotonicity ---

func TestEasingMonotonicity(t *testing.T) {
	// All standard presets (no overshoot) must be monotonically non-decreasing.
	presets := allPresets()
	const n = 1000
	for _, et := range presets {
		t.Run(string(et), func(t *testing.T) {
			c := NewEasingCurve(et)
			prev := c.Ease(0.0)
			for i := 1; i <= n; i++ {
				tt := float64(i) / float64(n)
				cur := c.Ease(tt)
				require.GreaterOrEqual(t, cur, prev-1e-9,
					"%s: not monotonic at t=%v: %v > %v", et, tt, prev, cur)
				prev = cur
			}
		})
	}
}

// --- 5. CSS preset accuracy ---

func TestEasingCSSPresetAccuracy(t *testing.T) {
	// Reference values computed with the CSS cubic-bezier specification.
	// Tolerance ±0.02 as implementations vary slightly.
	tests := []struct {
		name     EasingType
		t025     float64
		t050     float64
		t075     float64
	}{
		// ease: cubic-bezier(0.25, 0.1, 0.25, 1.0)
		{EasingEase, 0.409, 0.802, 0.960},
		// ease-in: cubic-bezier(0.42, 0, 1.0, 1.0)
		{EasingEaseIn, 0.093, 0.315, 0.622},
		// ease-out: cubic-bezier(0, 0, 0.58, 1.0)
		{EasingEaseOut, 0.378, 0.685, 0.907},
		// ease-in-out: cubic-bezier(0.42, 0, 0.58, 1.0)
		{EasingEaseInOut, 0.129, 0.500, 0.871},
	}

	for _, tc := range tests {
		t.Run(string(tc.name), func(t *testing.T) {
			c := NewEasingCurve(tc.name)

			got025 := c.Ease(0.25)
			require.InDelta(t, tc.t025, got025, 0.02,
				"%s at t=0.25: expected ~%v, got %v", tc.name, tc.t025, got025)

			got050 := c.Ease(0.50)
			require.InDelta(t, tc.t050, got050, 0.02,
				"%s at t=0.50: expected ~%v, got %v", tc.name, tc.t050, got050)

			got075 := c.Ease(0.75)
			require.InDelta(t, tc.t075, got075, 0.02,
				"%s at t=0.75: expected ~%v, got %v", tc.name, tc.t075, got075)
		})
	}
}

// --- 6. Custom bezier matches "ease" preset ---

func TestEasingCustomMatchesEase(t *testing.T) {
	preset := NewEasingCurve(EasingEase)
	custom, err := NewCustomEasingCurve(0.25, 0.1, 0.25, 1.0) // same control points as "ease"
	require.NoError(t, err)

	for i := 0; i <= 100; i++ {
		tt := float64(i) / 100.0
		expected := preset.Ease(tt)
		got := custom.Ease(tt)
		require.InDelta(t, expected, got, 1e-9,
			"Custom vs Ease preset at t=%v: %v vs %v", tt, expected, got)
	}
}

// --- 7. Overshoot ---

func TestEasingOvershoot(t *testing.T) {
	// cubic-bezier(0.68, -0.55, 0.265, 1.55) — a common "back" easing.
	// Y values outside [0,1] are valid; output CAN exceed [0,1].
	c, err := NewCustomEasingCurve(0.68, -0.55, 0.265, 1.55)
	require.NoError(t, err)

	// Endpoints must still be exact.
	require.InDelta(t, 0.0, c.Ease(0.0), 1e-9)
	require.InDelta(t, 1.0, c.Ease(1.0), 1e-9)

	// There should be at least one value < 0 or > 1 in the interior.
	hasOvershoot := false
	for i := 1; i < 100; i++ {
		tt := float64(i) / 100.0
		y := c.Ease(tt)
		if y < -0.01 || y > 1.01 {
			hasOvershoot = true
			break
		}
	}
	require.True(t, hasOvershoot, "Overshoot curve should produce values outside [0,1]")
}

// --- 8. Invalid x values ---

func TestEasingInvalidX(t *testing.T) {
	tests := []struct {
		name         string
		x1, y1, x2, y2 float64
	}{
		{"x1 negative", -0.1, 0.0, 0.5, 1.0},
		{"x1 above 1", 1.1, 0.0, 0.5, 1.0},
		{"x2 negative", 0.5, 0.0, -0.1, 1.0},
		{"x2 above 1", 0.5, 0.0, 1.1, 1.0},
		{"both x invalid", -0.1, 0.0, 1.1, 1.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewCustomEasingCurve(tc.x1, tc.y1, tc.x2, tc.y2)
			require.Error(t, err)
			require.Nil(t, c)
			require.Contains(t, err.Error(), "outside [0, 1]")
		})
	}
}

// --- 9. Nil safety ---

func TestEasingNilSafety(t *testing.T) {
	// A nil *EasingCurve should not panic. Document: it returns t (linear fallback).
	var c *EasingCurve
	require.NotPanics(t, func() {
		result := c.Ease(0.5)
		// Nil receiver falls back to linear: returns t.
		require.Equal(t, 0.5, result)
	})
}

// --- Additional: ValidEasingTypes coverage ---

func TestValidEasingTypes(t *testing.T) {
	expected := []EasingType{
		EasingLinear, EasingEase, EasingEaseIn, EasingEaseOut,
		EasingEaseInOut, EasingSmoothstep, EasingCustom,
	}
	for _, et := range expected {
		require.True(t, ValidEasingTypes[et], "%s should be in ValidEasingTypes", et)
	}
	require.False(t, ValidEasingTypes["nonexistent"])
}

// --- Additional: NewEasingCurve returns correct type field ---

func TestEasingCurveTypeField(t *testing.T) {
	for _, et := range allPresets() {
		c := NewEasingCurve(et)
		require.Equal(t, et, c.Type)
	}

	c, err := NewCustomEasingCurve(0.1, 0.2, 0.3, 0.4)
	require.NoError(t, err)
	require.Equal(t, EasingCustom, c.Type)
}

// --- Additional: Ease clamps input t to [0,1] ---

func TestEasingClampInput(t *testing.T) {
	c := NewEasingCurve(EasingEase)
	// Values outside [0,1] should be clamped.
	require.InDelta(t, 0.0, c.Ease(-0.5), 1e-9, "t<0 should clamp to 0")
	require.InDelta(t, 1.0, c.Ease(1.5), 1e-9, "t>1 should clamp to 1")
}

// --- Additional: Symmetry of ease-in-out ---

func TestEasingEaseInOutSymmetry(t *testing.T) {
	c := NewEasingCurve(EasingEaseInOut)
	// ease-in-out should be symmetric: Ease(t) + Ease(1-t) ≈ 1
	for i := 0; i <= 50; i++ {
		tt := float64(i) / 100.0
		sum := c.Ease(tt) + c.Ease(1.0-tt)
		require.InDelta(t, 1.0, sum, 0.02,
			"ease-in-out symmetry broken at t=%v: Ease(%v)+Ease(%v)=%v",
			tt, tt, 1.0-tt, sum)
	}
}

// --- Additional: Smoothstep symmetry ---

func TestEasingSmoothstepSymmetry(t *testing.T) {
	c := NewEasingCurve(EasingSmoothstep)
	require.InDelta(t, 0.5, c.Ease(0.5), 1e-9, "Smoothstep(0.5) should be 0.5")
	for i := 0; i <= 50; i++ {
		tt := float64(i) / 100.0
		sum := c.Ease(tt) + c.Ease(1.0-tt)
		require.InDelta(t, 1.0, sum, 1e-9,
			"Smoothstep symmetry broken at t=%v", tt)
	}
}

// --- Additional: x boundary values (0 and 1) are valid ---

func TestEasingXBoundaryValues(t *testing.T) {
	// x1=0, x2=1 should be valid (these are the boundary values).
	c, err := NewCustomEasingCurve(0.0, 0.5, 1.0, 0.5)
	require.NoError(t, err)
	require.NotNil(t, c)

	// x1=1, x2=0 should also be valid.
	c2, err := NewCustomEasingCurve(1.0, 0.5, 0.0, 0.5)
	require.NoError(t, err)
	require.NotNil(t, c2)
}

// --- 10. Benchmarks ---

func BenchmarkEaseSmoothstep(b *testing.B) {
	c := NewEasingCurve(EasingSmoothstep)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Ease(float64(i%1000) / 1000.0)
	}
}

func BenchmarkEaseLinear(b *testing.B) {
	c := NewEasingCurve(EasingLinear)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Ease(float64(i%1000) / 1000.0)
	}
}

func BenchmarkEaseCSS(b *testing.B) {
	c := NewEasingCurve(EasingEaseInOut)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Ease(float64(i%1000) / 1000.0)
	}
}

func BenchmarkEaseCustom(b *testing.B) {
	c, _ := NewCustomEasingCurve(0.68, -0.55, 0.265, 1.55)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Ease(float64(i%1000) / 1000.0)
	}
}

// BenchmarkEaseAllPresets benchmarks every preset to compare relative cost.
func BenchmarkEaseAllPresets(b *testing.B) {
	for _, et := range allPresets() {
		b.Run(string(et), func(b *testing.B) {
			c := NewEasingCurve(et)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Ease(float64(i%1000) / 1000.0)
			}
		})
	}
}

// TestEasingCurvePrint verifies String() for debugging.
func TestEasingCurveString(t *testing.T) {
	c := NewEasingCurve(EasingEase)
	s := fmt.Sprintf("%v", c.Type)
	require.Equal(t, "ease", s)

	custom, _ := NewCustomEasingCurve(0.1, 0.2, 0.3, 0.4)
	require.Equal(t, EasingCustom, custom.Type)
}

// TestEasingNewEasingCurveUnknownType verifies unknown type falls back to linear.
func TestEasingNewEasingCurveUnknownType(t *testing.T) {
	c := NewEasingCurve(EasingType("nonexistent"))
	// Should fallback to linear.
	require.NotNil(t, c)
	for i := 0; i <= 100; i++ {
		tt := float64(i) / 100.0
		require.InDelta(t, tt, c.Ease(tt), 1e-12, "Unknown type should fallback to linear")
	}
}

// TestEasingSubNormalInputs verifies tiny and near-1 inputs.
func TestEasingSubNormalInputs(t *testing.T) {
	curves := []*EasingCurve{
		NewEasingCurve(EasingEase),
		NewEasingCurve(EasingSmoothstep),
		NewEasingCurve(EasingLinear),
	}
	tinyInputs := []float64{1e-15, 1e-10, 1e-7, 1 - 1e-7, 1 - 1e-10, 1 - 1e-15}

	for _, c := range curves {
		t.Run(string(c.Type), func(t *testing.T) {
			for _, tt := range tinyInputs {
				result := c.Ease(tt)
				require.False(t, math.IsNaN(result), "NaN at t=%e", tt)
				require.False(t, math.IsInf(result, 0), "Inf at t=%e", tt)
			}
		})
	}
}
