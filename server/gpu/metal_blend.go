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

// BlendMix performs a uniform mix blend between two NV12 frames.
// The blend_uniform kernel treats every byte identically (position-based
// interpolation), so a single dispatch covering the full NV12 buffer
// (Y + UV = height*3/2 rows) produces correct results for both planes.
func BlendMix(ctx *Context, dst, a, b *GPUFrame, position float64) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || a == nil || b == nil {
		return ErrGPUNotAvailable
	}
	pos256 := int(position * 256.0)
	if pos256 < 0 {
		pos256 = 0
	} else if pos256 > 256 {
		pos256 = 256
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("blend_uniform")
	if err != nil {
		return fmt.Errorf("gpu: blend mix: %w", err)
	}

	// Single dispatch covers both Y and UV planes. blend_uniform applies the
	// same byte-level interpolation regardless of Y vs UV data.
	params := C.MetalBlendUniformParams{
		width:  C.uint32_t(a.Width),
		height: C.uint32_t(a.Height * 3 / 2), // Full NV12 height: Y + UV
		pitch:  C.uint32_t(a.Pitch),
		pos256: C.int32_t(pos256),
	}
	rc := C.metal_blend_uniform(mtl.queue, pipeline, dst.MetalBuf, a.MetalBuf, b.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: blend mix failed: %d", rc)
	}

	return nil
}

// BlendFTB fades a frame to black (BT.709 limited range).
// Y plane fades to Y=16, UV plane fades to UV=128. These require separate
// dispatches because the constant values differ between planes.
func BlendFTB(ctx *Context, dst, src *GPUFrame, position float64) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	pos256 := int(position * 256.0)
	if pos256 < 0 {
		pos256 = 0
	} else if pos256 > 256 {
		pos256 = 256
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("blend_fade_const")
	if err != nil {
		return fmt.Errorf("gpu: blend FTB: %w", err)
	}

	// Y plane: fade to Y=16 (BT.709 limited-range black)
	yParams := C.MetalBlendFadeConstParams{
		width:  C.uint32_t(src.Width),
		height: C.uint32_t(src.Height),
		pitch:  C.uint32_t(src.Pitch),
		pos256: C.int32_t(pos256),
		constY: C.uint8_t(16),
	}
	rc := C.metal_blend_fade_const(mtl.queue, pipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: blend FTB Y failed: %d", rc)
	}

	// UV plane: fade to UV=128 (neutral chroma).
	// Uses offset-aware dispatch to bind buffers at UV offset.
	uvOffset := C.int64_t(src.Pitch * src.Height)
	uvParams := C.MetalBlendFadeConstParams{
		width:  C.uint32_t(src.Width),
		height: C.uint32_t(src.Height / 2),
		pitch:  C.uint32_t(src.Pitch),
		pos256: C.int32_t(pos256),
		constY: C.uint8_t(128), // kernel uses constY for the constant value
	}
	dstBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	offsets := [2]C.int64_t{uvOffset, uvOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, pipeline,
		&dstBufs[0], &offsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(src.Width), C.uint32_t(src.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: blend FTB UV failed: %d", rc)
	}

	return nil
}

// BlendWipe performs a wipe transition between two NV12 frames.
// Pass 1: Generate luma-resolution wipe mask into maskBuf.
// Pass 2: Alpha-blend Y plane using mask.
// Pass 3: Downsample luma mask to chroma resolution.
// Pass 4: Alpha-blend UV plane using downsampled mask (offset-aware).
func BlendWipe(ctx *Context, dst, a, b, maskBuf *GPUFrame, position float64, dir WipeDirection, softEdge int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || a == nil || b == nil || maskBuf == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	// Pass 1: Generate wipe mask at luma resolution
	maskPipeline, err := mtl.getPipeline("wipe_mask_generate")
	if err != nil {
		return fmt.Errorf("gpu: wipe mask: %w", err)
	}
	maskParams := C.MetalWipeMaskParams{
		width:    C.uint32_t(a.Width),
		height:   C.uint32_t(a.Height),
		pitch:    C.uint32_t(a.Pitch),
		position: C.float(position),
		direction: C.int32_t(dir),
		softEdge: C.int32_t(softEdge),
	}
	rc := C.metal_wipe_mask_generate(mtl.queue, maskPipeline, maskBuf.MetalBuf, &maskParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: wipe mask generate failed: %d", rc)
	}

	// Pass 2: Alpha-blend Y plane
	blendPipeline, err := mtl.getPipeline("blend_alpha")
	if err != nil {
		return fmt.Errorf("gpu: wipe blend alpha: %w", err)
	}
	yParams := C.MetalBlendAlphaParams{
		width:      C.uint32_t(a.Width),
		height:     C.uint32_t(a.Height),
		pitch:      C.uint32_t(a.Pitch),
		alphaPitch: C.uint32_t(a.Pitch),
	}
	rc = C.metal_blend_alpha(mtl.queue, blendPipeline, dst.MetalBuf, a.MetalBuf, b.MetalBuf, maskBuf.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: wipe blend Y failed: %d", rc)
	}

	// Pass 3: Downsample luma mask to chroma resolution in the UV portion of maskBuf
	dsaPipeline, err := mtl.getPipeline("downsample_alpha_to_nv12_uv")
	if err != nil {
		return fmt.Errorf("gpu: wipe downsample: %w", err)
	}
	chromaW := a.Width / 2
	chromaH := a.Height / 2
	uvOffset := C.int64_t(a.Pitch * a.Height)

	dsaParams := C.MetalDownsampleAlphaParams{
		chromaW:  C.uint32_t(chromaW),
		chromaH:  C.uint32_t(chromaH),
		srcPitch: C.uint32_t(a.Pitch),
		dstPitch: C.uint32_t(a.Pitch),
	}
	// dst = UV region of maskBuf (at offset), src = Y region of maskBuf (at offset 0)
	dsaBufs := [2]C.MetalBufferRef{maskBuf.MetalBuf, maskBuf.MetalBuf}
	dsaOffsets := [2]C.int64_t{uvOffset, 0}
	rc = C.metal_dispatch_2d_offset(mtl.queue, dsaPipeline,
		&dsaBufs[0], &dsaOffsets[0], 2,
		unsafe.Pointer(&dsaParams), C.size_t(unsafe.Sizeof(dsaParams)), 2,
		C.uint32_t(chromaW), C.uint32_t(chromaH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: wipe downsample alpha failed: %d", rc)
	}

	// Pass 4: Alpha-blend UV plane with offset-aware dispatch
	uvBlendParams := C.MetalBlendAlphaParams{
		width:      C.uint32_t(a.Width),
		height:     C.uint32_t(a.Height / 2),
		pitch:      C.uint32_t(a.Pitch),
		alphaPitch: C.uint32_t(a.Pitch),
	}
	uvBufs := [4]C.MetalBufferRef{dst.MetalBuf, a.MetalBuf, b.MetalBuf, maskBuf.MetalBuf}
	uvOffsets := [4]C.int64_t{uvOffset, uvOffset, uvOffset, uvOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, blendPipeline,
		&uvBufs[0], &uvOffsets[0], 4,
		unsafe.Pointer(&uvBlendParams), C.size_t(unsafe.Sizeof(uvBlendParams)), 4,
		C.uint32_t(a.Width), C.uint32_t(a.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: wipe blend UV failed: %d", rc)
	}

	return nil
}

// BlendStinger composites a stinger overlay onto a base frame using per-pixel alpha.
// Pass 1: Alpha-blend Y plane.
// Pass 2: Downsample luma alpha to chroma resolution in the UV portion of alpha buffer.
// Pass 3: Alpha-blend UV plane using downsampled alpha (offset-aware).
func BlendStinger(ctx *Context, dst, base, overlay, alpha *GPUFrame) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || base == nil || overlay == nil || alpha == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	// Pass 1: Alpha-blend Y plane
	blendPipeline, err := mtl.getPipeline("blend_alpha")
	if err != nil {
		return fmt.Errorf("gpu: stinger blend alpha: %w", err)
	}
	yParams := C.MetalBlendAlphaParams{
		width:      C.uint32_t(base.Width),
		height:     C.uint32_t(base.Height),
		pitch:      C.uint32_t(base.Pitch),
		alphaPitch: C.uint32_t(alpha.Pitch),
	}
	rc := C.metal_blend_alpha(mtl.queue, blendPipeline, dst.MetalBuf, base.MetalBuf, overlay.MetalBuf, alpha.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stinger blend Y failed: %d", rc)
	}

	// Pass 2: Downsample luma alpha to chroma resolution in the UV portion of alpha buffer
	dsaPipeline, err := mtl.getPipeline("downsample_alpha_to_nv12_uv")
	if err != nil {
		return fmt.Errorf("gpu: stinger downsample: %w", err)
	}
	chromaW := base.Width / 2
	chromaH := base.Height / 2
	uvOffset := C.int64_t(alpha.Pitch * alpha.Height)

	dsaParams := C.MetalDownsampleAlphaParams{
		chromaW:  C.uint32_t(chromaW),
		chromaH:  C.uint32_t(chromaH),
		srcPitch: C.uint32_t(alpha.Pitch),
		dstPitch: C.uint32_t(alpha.Pitch),
	}
	dsaBufs := [2]C.MetalBufferRef{alpha.MetalBuf, alpha.MetalBuf}
	dsaOffsets := [2]C.int64_t{uvOffset, 0}
	rc = C.metal_dispatch_2d_offset(mtl.queue, dsaPipeline,
		&dsaBufs[0], &dsaOffsets[0], 2,
		unsafe.Pointer(&dsaParams), C.size_t(unsafe.Sizeof(dsaParams)), 2,
		C.uint32_t(chromaW), C.uint32_t(chromaH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stinger downsample alpha failed: %d", rc)
	}

	// Pass 3: Alpha-blend UV plane using downsampled alpha (offset-aware)
	baseUVOffset := C.int64_t(base.Pitch * base.Height)
	uvBlendParams := C.MetalBlendAlphaParams{
		width:      C.uint32_t(base.Width),
		height:     C.uint32_t(base.Height / 2),
		pitch:      C.uint32_t(base.Pitch),
		alphaPitch: C.uint32_t(alpha.Pitch),
	}
	uvBufs := [4]C.MetalBufferRef{dst.MetalBuf, base.MetalBuf, overlay.MetalBuf, alpha.MetalBuf}
	uvOffsets := [4]C.int64_t{baseUVOffset, baseUVOffset, C.int64_t(overlay.Pitch * overlay.Height), uvOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, blendPipeline,
		&uvBufs[0], &uvOffsets[0], 4,
		unsafe.Pointer(&uvBlendParams), C.size_t(unsafe.Sizeof(uvBlendParams)), 4,
		C.uint32_t(base.Width), C.uint32_t(base.Height/2))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stinger blend UV failed: %d", rc)
	}

	return nil
}
