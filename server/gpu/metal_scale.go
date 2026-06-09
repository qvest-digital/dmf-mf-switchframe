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

// ScaleBilinear scales an NV12 GPU frame using bilinear interpolation.
// Uses the context's default command queue.
func ScaleBilinear(ctx *Context, dst, src *GPUFrame) error {
	return ScaleBilinearOn(ctx, dst, src, nil)
}

// ScaleBilinearOn scales an NV12 GPU frame using bilinear interpolation
// on the specified work queue. If q is nil, the context's default queue is used.
func ScaleBilinearOn(ctx *Context, dst, src *GPUFrame, q *GPUWorkQueue) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	// Use the work queue's Metal command queue, or fall back to the default.
	queue := metalQueueRef(q)
	if queue == nil {
		queue = mtl.queue
	}

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
	rc := C.metal_scale_bilinear(queue, pipeline, dst.MetalBuf, src.MetalBuf, &yParams)
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
	rc = C.metal_dispatch_2d_offset(queue, uvPipeline,
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
//
// NOT CONCURRENCY-SAFE on the same Context: it uses the Context-global
// lanczosTmpBuf scratch buffer without holding mu across the kernel passes.
// Serialize calls (or use ScaleBilinear, which the production path uses).
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

// ScaleLanczos3P210 scales a P210 GPU frame using a two-pass separable
// Lanczos-3 kernel. It allocates (and caches on the Context) a temporary float
// buffer sized dstW * srcH floats, sufficient for both Y and UV passes.
// UV is full height (4:2:2), not half height like NV12.
//
// NOT CONCURRENCY-SAFE on the same Context: it uses the Context-global
// lanczosTmpBuf scratch buffer without holding mu across the kernel passes.
// Serialize calls (or use ScaleBilinearP210, which the production path uses).
func ScaleLanczos3P210(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	// Ensure temp buffer is large enough: dstW * srcH floats cover both passes.
	// For P210, UV is full height so the same temp size works.
	needed := dst.Width * src.Height
	if err := mtl.ensureLanczosTemp(needed); err != nil {
		return err
	}

	yHPipeline, err := mtl.getPipeline("scale_lanczos3_p210_h")
	if err != nil {
		return fmt.Errorf("gpu: scale lanczos3 p210 h: %w", err)
	}
	yVPipeline, err := mtl.getPipeline("scale_lanczos3_p210_v")
	if err != nil {
		return fmt.Errorf("gpu: scale lanczos3 p210 v: %w", err)
	}
	uvHPipeline, err := mtl.getPipeline("scale_lanczos3_p210_uv_h")
	if err != nil {
		return fmt.Errorf("gpu: scale lanczos3 p210 uv h: %w", err)
	}
	uvVPipeline, err := mtl.getPipeline("scale_lanczos3_p210_uv_v")
	if err != nil {
		return fmt.Errorf("gpu: scale lanczos3 p210 uv v: %w", err)
	}

	// --- Y plane ---
	// Pass 1: horizontal (srcW x srcH -> dstW x srcH)
	hParams := C.MetalLanczos3HParams{
		dstW:     C.uint32_t(dst.Width),
		srcW:     C.uint32_t(src.Width),
		srcH:     C.uint32_t(src.Height),
		srcPitch: C.uint32_t(src.Pitch),
	}
	rc := C.metal_scale_lanczos3_h(mtl.queue, yHPipeline, mtl.lanczosTmpBuf, src.MetalBuf, &hParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 p210 h Y failed: %d", rc)
	}

	// Pass 2: vertical (dstW x srcH -> dstW x dstH)
	vParams := C.MetalLanczos3VParams{
		dstW:     C.uint32_t(dst.Width),
		dstH:     C.uint32_t(dst.Height),
		dstPitch: C.uint32_t(dst.Pitch),
		srcH:     C.uint32_t(src.Height),
	}
	rc = C.metal_scale_lanczos3_v(mtl.queue, yVPipeline, dst.MetalBuf, mtl.lanczosTmpBuf, &vParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 p210 v Y failed: %d", rc)
	}

	// --- UV plane (P210: interleaved uint16 Cb/Cr pairs, FULL height for 4:2:2) ---
	// Dedicated UV kernels filter Cb and Cr channels independently to prevent
	// cross-channel mixing. srcW/dstW are in chroma samples (luma width / 2).
	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	chromaSrcW := src.Width / 2
	chromaDstW := dst.Width / 2

	// Pass 1 (horizontal): UV src -> tmpBuf (2 floats per chroma sample)
	uvHParams := C.MetalLanczos3HParams{
		dstW:     C.uint32_t(chromaDstW),
		srcW:     C.uint32_t(chromaSrcW),
		srcH:     C.uint32_t(src.Height), // full height for 4:2:2
		srcPitch: C.uint32_t(src.Pitch),
	}
	hBufs := [2]C.MetalBufferRef{mtl.lanczosTmpBuf, src.MetalBuf}
	hOffsets := [2]C.int64_t{0, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvHPipeline,
		&hBufs[0], &hOffsets[0], 2,
		unsafe.Pointer(&uvHParams), C.size_t(unsafe.Sizeof(uvHParams)), 2,
		C.uint32_t(chromaDstW), C.uint32_t(src.Height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 p210 h UV failed: %d", rc)
	}

	// Pass 2 (vertical): tmpBuf -> dst UV
	uvVParams := C.MetalLanczos3VParams{
		dstW:     C.uint32_t(chromaDstW),
		dstH:     C.uint32_t(dst.Height), // full height for 4:2:2
		dstPitch: C.uint32_t(dst.Pitch),
		srcH:     C.uint32_t(src.Height), // full height for 4:2:2
	}
	vBufs := [2]C.MetalBufferRef{dst.MetalBuf, mtl.lanczosTmpBuf}
	vOffsets := [2]C.int64_t{dstUVOffset, 0}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvVPipeline,
		&vBufs[0], &vOffsets[0], 2,
		unsafe.Pointer(&uvVParams), C.size_t(unsafe.Sizeof(uvVParams)), 2,
		C.uint32_t(chromaDstW), C.uint32_t(dst.Height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale lanczos3 p210 v UV failed: %d", rc)
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

// ScaleP210ToNV12On scales a P210 source frame to an NV12 destination frame
// using bilinear interpolation with 10-bit→8-bit conversion and 4:2:2→4:2:0
// chroma subsampling in a single GPU pass. Runs on the specified work queue
// (nil = default queue). This is the GPU-resident path for preview encoding
// in professional mode, eliminating the CPU round-trip.
func ScaleP210ToNV12On(ctx *Context, dst, src *GPUFrame, q *GPUWorkQueue) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	queue := metalQueueRef(q)
	if queue == nil {
		queue = mtl.queue
	}

	// Scale Y plane: P210 uint16 → NV12 uint8
	yPipeline, err := mtl.getPipeline("scale_p210_to_nv12_y")
	if err != nil {
		return fmt.Errorf("gpu: scale p210→nv12 Y: %w", err)
	}
	yParams := C.MetalScaleP210ToNV12Params{
		srcW:     C.uint32_t(src.Width),
		srcH:     C.uint32_t(src.Height),
		srcPitch: C.uint32_t(src.Pitch),
		dstW:     C.uint32_t(dst.Width),
		dstH:     C.uint32_t(dst.Height),
		dstPitch: C.uint32_t(dst.Pitch),
	}
	rc := C.metal_scale_p210_to_nv12_y(queue, yPipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale p210→nv12 Y dispatch: %d", rc)
	}

	// Scale UV plane: P210 full-height uint16 CbCr → NV12 half-height uint8 CbCr
	uvPipeline, err := mtl.getPipeline("scale_p210_to_nv12_uv")
	if err != nil {
		return fmt.Errorf("gpu: scale p210→nv12 UV: %w", err)
	}

	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	chromaSrcW := src.Width / 2
	chromaDstW := dst.Width / 2

	uvParams := C.MetalScaleP210ToNV12Params{
		srcW:     C.uint32_t(chromaSrcW),
		srcH:     C.uint32_t(src.Height), // P210: full-height UV (4:2:2)
		srcPitch: C.uint32_t(src.Pitch),
		dstW:     C.uint32_t(chromaDstW),
		dstH:     C.uint32_t(dst.Height / 2), // NV12: half-height UV (4:2:0)
		dstPitch: C.uint32_t(dst.Pitch),
	}
	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(chromaDstW), C.uint32_t(dst.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale p210→nv12 UV dispatch: %d", rc)
	}

	return nil
}
