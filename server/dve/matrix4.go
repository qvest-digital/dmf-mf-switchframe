package dve

import "math"

// Matrix4x4 is a row-major 4x4 homogeneous transform matrix for 3D DVE.
type Matrix4x4 [16]float32

// Identity4 returns the 4x4 identity matrix.
func Identity4() Matrix4x4 {
	return Matrix4x4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

// Multiply4 multiplies two 4x4 matrices.
func (m Matrix4x4) Multiply4(n Matrix4x4) Matrix4x4 {
	var r Matrix4x4
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			var sum float32
			for k := 0; k < 4; k++ {
				sum += m[i*4+k] * n[k*4+j]
			}
			r[i*4+j] = sum
		}
	}
	return r
}

// Translation4 returns a 4x4 translation matrix.
func Translation4(tx, ty, tz float32) Matrix4x4 {
	return Matrix4x4{
		1, 0, 0, tx,
		0, 1, 0, ty,
		0, 0, 1, tz,
		0, 0, 0, 1,
	}
}

// Scale4 returns a 4x4 scale matrix.
func Scale4(sx, sy, sz float32) Matrix4x4 {
	return Matrix4x4{
		sx, 0, 0, 0,
		0, sy, 0, 0,
		0, 0, sz, 0,
		0, 0, 0, 1,
	}
}

// RotationX4 returns a 4x4 rotation matrix around the X axis.
func RotationX4(degrees float32) Matrix4x4 {
	r := float64(degrees) * math.Pi / 180
	c := float32(math.Cos(r))
	s := float32(math.Sin(r))
	return Matrix4x4{
		1, 0, 0, 0,
		0, c, -s, 0,
		0, s, c, 0,
		0, 0, 0, 1,
	}
}

// RotationY4 returns a 4x4 rotation matrix around the Y axis.
func RotationY4(degrees float32) Matrix4x4 {
	r := float64(degrees) * math.Pi / 180
	c := float32(math.Cos(r))
	s := float32(math.Sin(r))
	return Matrix4x4{
		c, 0, s, 0,
		0, 1, 0, 0,
		-s, 0, c, 0,
		0, 0, 0, 1,
	}
}

// RotationZ4 returns a 4x4 rotation matrix around the Z axis.
func RotationZ4(degrees float32) Matrix4x4 {
	r := float64(degrees) * math.Pi / 180
	c := float32(math.Cos(r))
	s := float32(math.Sin(r))
	return Matrix4x4{
		c, -s, 0, 0,
		s, c, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// PerspectiveProjection returns a 4x4 perspective projection matrix.
// fovDeg is the vertical field of view in degrees.
// aspect is width/height. near and far are clipping planes.
func PerspectiveProjection(fovDeg, aspect, near, far float32) Matrix4x4 {
	f := float32(1.0 / math.Tan(float64(fovDeg)*math.Pi/360))
	nf := near - far
	return Matrix4x4{
		f / aspect, 0, 0, 0,
		0, f, 0, 0,
		0, 0, (far + near) / nf, 2 * far * near / nf,
		0, 0, -1, 0,
	}
}

// Compute3DMVP builds the Model-View-Projection matrix for 3D DVE.
// The video plane is a unit quad centered at origin in object space.
// Camera looks down -Z axis.
func Compute3DMVP(t3d *Transform3D, aspect float32) Matrix4x4 {
	if t3d == nil {
		return Identity4()
	}

	fov := float32(t3d.FOV)
	if fov <= 0 {
		fov = 20
	}
	dist := float32(t3d.Distance)
	if dist <= 0 {
		dist = 3.0
	}

	// Model: pure rotation only. Aspect ratio correction is handled by the
	// shader's intersection functions (geometry sized to srcAspect) and the
	// projection matrix (screen aspect). No model-level scaling needed.
	model := RotationX4(float32(t3d.RotateX)).
		Multiply4(RotationY4(float32(t3d.RotateY))).
		Multiply4(RotationZ4(float32(t3d.RotateZ)))

	// View: camera at (0, 0, dist) looking at origin
	view := Translation4(0, 0, -dist)

	// Projection: perspective
	proj := PerspectiveProjection(fov, aspect, 0.1, 100)

	return proj.Multiply4(view).Multiply4(model)
}

// Compute3DInverseMVP computes the inverse of the MVP matrix.
// Used for ray-casting: for each output pixel, project back to find
// the corresponding point on the 3D geometry.
func Compute3DInverseMVP(t3d *Transform3D, aspect float32) Matrix4x4 {
	mvp := Compute3DMVP(t3d, aspect)
	inv, ok := Invert4(mvp)
	if !ok {
		return Identity4()
	}
	return inv
}

// Invert4 inverts a 4x4 matrix using cofactor expansion.
// Returns (result, true) if invertible, (identity, false) if singular.
func Invert4(m Matrix4x4) (Matrix4x4, bool) {
	// Compute all 2x2 minors for the bottom two rows
	s0 := m[0]*m[5] - m[1]*m[4]
	s1 := m[0]*m[6] - m[2]*m[4]
	s2 := m[0]*m[7] - m[3]*m[4]
	s3 := m[1]*m[6] - m[2]*m[5]
	s4 := m[1]*m[7] - m[3]*m[5]
	s5 := m[2]*m[7] - m[3]*m[6]

	c5 := m[10]*m[15] - m[11]*m[14]
	c4 := m[9]*m[15] - m[11]*m[13]
	c3 := m[9]*m[14] - m[10]*m[13]
	c2 := m[8]*m[15] - m[11]*m[12]
	c1 := m[8]*m[14] - m[10]*m[12]
	c0 := m[8]*m[13] - m[9]*m[12]

	det := s0*c5 - s1*c4 + s2*c3 + s3*c2 - s4*c1 + s5*c0
	if det < 1e-10 && det > -1e-10 {
		return Identity4(), false
	}

	invDet := 1.0 / det

	var r Matrix4x4
	r[0] = (m[5]*c5 - m[6]*c4 + m[7]*c3) * invDet
	r[1] = (-m[1]*c5 + m[2]*c4 - m[3]*c3) * invDet
	r[2] = (m[13]*s5 - m[14]*s4 + m[15]*s3) * invDet
	r[3] = (-m[9]*s5 + m[10]*s4 - m[11]*s3) * invDet

	r[4] = (-m[4]*c5 + m[6]*c2 - m[7]*c1) * invDet
	r[5] = (m[0]*c5 - m[2]*c2 + m[3]*c1) * invDet
	r[6] = (-m[12]*s5 + m[14]*s2 - m[15]*s1) * invDet
	r[7] = (m[8]*s5 - m[10]*s2 + m[11]*s1) * invDet

	r[8] = (m[4]*c4 - m[5]*c2 + m[7]*c0) * invDet
	r[9] = (-m[0]*c4 + m[1]*c2 - m[3]*c0) * invDet
	r[10] = (m[12]*s4 - m[13]*s2 + m[15]*s0) * invDet
	r[11] = (-m[8]*s4 + m[9]*s2 - m[11]*s0) * invDet

	r[12] = (-m[4]*c3 + m[5]*c1 - m[6]*c0) * invDet
	r[13] = (m[0]*c3 - m[1]*c1 + m[2]*c0) * invDet
	r[14] = (-m[12]*s3 + m[13]*s1 - m[14]*s0) * invDet
	r[15] = (m[8]*s3 - m[9]*s1 + m[10]*s0) * invDet

	return r, true
}
