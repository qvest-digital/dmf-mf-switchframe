package colorgrade

import "math"

// CurvePoint represents a control point on a tone curve.
type CurvePoint struct {
	X float32 `json:"x"` // 0-1, input level
	Y float32 `json:"y"` // 0-1, output level
}

// ApplyTemperature shifts the color temperature of a LUT.
// Positive values warm (boost red, cut blue), negative values cool.
// Range: -100 to +100. Internally scaled to a small offset.
func ApplyTemperature(lut *LUT3D, temp float32) {
	if temp == 0 {
		return
	}
	// Scale to a reasonable offset range (±0.05 at extremes).
	scale := temp / 2000.0
	for i := 0; i < len(lut.Data); i += 3 {
		lut.Data[i] = clampf(lut.Data[i]+scale, 0, 1)     // R
		lut.Data[i+2] = clampf(lut.Data[i+2]-scale, 0, 1) // B
	}
}

// ApplyTint shifts the green-magenta tint of a LUT.
// Positive values shift toward magenta (cut green), negative toward green.
// Range: -100 to +100.
func ApplyTint(lut *LUT3D, tint float32) {
	if tint == 0 {
		return
	}
	scale := tint / 2000.0
	for i := 0; i < len(lut.Data); i += 3 {
		lut.Data[i+1] = clampf(lut.Data[i+1]-scale, 0, 1) // G
	}
}

// ApplyShadowsMidtonesHighlights applies separate RGB adjustments to
// shadow, midtone, and highlight regions using smooth luminance-based weights.
func ApplyShadowsMidtonesHighlights(lut *LUT3D, shadows, midtones, highlights [3]float32) {
	allZero := true
	for i := 0; i < 3; i++ {
		if shadows[i] != 0 || midtones[i] != 0 || highlights[i] != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return
	}

	for i := 0; i < len(lut.Data); i += 3 {
		r := lut.Data[i]
		g := lut.Data[i+1]
		b := lut.Data[i+2]

		// BT.709 luminance for zone weighting.
		lum := 0.2126*r + 0.7152*g + 0.0722*b

		// Smooth zone weights using cosine blending.
		// Shadows: strongest at lum=0, fades to 0 at lum=0.5
		// Highlights: strongest at lum=1, fades to 0 at lum=0.5
		// Midtones: peak at lum=0.5, fades toward 0 and 1
		shadowW := float32(1.0)
		highlightW := float32(0.0)
		if lum <= 0.5 {
			shadowW = 1.0 - lum*2.0
			highlightW = 0.0
		} else {
			shadowW = 0.0
			highlightW = (lum - 0.5) * 2.0
		}
		midtoneW := 1.0 - shadowW - highlightW

		// Scale adjustments (±1 maps to ±0.15 shift).
		const adjScale = 0.15
		rgb := [3]float32{r, g, b}
		for c := 0; c < 3; c++ {
			adj := shadowW*shadows[c]*adjScale +
				midtoneW*midtones[c]*adjScale +
				highlightW*highlights[c]*adjScale
			rgb[c] = clampf(rgb[c]+adj, 0, 1)
		}

		lut.Data[i] = rgb[0]
		lut.Data[i+1] = rgb[1]
		lut.Data[i+2] = rgb[2]
	}
}

// ApplyCurves applies tone curves (master + per-channel R, G, B) to a LUT.
// Each curve is defined by a set of control points; the curve is evaluated
// via monotone cubic interpolation between points. Nil or empty curves are skipped.
func ApplyCurves(lut *LUT3D, master, curveR, curveG, curveB []CurvePoint) {
	hasMaster := len(master) >= 2
	hasR := len(curveR) >= 2
	hasG := len(curveG) >= 2
	hasB := len(curveB) >= 2

	if !hasMaster && !hasR && !hasG && !hasB {
		return
	}

	for i := 0; i < len(lut.Data); i += 3 {
		r := lut.Data[i]
		g := lut.Data[i+1]
		b := lut.Data[i+2]

		// Apply master curve to all channels.
		if hasMaster {
			r = evalCurve(master, r)
			g = evalCurve(master, g)
			b = evalCurve(master, b)
		}

		// Apply per-channel curves.
		if hasR {
			r = evalCurve(curveR, r)
		}
		if hasG {
			g = evalCurve(curveG, g)
		}
		if hasB {
			b = evalCurve(curveB, b)
		}

		lut.Data[i] = clampf(r, 0, 1)
		lut.Data[i+1] = clampf(g, 0, 1)
		lut.Data[i+2] = clampf(b, 0, 1)
	}
}

// evalCurve evaluates a monotone cubic Hermite spline (Fritsch-Carlson method)
// at input value x. This matches the UI's CurveEditor.svelte algorithm exactly.
// Points must be sorted by X. Values outside the point range are clamped
// to the nearest endpoint Y value. For 2 points, falls back to linear.
func evalCurve(pts []CurvePoint, x float32) float32 {
	n := len(pts)
	if n == 0 {
		return x
	}
	if x <= pts[0].X {
		return pts[0].Y
	}
	if x >= pts[n-1].X {
		return pts[n-1].Y
	}
	// Two points: linear interpolation (matches UI behavior).
	if n == 2 {
		t := (x - pts[0].X) / (pts[1].X - pts[0].X)
		return pts[0].Y + t*(pts[1].Y-pts[0].Y)
	}

	// Compute secant slopes for each segment.
	dx := make([]float64, n-1)
	dy := make([]float64, n-1)
	m := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		dx[i] = float64(pts[i+1].X - pts[i].X)
		dy[i] = float64(pts[i+1].Y - pts[i].Y)
		if dx[i] == 0 {
			m[i] = 0
		} else {
			m[i] = dy[i] / dx[i]
		}
	}

	// Compute initial tangents (Fritsch-Carlson).
	tangents := make([]float64, n)
	tangents[0] = m[0]
	for i := 1; i < n-1; i++ {
		if m[i-1]*m[i] <= 0 {
			tangents[i] = 0
		} else {
			tangents[i] = (m[i-1] + m[i]) / 2
		}
	}
	tangents[n-1] = m[n-2]

	// Adjust tangents for monotonicity.
	for i := 0; i < n-1; i++ {
		if math.Abs(m[i]) < 1e-10 {
			tangents[i] = 0
			tangents[i+1] = 0
		} else {
			alpha := tangents[i] / m[i]
			beta := tangents[i+1] / m[i]
			s := alpha*alpha + beta*beta
			if s > 9 {
				tau := 3 / math.Sqrt(s)
				tangents[i] = tau * alpha * m[i]
				tangents[i+1] = tau * beta * m[i]
			}
		}
	}

	// Find the segment containing x.
	seg := 0
	for i := 1; i < n-1; i++ {
		if x >= pts[i].X {
			seg = i
		}
	}

	// Cubic Hermite interpolation on the segment [seg, seg+1].
	h := dx[seg]
	t := (float64(x) - float64(pts[seg].X)) / h
	t2 := t * t
	t3 := t2 * t

	// Hermite basis functions.
	h00 := 2*t3 - 3*t2 + 1
	h10 := t3 - 2*t2 + t
	h01 := -2*t3 + 3*t2
	h11 := t3 - t2

	y := h00*float64(pts[seg].Y) + h10*h*tangents[seg] +
		h01*float64(pts[seg+1].Y) + h11*h*tangents[seg+1]

	return float32(y)
}

// ─── CorrectorState ─────────────────────────────────────────────────────────

// CorrectorState holds all color corrector parameters for a single source
// or the program bus. These values combine into a single LUT via CombineLUT.
type CorrectorState struct {
	Lift        [3]float32   `json:"lift"`                  // RGB, -1 to +1
	Gamma       [3]float32   `json:"gamma"`                 // RGB, 0.1 to 4.0
	Gain        [3]float32   `json:"gain"`                  // RGB, 0 to 4.0
	Temperature float32      `json:"temperature"`           // -100 to +100
	Tint        float32      `json:"tint"`                  // -100 to +100
	Saturation  float32      `json:"saturation"`            // 0 to 2.0
	Contrast    float32      `json:"contrast"`              // -1 to +1
	Shadows     [3]float32   `json:"shadows"`               // RGB -1 to +1
	Midtones    [3]float32   `json:"midtones"`              // RGB -1 to +1
	Highlights  [3]float32   `json:"highlights"`            // RGB -1 to +1
	CurveMaster []CurvePoint `json:"curveMaster,omitempty"` // master tone curve
	CurveR      []CurvePoint `json:"curveR,omitempty"`      // red channel curve
	CurveG      []CurvePoint `json:"curveG,omitempty"`      // green channel curve
	CurveB      []CurvePoint `json:"curveB,omitempty"`      // blue channel curve
	Sharpness   float32      `json:"sharpness"`             // 0 to 1.0
}

// DefaultCorrectorState returns a neutral corrector (no color change).
func DefaultCorrectorState() CorrectorState {
	return CorrectorState{
		Lift:       [3]float32{0, 0, 0},
		Gamma:      [3]float32{1, 1, 1},
		Gain:       [3]float32{1, 1, 1},
		Saturation: 1.0,
		// All others zero-valued: Temperature, Tint, Contrast, Shadows, Midtones, Highlights, Sharpness.
		// Curve slices are nil (no curve = passthrough).
	}
}

// IsDefault returns true if the corrector state equals the neutral default.
func (c CorrectorState) IsDefault() bool {
	d := DefaultCorrectorState()
	if c.Lift != d.Lift || c.Gamma != d.Gamma || c.Gain != d.Gain {
		return false
	}
	if c.Temperature != d.Temperature || c.Tint != d.Tint {
		return false
	}
	if c.Saturation != d.Saturation || c.Contrast != d.Contrast {
		return false
	}
	if c.Shadows != d.Shadows || c.Midtones != d.Midtones || c.Highlights != d.Highlights {
		return false
	}
	if c.Sharpness != d.Sharpness {
		return false
	}
	// Slice fields: nil/empty curves are both considered "no curve".
	if len(c.CurveMaster) != 0 || len(c.CurveR) != 0 || len(c.CurveG) != 0 || len(c.CurveB) != 0 {
		return false
	}
	return true
}

// CombineLUT builds a single 33x33x33 LUT that chains all corrector adjustments
// and optionally a look LUT. Processing order:
//  1. Start with identity LUT
//  2. Apply CDL (Gain=slope, Lift=offset, Gamma=power)
//  3. Apply Temperature
//  4. Apply Tint
//  5. Apply Shadows/Midtones/Highlights
//  6. Apply Saturation
//  7. Apply Contrast
//  8. Apply Curves
//  9. Chain look LUT on top (if non-nil)
func CombineLUT(c CorrectorState, look *LUT3D) *LUT3D {
	lut := GenerateIdentityLUT()

	// 1. CDL: Gain → slope, Lift → offset, Gamma → power.
	ApplyCDL(lut,
		c.Gain[0], c.Gain[1], c.Gain[2],
		c.Lift[0], c.Lift[1], c.Lift[2],
		c.Gamma[0], c.Gamma[1], c.Gamma[2],
	)

	// 2. Temperature.
	ApplyTemperature(lut, c.Temperature)

	// 3. Tint.
	ApplyTint(lut, c.Tint)

	// 4. Shadows/Midtones/Highlights.
	ApplyShadowsMidtonesHighlights(lut, c.Shadows, c.Midtones, c.Highlights)

	// 5. Saturation.
	if c.Saturation != 1.0 {
		ApplySaturation(lut, c.Saturation)
	}

	// 6. Contrast.
	if c.Contrast != 0 {
		ApplyContrast(lut, c.Contrast)
	}

	// 7. Curves.
	ApplyCurves(lut, c.CurveMaster, c.CurveR, c.CurveG, c.CurveB)

	// 8. Chain the look LUT.
	if look != nil {
		chainLUT(lut, look)
	}

	return lut
}

// chainLUT samples each entry of lut through look via trilinear interpolation.
// For each RGB triplet in lut.Data, use it as coordinates to look up the
// corresponding output from the look LUT.
func chainLUT(lut, look *LUT3D) {
	size := float32(look.Size - 1)
	lookSize := look.Size
	lookData := look.Data

	for i := 0; i < len(lut.Data); i += 3 {
		r := lut.Data[i] * size
		g := lut.Data[i+1] * size
		b := lut.Data[i+2] * size

		// Integer floors.
		r0 := int(r)
		g0 := int(g)
		b0 := int(b)

		// Clamp to valid range.
		if r0 < 0 {
			r0 = 0
		}
		if g0 < 0 {
			g0 = 0
		}
		if b0 < 0 {
			b0 = 0
		}
		maxIdx := lookSize - 2
		if r0 > maxIdx {
			r0 = maxIdx
		}
		if g0 > maxIdx {
			g0 = maxIdx
		}
		if b0 > maxIdx {
			b0 = maxIdx
		}

		r1 := r0 + 1
		g1 := g0 + 1
		b1 := b0 + 1

		// Fractional parts.
		fr := r - float32(r0)
		fg := g - float32(g0)
		fb := b - float32(b0)

		// Clamp fractional parts.
		fr = clampf(fr, 0, 1)
		fg = clampf(fg, 0, 1)
		fb = clampf(fb, 0, 1)

		// 8-corner lookup (red-major order: index = (b*size + g)*size + r).
		idx000 := ((b0*lookSize+g0)*lookSize + r0) * 3
		idx100 := ((b0*lookSize+g0)*lookSize + r1) * 3
		idx010 := ((b0*lookSize+g1)*lookSize + r0) * 3
		idx110 := ((b0*lookSize+g1)*lookSize + r1) * 3
		idx001 := ((b1*lookSize+g0)*lookSize + r0) * 3
		idx101 := ((b1*lookSize+g0)*lookSize + r1) * 3
		idx011 := ((b1*lookSize+g1)*lookSize + r0) * 3
		idx111 := ((b1*lookSize+g1)*lookSize + r1) * 3

		// Trilinear interpolation for each channel.
		for c := 0; c < 3; c++ {
			c000 := lookData[idx000+c]
			c100 := lookData[idx100+c]
			c010 := lookData[idx010+c]
			c110 := lookData[idx110+c]
			c001 := lookData[idx001+c]
			c101 := lookData[idx101+c]
			c011 := lookData[idx011+c]
			c111 := lookData[idx111+c]

			// Lerp along R axis.
			c00 := c000 + fr*(c100-c000)
			c10 := c010 + fr*(c110-c010)
			c01 := c001 + fr*(c101-c001)
			c11 := c011 + fr*(c111-c011)

			// Lerp along G axis.
			c0 := c00 + fg*(c10-c00)
			c1 := c01 + fg*(c11-c01)

			// Lerp along B axis.
			lut.Data[i+c] = c0 + fb*(c1-c0)
		}
	}
}

// lutsEqual returns true if two LUTs have the same size and data within tolerance.
func lutsEqual(a, b *LUT3D, tol float64) bool {
	if a.Size != b.Size || len(a.Data) != len(b.Data) {
		return false
	}
	for i := range a.Data {
		if math.Abs(float64(a.Data[i]-b.Data[i])) > tol {
			return false
		}
	}
	return true
}
