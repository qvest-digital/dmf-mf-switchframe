//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"log/slog"
)

// FRUC provides GPU-accelerated frame rate up-conversion on Metal.
// Apple Silicon does not have dedicated optical flow hardware like NVOFA,
// so this always uses the blend fallback path.
type FRUC struct {
	gpuCtx *Context
	width  int
	height int
}

// FRUCAvailable returns true if the GPU FRUC subsystem can be used.
func FRUCAvailable() bool {
	return true // Blend fallback always works
}

// NewFRUC creates a FRUC instance for the given frame dimensions.
func NewFRUC(ctx *Context, width, height int) (*FRUC, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}

	slog.Info("gpu: FRUC initialized with blend fallback (Metal, no hardware optical flow)",
		"width", width, "height", height)

	return &FRUC{
		gpuCtx: ctx,
		width:  width,
		height: height,
	}, nil
}

// Interpolate generates an intermediate frame between prev and curr.
// alpha is the temporal position: 0.0 = prev, 1.0 = curr.
// Uses linear blend (no motion compensation on Apple Silicon).
func (f *FRUC) Interpolate(prev, curr, output *GPUFrame, alpha float64) error {
	if f == nil || prev == nil || curr == nil || output == nil {
		return ErrGPUNotAvailable
	}

	mtl := f.gpuCtx.mtl
	pipeline, err := mtl.getPipeline("fruc_blend")
	if err != nil {
		return fmt.Errorf("gpu: FRUC blend: %w", err)
	}

	// Blend full NV12 frame (Y + UV) in one dispatch
	params := C.MetalFRUCBlendParams{
		width:     C.uint32_t(f.width),
		height:    C.uint32_t(f.height * 3 / 2), // Full NV12 height
		dstPitch:  C.uint32_t(output.Pitch),
		prevPitch: C.uint32_t(prev.Pitch),
		currPitch: C.uint32_t(curr.Pitch),
		alpha:     C.float(alpha),
	}

	rc := C.metal_fruc_blend(mtl.queue, pipeline, output.MetalBuf, prev.MetalBuf, curr.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: FRUC blend failed: %d", rc)
	}
	return nil
}

// Close releases FRUC resources.
func (f *FRUC) Close() {
	// No GPU-resident resources to free (Metal buffers are in the pool)
}
