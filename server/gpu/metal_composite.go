//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// PIPComposite scales a source GPU frame and composites it into a destination
// frame at the specified rectangle.
func PIPComposite(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	alpha256 := int(alpha * 256.0)
	if alpha256 < 0 {
		alpha256 = 0
	} else if alpha256 > 256 {
		alpha256 = 256
	}

	mtl := ctx.mtl

	// Y plane
	yPipeline, err := mtl.getPipeline("pip_composite_y")
	if err != nil {
		return fmt.Errorf("gpu: pip composite Y: %w", err)
	}
	yParams := C.MetalPIPCompositeYParams{
		dstW: C.uint32_t(dst.Width), dstH: C.uint32_t(dst.Height), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(src.Width), srcH: C.uint32_t(src.Height), srcPitch: C.uint32_t(src.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectW: C.int32_t(rect.W), rectH: C.int32_t(rect.H),
		alpha256: C.int32_t(alpha256),
	}
	rc := C.metal_pip_composite_y(mtl.queue, yPipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: pip composite Y failed: %d", rc)
	}

	// UV plane: use offset-aware dispatch with dedicated UV kernel
	uvPipeline, err := mtl.getPipeline("pip_composite_uv")
	if err != nil {
		return fmt.Errorf("gpu: pip composite UV: %w", err)
	}
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	srcUVOffset := C.int64_t(src.Pitch * src.Height)

	chromaRectW := rect.W / 2
	chromaRectH := rect.H / 2
	if chromaRectW < 1 {
		chromaRectW = 1
	}
	if chromaRectH < 1 {
		chromaRectH = 1
	}

	uvParams := C.MetalPIPCompositeUVParams{
		dstW: C.uint32_t(dst.Width), dstChromaH: C.uint32_t(dst.Height / 2), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(src.Width), srcChromaH: C.uint32_t(src.Height / 2), srcPitch: C.uint32_t(src.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectCW: C.int32_t(chromaRectW), rectCH: C.int32_t(chromaRectH),
		alpha256: C.int32_t(alpha256),
	}
	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(chromaRectW), C.uint32_t(chromaRectH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: pip composite UV failed: %d", rc)
	}

	return nil
}

// DrawBorder draws a colored border outside the given rectangle on a GPU frame.
func DrawBorder(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor, thickness int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("draw_border_nv12")
	if err != nil {
		return fmt.Errorf("gpu: draw border: %w", err)
	}

	outerX := rect.X - thickness
	outerY := rect.Y - thickness
	outerW := rect.W + thickness*2
	outerH := rect.H + thickness*2

	params := C.MetalBorderParams{
		dstW: C.uint32_t(frame.Width), dstH: C.uint32_t(frame.Height), dstPitch: C.uint32_t(frame.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectW: C.int32_t(rect.W), rectH: C.int32_t(rect.H),
		outerX: C.int32_t(outerX), outerY: C.int32_t(outerY),
		outerW: C.int32_t(outerW), outerH: C.int32_t(outerH),
		thickness: C.int32_t(thickness),
		colorY: C.uint8_t(color.Y), colorCb: C.uint8_t(color.Cb), colorCr: C.uint8_t(color.Cr),
	}

	rc := C.metal_draw_border(mtl.queue, pipeline, frame.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: draw border failed: %d", rc)
	}
	return nil
}

// FillRect fills a rectangular region with a constant color on a GPU frame.
func FillRect(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("fill_rect_nv12")
	if err != nil {
		return fmt.Errorf("gpu: fill rect: %w", err)
	}

	params := C.MetalFillRectParams{
		dstW: C.uint32_t(frame.Width), dstH: C.uint32_t(frame.Height), dstPitch: C.uint32_t(frame.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectW: C.int32_t(rect.W), rectH: C.int32_t(rect.H),
		colorY: C.uint8_t(color.Y), colorCb: C.uint8_t(color.Cb), colorCr: C.uint8_t(color.Cr),
	}

	rc := C.metal_fill_rect(mtl.queue, pipeline, frame.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: fill rect failed: %d", rc)
	}
	return nil
}
