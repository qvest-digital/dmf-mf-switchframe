package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBlendWipeMapP210_NilArgs verifies nil argument handling for BlendWipeMapP210.
func TestBlendWipeMapP210_NilArgs(t *testing.T) {
	cfg := WipeMapBlendConfig{Position: 0.5, SoftEdge: 0.02}
	assert.ErrorIs(t, BlendWipeMapP210(nil, nil, nil, nil, nil, cfg), ErrGPUNotAvailable)
}

// TestBlendWipeMapP210_Dispatch_Routes verifies GPUBlendWipeMapDispatch routes
// based on the destination frame's Format field.
func TestBlendWipeMapP210_Dispatch_Routes(t *testing.T) {
	cfg := WipeMapBlendConfig{Position: 0.5, SoftEdge: 0.02}

	// P210 frame: dispatch should call BlendWipeMapP210 (returns ErrGPUNotAvailable in stub)
	p210Frame := &GPUFrame{Width: 1920, Height: 1080, Pitch: 3840, Format: GPUFormatP210}
	err := GPUBlendWipeMapDispatch(nil, p210Frame, nil, nil, nil, cfg)
	assert.ErrorIs(t, err, ErrGPUNotAvailable, "P210 dispatch should call BlendWipeMapP210")

	// NV12 frame: dispatch should call BlendWipeMap (returns ErrGPUNotAvailable in stub)
	nv12Frame := &GPUFrame{Width: 1920, Height: 1080, Pitch: 1920, Format: GPUFormatNV12}
	err = GPUBlendWipeMapDispatch(nil, nv12Frame, nil, nil, nil, cfg)
	assert.ErrorIs(t, err, ErrGPUNotAvailable, "NV12 dispatch should call BlendWipeMap")

	// Nil dst: should fall through to NV12 path (returns ErrGPUNotAvailable in stub)
	err = GPUBlendWipeMapDispatch(nil, nil, nil, nil, nil, cfg)
	assert.ErrorIs(t, err, ErrGPUNotAvailable, "nil dst dispatch should call BlendWipeMap")
}

// TestBlendWipeMapP210_ConfigFields verifies WipeMapBlendConfig fields are accessible.
func TestBlendWipeMapP210_ConfigFields(t *testing.T) {
	cfg := WipeMapBlendConfig{
		Position:      0.75,
		SoftEdge:      0.05,
		BorderWidth:   0.02,
		BorderColorY:  200,
		BorderColorCb: 128,
		BorderColorCr: 128,
		Reverse:       true,
		Multiply:      3,
	}

	assert.Equal(t, 0.75, cfg.Position)
	assert.Equal(t, 0.05, cfg.SoftEdge)
	assert.Equal(t, 0.02, cfg.BorderWidth)
	assert.Equal(t, byte(200), cfg.BorderColorY)
	assert.Equal(t, byte(128), cfg.BorderColorCb)
	assert.Equal(t, byte(128), cfg.BorderColorCr)
	assert.True(t, cfg.Reverse)
	assert.Equal(t, 3, cfg.Multiply)
}
