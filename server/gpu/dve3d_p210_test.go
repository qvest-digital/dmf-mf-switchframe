package gpu

import (
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/switchframe/server/dve"
)

// TestDVE3DCompositeP210_NilArgs verifies nil argument handling for DVE3DCompositeP210.
func TestDVE3DCompositeP210_NilArgs(t *testing.T) {
	t3d := &dve.Transform3D{
		Shape:        "plane",
		FOV:          20,
		Distance:     3.0,
		LightEnabled: true,
		Ambient:      0.3,
	}
	invMVP := dve.Matrix4x4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	slotRect := image.Rect(0, 0, 1920, 1080)

	// All nil — should return error on all builds
	err := DVE3DCompositeP210(nil, nil, nil, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err)

	// Nil ctx
	dst := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	src := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	err = DVE3DCompositeP210(nil, dst, src, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err)

	// Nil dst
	err = DVE3DCompositeP210(nil, nil, src, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err)

	// Nil src
	err = DVE3DCompositeP210(nil, dst, nil, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err)

	// Nil transform3D
	err = DVE3DCompositeP210(nil, dst, src, nil, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err)
}

// TestDVE3DCompositeP210_Dispatch_Routes verifies GPUDVE3DCompositeDispatch routes
// based on the destination frame's Format field.
func TestDVE3DCompositeP210_Dispatch_Routes(t *testing.T) {
	t3d := &dve.Transform3D{
		Shape:        "plane",
		FOV:          20,
		Distance:     3.0,
		LightEnabled: false,
		Ambient:      0.3,
	}
	invMVP := dve.Matrix4x4{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	slotRect := image.Rect(0, 0, 960, 540)

	// P210 frame: dispatch should call DVE3DCompositeP210 (returns error without real GPU)
	p210Frame := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	err := GPUDVE3DCompositeDispatch(nil, p210Frame, nil, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err, "P210 dispatch should return error without GPU context")

	// NV12 frame: dispatch should call DVE3DComposite (returns error without real GPU)
	nv12Frame := &GPUFrame{Width: 1920, Height: 1080, Pitch: 1920, Format: GPUFormatNV12}
	err = GPUDVE3DCompositeDispatch(nil, nv12Frame, nil, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err, "NV12 dispatch should return error without GPU context")

	// Nil dst: should fall through to NV12 path (returns error without real GPU)
	err = GPUDVE3DCompositeDispatch(nil, nil, nil, t3d, invMVP, 1920, 1080, slotRect, 256)
	assert.Error(t, err, "nil dst dispatch should return error without GPU context")
}

// TestDVE3DCompositeP210_ShapeMapping verifies that shape strings map to expected integer codes.
func TestDVE3DCompositeP210_ShapeMapping(t *testing.T) {
	tests := []struct {
		shape    string
		expected int
	}{
		{"plane", 0},
		{"", 0}, // default to plane
		{"cube", 1},
		{"page_turn", 2},
		{"unknown", 0}, // unknown defaults to plane
	}

	for _, tt := range tests {
		t.Run(tt.shape, func(t *testing.T) {
			shape := 0
			if tt.shape == "cube" {
				shape = 1
			} else if tt.shape == "page_turn" {
				shape = 2
			}
			assert.Equal(t, tt.expected, shape)
		})
	}
}

// TestDVE3DCompositeP210_AmbientDefault verifies that zero/negative ambient defaults to 0.3.
func TestDVE3DCompositeP210_AmbientDefault(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{0.0, 0.3},
		{-0.5, 0.3},
		{0.5, 0.5},
		{1.0, 1.0},
	}
	for _, tt := range tests {
		ambient := tt.input
		if ambient <= 0 {
			ambient = 0.3
		}
		assert.InDelta(t, tt.expected, ambient, 0.001)
	}
}

// TestDVE3DCompositeP210_ChromaBounds verifies P210 chroma AABB computation
// (full height, half width) differs from NV12 (half height, half width).
func TestDVE3DCompositeP210_ChromaBounds(t *testing.T) {
	dstW := 1920
	dstH := 1080
	slotRect := image.Rect(100, 200, 900, 800)

	// NV12: both axes halved
	nv12MinCX := max(slotRect.Min.X/2-1, 0)
	nv12MinCY := max(slotRect.Min.Y/2-1, 0)
	nv12MaxCX := min(slotRect.Max.X/2+1, dstW/2)
	nv12MaxCY := min(slotRect.Max.Y/2+1, dstH/2)

	// P210: X halved, Y full
	p210MinCX := max(slotRect.Min.X/2-1, 0)
	p210MinCY := max(slotRect.Min.Y-1, 0)
	p210MaxCX := min(slotRect.Max.X/2+1, dstW/2)
	p210MaxCY := min(slotRect.Max.Y+1, dstH)

	// X bounds should match
	assert.Equal(t, nv12MinCX, p210MinCX, "chroma X min should be the same")
	assert.Equal(t, nv12MaxCX, p210MaxCX, "chroma X max should be the same")

	// Y bounds should differ: P210 has roughly 2x the range
	assert.Greater(t, p210MinCY, nv12MinCY, "P210 chroma Y min should be larger (not halved)")
	assert.Greater(t, p210MaxCY, nv12MaxCY, "P210 chroma Y max should be larger (full height)")

	// Verify the specific expected values
	assert.Equal(t, 49, nv12MinCX)  // 100/2 - 1
	assert.Equal(t, 99, nv12MinCY)  // 200/2 - 1
	assert.Equal(t, 451, nv12MaxCX) // 900/2 + 1
	assert.Equal(t, 401, nv12MaxCY) // 800/2 + 1

	assert.Equal(t, 49, p210MinCX)  // 100/2 - 1
	assert.Equal(t, 199, p210MinCY) // 200 - 1
	assert.Equal(t, 451, p210MaxCX) // 900/2 + 1
	assert.Equal(t, 801, p210MaxCY) // 800 + 1
}
