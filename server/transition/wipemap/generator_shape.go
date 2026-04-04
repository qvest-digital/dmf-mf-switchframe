package wipemap

import "math"

// GenTriangle creates a triangular wipe from the named edge.
// direction: "up" (triangle points up from bottom), "down" (points down from top),
// "left" (points left from right), "right" (points right from left).
// Gradient = distance from triangle interior to exterior.
func GenTriangle(direction string) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		fw := float64(w)
		fh := float64(h)

		for y := 0; y < h; y++ {
			fy := float64(y)
			rowOff := y * w
			for x := 0; x < w; x++ {
				fx := float64(x)
				var dist float64

				switch direction {
				case "up":
					// Triangle: apex at top center, base at bottom.
					// Inside if |x - w/2| / (w/2) <= y / h
					xNorm := math.Abs(fx-fw/2) / (fw / 2)
					yNorm := fy / fh
					dist = xNorm - yNorm // negative = inside, positive = outside
				case "down":
					// Apex at bottom center, base at top.
					xNorm := math.Abs(fx-fw/2) / (fw / 2)
					yNorm := 1.0 - fy/fh
					dist = xNorm - yNorm
				case "left":
					// Apex at left center, base at right.
					yNorm := math.Abs(fy-fh/2) / (fh / 2)
					xNorm := fx / fw
					dist = yNorm - xNorm
				case "right":
					// Apex at right center, base at left.
					yNorm := math.Abs(fy-fh/2) / (fh / 2)
					xNorm := 1.0 - fx/fw
					dist = yNorm - xNorm
				default:
					dist = 0
				}

				// Map: inside (dist <= 0) = low values, outside (dist > 0) = high values.
				// Normalize: dist ranges roughly from -1 to 1.
				v := (dist + 1.0) / 2.0 * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenStar creates a star-shaped iris with the given number of points.
// Gradient = normalized distance from center to star boundary.
// innerRatio sets the inner/outer radius ratio. 0 uses default per point count:
// 5-point uses the US flag golden ratio (sin(18°)/cos(36°) ≈ 0.382).
func GenStar(points int) GeneratorFunc {
	return GenStarWithRatio(points, 0)
}

// GenStarWithRatio creates a star with explicit inner/outer radius ratio.
// The star has straight edges (line segments between outer and inner vertices),
// matching the geometry of the US flag star and standard broadcast star wipes.
func GenStarWithRatio(points int, innerRatio float64) GeneratorFunc {
	if points < 3 {
		points = 3
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		maxDim := math.Max(cx, cy)
		if maxDim < 1 {
			maxDim = 1
		}

		innerR := innerRatio
		if innerR <= 0 {
			if points == 5 {
				// US flag star: golden ratio proportions.
				// sin(18°) / cos(36°) ≈ 0.38197
				innerR = math.Sin(math.Pi/10) / math.Cos(math.Pi/5)
			} else {
				innerR = 0.3
			}
		}
		outerR := 1.0
		halfAngle := math.Pi / float64(points)

		// Precompute star vertices (alternating outer/inner), first point at top.
		numVerts := points * 2
		vx := make([]float64, numVerts)
		vy := make([]float64, numVerts)
		for i := 0; i < numVerts; i++ {
			a := -math.Pi/2.0 + float64(i)*halfAngle
			r := outerR
			if i%2 == 1 {
				r = innerR
			}
			vx[i] = r * math.Cos(a)
			vy[i] = r * math.Sin(a)
		}

		for y := 0; y < h; y++ {
			py := (float64(y) - cy) / maxDim
			rowOff := y * w
			for x := 0; x < w; x++ {
				px := (float64(x) - cx) / maxDim
				r := math.Sqrt(px*px + py*py)

				// Find the star boundary radius along the ray from origin to (px, py).
				// For each edge of the star, intersect the ray with the edge line.
				// The boundary is the nearest intersection.
				starR := outerR // fallback
				if r > 1e-9 {
					// Ray direction (not normalized — origin to pixel).
					// Find which sector: angle from -π to π, offset so vertex 0 is at top.
					angle := math.Atan2(py, px) + math.Pi/2.0
					if angle < 0 {
						angle += 2 * math.Pi
					}
					if angle >= 2*math.Pi {
						angle -= 2 * math.Pi
					}
					// Sector index
					si := int(angle / halfAngle)
					if si >= numVerts {
						si = numVerts - 1
					}
					ni := (si + 1) % numVerts

					// Ray: P(λ) = λ * (px, py)
					// Edge: Q(s) = V[si] + s * (V[ni] - V[si])
					// Solve: λ * px = vx[si] + s * (vx[ni] - vx[si])
					//         λ * py = vy[si] + s * (vy[ni] - vy[si])
					// Using Cramer's rule:
					ex := vx[ni] - vx[si]
					ey := vy[ni] - vy[si]
					denom := px*ey - py*ex
					if math.Abs(denom) > 1e-12 {
						lambda := (vx[si]*ey - vy[si]*ex) / denom
						if lambda > 0 {
							// Boundary radius = distance from origin to intersection point.
							starR = lambda * r
						}
					}
				}

				if starR < 1e-12 {
					starR = outerR
				}

				v := r / starR * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenCross creates a cross/plus shape.
// If rotated is true, the cross is rotated 45° to form an X.
// Gradient = distance from cross arms.
func GenCross(rotated bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		armW := 0.2 // arm width as fraction of dimension

		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx

				var distH, distV float64
				if rotated {
					// 45° rotated: use diagonal axes
					u := (dx + dy) / math.Sqrt(2)
					v := (dx - dy) / math.Sqrt(2)
					distH = math.Abs(v) / (float64(h) / 2.0)
					distV = math.Abs(u) / (float64(w) / 2.0)
				} else {
					distH = math.Abs(dy) / (float64(h) / 2.0)
					distV = math.Abs(dx) / (float64(w) / 2.0)
				}

				// Distance to cross: min distance to either arm.
				dCross := math.Min(distH, distV)
				// Subtract arm width to get signed distance.
				signed := dCross - armW
				// Map to 0-255: inside arm = low, outside = high.
				v := (signed + armW) / (1.0 + armW) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenPolygon creates a regular polygon iris with the given number of sides.
// Gradient = distance from center normalized by distance to polygon edge.
func GenPolygon(sides int) GeneratorFunc {
	if sides < 3 {
		sides = 3
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		maxDim := math.Max(cx, cy)
		if maxDim < 1 {
			maxDim = 1
		}

		nf := float64(sides)
		sectorAngle := 2 * math.Pi / nf

		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				r := math.Sqrt(dx*dx+dy*dy) / maxDim
				angle := math.Atan2(dy, dx)
				if angle < 0 {
					angle += 2 * math.Pi
				}

				// Distance to polygon edge at this angle:
				// For a regular polygon, edge distance = cos(π/n) / cos(angle mod sectorAngle - π/n)
				sectorPos := math.Mod(angle, sectorAngle) - sectorAngle/2
				edgeR := math.Cos(math.Pi/nf) / math.Cos(sectorPos)

				v := r / edgeR * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenHeart creates a heart-shaped iris using the parametric heart curve.
func GenHeart() GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) * 0.45 // Heart center slightly above middle

		// Precompute heart boundary points for distance field.
		// Parametric heart: x = 16sin³(t), y = -(13cos(t) - 5cos(2t) - 2cos(3t) - cos(4t))
		// We negate y because screen y goes down.
		const nSamples = 360
		heartX := make([]float64, nSamples)
		heartY := make([]float64, nSamples)
		maxExtent := 0.0
		for i := 0; i < nSamples; i++ {
			t := float64(i) / float64(nSamples) * 2 * math.Pi
			sinT := math.Sin(t)
			hx := 16 * sinT * sinT * sinT
			hy := -(13*math.Cos(t) - 5*math.Cos(2*t) - 2*math.Cos(3*t) - math.Cos(4*t))
			heartX[i] = hx
			heartY[i] = hy
			ext := math.Max(math.Abs(hx), math.Abs(hy))
			if ext > maxExtent {
				maxExtent = ext
			}
		}

		// Scale heart to fit in image.
		scale := math.Min(float64(w), float64(h)) * 0.4 / maxExtent
		if scale < 0.001 {
			scale = 0.001
		}

		for y := 0; y < h; y++ {
			py := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				px := float64(x) - cx

				// Find minimum distance to heart boundary.
				minDist := math.Inf(1)
				for i := 0; i < nSamples; i++ {
					dx := px - heartX[i]*scale
					dy := py - heartY[i]*scale
					d := math.Sqrt(dx*dx + dy*dy)
					if d < minDist {
						minDist = d
					}
				}

				// Check if point is inside heart using winding.
				inside := isInsideHeart(px/scale, py/scale)
				if inside {
					minDist = -minDist
				}

				// Map distance to gradient: inside = low, outside = high.
				maxD := math.Max(float64(w), float64(h)) * 0.5
				v := (minDist/maxD + 0.5) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// isInsideHeart tests if a point is inside the parametric heart using a simple
// angle-based approach.
func isInsideHeart(px, py float64) bool {
	// Use the implicit form: for the parametric heart, we can check via
	// distance to center relative to the heart radius at that angle.
	angle := math.Atan2(py, px)
	sinA := math.Sin(angle)
	cosA := math.Cos(angle)

	// Heart radius at this angle (approximate from parametric form).
	t := angle + math.Pi/2 // rotate to match parametric convention
	sinT := math.Sin(t)
	hx := 16 * sinT * sinT * sinT
	hy := -(13*math.Cos(t) - 5*math.Cos(2*t) - 2*math.Cos(3*t) - math.Cos(4*t))

	// Project heart boundary point onto the line from center to our point.
	heartR := hx*cosA + hy*sinA
	pointR := math.Sqrt(px*px + py*py)

	return pointR < math.Abs(heartR)
}

// GenKeyhole creates a keyhole shape: circle on top + rectangle on bottom.
func GenKeyhole() GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		// Circle center in upper third.
		circleY := float64(h) * 0.33
		circleR := float64(min(w, h)) * 0.22

		// Rectangle: centered, starts from circle bottom to bottom of frame.
		rectHalfW := circleR * 0.45
		rectTop := circleY + circleR*0.5
		rectBottom := float64(h) * 0.85

		maxDist := math.Max(float64(w), float64(h)) * 0.6
		if maxDist < 1 {
			maxDist = 1
		}

		for y := 0; y < h; y++ {
			fy := float64(y)
			rowOff := y * w
			for x := 0; x < w; x++ {
				fx := float64(x)

				// Signed distance to keyhole (negative = inside).
				dx := fx - cx
				dy := fy - circleY
				distCircle := math.Sqrt(dx*dx+dy*dy) - circleR

				// Distance to rectangle.
				var distRect float64
				if fy >= rectTop && fy <= rectBottom {
					distRect = math.Abs(fx-cx) - rectHalfW
				} else {
					// Outside rect vertical range.
					dxR := math.Max(0, math.Abs(fx-cx)-rectHalfW)
					var dyR float64
					if fy < rectTop {
						dyR = rectTop - fy
					} else {
						dyR = fy - rectBottom
					}
					distRect = math.Sqrt(dxR*dxR + dyR*dyR)
				}

				dist := math.Min(distCircle, distRect)
				v := (dist/maxDist + 0.5) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}
