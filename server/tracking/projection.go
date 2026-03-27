package tracking

import "math"

// ProjectionMatrix generates a 4x4 perspective projection matrix from camera
// intrinsics. The matrix maps from camera space to clip space using the OpenGL
// convention (-Z forward, Y up). The result is in column-major order as a
// flat [16]float64 array.
//
// The intrinsics' principal point (cx, cy) is used as the optical center,
// and the focal length is derived from the sensor width and the current
// focal length value in the intrinsics. If SensorWidth is zero, a default
// full-frame sensor width of 36mm is assumed.
//
// near and far are the near/far clipping planes in meters.
func ProjectionMatrix(intrinsics CameraIntrinsics, width, height int, near, far float64) [16]float64 {
	if width <= 0 || height <= 0 || near <= 0 || far <= near {
		return [16]float64{}
	}

	// Get the focal length from the first curve point, or use a default.
	focalMM := float64(0)
	if len(intrinsics.FocalLengthCurve) > 0 {
		focalMM = intrinsics.FocalLengthCurve[0][1]
	}
	if focalMM <= 0 {
		focalMM = 50 // default 50mm lens
	}

	sensorW := intrinsics.SensorWidth
	if sensorW <= 0 {
		sensorW = 36.0 // default full-frame 36mm
	}

	// Compute the sensor height from the aspect ratio.
	aspect := float64(width) / float64(height)
	sensorH := sensorW / aspect

	// Convert focal length from mm to pixel units.
	fxPx := focalMM * float64(width) / sensorW
	fyPx := focalMM * float64(height) / sensorH

	// Principal point in pixel coordinates.
	cx := intrinsics.PrincipalPoint[0] * float64(width)
	cy := intrinsics.PrincipalPoint[1] * float64(height)

	// Build the OpenGL-style projection matrix from the pinhole camera model.
	// Reference: http://www.songho.ca/opengl/gl_projectionmatrix.html
	//
	// The standard OpenGL projection maps:
	//   x_clip = (2*fx/W)*x_cam + (2*cx/W - 1)*z_cam
	//   y_clip = (2*fy/H)*y_cam + (2*cy/H - 1)*z_cam  (note: flipped for NDC y-up)
	//   z_clip = -(f+n)/(f-n)*z_cam - 2*f*n/(f-n)
	//   w_clip = -z_cam
	//
	// Column-major layout:
	//   [ m[0] m[4] m[8]  m[12] ]
	//   [ m[1] m[5] m[9]  m[13] ]
	//   [ m[2] m[6] m[10] m[14] ]
	//   [ m[3] m[7] m[11] m[15] ]

	w := float64(width)
	h := float64(height)

	var m [16]float64

	m[0] = 2.0 * fxPx / w
	m[5] = 2.0 * fyPx / h
	m[8] = 2.0*cx/w - 1.0
	m[9] = 2.0*cy/h - 1.0
	m[10] = -(far + near) / (far - near)
	m[11] = -1.0
	m[14] = -2.0 * far * near / (far - near)

	return m
}

// ViewMatrix generates a 4x4 view matrix from a CameraPose. The matrix
// transforms from world space to camera space. The rotation follows the
// extrinsic ZYX (pan, tilt, roll) convention matching Free-D protocol
// orientation order.
//
// The result is in column-major order as a flat [16]float64 array.
func ViewMatrix(pose CameraPose) [16]float64 {
	// Convert degrees to radians.
	panRad := pose.Orientation[0] * math.Pi / 180.0
	tiltRad := pose.Orientation[1] * math.Pi / 180.0
	rollRad := pose.Orientation[2] * math.Pi / 180.0

	// Precompute trig values.
	cp := math.Cos(panRad)
	sp := math.Sin(panRad)
	ct := math.Cos(tiltRad)
	st := math.Sin(tiltRad)
	cr := math.Cos(rollRad)
	sr := math.Sin(rollRad)

	// Extrinsic ZYX rotation matrix R = Rz(pan) * Ry(tilt) * Rx(roll)
	// This matches the Free-D convention: pan around Z, tilt around Y, roll around X.
	r00 := cp*ct
	r01 := cp*st*sr - sp*cr
	r02 := cp*st*cr + sp*sr
	r10 := sp*ct
	r11 := sp*st*sr + cp*cr
	r12 := sp*st*cr - cp*sr
	r20 := -st
	r21 := ct*sr
	r22 := ct*cr

	// The view matrix is the inverse of the camera's model matrix.
	// For a rotation R and translation T, the inverse is R^T with
	// the translation -R^T * T.
	tx := pose.Position[0]
	ty := pose.Position[1]
	tz := pose.Position[2]

	// Column-major layout.
	var m [16]float64

	// Transpose of rotation.
	m[0] = r00
	m[1] = r10
	m[2] = r20

	m[4] = r01
	m[5] = r11
	m[6] = r21

	m[8] = r02
	m[9] = r12
	m[10] = r22

	// -R^T * T
	m[12] = -(r00*tx + r01*ty + r02*tz)
	m[13] = -(r10*tx + r11*ty + r12*tz)
	m[14] = -(r20*tx + r21*ty + r22*tz)

	m[15] = 1.0

	return m
}

// ViewProjectionMatrix combines the view and projection transforms into a
// single 4x4 matrix. This is the complete transform from world space to
// clip space. The result is in column-major order.
func ViewProjectionMatrix(pose CameraPose, intrinsics CameraIntrinsics, width, height int, near, far float64) [16]float64 {
	view := ViewMatrix(pose)
	proj := ProjectionMatrix(intrinsics, width, height, near, far)
	return mat4Mul(proj, view)
}

// mat4Mul multiplies two 4x4 matrices stored in column-major order.
func mat4Mul(a, b [16]float64) [16]float64 {
	var c [16]float64
	for col := 0; col < 4; col++ {
		for row := 0; row < 4; row++ {
			sum := 0.0
			for k := 0; k < 4; k++ {
				sum += a[k*4+row] * b[col*4+k]
			}
			c[col*4+row] = sum
		}
	}
	return c
}
