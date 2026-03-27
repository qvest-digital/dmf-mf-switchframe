package tracking

import (
	"math"
	"testing"
)

func TestSolvePnP_KnownGeometry(t *testing.T) {
	// Set up a known camera pose and project 3D points to get 2D correspondences.
	// Then solve and verify we recover the original pose.
	//
	// Camera at (0, 0, -5) looking along +Z axis (standard CV convention)
	// with small pan/tilt offsets.
	truePan := 3.0
	trueTilt := 2.0
	trueRoll := 0.0
	truePos := [3]float64{0.0, 0.0, -5.0}

	// Camera intrinsics: pixel-scale focal length.
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320.0, 240.0},
		SensorWidth:      0, // Use focalMM directly as pixel focal length
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}

	focalMM := 500.0 // Use as pixel focal length (SensorWidth=0 triggers direct use)
	fx := focalMM
	fy := fx
	cx := intrinsics.PrincipalPoint[0]
	cy := intrinsics.PrincipalPoint[1]

	// 3D world points: non-coplanar, spread near origin (in front of camera at Z=-5).
	points3D := [][3]float64{
		{-1, -1, 0},
		{1, -1, 0},
		{1, 1, 0},
		{-1, 1, 0},
		{0, 0, 0},
		{0.5, 0.5, -1},
		{-0.5, -0.5, -1},
		{0, 0, -2},
		{-1, 0, -0.5},
		{1, 0, -0.5},
		{0, -1, -0.5},
		{0, 1, -0.5},
	}

	// Project points using the known pose.
	R := eulerToRotation(truePan, trueTilt, trueRoll)
	tVec := poseToTranslation([6]float64{truePos[0], truePos[1], truePos[2], truePan, trueTilt, trueRoll}, R)

	points2D := make([][2]float64, len(points3D))
	allVisible := true
	for i, pt := range points3D {
		proj := projectPoint(pt, R, tVec, fx, fy, cx, cy)
		points2D[i] = proj

		// Verify point is in front of camera.
		camZ := R[2][0]*pt[0] + R[2][1]*pt[1] + R[2][2]*pt[2] + tVec[2]
		if camZ <= 0 {
			t.Logf("Point %d (%v) is behind camera (camZ=%.3f)", i, pt, camZ)
			allVisible = false
		}
	}

	if !allVisible {
		t.Fatal("not all points are visible from the test camera pose")
	}

	// Solve PnP.
	pose, err := SolvePnP(points2D, points3D, intrinsics, focalMM)
	if err != nil {
		t.Fatalf("SolvePnP: %v", err)
	}

	// Verify position accuracy.
	posErr := math.Sqrt(
		sq(pose.Position[0]-truePos[0]) +
			sq(pose.Position[1]-truePos[1]) +
			sq(pose.Position[2]-truePos[2]),
	)

	// Verify orientation accuracy.
	panErr := math.Abs(angleDiff(pose.Orientation[0], truePan))
	tiltErr := math.Abs(angleDiff(pose.Orientation[1], trueTilt))
	rollErr := math.Abs(angleDiff(pose.Orientation[2], trueRoll))

	t.Logf("Position error: %.4f m (true: %v, solved: %v)", posErr, truePos, pose.Position)
	t.Logf("Pan error: %.4f deg (true: %.1f, solved: %.4f)", panErr, truePan, pose.Orientation[0])
	t.Logf("Tilt error: %.4f deg (true: %.1f, solved: %.4f)", tiltErr, trueTilt, pose.Orientation[1])
	t.Logf("Roll error: %.4f deg (true: %.1f, solved: %.4f)", rollErr, trueRoll, pose.Orientation[2])
	t.Logf("Confidence: %.4f", pose.Confidence)

	// Tolerance: 10 cm and 5 degrees (DLT is approximate; RANSAC+EPnP would be tighter).
	if posErr > 0.10 {
		t.Errorf("position error %.4f m exceeds 10 cm tolerance", posErr)
	}
	if panErr > 5.0 {
		t.Errorf("pan error %.4f deg exceeds 5 degree tolerance", panErr)
	}
	if tiltErr > 5.0 {
		t.Errorf("tilt error %.4f deg exceeds 5 degree tolerance", tiltErr)
	}
	if rollErr > 5.0 {
		t.Errorf("roll error %.4f deg exceeds 5 degree tolerance", rollErr)
	}
}

func TestSolvePnP_IdentityPose(t *testing.T) {
	// Camera at (0, 0, -5), looking along +Z (standard CV convention), zero rotation.
	truePos := [3]float64{0, 0, -5}
	truePan := 0.0
	trueTilt := 0.0
	trueRoll := 0.0

	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320.0, 240.0},
		SensorWidth:      0, // Use focalMM directly as pixel focal length
		FocalLengthCurve: [][2]float64{{0, 500.0}},
	}

	focalMM := 500.0
	fx := focalMM
	fy := fx
	cx := intrinsics.PrincipalPoint[0]
	cy := intrinsics.PrincipalPoint[1]

	// Non-coplanar points near the origin (in front of camera at Z=-5).
	points3D := [][3]float64{
		{-1, -1, 0},
		{1, -1, 0},
		{1, 1, 0},
		{-1, 1, 0},
		{0, 0, -1},
		{0.5, 0.5, -0.5},
		{-0.5, 0.5, 0.5},
		{0.5, -0.5, 0.5},
	}

	R := eulerToRotation(truePan, trueTilt, trueRoll)
	tVec := poseToTranslation([6]float64{truePos[0], truePos[1], truePos[2], truePan, trueTilt, trueRoll}, R)

	points2D := make([][2]float64, len(points3D))
	for i, pt := range points3D {
		points2D[i] = projectPoint(pt, R, tVec, fx, fy, cx, cy)
	}

	pose, err := SolvePnP(points2D, points3D, intrinsics, focalMM)
	if err != nil {
		t.Fatalf("SolvePnP: %v", err)
	}

	posErr := math.Sqrt(
		sq(pose.Position[0]-truePos[0]) +
			sq(pose.Position[1]-truePos[1]) +
			sq(pose.Position[2]-truePos[2]),
	)

	t.Logf("Position error: %.4f m (true: %v, solved: %v)", posErr, truePos, pose.Position)
	t.Logf("Orientation: pan=%.2f tilt=%.2f roll=%.2f", pose.Orientation[0], pose.Orientation[1], pose.Orientation[2])

	if posErr > 0.10 {
		t.Errorf("position error %.4f m exceeds 10 cm tolerance", posErr)
	}
}

func TestSolvePnP_Errors(t *testing.T) {
	intrinsics := CameraIntrinsics{
		PrincipalPoint:   [2]float64{320, 240},
		SensorWidth:      36.0,
		FocalLengthCurve: [][2]float64{{0, 50.0}},
	}

	t.Run("too few points", func(t *testing.T) {
		points2D := [][2]float64{{1, 2}, {3, 4}, {5, 6}}
		points3D := [][3]float64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}
		_, err := SolvePnP(points2D, points3D, intrinsics, 50.0)
		if err != ErrTooFewPoints {
			t.Fatalf("expected ErrTooFewPoints, got: %v", err)
		}
	})

	t.Run("point count mismatch", func(t *testing.T) {
		points2D := [][2]float64{{1, 2}, {3, 4}}
		points3D := [][3]float64{{1, 2, 3}}
		_, err := SolvePnP(points2D, points3D, intrinsics, 50.0)
		if err != ErrPointCountMismatch {
			t.Fatalf("expected ErrPointCountMismatch, got: %v", err)
		}
	})

	t.Run("zero focal length", func(t *testing.T) {
		points2D := make([][2]float64, 6)
		points3D := make([][3]float64, 6)
		_, err := SolvePnP(points2D, points3D, intrinsics, 0)
		if err != ErrInvalidIntrinsics {
			t.Fatalf("expected ErrInvalidIntrinsics, got: %v", err)
		}
	})

	t.Run("negative focal length", func(t *testing.T) {
		points2D := make([][2]float64, 6)
		points3D := make([][3]float64, 6)
		_, err := SolvePnP(points2D, points3D, intrinsics, -10)
		if err != ErrInvalidIntrinsics {
			t.Fatalf("expected ErrInvalidIntrinsics, got: %v", err)
		}
	})
}

func TestRotationRoundTrip(t *testing.T) {
	// Verify euler -> rotation -> euler round-trip.
	testCases := [][3]float64{
		{0, 0, 0},
		{30, 0, 0},
		{0, 20, 0},
		{0, 0, 15},
		{-45, 30, 10},
		{90, 0, 0},
		{0, -45, 0},
		{10, 20, 30},
	}

	for _, tc := range testCases {
		pan, tilt, roll := tc[0], tc[1], tc[2]
		R := eulerToRotation(pan, tilt, roll)
		p2, t2, r2 := rotationToEuler(R)

		if math.Abs(angleDiff(p2, pan)) > 0.01 {
			t.Errorf("pan: input=%.1f, recovered=%.4f", pan, p2)
		}
		if math.Abs(angleDiff(t2, tilt)) > 0.01 {
			t.Errorf("tilt: input=%.1f, recovered=%.4f", tilt, t2)
		}
		if math.Abs(angleDiff(r2, roll)) > 0.01 {
			t.Errorf("roll: input=%.1f, recovered=%.4f", roll, r2)
		}
	}
}

func TestClosestRotation(t *testing.T) {
	// Start with a valid rotation and add noise, then verify closestRotation
	// produces a proper rotation (det = 1, R * R^T = I).
	R := eulerToRotation(30, 20, 10)

	// Add noise.
	noisy := R
	noisy[0][0] += 0.1
	noisy[1][2] -= 0.05

	result := closestRotation(noisy)

	// Check det(result) ~= 1.
	det := determinant3x3(result)
	if math.Abs(det-1.0) > 0.01 {
		t.Errorf("determinant = %f, expected 1.0", det)
	}

	// Check R * R^T ~= I.
	RRt := multiply3x3(result, transpose3x3(result))
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			expected := 0.0
			if i == j {
				expected = 1.0
			}
			if math.Abs(RRt[i][j]-expected) > 0.01 {
				t.Errorf("R*R^T[%d][%d] = %f, expected %f", i, j, RRt[i][j], expected)
			}
		}
	}
}

func TestDeterminant3x3(t *testing.T) {
	// Identity matrix -> det = 1.
	I := [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	if d := determinant3x3(I); math.Abs(d-1.0) > 1e-10 {
		t.Errorf("identity det = %f, expected 1.0", d)
	}

	// Known matrix.
	M := [3][3]float64{{1, 2, 3}, {0, 1, 4}, {5, 6, 0}}
	expected := 1.0*(1*0-4*6) - 2.0*(0*0-4*5) + 3.0*(0*6-1*5) // 1*(-24) - 2*(-20) + 3*(-5) = -24+40-15 = 1
	if d := determinant3x3(M); math.Abs(d-expected) > 1e-10 {
		t.Errorf("det = %f, expected %f", d, expected)
	}
}

func TestProjectPoint(t *testing.T) {
	// Camera at origin, identity rotation, point at (0, 0, 5).
	// With fx=fy=1, cx=cy=0, projected point should be (0, 0).
	R := [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	tVec := [3]float64{0, 0, 0}

	proj := projectPoint([3]float64{0, 0, 5}, R, tVec, 1, 1, 0, 0)
	if math.Abs(proj[0]) > 1e-10 || math.Abs(proj[1]) > 1e-10 {
		t.Errorf("expected (0,0), got (%f, %f)", proj[0], proj[1])
	}

	// Point at (1, 0, 5) -> (0.2, 0) with fx=1.
	proj = projectPoint([3]float64{1, 0, 5}, R, tVec, 1, 1, 0, 0)
	if math.Abs(proj[0]-0.2) > 1e-10 || math.Abs(proj[1]) > 1e-10 {
		t.Errorf("expected (0.2, 0), got (%f, %f)", proj[0], proj[1])
	}

	// With principal point offset.
	proj = projectPoint([3]float64{0, 0, 5}, R, tVec, 1, 1, 320, 240)
	if math.Abs(proj[0]-320) > 1e-10 || math.Abs(proj[1]-240) > 1e-10 {
		t.Errorf("expected (320, 240), got (%f, %f)", proj[0], proj[1])
	}
}

func TestL2DistanceSquared(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{4, 5, 6}
	// (4-1)^2 + (5-2)^2 + (6-3)^2 = 9+9+9 = 27
	d := l2DistanceSquared(a, b)
	if math.Abs(float64(d)-27.0) > 1e-5 {
		t.Errorf("expected 27, got %f", d)
	}

	// Same vector -> 0.
	d = l2DistanceSquared(a, a)
	if d != 0 {
		t.Errorf("expected 0, got %f", d)
	}

	// Different lengths.
	d = l2DistanceSquared([]float32{1, 2}, []float32{1, 2, 3})
	if d != 0 {
		t.Errorf("expected 0, got %f", d)
	}
}

func TestMatchDescriptors(t *testing.T) {
	// Create frame descriptors and map descriptors with known matches.
	numFrame := 3
	frameDescs := make([]float32, numFrame*superpointDescDim)

	mapDescs := make([][]float32, 5)
	for i := range mapDescs {
		mapDescs[i] = make([]float32, superpointDescDim)
		for j := range mapDescs[i] {
			mapDescs[i][j] = float32(i*superpointDescDim+j) * 0.01
		}
	}

	// Frame descriptor 0 matches map descriptor 2 exactly.
	copy(frameDescs[0:superpointDescDim], mapDescs[2])

	// Frame descriptor 1 matches map descriptor 4 exactly.
	copy(frameDescs[superpointDescDim:2*superpointDescDim], mapDescs[4])

	// Frame descriptor 2 is random noise (no good match).
	for j := 0; j < superpointDescDim; j++ {
		frameDescs[2*superpointDescDim+j] = float32(j) * 100.0
	}

	matches := matchDescriptors(frameDescs, mapDescs, numFrame, 0.7)

	// Should find at least the two exact matches.
	if len(matches) < 2 {
		t.Fatalf("expected at least 2 matches, got %d", len(matches))
	}

	// Verify the exact matches are present.
	foundMap2 := false
	foundMap4 := false
	for _, m := range matches {
		if m.queryIdx == 0 && m.trainIdx == 2 {
			foundMap2 = true
		}
		if m.queryIdx == 1 && m.trainIdx == 4 {
			foundMap4 = true
		}
	}
	if !foundMap2 {
		t.Error("expected match: frame[0] -> map[2]")
	}
	if !foundMap4 {
		t.Error("expected match: frame[1] -> map[4]")
	}
}

// Helper functions.

func sq(x float64) float64 { return x * x }

func angleDiff(a, b float64) float64 {
	d := a - b
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	return d
}
