package tracking

import (
	"math"
	"testing"
)

const epsilon = 1e-9

func TestProjectionMatrix_BasicProperties(t *testing.T) {
	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 50}},
		PrincipalPoint:   [2]float64{0.5, 0.5},
		SensorWidth:      36,
	}

	m := ProjectionMatrix(intrinsics, 1920, 1080, 0.1, 100)

	// With centered principal point (0.5, 0.5), the off-diagonal
	// projection elements m[8] and m[9] should be zero.
	if math.Abs(m[8]) > epsilon {
		t.Errorf("m[8] (x offset) should be ~0 for centered PP, got %f", m[8])
	}
	if math.Abs(m[9]) > epsilon {
		t.Errorf("m[9] (y offset) should be ~0 for centered PP, got %f", m[9])
	}

	// m[11] should be -1 (perspective division).
	if math.Abs(m[11]-(-1.0)) > epsilon {
		t.Errorf("m[11] should be -1, got %f", m[11])
	}

	// m[15] should be 0 (perspective projection, not affine).
	if math.Abs(m[15]) > epsilon {
		t.Errorf("m[15] should be 0, got %f", m[15])
	}

	// m[0] and m[5] should be positive (scale factors).
	if m[0] <= 0 {
		t.Errorf("m[0] (x scale) should be positive, got %f", m[0])
	}
	if m[5] <= 0 {
		t.Errorf("m[5] (y scale) should be positive, got %f", m[5])
	}
}

func TestProjectionMatrix_FocalLengthAffectsScale(t *testing.T) {
	base := CameraIntrinsics{
		PrincipalPoint: [2]float64{0.5, 0.5},
		SensorWidth:    36,
	}

	// Wide angle lens.
	wide := base
	wide.FocalLengthCurve = [][2]float64{{0, 24}}
	mWide := ProjectionMatrix(wide, 1920, 1080, 0.1, 100)

	// Telephoto lens.
	tele := base
	tele.FocalLengthCurve = [][2]float64{{0, 200}}
	mTele := ProjectionMatrix(tele, 1920, 1080, 0.1, 100)

	// Telephoto should have larger scale factors (narrower FOV).
	if mTele[0] <= mWide[0] {
		t.Errorf("telephoto x scale (%f) should be > wide x scale (%f)", mTele[0], mWide[0])
	}
	if mTele[5] <= mWide[5] {
		t.Errorf("telephoto y scale (%f) should be > wide y scale (%f)", mTele[5], mWide[5])
	}
}

func TestProjectionMatrix_OffCenterPrincipalPoint(t *testing.T) {
	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 50}},
		PrincipalPoint:   [2]float64{0.55, 0.48},
		SensorWidth:      36,
	}

	m := ProjectionMatrix(intrinsics, 1920, 1080, 0.1, 100)

	// Off-center principal point should produce non-zero offsets.
	if math.Abs(m[8]) < epsilon {
		t.Error("m[8] should be non-zero for off-center PP")
	}
	if math.Abs(m[9]) < epsilon {
		t.Error("m[9] should be non-zero for off-center PP")
	}
}

func TestProjectionMatrix_InvalidInputs(t *testing.T) {
	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 50}},
		PrincipalPoint:   [2]float64{0.5, 0.5},
		SensorWidth:      36,
	}

	tests := []struct {
		name   string
		w, h   int
		near   float64
		far    float64
	}{
		{"zero width", 0, 1080, 0.1, 100},
		{"zero height", 1920, 0, 0.1, 100},
		{"negative near", 1920, 1080, -1, 100},
		{"near >= far", 1920, 1080, 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ProjectionMatrix(intrinsics, tt.w, tt.h, tt.near, tt.far)
			// Should return zero matrix for invalid inputs.
			for i, v := range m {
				if v != 0 {
					t.Errorf("m[%d] = %f, want 0 for invalid inputs", i, v)
				}
			}
		})
	}
}

func TestProjectionMatrix_DefaultFocalLength(t *testing.T) {
	// Empty focal length curve should use default 50mm.
	intrinsics := CameraIntrinsics{
		PrincipalPoint: [2]float64{0.5, 0.5},
		SensorWidth:    36,
	}

	m := ProjectionMatrix(intrinsics, 1920, 1080, 0.1, 100)

	// Should still produce a valid projection.
	if m[0] <= 0 {
		t.Errorf("m[0] should be positive with default focal length, got %f", m[0])
	}
}

func TestProjectionMatrix_CorrectFOV(t *testing.T) {
	// 50mm on 36mm sensor = horizontal FOV of ~39.6 degrees.
	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 50}},
		PrincipalPoint:   [2]float64{0.5, 0.5},
		SensorWidth:      36,
	}

	m := ProjectionMatrix(intrinsics, 1920, 1080, 0.1, 100)

	// m[0] = 2*fx/width where fx = focal*width/sensorW
	// So m[0] = 2*focal/sensorW = 2*50/36 = 2.778
	expected := 2.0 * 50.0 / 36.0
	if math.Abs(m[0]-expected) > epsilon {
		t.Errorf("m[0] = %f, want %f (2*f/sensorW)", m[0], expected)
	}
}

func TestViewMatrix_IdentityPose(t *testing.T) {
	pose := CameraPose{
		Position:    [3]float64{0, 0, 0},
		Orientation: [3]float64{0, 0, 0},
	}

	m := ViewMatrix(pose)

	// Identity rotation + zero translation = identity matrix.
	identity := [16]float64{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}

	for i := range m {
		if math.Abs(m[i]-identity[i]) > epsilon {
			t.Errorf("m[%d] = %f, want %f", i, m[i], identity[i])
		}
	}
}

func TestViewMatrix_TranslationOnly(t *testing.T) {
	pose := CameraPose{
		Position:    [3]float64{1, 2, 3},
		Orientation: [3]float64{0, 0, 0},
	}

	m := ViewMatrix(pose)

	// With no rotation, the view matrix should have -T in the last column.
	if math.Abs(m[12]-(-1)) > epsilon {
		t.Errorf("m[12] = %f, want -1", m[12])
	}
	if math.Abs(m[13]-(-2)) > epsilon {
		t.Errorf("m[13] = %f, want -2", m[13])
	}
	if math.Abs(m[14]-(-3)) > epsilon {
		t.Errorf("m[14] = %f, want -3", m[14])
	}
}

func TestViewMatrix_90DegreePan(t *testing.T) {
	pose := CameraPose{
		Position:    [3]float64{0, 0, 0},
		Orientation: [3]float64{90, 0, 0}, // 90 degree pan (rotation around Z)
	}

	m := ViewMatrix(pose)

	// After 90 degree pan around Z:
	// R_z(90) = [0 -1 0; 1 0 0; 0 0 1]
	// View matrix uses R^T, so:
	// m[0..2] (col 0) = R^T col 0 = [0, 1, 0]
	// m[4..6] (col 1) = R^T col 1 = [-1, 0, 0]

	if math.Abs(m[0]) > epsilon {
		t.Errorf("m[0] should be ~0, got %f", m[0])
	}
	if math.Abs(m[1]-1.0) > epsilon {
		t.Errorf("m[1] should be ~1, got %f", m[1])
	}
	if math.Abs(m[4]-(-1.0)) > epsilon {
		t.Errorf("m[4] should be ~-1, got %f", m[4])
	}
}

func TestViewMatrix_Determinant(t *testing.T) {
	pose := CameraPose{
		Position:    [3]float64{5, -3, 7},
		Orientation: [3]float64{30, 45, 15},
	}

	m := ViewMatrix(pose)

	// The determinant of a rigid body transform should be +1.
	det := mat4Det(m)
	if math.Abs(det-1.0) > 1e-6 {
		t.Errorf("determinant = %f, want 1.0 for rigid body transform", det)
	}
}

func TestViewProjectionMatrix_RoundTrip(t *testing.T) {
	pose := CameraPose{
		Position:    [3]float64{0, 0, 5},
		Orientation: [3]float64{0, 0, 0},
	}

	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 50}},
		PrincipalPoint:   [2]float64{0.5, 0.5},
		SensorWidth:      36,
	}

	vp := ViewProjectionMatrix(pose, intrinsics, 1920, 1080, 0.1, 100)

	// A point at the camera origin in world space (0, 0, 5) should map
	// to the center of the screen in clip space (after perspective divide).
	// In camera space this point is (0, 0, 0) which gets mapped by the
	// projection matrix. Actually, the origin in camera space after the
	// view transform would be at distance 0 from the camera — let's use
	// a point directly in front of the camera instead.
	//
	// Point at world (0, 0, 0): camera at (0,0,5) looking along -Z in camera space
	// means this point is at camera space (0, 0, -5).
	// After projection: x_clip = m[0]*0 = 0, y_clip = m[5]*0 = 0
	// So x_ndc = 0, y_ndc = 0 — centered.

	// Transform world point (0, 0, 0) through VP.
	clipW := vp[3]*0 + vp[7]*0 + vp[11]*0 + vp[15]
	clipX := vp[0]*0 + vp[4]*0 + vp[8]*0 + vp[12]
	clipY := vp[1]*0 + vp[5]*0 + vp[9]*0 + vp[13]

	if clipW == 0 {
		t.Fatal("clip W is 0, can't do perspective divide")
	}

	ndcX := clipX / clipW
	ndcY := clipY / clipW

	if math.Abs(ndcX) > epsilon {
		t.Errorf("NDC X should be ~0 for centered point, got %f", ndcX)
	}
	if math.Abs(ndcY) > epsilon {
		t.Errorf("NDC Y should be ~0 for centered point, got %f", ndcY)
	}
}

func TestViewProjectionMatrix_OffCenterProjection(t *testing.T) {
	pose := CameraPose{
		Position:    [3]float64{0, 0, 10},
		Orientation: [3]float64{0, 0, 0},
	}

	intrinsics := CameraIntrinsics{
		FocalLengthCurve: [][2]float64{{0, 50}},
		PrincipalPoint:   [2]float64{0.5, 0.5},
		SensorWidth:      36,
	}

	vp := ViewProjectionMatrix(pose, intrinsics, 1920, 1080, 0.1, 100)

	// A point at world (2, 0, 0) should project to the right of center.
	clipW := vp[3]*2 + vp[7]*0 + vp[11]*0 + vp[15]
	clipX := vp[0]*2 + vp[4]*0 + vp[8]*0 + vp[12]

	ndcX := clipX / clipW

	if ndcX <= 0 {
		t.Errorf("point at +X should project to positive NDC X, got %f", ndcX)
	}
}

func TestMat4Mul_Identity(t *testing.T) {
	identity := [16]float64{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}

	other := [16]float64{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12,
		13, 14, 15, 16,
	}

	result := mat4Mul(identity, other)
	for i := range result {
		if math.Abs(result[i]-other[i]) > epsilon {
			t.Errorf("identity * M: result[%d] = %f, want %f", i, result[i], other[i])
		}
	}

	result2 := mat4Mul(other, identity)
	for i := range result2 {
		if math.Abs(result2[i]-other[i]) > epsilon {
			t.Errorf("M * identity: result[%d] = %f, want %f", i, result2[i], other[i])
		}
	}
}

// mat4Det computes the determinant of a 4x4 column-major matrix.
func mat4Det(m [16]float64) float64 {
	// Expand along first row.
	return m[0]*(m[5]*(m[10]*m[15]-m[14]*m[11])-m[9]*(m[6]*m[15]-m[14]*m[7])+m[13]*(m[6]*m[11]-m[10]*m[7])) -
		m[4]*(m[1]*(m[10]*m[15]-m[14]*m[11])-m[9]*(m[2]*m[15]-m[14]*m[3])+m[13]*(m[2]*m[11]-m[10]*m[3])) +
		m[8]*(m[1]*(m[6]*m[15]-m[14]*m[7])-m[5]*(m[2]*m[15]-m[14]*m[3])+m[13]*(m[2]*m[7]-m[6]*m[3])) -
		m[12]*(m[1]*(m[6]*m[11]-m[10]*m[7])-m[5]*(m[2]*m[11]-m[10]*m[3])+m[9]*(m[2]*m[7]-m[6]*m[3]))
}
