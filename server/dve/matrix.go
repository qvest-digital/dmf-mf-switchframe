package dve

import (
	"image"
	"math"
)

// Matrix3x3 is a row-major 3x3 homogeneous transform matrix.
// Elements: [m00, m01, m02, m10, m11, m12, m20, m21, m22]
type Matrix3x3 [9]float32

// Identity returns the 3x3 identity matrix.
func Identity() Matrix3x3 {
	return Matrix3x3{1, 0, 0, 0, 1, 0, 0, 0, 1}
}

// Multiply performs standard 3x3 matrix multiplication: m * n.
func (m Matrix3x3) Multiply(n Matrix3x3) Matrix3x3 {
	return Matrix3x3{
		m[0]*n[0] + m[1]*n[3] + m[2]*n[6],
		m[0]*n[1] + m[1]*n[4] + m[2]*n[7],
		m[0]*n[2] + m[1]*n[5] + m[2]*n[8],

		m[3]*n[0] + m[4]*n[3] + m[5]*n[6],
		m[3]*n[1] + m[4]*n[4] + m[5]*n[7],
		m[3]*n[2] + m[4]*n[5] + m[5]*n[8],

		m[6]*n[0] + m[7]*n[3] + m[8]*n[6],
		m[6]*n[1] + m[7]*n[4] + m[8]*n[7],
		m[6]*n[2] + m[7]*n[5] + m[8]*n[8],
	}
}

// Invert computes the inverse of a 3x3 matrix. Returns (result, true) if
// the matrix is invertible, or (zero, false) if singular.
func (m Matrix3x3) Invert() (Matrix3x3, bool) {
	// Cofactor matrix
	c00 := m[4]*m[8] - m[5]*m[7]
	c01 := -(m[3]*m[8] - m[5]*m[6])
	c02 := m[3]*m[7] - m[4]*m[6]
	c10 := -(m[1]*m[8] - m[2]*m[7])
	c11 := m[0]*m[8] - m[2]*m[6]
	c12 := -(m[0]*m[7] - m[1]*m[6])
	c20 := m[1]*m[5] - m[2]*m[4]
	c21 := -(m[0]*m[5] - m[2]*m[3])
	c22 := m[0]*m[4] - m[1]*m[3]

	det := m[0]*c00 + m[1]*c01 + m[2]*c02
	// Singularity threshold. This is below float32 epsilon (~5.96e-8) but works
	// in practice because our matrices come from well-conditioned geometric transforms
	// (rotation, scale, translation). Near-singular matrices from extreme transforms
	// (e.g., 89.99 deg rotation with near-zero scale) will fall back to identity.
	if det < 1e-12 && det > -1e-12 {
		return Matrix3x3{}, false
	}

	invDet := 1.0 / det
	// Transpose of cofactor / det
	return Matrix3x3{
		c00 * invDet, c10 * invDet, c20 * invDet,
		c01 * invDet, c11 * invDet, c21 * invDet,
		c02 * invDet, c12 * invDet, c22 * invDet,
	}, true
}

// Translation returns a matrix that translates by (tx, ty).
func Translation(tx, ty float32) Matrix3x3 {
	return Matrix3x3{1, 0, tx, 0, 1, ty, 0, 0, 1}
}

// Scale returns a matrix that scales by (sx, sy).
func Scale(sx, sy float32) Matrix3x3 {
	return Matrix3x3{sx, 0, 0, 0, sy, 0, 0, 0, 1}
}

// Rotation returns a Z-axis rotation matrix for the given angle in degrees.
func Rotation(degrees float32) Matrix3x3 {
	rad := float64(degrees) * math.Pi / 180.0
	c := float32(math.Cos(rad))
	s := float32(math.Sin(rad))
	return Matrix3x3{c, -s, 0, s, c, 0, 0, 0, 1}
}

// Perspective returns a perspective matrix that creates foreshortening.
// tiltXDeg and tiltYDeg are tilt angles in degrees. Uses a narrow-FOV
// broadcast-natural model (~20 degree FOV equivalent).
func Perspective(tiltXDeg, tiltYDeg float32) Matrix3x3 {
	const perspScale = 0.002
	sx := float32(math.Sin(float64(tiltXDeg) * math.Pi / 180.0))
	sy := float32(math.Sin(float64(tiltYDeg) * math.Pi / 180.0))
	return Matrix3x3{
		1, 0, sy * perspScale,
		0, 1, sx * perspScale,
		sy * perspScale * 0.5, sx * perspScale * 0.5, 1,
	}
}

// ComputeForwardTransform builds the forward transform matrix from DVE
// parameters. The forward transform maps source pixels to output pixels:
//
//	M = T(posX + anchorAbsX, posY + anchorAbsY) * P(perspX, perspY) * R(rotation) * S(scaleX, scaleY) * T(-anchorSrcX, -anchorSrcY)
//
// where anchorSrcX = anchorX * srcW, anchorSrcY = anchorY * srcH.
func ComputeForwardTransform(t Transform, srcW, srcH int) Matrix3x3 {
	scaleX := float32(t.Width / float64(srcW))
	scaleY := float32(t.Height / float64(srcH))

	anchorX, anchorY := t.EffectiveAnchor()
	anchorSrcX := float32(anchorX * float64(srcW))
	anchorSrcY := float32(anchorY * float64(srcH))

	anchorAbsX := float32(anchorX * t.Width)
	anchorAbsY := float32(anchorY * t.Height)

	posX := float32(t.PositionX)
	posY := float32(t.PositionY)

	// Build chain right-to-left:
	// M = T(posX + anchorAbsX, posY + anchorAbsY) * P * R * S * T(-anchorSrcX, -anchorSrcY)
	m := Translation(-anchorSrcX, -anchorSrcY)
	m = Scale(scaleX, scaleY).Multiply(m)
	m = Rotation(float32(t.RotationDeg)).Multiply(m)
	m = Perspective(float32(t.PerspectiveX), float32(t.PerspectiveY)).Multiply(m)
	m = Translation(posX+anchorAbsX, posY+anchorAbsY).Multiply(m)

	return m
}

// ComputeInverseTransform computes the inverse of ComputeForwardTransform.
// Falls back to identity if the forward transform is singular.
func ComputeInverseTransform(t Transform, srcW, srcH int) Matrix3x3 {
	fwd := ComputeForwardTransform(t, srcW, srcH)
	inv, ok := fwd.Invert()
	if !ok {
		return Identity()
	}
	return inv
}

// ComputeAABB computes the axis-aligned bounding box of the transformed slot
// in output space. Applies the forward transform to the 4 source corners,
// finds min/max, clamps to frame bounds, and even-aligns.
func ComputeAABB(t Transform, srcW, srcH, frameW, frameH int) image.Rectangle {
	m := ComputeForwardTransform(t, srcW, srcH)

	corners := [4][2]float32{
		{0, 0},
		{float32(srcW), 0},
		{float32(srcW), float32(srcH)},
		{0, float32(srcH)},
	}

	var minX, minY, maxX, maxY float32
	for i, c := range corners {
		ox := m[0]*c[0] + m[1]*c[1] + m[2]
		oy := m[3]*c[0] + m[4]*c[1] + m[5]
		ow := m[6]*c[0] + m[7]*c[1] + m[8]
		if ow > 0 {
			ox /= ow
			oy /= ow
		} else if ow < 0 {
			// Point is behind camera; clamp to full frame.
			ox = float32(frameW)
			oy = float32(frameH)
		}
		if i == 0 {
			minX, maxX = ox, ox
			minY, maxY = oy, oy
		} else {
			if ox < minX {
				minX = ox
			}
			if ox > maxX {
				maxX = ox
			}
			if oy < minY {
				minY = oy
			}
			if oy > maxY {
				maxY = oy
			}
		}
	}

	// Clamp to frame bounds.
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	fW := float32(frameW)
	fH := float32(frameH)
	if maxX > fW {
		maxX = fW
	}
	if maxY > fH {
		maxY = fH
	}

	// Even-align: floor for min, ceil-to-even for max.
	x0 := EvenAlign(int(minX))
	y0 := EvenAlign(int(minY))
	x1 := evenCeil(int(math.Ceil(float64(maxX))))
	y1 := evenCeil(int(math.Ceil(float64(maxY))))

	// Clamp again after rounding.
	if x1 > frameW {
		x1 = EvenAlign(frameW)
	}
	if y1 > frameH {
		y1 = EvenAlign(frameH)
	}

	return image.Rect(x0, y0, x1, y1)
}

// evenCeil rounds up to the nearest even number.
func evenCeil(v int) int {
	return (v + 1) &^ 1
}
