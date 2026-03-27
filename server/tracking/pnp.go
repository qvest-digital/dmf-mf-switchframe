package tracking

import (
	"errors"
	"math"
)

// PnP (Perspective-n-Point) solver errors.
var (
	// ErrTooFewPoints indicates fewer than 6 point correspondences were provided.
	// DLT requires at least 6 correspondences (12 equations for 11 unknowns).
	ErrTooFewPoints = errors.New("tracking: PnP requires at least 6 point correspondences")

	// ErrPointCountMismatch indicates mismatched 2D and 3D point counts.
	ErrPointCountMismatch = errors.New("tracking: 2D and 3D point counts must match")

	// ErrSingularMatrix indicates the DLT system matrix is singular.
	ErrSingularMatrix = errors.New("tracking: singular matrix in PnP solve")

	// ErrInvalidIntrinsics indicates the camera intrinsics are unusable.
	ErrInvalidIntrinsics = errors.New("tracking: invalid camera intrinsics for PnP")
)

// SolvePnP computes camera pose from 2D-3D correspondences using the DLT
// (Direct Linear Transform) algorithm followed by iterative refinement.
//
// points2D are pixel coordinates [u, v] in the image.
// points3D are world coordinates [X, Y, Z] in meters.
// intrinsics provides the camera calibration (principal point, focal length curve).
// focalLengthMM is the current focal length in millimeters.
//
// Returns a CameraPose with position [X, Y, Z] in meters and orientation
// [pan, tilt, roll] in degrees.
func SolvePnP(points2D [][2]float64, points3D [][3]float64, intrinsics CameraIntrinsics, focalLengthMM float64) (*CameraPose, error) {
	n := len(points2D)
	if n != len(points3D) {
		return nil, ErrPointCountMismatch
	}
	if n < 6 {
		return nil, ErrTooFewPoints
	}

	// Build the camera matrix K from intrinsics.
	// We need focal length in pixels. Given sensor width in mm and image width,
	// fx = fy = focalLengthMM * imageWidth / sensorWidth.
	// Since we don't know image dimensions here, we normalize 2D points using
	// the principal point and assume points are already in pixel coordinates
	// with an implicit image size. We use a focal length of 1.0 (normalized)
	// and scale the 2D points by the principal point offset.
	//
	// For the DLT approach, we normalize 2D points to camera coordinates first:
	//   x_cam = (u - cx) / fx
	//   y_cam = (v - cy) / fy
	// This removes the intrinsics from the projection model, letting us solve
	// for the extrinsic [R|t] directly.
	cx := intrinsics.PrincipalPoint[0]
	cy := intrinsics.PrincipalPoint[1]

	if focalLengthMM <= 0 {
		return nil, ErrInvalidIntrinsics
	}

	// Convert focal length from mm to a normalized value.
	// If sensor width is available, compute fx in normalized coordinates.
	// Otherwise, use focalLengthMM directly as a pixel-scale value.
	fx := focalLengthMM
	fy := focalLengthMM
	if intrinsics.SensorWidth > 0 {
		// Assume square pixels. Normalized so that image width = sensorWidth.
		fx = focalLengthMM / intrinsics.SensorWidth
		fy = fx
	}

	if fx <= 0 || fy <= 0 {
		return nil, ErrInvalidIntrinsics
	}

	// Normalize 2D points to camera coordinates.
	normalized2D := make([][2]float64, n)
	for i := range points2D {
		normalized2D[i][0] = (points2D[i][0] - cx) / fx
		normalized2D[i][1] = (points2D[i][1] - cy) / fy
	}

	// Normalize 3D points for numerical stability.
	mean3D := centroid3D(points3D)
	scale3D := meanDistance3D(points3D, mean3D)
	if scale3D < 1e-12 {
		scale3D = 1.0
	}
	norm3D := make([][3]float64, n)
	for i := range points3D {
		norm3D[i][0] = (points3D[i][0] - mean3D[0]) / scale3D
		norm3D[i][1] = (points3D[i][1] - mean3D[1]) / scale3D
		norm3D[i][2] = (points3D[i][2] - mean3D[2]) / scale3D
	}

	// Build the DLT equation system: A * p = 0
	// For each correspondence, we get 2 equations:
	//   X*p1 + Y*p2 + Z*p3 + p4 - x*(X*p9 + Y*p10 + Z*p11 + p12) = 0
	//   X*p5 + Y*p6 + Z*p7 + p8 - y*(X*p9 + Y*p10 + Z*p11 + p12) = 0
	// where p is the 12-element projection matrix [R|t] flattened.
	rows := 2 * n
	cols := 12
	A := make([][]float64, rows)
	for i := 0; i < rows; i++ {
		A[i] = make([]float64, cols)
	}

	for i := 0; i < n; i++ {
		X := norm3D[i][0]
		Y := norm3D[i][1]
		Z := norm3D[i][2]
		x := normalized2D[i][0]
		y := normalized2D[i][1]

		r := 2 * i
		// Row 1: [X Y Z 1 0 0 0 0 -x*X -x*Y -x*Z -x]
		A[r][0] = X
		A[r][1] = Y
		A[r][2] = Z
		A[r][3] = 1.0
		A[r][8] = -x * X
		A[r][9] = -x * Y
		A[r][10] = -x * Z
		A[r][11] = -x

		// Row 2: [0 0 0 0 X Y Z 1 -y*X -y*Y -y*Z -y]
		A[r+1][4] = X
		A[r+1][5] = Y
		A[r+1][6] = Z
		A[r+1][7] = 1.0
		A[r+1][8] = -y * X
		A[r+1][9] = -y * Y
		A[r+1][10] = -y * Z
		A[r+1][11] = -y
	}

	// Solve via SVD of A^T * A — find the eigenvector with smallest eigenvalue.
	// For a 12x12 symmetric matrix, we use the power method on the inverse
	// (inverse iteration) to find the smallest eigenvector.
	AtA := multiplyAtA(A, rows, cols)

	// Find the null space of A^T*A using inverse iteration.
	p, err := smallestEigenvector(AtA, cols)
	if err != nil {
		return nil, err
	}

	// Extract R_raw and t_raw from the projection matrix P = [R|t].
	// P is 3x4, stored row-major in p[0..11].
	// The null space solution is up to an arbitrary scale factor.
	Rraw := [3][3]float64{
		{p[0], p[1], p[2]},
		{p[4], p[5], p[6]},
		{p[8], p[9], p[10]},
	}
	tRaw := [3]float64{p[3], p[7], p[11]}

	// Determine the scale factor from the raw R matrix.
	// For a true rotation, each row has unit norm. Compute the average row norm
	// to find the scale factor that was baked into the null space solution.
	var scaleSum float64
	for i := 0; i < 3; i++ {
		var rowNorm float64
		for j := 0; j < 3; j++ {
			rowNorm += Rraw[i][j] * Rraw[i][j]
		}
		scaleSum += math.Sqrt(rowNorm)
	}
	pScale := scaleSum / 3.0
	if pScale < 1e-15 {
		return nil, ErrSingularMatrix
	}

	// Normalize R and t by the scale factor.
	var R [3][3]float64
	var t [3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			R[i][j] = Rraw[i][j] / pScale
		}
		t[i] = tRaw[i] / pScale
	}

	// Enforce R as a proper rotation via SVD-based closest rotation.
	R = closestRotation(R)

	// Determine sign: check that the majority of points project with positive depth.
	// If not, flip the sign of R and t.
	positiveDepth := 0
	for i := 0; i < n; i++ {
		camZ := R[2][0]*norm3D[i][0] + R[2][1]*norm3D[i][1] + R[2][2]*norm3D[i][2] + t[2]
		if camZ > 0 {
			positiveDepth++
		}
	}
	if positiveDepth < n/2 {
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				R[i][j] = -R[i][j]
			}
			t[i] = -t[i]
		}
	}

	// De-normalize: undo the 3D point normalization.
	// The DLT solved P_norm such that x = P_norm * X_norm_homogeneous
	// where X_norm = (X_world - mean) / scale
	//
	// P_norm = [R_n | t_n]  (in normalized 3D coordinates)
	// P_world = P_norm * T_norm  where T_norm transforms world -> normalized
	//
	// T_norm = [1/s * I | -mean/s]  (as 3x4 applied to homogeneous world coords)
	//          [0  0  0 |    1   ]
	//
	// So P_world = [R_n/s | t_n - R_n*mean/s] = [R_n/s | t_n - R_n*mean/s]
	// But we already divided R_n by pScale (making it unit). The 3D normalization
	// scale 's' is separate.
	//
	// After proper scale extraction and R enforcement:
	// R is the rotation from normalized 3D to camera (unit matrix).
	// t is the translation in normalized 3D space.
	// To get the translation in world space:
	//   t_world = t * scale3D + R * mean3D (shifting back to world frame)
	// Then camera position = -R^T * t_world
	tWorld := [3]float64{
		t[0]*scale3D + R[0][0]*mean3D[0] + R[0][1]*mean3D[1] + R[0][2]*mean3D[2],
		t[1]*scale3D + R[1][0]*mean3D[0] + R[1][1]*mean3D[1] + R[1][2]*mean3D[2],
		t[2]*scale3D + R[2][0]*mean3D[0] + R[2][1]*mean3D[1] + R[2][2]*mean3D[2],
	}

	// Camera position in world coordinates: C = -R^T * t_world
	Rt := transpose3x3(R)
	position := [3]float64{
		-(Rt[0][0]*tWorld[0] + Rt[0][1]*tWorld[1] + Rt[0][2]*tWorld[2]),
		-(Rt[1][0]*tWorld[0] + Rt[1][1]*tWorld[1] + Rt[1][2]*tWorld[2]),
		-(Rt[2][0]*tWorld[0] + Rt[2][1]*tWorld[1] + Rt[2][2]*tWorld[2]),
	}

	// Extract Euler angles (pan, tilt, roll) from R.
	// Convention: R = Rz(roll) * Rx(tilt) * Ry(pan)
	pan, tilt, roll := rotationToEuler(R)

	// Iteratively refine the pose using Gauss-Newton optimization.
	position, pan, tilt, roll = refinepose(
		points2D, points3D,
		position, pan, tilt, roll,
		fx, fy, cx, cy,
		5, // iterations
	)

	return &CameraPose{
		Position:    position,
		Orientation: [3]float64{pan, tilt, roll},
		FocalLength: focalLengthMM,
		Confidence:  computePnPConfidence(points2D, points3D, position, pan, tilt, roll, fx, fy, cx, cy),
		Source:      "visual",
	}, nil
}

// centroid3D computes the mean of a set of 3D points.
func centroid3D(pts [][3]float64) [3]float64 {
	var c [3]float64
	n := float64(len(pts))
	for _, p := range pts {
		c[0] += p[0]
		c[1] += p[1]
		c[2] += p[2]
	}
	c[0] /= n
	c[1] /= n
	c[2] /= n
	return c
}

// meanDistance3D computes the mean distance from points to a center.
func meanDistance3D(pts [][3]float64, center [3]float64) float64 {
	var total float64
	for _, p := range pts {
		dx := p[0] - center[0]
		dy := p[1] - center[1]
		dz := p[2] - center[2]
		total += math.Sqrt(dx*dx + dy*dy + dz*dz)
	}
	return total / float64(len(pts))
}

// multiplyAtA computes A^T * A for an m x n matrix A.
func multiplyAtA(A [][]float64, m, n int) [][]float64 {
	result := make([][]float64, n)
	for i := range result {
		result[i] = make([]float64, n)
	}

	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			var sum float64
			for k := 0; k < m; k++ {
				sum += A[k][i] * A[k][j]
			}
			result[i][j] = sum
			result[j][i] = sum // symmetric
		}
	}
	return result
}

// smallestEigenvector finds the eigenvector corresponding to the smallest
// eigenvalue of a symmetric positive semi-definite matrix using inverse iteration.
func smallestEigenvector(M [][]float64, n int) ([]float64, error) {
	// Use shifted inverse iteration: (M - sigma*I)^-1 * v
	// With sigma = 0, this converges to the smallest eigenvector.

	// First, add a small regularization to make M invertible.
	reg := make([][]float64, n)
	for i := range reg {
		reg[i] = make([]float64, n)
		copy(reg[i], M[i])
		reg[i][i] += 1e-10 // tiny regularization
	}

	// LU decomposition of the regularized matrix.
	lu, piv, err := luDecompose(reg, n)
	if err != nil {
		return nil, ErrSingularMatrix
	}

	// Start with a random-ish vector.
	v := make([]float64, n)
	for i := range v {
		v[i] = 1.0 / float64(i+1)
	}
	normalizeVec(v)

	// Iterate.
	for iter := 0; iter < 100; iter++ {
		w := luSolve(lu, piv, v, n)
		normalizeVec(w)
		v = w
	}

	return v, nil
}

// luDecompose performs LU decomposition with partial pivoting.
func luDecompose(A [][]float64, n int) ([][]float64, []int, error) {
	lu := make([][]float64, n)
	for i := range lu {
		lu[i] = make([]float64, n)
		copy(lu[i], A[i])
	}

	piv := make([]int, n)
	for i := range piv {
		piv[i] = i
	}

	for k := 0; k < n; k++ {
		// Find pivot.
		maxVal := math.Abs(lu[k][k])
		maxRow := k
		for i := k + 1; i < n; i++ {
			if math.Abs(lu[i][k]) > maxVal {
				maxVal = math.Abs(lu[i][k])
				maxRow = i
			}
		}

		if maxVal < 1e-15 {
			return nil, nil, ErrSingularMatrix
		}

		// Swap rows.
		if maxRow != k {
			lu[k], lu[maxRow] = lu[maxRow], lu[k]
			piv[k], piv[maxRow] = piv[maxRow], piv[k]
		}

		// Eliminate below.
		for i := k + 1; i < n; i++ {
			lu[i][k] /= lu[k][k]
			for j := k + 1; j < n; j++ {
				lu[i][j] -= lu[i][k] * lu[k][j]
			}
		}
	}

	return lu, piv, nil
}

// luSolve solves LU * x = b using forward and back substitution.
func luSolve(lu [][]float64, piv []int, b []float64, n int) []float64 {
	x := make([]float64, n)
	// Apply permutation.
	for i := range x {
		x[i] = b[piv[i]]
	}

	// Forward substitution (L * y = Pb).
	for i := 1; i < n; i++ {
		for j := 0; j < i; j++ {
			x[i] -= lu[i][j] * x[j]
		}
	}

	// Back substitution (U * x = y).
	for i := n - 1; i >= 0; i-- {
		for j := i + 1; j < n; j++ {
			x[i] -= lu[i][j] * x[j]
		}
		x[i] /= lu[i][i]
	}

	return x
}

// normalizeVec normalizes a vector to unit length in-place.
func normalizeVec(v []float64) {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	norm := math.Sqrt(sum)
	if norm < 1e-15 {
		return
	}
	for i := range v {
		v[i] /= norm
	}
}

// closestRotation finds the closest proper rotation matrix to M
// using SVD: R = U * V^T where M = U * S * V^T.
// Uses a simplified 3x3 SVD via the polar decomposition.
func closestRotation(M [3][3]float64) [3][3]float64 {
	// Compute M^T * M.
	MtM := multiply3x3(transpose3x3(M), M)

	// Eigendecomposition of the 3x3 symmetric matrix MtM.
	// Use Jacobi iteration for 3x3 symmetric eigendecomposition.
	eigenvals, V := jacobi3x3(MtM)

	// S = sqrt of eigenvalues (singular values of M).
	// Compute S^-1.
	var Sinv [3][3]float64
	for i := 0; i < 3; i++ {
		if eigenvals[i] > 1e-12 {
			Sinv[i][i] = 1.0 / math.Sqrt(eigenvals[i])
		}
	}

	// U = M * V * S^-1
	MV := multiply3x3(M, V)
	U := multiply3x3(MV, Sinv)

	// R = U * V^T
	R := multiply3x3(U, transpose3x3(V))

	// Ensure det(R) = +1 (proper rotation, not reflection).
	det := determinant3x3(R)
	if det < 0 {
		// Flip the sign of the column of U corresponding to the smallest singular value.
		minIdx := 0
		minVal := eigenvals[0]
		for i := 1; i < 3; i++ {
			if eigenvals[i] < minVal {
				minVal = eigenvals[i]
				minIdx = i
			}
		}
		for i := 0; i < 3; i++ {
			U[i][minIdx] = -U[i][minIdx]
		}
		R = multiply3x3(U, transpose3x3(V))
	}

	return R
}

// jacobi3x3 performs eigendecomposition of a 3x3 symmetric matrix
// using Jacobi rotation. Returns eigenvalues and eigenvector matrix.
func jacobi3x3(A [3][3]float64) ([3]float64, [3][3]float64) {
	var M [3][3]float64
	M = A

	// Initialize V to identity.
	var V [3][3]float64
	V[0][0] = 1
	V[1][1] = 1
	V[2][2] = 1

	for iter := 0; iter < 100; iter++ {
		// Find the largest off-diagonal element.
		p, q := 0, 1
		maxOff := math.Abs(M[0][1])
		if math.Abs(M[0][2]) > maxOff {
			p, q = 0, 2
			maxOff = math.Abs(M[0][2])
		}
		if math.Abs(M[1][2]) > maxOff {
			p, q = 1, 2
			maxOff = math.Abs(M[1][2])
		}

		if maxOff < 1e-15 {
			break
		}

		// Compute rotation angle.
		var theta float64
		diff := M[q][q] - M[p][p]
		if math.Abs(diff) < 1e-15 {
			theta = math.Pi / 4
		} else {
			theta = 0.5 * math.Atan2(2*M[p][q], diff)
		}

		c := math.Cos(theta)
		s := math.Sin(theta)

		// Apply Jacobi rotation: M' = G^T * M * G
		var Mnew [3][3]float64
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				Mnew[i][j] = M[i][j]
			}
		}

		Mnew[p][p] = c*c*M[p][p] - 2*s*c*M[p][q] + s*s*M[q][q]
		Mnew[q][q] = s*s*M[p][p] + 2*s*c*M[p][q] + c*c*M[q][q]
		Mnew[p][q] = 0
		Mnew[q][p] = 0

		for i := 0; i < 3; i++ {
			if i != p && i != q {
				Mnew[i][p] = c*M[i][p] - s*M[i][q]
				Mnew[p][i] = Mnew[i][p]
				Mnew[i][q] = s*M[i][p] + c*M[i][q]
				Mnew[q][i] = Mnew[i][q]
			}
		}

		M = Mnew

		// Update eigenvectors: V' = V * G
		var Vnew [3][3]float64
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				Vnew[i][j] = V[i][j]
			}
			Vnew[i][p] = c*V[i][p] - s*V[i][q]
			Vnew[i][q] = s*V[i][p] + c*V[i][q]
		}
		V = Vnew
	}

	return [3]float64{M[0][0], M[1][1], M[2][2]}, V
}

// multiply3x3 multiplies two 3x3 matrices.
func multiply3x3(A, B [3][3]float64) [3][3]float64 {
	var C [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				C[i][j] += A[i][k] * B[k][j]
			}
		}
	}
	return C
}

// transpose3x3 transposes a 3x3 matrix.
func transpose3x3(M [3][3]float64) [3][3]float64 {
	return [3][3]float64{
		{M[0][0], M[1][0], M[2][0]},
		{M[0][1], M[1][1], M[2][1]},
		{M[0][2], M[1][2], M[2][2]},
	}
}

// determinant3x3 computes the determinant of a 3x3 matrix.
func determinant3x3(M [3][3]float64) float64 {
	return M[0][0]*(M[1][1]*M[2][2]-M[1][2]*M[2][1]) -
		M[0][1]*(M[1][0]*M[2][2]-M[1][2]*M[2][0]) +
		M[0][2]*(M[1][0]*M[2][1]-M[1][1]*M[2][0])
}

// rotationToEuler extracts Euler angles (pan, tilt, roll) in degrees
// from a rotation matrix R.
// Convention: R = Rz(pan) * Ry(tilt) * Rx(roll)  (ZYX / roll-pitch-yaw)
func rotationToEuler(R [3][3]float64) (pan, tilt, roll float64) {
	// From the rotation matrix R = Rz(γ)*Rx(β)*Ry(α):
	// R[2][0] = -sin(β)*cos(α) + cos(β)*sin(γ)*sin(α)... this gets complex.
	//
	// Use the simpler ZYX convention which is equivalent to roll-pitch-yaw:
	// R = Rz(yaw)*Ry(pitch)*Rx(roll)
	// tilt (pitch) = -asin(R[2][0])
	// pan (yaw) = atan2(R[1][0], R[0][0])
	// roll = atan2(R[2][1], R[2][2])

	tiltRad := -math.Asin(clamp(R[2][0], -1, 1))
	panRad := math.Atan2(R[1][0], R[0][0])
	rollRad := math.Atan2(R[2][1], R[2][2])

	pan = panRad * 180.0 / math.Pi
	tilt = tiltRad * 180.0 / math.Pi
	roll = rollRad * 180.0 / math.Pi
	return
}

// eulerToRotation converts Euler angles (pan, tilt, roll) in degrees
// to a 3x3 rotation matrix.
// Convention: R = Rz(pan)*Ry(tilt)*Rx(roll)
func eulerToRotation(pan, tilt, roll float64) [3][3]float64 {
	a := pan * math.Pi / 180.0   // yaw
	b := tilt * math.Pi / 180.0  // pitch
	c := roll * math.Pi / 180.0  // roll

	ca, sa := math.Cos(a), math.Sin(a)
	cb, sb := math.Cos(b), math.Sin(b)
	cc, sc := math.Cos(c), math.Sin(c)

	return [3][3]float64{
		{ca*cb, ca*sb*sc - sa*cc, ca*sb*cc + sa*sc},
		{sa*cb, sa*sb*sc + ca*cc, sa*sb*cc - ca*sc},
		{-sb, cb*sc, cb*cc},
	}
}

// clamp restricts v to the range [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// projectPoint projects a 3D world point to 2D pixel coordinates.
func projectPoint(pt [3]float64, R [3][3]float64, t [3]float64, fx, fy, cx, cy float64) [2]float64 {
	// Transform to camera coordinates: p_cam = R * p_world + t
	camX := R[0][0]*pt[0] + R[0][1]*pt[1] + R[0][2]*pt[2] + t[0]
	camY := R[1][0]*pt[0] + R[1][1]*pt[1] + R[1][2]*pt[2] + t[1]
	camZ := R[2][0]*pt[0] + R[2][1]*pt[1] + R[2][2]*pt[2] + t[2]

	if math.Abs(camZ) < 1e-12 {
		return [2]float64{cx, cy}
	}

	// Project: u = fx * (X/Z) + cx, v = fy * (Y/Z) + cy
	u := fx*(camX/camZ) + cx
	v := fy*(camY/camZ) + cy
	return [2]float64{u, v}
}

// refinepose applies Gauss-Newton refinement to the initial pose estimate.
func refinepose(
	points2D [][2]float64, points3D [][3]float64,
	position [3]float64, pan, tilt, roll float64,
	fx, fy, cx, cy float64,
	iterations int,
) ([3]float64, float64, float64, float64) {
	n := len(points2D)
	if n < 6 {
		return position, pan, tilt, roll
	}

	step := 1e-6 // numerical differentiation step

	for iter := 0; iter < iterations; iter++ {
		// Current parameters: [tx, ty, tz, pan, tilt, roll]
		params := [6]float64{position[0], position[1], position[2], pan, tilt, roll}

		// Compute residuals and Jacobian.
		J := make([][]float64, 2*n)
		residuals := make([]float64, 2*n)

		R := eulerToRotation(params[3], params[4], params[5])
		t := poseToTranslation(params, R)

		for i := 0; i < n; i++ {
			proj := projectPoint(points3D[i], R, t, fx, fy, cx, cy)
			residuals[2*i] = proj[0] - points2D[i][0]
			residuals[2*i+1] = proj[1] - points2D[i][1]

			// Numerical Jacobian.
			J[2*i] = make([]float64, 6)
			J[2*i+1] = make([]float64, 6)

			for p := 0; p < 6; p++ {
				paramsP := params
				paramsP[p] += step
				Rp := eulerToRotation(paramsP[3], paramsP[4], paramsP[5])
				tp := poseToTranslation(paramsP, Rp)
				projP := projectPoint(points3D[i], Rp, tp, fx, fy, cx, cy)

				J[2*i][p] = (projP[0] - proj[0]) / step
				J[2*i+1][p] = (projP[1] - proj[1]) / step
			}
		}

		// Solve normal equations: J^T*J * delta = -J^T * r
		JtJ := make([][]float64, 6)
		Jtr := make([]float64, 6)
		for i := 0; i < 6; i++ {
			JtJ[i] = make([]float64, 6)
			for j := 0; j < 6; j++ {
				var sum float64
				for k := 0; k < 2*n; k++ {
					sum += J[k][i] * J[k][j]
				}
				JtJ[i][j] = sum
			}
			var sum float64
			for k := 0; k < 2*n; k++ {
				sum += J[k][i] * residuals[k]
			}
			Jtr[i] = -sum
		}

		// Add damping (Levenberg-Marquardt style).
		for i := 0; i < 6; i++ {
			JtJ[i][i] += 1e-6
		}

		lu, piv, err := luDecompose(JtJ, 6)
		if err != nil {
			break
		}
		delta := luSolve(lu, piv, Jtr, 6)

		// Apply update with step size limiting.
		maxStep := 0.0
		for _, d := range delta {
			if math.Abs(d) > maxStep {
				maxStep = math.Abs(d)
			}
		}
		scale := 1.0
		if maxStep > 1.0 {
			scale = 1.0 / maxStep
		}

		position[0] += delta[0] * scale
		position[1] += delta[1] * scale
		position[2] += delta[2] * scale
		pan += delta[3] * scale
		tilt += delta[4] * scale
		roll += delta[5] * scale
	}

	return position, pan, tilt, roll
}

// poseToTranslation computes the translation vector t = -R * position.
func poseToTranslation(params [6]float64, R [3][3]float64) [3]float64 {
	pos := [3]float64{params[0], params[1], params[2]}
	return [3]float64{
		-(R[0][0]*pos[0] + R[0][1]*pos[1] + R[0][2]*pos[2]),
		-(R[1][0]*pos[0] + R[1][1]*pos[1] + R[1][2]*pos[2]),
		-(R[2][0]*pos[0] + R[2][1]*pos[1] + R[2][2]*pos[2]),
	}
}

// computePnPConfidence estimates tracking confidence based on reprojection error.
func computePnPConfidence(
	points2D [][2]float64, points3D [][3]float64,
	position [3]float64, pan, tilt, roll float64,
	fx, fy, cx, cy float64,
) float32 {
	R := eulerToRotation(pan, tilt, roll)
	t := poseToTranslation([6]float64{position[0], position[1], position[2], pan, tilt, roll}, R)

	var totalErr float64
	for i := range points2D {
		proj := projectPoint(points3D[i], R, t, fx, fy, cx, cy)
		dx := proj[0] - points2D[i][0]
		dy := proj[1] - points2D[i][1]
		totalErr += math.Sqrt(dx*dx + dy*dy)
	}
	meanErr := totalErr / float64(len(points2D))

	// Map mean reprojection error to confidence:
	// < 1 pixel -> 1.0, > 10 pixels -> 0.0
	conf := 1.0 - (meanErr / 10.0)
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	return float32(conf)
}
