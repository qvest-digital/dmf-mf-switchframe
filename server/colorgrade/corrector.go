package colorgrade

// CurvePoint defines a single control point on a transfer curve.
type CurvePoint struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
}

// ApplyTemperature shifts color temperature of a LUT.
// amount: -100 (cool/blue) to +100 (warm/amber). 0 = no change.
// Scale: ±15% slope change at ±100.
func ApplyTemperature(lut *LUT3D, amount float32) {
	if amount == 0 {
		return
	}
	// Normalize to [-1, +1] and scale to ±15%.
	norm := amount / 100.0
	rSlope := 1.0 + norm*0.15 // warm: boost red, cool: cut red
	bSlope := 1.0 - norm*0.15 // warm: cut blue, cool: boost blue

	for i := 0; i < len(lut.Data); i += 3 {
		lut.Data[i] = clampf(lut.Data[i]*rSlope, 0, 1)
		lut.Data[i+2] = clampf(lut.Data[i+2]*bSlope, 0, 1)
	}
}

// ApplyTint shifts the green-magenta axis of a LUT.
// amount: -100 (green) to +100 (magenta). 0 = no change.
func ApplyTint(lut *LUT3D, amount float32) {
	if amount == 0 {
		return
	}
	// Normalize to [-1, +1] and scale to ±15%.
	norm := amount / 100.0

	// Green direction (negative amount): boost green slope.
	// Magenta direction (positive amount): cut green slope, slight boost to red+blue.
	gSlope := 1.0 - norm*0.15
	rSlope := float32(1.0)
	bSlope := float32(1.0)
	if norm > 0 {
		// Magenta: slight red+blue boost (half the green cut).
		rSlope = 1.0 + norm*0.075
		bSlope = 1.0 + norm*0.075
	}

	for i := 0; i < len(lut.Data); i += 3 {
		lut.Data[i] = clampf(lut.Data[i]*rSlope, 0, 1)
		lut.Data[i+1] = clampf(lut.Data[i+1]*gSlope, 0, 1)
		lut.Data[i+2] = clampf(lut.Data[i+2]*bSlope, 0, 1)
	}
}

// smoothstep computes the standard smoothstep: clamp((x-edge0)/(edge1-edge0), 0, 1) then 3t^2 - 2t^3.
func smoothstep(edge0, edge1, x float32) float32 {
	t := (x - edge0) / (edge1 - edge0)
	t = clampf(t, 0, 1)
	return t * t * (3 - 2*t)
}

// ApplyShadowsMidtonesHighlights applies luminance-zoned RGB adjustments to a LUT.
// shadows, midtones, highlights are [R,G,B] offsets in [-1, +1].
// Uses BT.709 luminance and soft smoothstep zone weights.
func ApplyShadowsMidtonesHighlights(lut *LUT3D, shadows, midtones, highlights [3]float32) {
	// Quick check: if all offsets are zero, skip.
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

	const scale = float32(0.2) // Scale factor for reasonable range.

	for i := 0; i < len(lut.Data); i += 3 {
		r := lut.Data[i]
		g := lut.Data[i+1]
		b := lut.Data[i+2]

		// BT.709 luminance.
		lum := 0.2126*r + 0.7152*g + 0.0722*b

		// Zone weights via smoothstep.
		// Shadows: weight is high when lum < 0.35, falling off toward 0.5.
		shadowW := 1.0 - smoothstep(0.2, 0.5, lum)
		// Highlights: weight is high when lum > 0.65, rising from 0.5.
		highlightW := smoothstep(0.5, 0.8, lum)
		// Midtones: fills the gap (peaks around 0.5).
		midtoneW := 1.0 - shadowW - highlightW
		if midtoneW < 0 {
			midtoneW = 0
		}

		for ch := 0; ch < 3; ch++ {
			offset := shadowW*shadows[ch] + midtoneW*midtones[ch] + highlightW*highlights[ch]
			lut.Data[i+ch] = clampf(lut.Data[i+ch]+offset*scale, 0, 1)
		}
	}
}

// buildCurveLUT builds a 256-entry lookup table from a set of control points
// using linear interpolation. Returns identity if points is empty.
func buildCurveLUT(points []CurvePoint) [256]float32 {
	var table [256]float32

	if len(points) == 0 {
		// Identity.
		for i := range table {
			table[i] = float32(i) / 255.0
		}
		return table
	}

	// For each of the 256 entries, find the surrounding control points and lerp.
	for i := range table {
		x := float32(i) / 255.0

		// Find surrounding points.
		if x <= points[0].X {
			table[i] = clampf(points[0].Y, 0, 1)
			continue
		}
		if x >= points[len(points)-1].X {
			table[i] = clampf(points[len(points)-1].Y, 0, 1)
			continue
		}

		// Binary-ish search for the segment (points are few, linear scan is fine).
		for j := 0; j < len(points)-1; j++ {
			if x >= points[j].X && x <= points[j+1].X {
				// Linear interpolation.
				dx := points[j+1].X - points[j].X
				if dx < 1e-9 {
					table[i] = clampf(points[j].Y, 0, 1)
				} else {
					t := (x - points[j].X) / dx
					y := points[j].Y + t*(points[j+1].Y-points[j].Y)
					table[i] = clampf(y, 0, 1)
				}
				break
			}
		}
	}

	return table
}

// ApplyCurves applies per-channel transfer curves to a LUT.
// master curve applies to all channels first, then per-channel curves (r, g, b).
// An empty or nil slice is treated as identity (no change).
func ApplyCurves(lut *LUT3D, master, r, g, b []CurvePoint) {
	hasMaster := len(master) > 0
	hasR := len(r) > 0
	hasG := len(g) > 0
	hasB := len(b) > 0

	if !hasMaster && !hasR && !hasG && !hasB {
		return
	}

	var masterLUT, rLUT, gLUT, bLUT [256]float32
	if hasMaster {
		masterLUT = buildCurveLUT(master)
	}
	if hasR {
		rLUT = buildCurveLUT(r)
	}
	if hasG {
		gLUT = buildCurveLUT(g)
	}
	if hasB {
		bLUT = buildCurveLUT(b)
	}

	for i := 0; i < len(lut.Data); i += 3 {
		rv := lut.Data[i]
		gv := lut.Data[i+1]
		bv := lut.Data[i+2]

		// Apply master curve first.
		if hasMaster {
			rv = lookupCurve(masterLUT, rv)
			gv = lookupCurve(masterLUT, gv)
			bv = lookupCurve(masterLUT, bv)
		}

		// Apply per-channel curves.
		if hasR {
			rv = lookupCurve(rLUT, rv)
		}
		if hasG {
			gv = lookupCurve(gLUT, gv)
		}
		if hasB {
			bv = lookupCurve(bLUT, bv)
		}

		lut.Data[i] = rv
		lut.Data[i+1] = gv
		lut.Data[i+2] = bv
	}
}

// lookupCurve samples a 256-entry LUT with linear interpolation between entries.
func lookupCurve(table [256]float32, v float32) float32 {
	v = clampf(v, 0, 1)
	idx := v * 255.0
	lo := int(idx)
	if lo >= 255 {
		return table[255]
	}
	frac := idx - float32(lo)
	return table[lo]*(1-frac) + table[lo+1]*frac
}
