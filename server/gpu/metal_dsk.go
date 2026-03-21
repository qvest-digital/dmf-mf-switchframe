//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// UploadOverlay uploads an RGBA image to GPU memory for DSK compositing.
func UploadOverlay(ctx *Context, rgba []byte, width, height int) (*GPUOverlay, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}
	if len(rgba) < width*height*4 {
		return nil, fmt.Errorf("gpu: RGBA buffer too small: %d < %d", len(rgba), width*height*4)
	}

	pitch := width * 4 // RGBA, no extra alignment needed for Metal
	size := pitch * height
	buf, err := ctx.mtl.allocBuffer(size)
	if err != nil {
		return nil, fmt.Errorf("gpu: overlay alloc failed: %w", err)
	}

	C.memcpy(C.metal_buffer_contents(buf), unsafe.Pointer(&rgba[0]), C.size_t(size))

	return &GPUOverlay{
		MetalBuf: buf,
		Width:    width,
		Height:   height,
		Pitch:    pitch,
	}, nil
}

// FreeOverlay releases GPU memory for an overlay.
func FreeOverlay(overlay *GPUOverlay) {
	if overlay != nil && overlay.MetalBuf != nil {
		C.metal_buffer_free(overlay.MetalBuf)
		overlay.MetalBuf = nil
	}
}

// DSKCompositeFullFrame composites a full-frame RGBA overlay onto an NV12 GPU frame.
func DSKCompositeFullFrame(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, alphaScale float64) error {
	if ctx == nil || ctx.mtl == nil || frame == nil || overlay == nil {
		return ErrGPUNotAvailable
	}

	alphaScale256 := int(alphaScale * 256.0)
	if alphaScale256 < 0 {
		alphaScale256 = 0
	} else if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("dsk_overlay_nv12")
	if err != nil {
		return fmt.Errorf("gpu: DSK full-frame: %w", err)
	}

	params := C.MetalDSKFullFrameParams{
		width:         C.uint32_t(frame.Width),
		height:        C.uint32_t(frame.Height),
		nv12Pitch:     C.uint32_t(frame.Pitch),
		rgbaPitch:     C.uint32_t(overlay.Pitch),
		alphaScale256: C.int32_t(alphaScale256),
	}

	rc := C.metal_dsk_overlay_full(mtl.queue, pipeline, frame.MetalBuf, overlay.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: DSK full-frame composite failed: %d", rc)
	}
	return nil
}

// DSKCompositeRect composites an RGBA overlay into a rectangular region.
func DSKCompositeRect(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, rect Rect, alphaScale float64) error {
	if ctx == nil || ctx.mtl == nil || frame == nil || overlay == nil {
		return ErrGPUNotAvailable
	}

	alphaScale256 := int(alphaScale * 256.0)
	if alphaScale256 < 0 {
		alphaScale256 = 0
	} else if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("dsk_overlay_rect_nv12")
	if err != nil {
		return fmt.Errorf("gpu: DSK rect: %w", err)
	}

	params := C.MetalDSKRectParams{
		frameW: C.uint32_t(frame.Width), frameH: C.uint32_t(frame.Height),
		nv12Pitch: C.uint32_t(frame.Pitch),
		overlayW: C.uint32_t(overlay.Width), overlayH: C.uint32_t(overlay.Height),
		rgbaPitch: C.uint32_t(overlay.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectW: C.int32_t(rect.W), rectH: C.int32_t(rect.H),
		alphaScale256: C.int32_t(alphaScale256),
	}

	rc := C.metal_dsk_overlay_rect(mtl.queue, pipeline, frame.MetalBuf, overlay.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: DSK rect composite failed: %d", rc)
	}
	return nil
}
