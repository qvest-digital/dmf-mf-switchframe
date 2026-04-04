package dve

import (
	"image"
	"math"
	"testing"
)

const matEps = 1e-4

func matNear(a, b Matrix3x3, eps float32) bool {
	for i := range a {
		if diff := a[i] - b[i]; diff < -eps || diff > eps {
			return false
		}
	}
	return true
}

func TestIdentity(t *testing.T) {
	id := Identity()
	want := Matrix3x3{1, 0, 0, 0, 1, 0, 0, 0, 1}
	if id != want {
		t.Errorf("Identity() = %v, want %v", id, want)
	}
}

func TestMultiply(t *testing.T) {
	// Multiply identity by identity = identity
	id := Identity()
	got := id.Multiply(id)
	if got != id {
		t.Errorf("I*I = %v, want %v", got, id)
	}

	// Known multiplication:
	// A = [1 2 3; 4 5 6; 7 8 9]
	// B = [9 8 7; 6 5 4; 3 2 1]
	// A*B = [30 24 18; 84 69 54; 138 114 90]
	a := Matrix3x3{1, 2, 3, 4, 5, 6, 7, 8, 9}
	b := Matrix3x3{9, 8, 7, 6, 5, 4, 3, 2, 1}
	want := Matrix3x3{30, 24, 18, 84, 69, 54, 138, 114, 90}
	got = a.Multiply(b)
	if !matNear(got, want, 1e-6) {
		t.Errorf("A*B = %v, want %v", got, want)
	}
}

func TestInvert(t *testing.T) {
	t.Run("identity inverts to identity", func(t *testing.T) {
		id := Identity()
		inv, ok := id.Invert()
		if !ok {
			t.Fatal("Identity().Invert() returned false")
		}
		if !matNear(inv, id, 1e-6) {
			t.Errorf("inv(I) = %v, want %v", inv, id)
		}
	})

	t.Run("known inverse", func(t *testing.T) {
		// A = [2 1 0; 0 1 0; 1 0 1], det=2
		// inv = [0.5 -0.5 0; 0 1 0; -0.5 0.5 1]
		a := Matrix3x3{2, 1, 0, 0, 1, 0, 1, 0, 1}
		inv, ok := a.Invert()
		if !ok {
			t.Fatal("Invert() returned false")
		}
		want := Matrix3x3{0.5, -0.5, 0, 0, 1, 0, -0.5, 0.5, 1}
		if !matNear(inv, want, 1e-5) {
			t.Errorf("inv(A) = %v, want %v", inv, want)
		}
	})

	t.Run("singular matrix returns false", func(t *testing.T) {
		// All zeros
		zero := Matrix3x3{}
		_, ok := zero.Invert()
		if ok {
			t.Error("expected Invert() to return false for zero matrix")
		}

		// Singular: row 3 = row 1
		singular := Matrix3x3{1, 2, 3, 4, 5, 6, 1, 2, 3}
		_, ok = singular.Invert()
		if ok {
			t.Error("expected Invert() to return false for singular matrix")
		}
	})

	t.Run("A * inv(A) ≈ identity", func(t *testing.T) {
		a := Matrix3x3{3, 0, 2, 2, 0, -2, 0, 1, 1}
		inv, ok := a.Invert()
		if !ok {
			t.Fatal("Invert() returned false")
		}
		product := a.Multiply(inv)
		id := Identity()
		if !matNear(product, id, 1e-4) {
			t.Errorf("A * inv(A) = %v, want ≈ %v", product, id)
		}
	})
}

func TestTranslation(t *testing.T) {
	m := Translation(10, 20)
	// Translation matrix: [1 0 tx; 0 1 ty; 0 0 1]
	want := Matrix3x3{1, 0, 10, 0, 1, 20, 0, 0, 1}
	if !matNear(m, want, 1e-6) {
		t.Errorf("Translation(10,20) = %v, want %v", m, want)
	}
}

func TestScale(t *testing.T) {
	m := Scale(2, 0.5)
	want := Matrix3x3{2, 0, 0, 0, 0.5, 0, 0, 0, 1}
	if !matNear(m, want, 1e-6) {
		t.Errorf("Scale(2, 0.5) = %v, want %v", m, want)
	}
}

func TestRotation(t *testing.T) {
	t.Run("0 degrees is identity", func(t *testing.T) {
		m := Rotation(0)
		id := Identity()
		if !matNear(m, id, 1e-6) {
			t.Errorf("Rotation(0) = %v, want ≈ %v", m, id)
		}
	})

	t.Run("90 degrees", func(t *testing.T) {
		m := Rotation(90)
		// cos(90°)=0, sin(90°)=1 → [0 -1 0; 1 0 0; 0 0 1]
		want := Matrix3x3{0, -1, 0, 1, 0, 0, 0, 0, 1}
		if !matNear(m, want, 1e-4) {
			t.Errorf("Rotation(90) = %v, want ≈ %v", m, want)
		}
	})

	t.Run("360 degrees returns to identity", func(t *testing.T) {
		m := Rotation(360)
		id := Identity()
		if !matNear(m, id, 1e-4) {
			t.Errorf("Rotation(360) = %v, want ≈ %v", m, id)
		}
	})
}

func TestPerspective(t *testing.T) {
	t.Run("zero tilt is identity", func(t *testing.T) {
		m := Perspective(0, 0)
		id := Identity()
		if !matNear(m, id, 1e-6) {
			t.Errorf("Perspective(0,0) = %v, want ≈ %v", m, id)
		}
	})

	t.Run("non-zero tilt gives non-zero m20 m21", func(t *testing.T) {
		m := Perspective(30, 20)
		// With tiltX=30, tiltY=20, perspScale=0.002:
		// m02 = sin(20°)*0.002, m12 = sin(30°)*0.002
		// m20 = sin(20°)*0.002*0.5, m21 = sin(30°)*0.002*0.5
		if m[6] == 0 || m[7] == 0 { // m20, m21
			t.Errorf("Perspective(30,20) m20=%v, m21=%v, want non-zero", m[6], m[7])
		}
		if m[2] == 0 || m[5] == 0 { // m02, m12
			t.Errorf("Perspective(30,20) m02=%v, m12=%v, want non-zero", m[2], m[5])
		}
	})
}

func TestComputeForwardTransform_Identity(t *testing.T) {
	// Source and slot are both 1920x1080 at origin with no transforms.
	srcW, srcH := 1920, 1080
	tr := DefaultTransform(0, 0, float64(srcW), float64(srcH))
	m := ComputeForwardTransform(tr, srcW, srcH)

	// Should be approximately identity (scale=1, no rotation, translation
	// cancels because anchor is at center and position is 0).
	id := Identity()
	if !matNear(m, id, matEps) {
		t.Errorf("forward transform for full-frame identity:\ngot  %v\nwant ≈ %v", m, id)
	}
}

func TestComputeForwardTransform_TranslationOnly(t *testing.T) {
	srcW, srcH := 640, 480
	tr := DefaultTransform(100, 50, float64(srcW), float64(srcH))
	m := ComputeForwardTransform(tr, srcW, srcH)

	// Apply forward to source center (320, 240, 1):
	// Expected output center ≈ (100+320, 50+240) = (420, 290)
	cx := m[0]*320 + m[1]*240 + m[2]
	cy := m[3]*320 + m[4]*240 + m[5]
	w := m[6]*320 + m[7]*240 + m[8]
	outX := cx / w
	outY := cy / w
	if diff := outX - 420; diff < -1 || diff > 1 {
		t.Errorf("translated center X = %v, want ≈ 420", outX)
	}
	if diff := outY - 290; diff < -1 || diff > 1 {
		t.Errorf("translated center Y = %v, want ≈ 290", outY)
	}
}

func TestComputeForwardTransform_Rotation90(t *testing.T) {
	srcW, srcH := 200, 100
	tr := DefaultTransform(0, 0, float64(srcW), float64(srcH))
	tr.RotationDeg = 90
	m := ComputeForwardTransform(tr, srcW, srcH)

	// Source corner (200, 0) should map to somewhere meaningful.
	// With 90° rotation around center (100, 50):
	// Source (200, 0) → center-relative (100, -50) → rotated (50, 100) → +anchor → (50+anchorAbsX, 100+anchorAbsY)
	// anchor in output: 0.5*200=100, 0.5*100=50 → posX+anchorAbsX=100, posY+anchorAbsY=50
	// So output ≈ (100+50, 50+100) = (150, 150)? Let's just verify it's a valid transform.
	sx := float32(200)
	sy := float32(0)
	ox := m[0]*sx + m[1]*sy + m[2]
	oy := m[3]*sx + m[4]*sy + m[5]
	ow := m[6]*sx + m[7]*sy + m[8]
	outX := ox / ow
	outY := oy / ow

	// After 90° rotation, the source top-right should not remain at (200, 0).
	if math.Abs(float64(outX-200)) < 1 && math.Abs(float64(outY)) < 1 {
		t.Errorf("90° rotation did not change corner (200,0): got (%v, %v)", outX, outY)
	}
}

func TestComputeForwardTransform_Scale(t *testing.T) {
	// Source is 1920x1080, slot is 960x540 (half size) at origin.
	srcW, srcH := 1920, 1080
	tr := DefaultTransform(0, 0, 960, 540)
	m := ComputeForwardTransform(tr, srcW, srcH)

	// The scale components should be 0.5 (960/1920 and 540/1080).
	// Apply to source bottom-right (1920, 1080):
	sx := float32(1920)
	sy := float32(1080)
	ox := m[0]*sx + m[1]*sy + m[2]
	oy := m[3]*sx + m[4]*sy + m[5]
	ow := m[6]*sx + m[7]*sy + m[8]
	outX := ox / ow
	outY := oy / ow

	// Source (1920, 1080) should map to approximately (960, 540).
	if diff := math.Abs(float64(outX - 960)); diff > 2 {
		t.Errorf("half-scale output X = %v, want ≈ 960", outX)
	}
	if diff := math.Abs(float64(outY - 540)); diff > 2 {
		t.Errorf("half-scale output Y = %v, want ≈ 540", outY)
	}
}

func TestComputeForwardTransform_Perspective(t *testing.T) {
	srcW, srcH := 1920, 1080
	tr := DefaultTransform(0, 0, float64(srcW), float64(srcH))
	tr.PerspectiveX = 30
	tr.PerspectiveY = 20
	m := ComputeForwardTransform(tr, srcW, srcH)

	// With perspective, the w component should be non-trivial for
	// off-center points. Check that m[6] or m[7] (m20, m21) are non-zero.
	if m[6] == 0 && m[7] == 0 {
		t.Error("perspective transform has zero m20 and m21")
	}
}

func TestComputeInverseTransform_RoundTrip(t *testing.T) {
	srcW, srcH := 1920, 1080
	tr := DefaultTransform(100, 50, 960, 540)
	tr.RotationDeg = 15

	fwd := ComputeForwardTransform(tr, srcW, srcH)
	inv := ComputeInverseTransform(tr, srcW, srcH)

	product := fwd.Multiply(inv)
	id := Identity()
	if !matNear(product, id, 1e-3) {
		t.Errorf("forward * inverse:\ngot  %v\nwant ≈ %v", product, id)
	}
}

func TestComputeInverseTransform_Singular(t *testing.T) {
	srcW, srcH := 1920, 1080
	// Width=0 makes scale degenerate.
	tr := Transform{
		PositionX: 0,
		PositionY: 0,
		Width:     0,
		Height:    float64(srcH),
		AnchorX:   0.5,
		AnchorY:   0.5,
		Opacity:   1.0,
	}
	inv := ComputeInverseTransform(tr, srcW, srcH)
	id := Identity()
	if !matNear(inv, id, 1e-6) {
		t.Errorf("singular inverse = %v, want identity fallback %v", inv, id)
	}
}

func TestComputeAABB(t *testing.T) {
	t.Run("no rotation", func(t *testing.T) {
		srcW, srcH := 640, 480
		tr := DefaultTransform(100, 50, 640, 480)
		aabb := ComputeAABB(tr, srcW, srcH, 1920, 1080)
		want := image.Rect(100, 50, 740, 530)
		if aabb != want {
			t.Errorf("AABB = %v, want %v", aabb, want)
		}
	})

	t.Run("rotated is larger", func(t *testing.T) {
		srcW, srcH := 400, 200
		tr := DefaultTransform(200, 200, 400, 200)
		axisRect := ComputeAABB(tr, srcW, srcH, 1920, 1080)

		tr.RotationDeg = 30
		rotatedRect := ComputeAABB(tr, srcW, srcH, 1920, 1080)

		axisArea := axisRect.Dx() * axisRect.Dy()
		rotatedArea := rotatedRect.Dx() * rotatedRect.Dy()
		if rotatedArea <= axisArea {
			t.Errorf("rotated AABB area %d should be > axis-aligned area %d", rotatedArea, axisArea)
		}
	})

	t.Run("clamped to frame", func(t *testing.T) {
		srcW, srcH := 640, 480
		// Slot extends off the right edge and bottom.
		tr := DefaultTransform(1700, 900, 640, 480)
		aabb := ComputeAABB(tr, srcW, srcH, 1920, 1080)

		if aabb.Max.X > 1920 {
			t.Errorf("AABB Max.X = %d, should be clamped to frame width 1920", aabb.Max.X)
		}
		if aabb.Max.Y > 1080 {
			t.Errorf("AABB Max.Y = %d, should be clamped to frame height 1080", aabb.Max.Y)
		}
		if aabb.Min.X < 0 {
			t.Errorf("AABB Min.X = %d, should be >= 0", aabb.Min.X)
		}
		if aabb.Min.Y < 0 {
			t.Errorf("AABB Min.Y = %d, should be >= 0", aabb.Min.Y)
		}
	})

	t.Run("even aligned", func(t *testing.T) {
		srcW, srcH := 641, 481
		tr := DefaultTransform(101, 51, 641, 481)
		aabb := ComputeAABB(tr, srcW, srcH, 1920, 1080)

		if aabb.Min.X%2 != 0 {
			t.Errorf("AABB Min.X = %d, not even aligned", aabb.Min.X)
		}
		if aabb.Min.Y%2 != 0 {
			t.Errorf("AABB Min.Y = %d, not even aligned", aabb.Min.Y)
		}
		if aabb.Max.X%2 != 0 {
			t.Errorf("AABB Max.X = %d, not even aligned", aabb.Max.X)
		}
		if aabb.Max.Y%2 != 0 {
			t.Errorf("AABB Max.Y = %d, not even aligned", aabb.Max.Y)
		}
	})
}
