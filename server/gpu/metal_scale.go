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

	// Scale UV plane using UV-aware kernel that interpolates CbCr pairs
	// independently. Width is in CHROMA SAMPLES (width/2), not bytes.
	uvPipeline, err := mtl.getPipeline("scale_bilinear_uv")
	if err != nil {
		return fmt.Errorf("gpu: scale bilinear UV pipeline: %w", err)
	}

	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	chromaSrcW := src.Width / 2
	chromaDstW := dst.Width / 2
	uvParams := C.MetalScaleParams{
		srcW:     C.uint32_t(chromaSrcW),
		srcH:     C.uint32_t(src.Height / 2),
		srcPitch: C.uint32_t(src.Pitch),
		dstW:     C.uint32_t(chromaDstW),
		dstH:     C.uint32_t(dst.Height / 2),
		dstPitch: C.uint32_t(dst.Pitch),
	}
	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(chromaDstW), C.uint32_t(dst.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale bilinear UV failed: %d", rc)
	}

	return nil
}

// ScaleLanczos3 scales an NV12 GPU frame using a two-pass separable Lanczos-3
// kernel. It allocates (and caches on the Context) a temporary float buffer
// sized dstW * srcH floats, sufficient for both Y and UV passes.
func ScaleLanczos3(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	// Ensure temp buffer is large enough: dstW * srcH floats cover both passes.
	needed := dst.Width * src.Height
	if err := mtl.ensureLanczosTemp(needed); err != nil {
		return err
	}

	hPipeline, err := mtl.getPipeline("scale_lanczos3_h")
	if err != nil {
		return fmt.Errorf("gpu: scale lanczos3 h: %w", err)
	}
	vPipeline, err := mtl.getPipeline("scale_lanczos3_v")
	if err != nil {
		return fmt.Errorf("gpu: scale lanczos3 v: %w", err)
	}

	// --- Y plane ---
	// Pass 1: horizontal (srcW x srcH -> dstW x srcH)
	hParams := C.MetalLanczos3HParams{
		dstW:     C.uint32_t(dst.Width),
		srcW:     C.uint32_t(src.Width),
		srcH:     C.uint32_t(src.Height),
		srcPitch: C.uint32_t(src.Pitch),
	}
	rc := C.metal_scale_lanczos3_h(mtl.queue, hPipeline, mtl.lanczosTmpBuf, src.MetalBuf, &hParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 h Y failed: %d", rc)
	}

	// Pass 2: vertical (dstW x srcH -> dstW x dstH)
	vParams := C.MetalLanczos3VParams{
		dstW:     C.uint32_t(dst.Width),
		dstH:     C.uint32_t(dst.Height),
		dstPitch: C.uint32_t(dst.Pitch),
		srcH:     C.uint32_t(src.Height),
	}
	rc = C.metal_scale_lanczos3_v(mtl.queue, vPipeline, dst.MetalBuf, mtl.lanczosTmpBuf, &vParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 v Y failed: %d", rc)
	}

	// --- UV plane (NV12: interleaved Cb/Cr, same byte width as Y, half height) ---
	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	srcChromaH := src.Height / 2
	dstChromaH := dst.Height / 2

	// Pass 1 (horizontal): UV src -> tmpBuf
	uvHParams := C.MetalLanczos3HParams{
		dstW:     C.uint32_t(dst.Width),
		srcW:     C.uint32_t(src.Width),
		srcH:     C.uint32_t(srcChromaH),
		srcPitch: C.uint32_t(src.Pitch),
	}
	// src at UV offset, tmpBuf at offset 0
	hBufs := [2]C.MetalBufferRef{mtl.lanczosTmpBuf, src.MetalBuf}
	hOffsets := [2]C.int64_t{0, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, hPipeline,
		&hBufs[0], &hOffsets[0], 2,
		unsafe.Pointer(&uvHParams), C.size_t(unsafe.Sizeof(uvHParams)), 2,
		C.uint32_t(dst.Width), C.uint32_t(srcChromaH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 h UV failed: %d", rc)
	}

	// Pass 2 (vertical): tmpBuf -> dst UV
	uvVParams := C.MetalLanczos3VParams{
		dstW:     C.uint32_t(dst.Width),
		dstH:     C.uint32_t(dstChromaH),
		dstPitch: C.uint32_t(dst.Pitch),
		srcH:     C.uint32_t(srcChromaH),
	}
	vBufs := [2]C.MetalBufferRef{dst.MetalBuf, mtl.lanczosTmpBuf}
	vOffsets := [2]C.int64_t{dstUVOffset, 0}
	rc = C.metal_dispatch_2d_offset(mtl.queue, vPipeline,
		&vBufs[0], &vOffsets[0], 2,
		unsafe.Pointer(&uvVParams), C.size_t(unsafe.Sizeof(uvVParams)), 2,
		C.uint32_t(dst.Width), C.uint32_t(dstChromaH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 v UV failed: %d", rc)
	}

	return nil
}

// Scale scales a GPU frame with the specified quality.
func Scale(ctx *Context, dst, src *GPUFrame, quality ScaleQuality) error {
	switch quality {
	case ScaleQualityLanczos:
		return ScaleLanczos3(ctx, dst, src)
	default:
		return ScaleBilinear(ctx, dst, src)
	}
}
