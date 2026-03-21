//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import "fmt"

// ScaleBilinear scales an NV12 GPU frame using bilinear interpolation.
func ScaleBilinear(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("scale_bilinear")
	if err != nil {
		return fmt.Errorf("gpu: scale bilinear: %w", err)
	}

	// Scale Y plane
	yParams := C.MetalScaleParams{
		srcW:     C.uint32_t(src.Width),
		srcH:     C.uint32_t(src.Height),
		srcPitch: C.uint32_t(src.Pitch),
		dstW:     C.uint32_t(dst.Width),
		dstH:     C.uint32_t(dst.Height),
		dstPitch: C.uint32_t(dst.Pitch),
	}
	rc := C.metal_scale_bilinear(mtl.queue, pipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale bilinear Y failed: %d", rc)
	}

	// Scale UV plane: same byte width, half height
	// For proper NV12 UV scaling, we need offset-aware buffers.
	// TODO: Add UV-plane scaling with buffer offset support.

	return nil
}

// Scale scales a GPU frame with the specified quality.
func Scale(ctx *Context, dst, src *GPUFrame, quality ScaleQuality) error {
	return ScaleBilinear(ctx, dst, src)
}
