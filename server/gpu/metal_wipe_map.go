//go:build cgo && darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// WipeMapBlendConfig holds parameters for a gradient-map-driven wipe blend.
type WipeMapBlendConfig struct {
	Position    float64 // 0.0-1.0 transition progress
	SoftEdge    float64 // 0.0-1.0 edge softness
	BorderWidth float64 // 0.0-1.0 border zone width
	BorderColorY  byte
	BorderColorCb byte
	BorderColorCr byte
	Reverse  bool // invert gradient
	Multiply int  // repeat count (0 or 1 = no multiply)
}

// BlendWipeMap performs a gradient-map-driven wipe transition between two NV12
// frames. The gradient map is a single-channel (Y-plane-resolution) grayscale
// buffer that determines the transition threshold at each pixel.
func BlendWipeMap(ctx *Context, dst, srcA, srcB, gradMap *GPUFrame, cfg WipeMapBlendConfig) error {
	if ctx == nil || ctx.mtl == nil {
		return ErrGPUNotAvailable
	}
	if dst == nil || srcA == nil || srcB == nil || gradMap == nil {
		return fmt.Errorf("gpu: BlendWipeMap: nil frame argument")
	}

	mtl := ctx.mtl

	reverse := C.int32_t(0)
	if cfg.Reverse {
		reverse = 1
	}

	params := C.MetalWipeMapBlendParams{
		width:         C.uint32_t(dst.Width),
		height:        C.uint32_t(dst.Height),
		pitch:         C.uint32_t(dst.Pitch),
		gradPitch:     C.uint32_t(gradMap.Pitch),
		position:      C.float(cfg.Position),
		softEdge:      C.float(cfg.SoftEdge),
		borderWidth:   C.float(cfg.BorderWidth),
		borderColorY:  C.uint8_t(cfg.BorderColorY),
		borderColorCb: C.uint8_t(cfg.BorderColorCb),
		borderColorCr: C.uint8_t(cfg.BorderColorCr),
		reverse:       reverse,
		multiply:      C.int32_t(cfg.Multiply),
	}

	// Y plane
	yPipeline, err := mtl.getPipeline("wipe_map_blend_y")
	if err != nil {
		return fmt.Errorf("gpu: wipe map blend Y pipeline: %w", err)
	}

	rc := C.metal_wipe_map_blend_y(mtl.queue, yPipeline,
		srcA.MetalBuf, srcB.MetalBuf, gradMap.MetalBuf, dst.MetalBuf,
		&params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: wipe map blend Y failed: %d", rc)
	}

	// UV plane
	uvPipeline, err := mtl.getPipeline("wipe_map_blend_uv")
	if err != nil {
		return fmt.Errorf("gpu: wipe map blend UV pipeline: %w", err)
	}

	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	srcAUVOffset := C.int64_t(srcA.Pitch * srcA.Height)
	srcBUVOffset := C.int64_t(srcB.Pitch * srcB.Height)

	uvBufs := [4]C.MetalBufferRef{srcA.MetalBuf, srcB.MetalBuf, gradMap.MetalBuf, dst.MetalBuf}
	uvOffsets := [4]C.int64_t{srcAUVOffset, srcBUVOffset, 0, dstUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 4,
		unsafe.Pointer(&params), C.size_t(unsafe.Sizeof(params)), 4,
		C.uint32_t(dst.Width/2), C.uint32_t(dst.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: wipe map blend UV failed: %d", rc)
	}

	return nil
}
