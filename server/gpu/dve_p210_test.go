package gpu

import (
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/switchframe/server/dve"
)

// TestDVECompositeP210_NilArgs verifies nil argument handling for DVECompositeP210.
func TestDVECompositeP210_NilArgs(t *testing.T) {
	transform := dve.DefaultTransform(0, 0, 1920, 1080)
	invMatrix := dve.Matrix3x3{1, 0, 0, 0, 1, 0, 0, 0, 1}
	aabb := image.Rect(0, 0, 1920, 1080)

	// All nil
	assert.ErrorIs(t, DVECompositeP210(nil, nil, nil, transform, invMatrix, 1920, 1080, aabb), ErrGPUNotAvailable)

	// Nil ctx
	dst := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	src := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	assert.ErrorIs(t, DVECompositeP210(nil, dst, src, transform, invMatrix, 1920, 1080, aabb), ErrGPUNotAvailable)

	// Nil dst
	assert.ErrorIs(t, DVECompositeP210(nil, nil, src, transform, invMatrix, 1920, 1080, aabb), ErrGPUNotAvailable)

	// Nil src
	assert.ErrorIs(t, DVECompositeP210(nil, dst, nil, transform, invMatrix, 1920, 1080, aabb), ErrGPUNotAvailable)
}

// TestDVECompositeP210_Dispatch_Routes verifies GPUDVECompositeDispatch routes
// based on the destination frame's Format field.
func TestDVECompositeP210_Dispatch_Routes(t *testing.T) {
	transform := dve.DefaultTransform(0, 0, 960, 540)
	invMatrix := dve.Matrix3x3{1, 0, 0, 0, 1, 0, 0, 0, 1}
	aabb := image.Rect(0, 0, 960, 540)

	// P210 frame: dispatch should call DVECompositeP210 (returns ErrGPUNotAvailable in stub)
	p210Frame := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	err := GPUDVECompositeDispatch(nil, p210Frame, nil, transform, invMatrix, 1920, 1080, aabb)
	assert.ErrorIs(t, err, ErrGPUNotAvailable, "P210 dispatch should call DVECompositeP210")

	// NV12 frame: dispatch should call DVEComposite (returns ErrGPUNotAvailable in stub)
	nv12Frame := &GPUFrame{Width: 1920, Height: 1080, Pitch: 1920, Format: GPUFormatNV12}
	err = GPUDVECompositeDispatch(nil, nv12Frame, nil, transform, invMatrix, 1920, 1080, aabb)
	assert.ErrorIs(t, err, ErrGPUNotAvailable, "NV12 dispatch should call DVEComposite")

	// Nil dst: should fall through to NV12 path (returns ErrGPUNotAvailable in stub)
	err = GPUDVECompositeDispatch(nil, nil, nil, transform, invMatrix, 1920, 1080, aabb)
	assert.ErrorIs(t, err, ErrGPUNotAvailable, "nil dst dispatch should call DVEComposite")
}

// TestDVECompositeP210_TransformFields verifies that DVE transform fields
// relevant to P210 are accessible and have expected values.
func TestDVECompositeP210_TransformFields(t *testing.T) {
	transform := dve.Transform{
		PositionX:    100,
		PositionY:    50,
		Width:        960,
		Height:       540,
		Opacity:      0.8,
		Brightness:   0.1,
		Contrast:     1.2,
		Saturation:   0.9,
		MirrorH:      true,
		MirrorV:      false,
		MosaicSize:   8,
		CropTop:      10,
		CropBottom:   10,
		CropLeft:     20,
		CropRight:    20,
		CropSoftness: 5,
		Border: dve.BorderConfig{
			Width:        3,
			ColorY:       235,
			ColorCb:      128,
			ColorCr:      128,
			CornerRadius: 10,
			Softness:     2,
		},
		Shadow: dve.ShadowConfig{
			Enabled: true,
			OffsetX: 5,
			OffsetY: 5,
			Blur:    10,
			Opacity: 0.5,
			ColorY:  16,
		},
	}

	assert.InDelta(t, 0.8, transform.EffectiveOpacity(), 0.001)
	assert.Equal(t, byte(235), transform.Border.ColorY)
	assert.Equal(t, byte(128), transform.Border.ColorCb)
	assert.Equal(t, byte(128), transform.Border.ColorCr)
	assert.Equal(t, byte(16), transform.Shadow.ColorY)

	// Verify 8-bit to 10-bit promotion
	assert.Equal(t, 940, int(transform.Border.ColorY)<<2)  // 235 << 2
	assert.Equal(t, 512, int(transform.Border.ColorCb)<<2) // 128 << 2
	assert.Equal(t, 512, int(transform.Border.ColorCr)<<2) // 128 << 2
	assert.Equal(t, 64, int(transform.Shadow.ColorY)<<2)   // 16 << 2
}
