//go:build cgo && darwin

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

// BlendMixP210 performs a uniform mix blend between two P210 frames.
// A single dispatch covers both Y and UV planes since blend_uniform_p210
// treats every uint16 sample identically (P210 has full-height UV).
func BlendMixP210(ctx *Context, dst, a, b *GPUFrame, position float64) error {
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
	pipeline, err := mtl.getPipeline("blend_uniform_p210")
	if err != nil {
		return fmt.Errorf("gpu: blend mix p210: %w", err)
	}

	// Single dispatch covers Y + UV (P210 total height = height * 2)
	params := C.MetalBlendUniformP210Params{
		width:  C.uint32_t(a.Width),
		height: C.uint32_t(a.Height * 2), // Y plane + UV plane (both full height)
		pitch:  C.uint32_t(a.Pitch),
		pos256: C.int32_t(pos256),
	}
	rc := C.metal_blend_uniform_p210(mtl.queue, pipeline, dst.MetalBuf, a.MetalBuf, b.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: blend mix p210 failed: %d", rc)
	}
	return nil
}

// BlendFTBP210 fades a P210 frame to black (BT.709 10-bit limited range).
// Y plane fades to Y=64, UV plane fades to UV=512. Separate dispatches needed
// because the constant values differ between planes.
func BlendFTBP210(ctx *Context, dst, src *GPUFrame, position float64) error {
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
	pipeline, err := mtl.getPipeline("blend_fade_const_p210")
	if err != nil {
		return fmt.Errorf("gpu: blend FTB p210: %w", err)
	}

	// Y plane: fade to Y=64 (10-bit limited-range black)
	yParams := C.MetalBlendFadeConstP210Params{
		width:    C.uint32_t(src.Width),
		height:   C.uint32_t(src.Height),
		pitch:    C.uint32_t(src.Pitch),
		pos256:   C.int32_t(pos256),
		constVal: C.uint16_t(64),
	}
	rc := C.metal_blend_fade_const_p210(mtl.queue, pipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: blend FTB p210 Y failed: %d", rc)
	}

	// UV plane: fade to UV=512 (10-bit neutral chroma).
	// Uses offset-aware dispatch to bind buffers at UV offset.
	uvOffset := C.int64_t(src.Pitch * src.Height)
	uvParams := C.MetalBlendFadeConstP210Params{
		width:    C.uint32_t(src.Width),
		height:   C.uint32_t(src.Height), // P210: full height UV
		pitch:    C.uint32_t(src.Pitch),
		pos256:   C.int32_t(pos256),
		constVal: C.uint16_t(512),
	}
	dstBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	offsets := [2]C.int64_t{uvOffset, uvOffset}
	samplesPerRow := C.uint32_t(src.Pitch / 2)
	rc = C.metal_dispatch_2d_offset(mtl.queue, pipeline,
		&dstBufs[0], &offsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		samplesPerRow, C.uint32_t(src.Height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: blend FTB p210 UV failed: %d", rc)
	}

	return nil
}

// ScaleBilinearP210 scales a P210 GPU frame using bilinear interpolation.
func ScaleBilinearP210(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl

	// Scale Y plane
	yPipeline, err := mtl.getPipeline("scale_bilinear_p210_y")
	if err != nil {
		return fmt.Errorf("gpu: scale bilinear p210 Y: %w", err)
	}
	yParams := C.MetalScaleP210Params{
		srcW:     C.uint32_t(src.Width),
		srcH:     C.uint32_t(src.Height),
		srcPitch: C.uint32_t(src.Pitch),
		dstW:     C.uint32_t(dst.Width),
		dstH:     C.uint32_t(dst.Height),
		dstPitch: C.uint32_t(dst.Pitch),
	}
	rc := C.metal_scale_bilinear_p210_y(mtl.queue, yPipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale bilinear p210 Y failed: %d", rc)
	}

	// Scale UV plane using UV-aware kernel
	uvPipeline, err := mtl.getPipeline("scale_bilinear_p210_uv")
	if err != nil {
		return fmt.Errorf("gpu: scale bilinear p210 UV: %w", err)
	}

	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	chromaSrcW := src.Width / 2
	chromaDstW := dst.Width / 2
	uvParams := C.MetalScaleP210Params{
		srcW:     C.uint32_t(chromaSrcW),
		srcH:     C.uint32_t(src.Height), // P210: full height chroma
		srcPitch: C.uint32_t(src.Pitch),
		dstW:     C.uint32_t(chromaDstW),
		dstH:     C.uint32_t(dst.Height), // P210: full height chroma
		dstPitch: C.uint32_t(dst.Pitch),
	}
	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(chromaDstW), C.uint32_t(dst.Height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: scale bilinear p210 UV failed: %d", rc)
	}

	return nil
}

// PIPCompositeP210 scales a source P210 frame and composites it into a
// destination P210 frame at the specified rectangle.
func PIPCompositeP210(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	return PIPCompositeP210WithCrop(ctx, dst, src, rect, alpha, 0, 0, 0, 0)
}

// PIPCompositeP210WithCrop scales a source P210 frame (or sub-region) and
// composites it into a destination P210 frame at the specified rectangle.
func PIPCompositeP210WithCrop(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64,
	cropX, cropY, cropW, cropH int) error {
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
	yPipeline, err := mtl.getPipeline("pip_composite_p210_y")
	if err != nil {
		return fmt.Errorf("gpu: pip composite p210 Y: %w", err)
	}
	yParams := C.MetalPIPCompositeP210YParams{
		dstW: C.uint32_t(dst.Width), dstH: C.uint32_t(dst.Height), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(src.Width), srcH: C.uint32_t(src.Height), srcPitch: C.uint32_t(src.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectW: C.int32_t(rect.W), rectH: C.int32_t(rect.H),
		alpha256: C.int32_t(alpha256),
		cropX:    C.int32_t(cropX), cropY: C.int32_t(cropY),
		cropW: C.int32_t(cropW), cropH: C.int32_t(cropH),
	}
	rc := C.metal_pip_composite_p210_y(mtl.queue, yPipeline, dst.MetalBuf, src.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: pip composite p210 Y failed: %d", rc)
	}

	// UV plane: offset-aware dispatch
	uvPipeline, err := mtl.getPipeline("pip_composite_p210_uv")
	if err != nil {
		return fmt.Errorf("gpu: pip composite p210 UV: %w", err)
	}
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	srcUVOffset := C.int64_t(src.Pitch * src.Height)

	// P210 chroma: full height, half width
	chromaRectW := rect.W / 2
	chromaRectH := rect.H // full height for 4:2:2
	if chromaRectW < 1 {
		chromaRectW = 1
	}
	if chromaRectH < 1 {
		chromaRectH = 1
	}

	chromaCropX := cropX / 2
	chromaCropY := cropY // full height for 4:2:2
	chromaCropW := cropW / 2
	chromaCropH := cropH // full height for 4:2:2

	uvParams := C.MetalPIPCompositeP210UVParams{
		dstW: C.uint32_t(dst.Width / 2), dstChromaH: C.uint32_t(dst.Height), dstPitch: C.uint32_t(dst.Pitch),
		srcW: C.uint32_t(src.Width), srcChromaH: C.uint32_t(src.Height), srcPitch: C.uint32_t(src.Pitch),
		rectX: C.int32_t(rect.X), rectY: C.int32_t(rect.Y),
		rectCW: C.int32_t(chromaRectW), rectCH: C.int32_t(chromaRectH),
		alpha256: C.int32_t(alpha256),
		cropX:    C.int32_t(chromaCropX), cropY: C.int32_t(chromaCropY),
		cropCW: C.int32_t(chromaCropW), cropCH: C.int32_t(chromaCropH),
	}
	uvBufs := [2]C.MetalBufferRef{dst.MetalBuf, src.MetalBuf}
	uvOffsets := [2]C.int64_t{dstUVOffset, srcUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 2,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 2,
		C.uint32_t(chromaRectW), C.uint32_t(chromaRectH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: pip composite p210 UV failed: %d", rc)
	}

	return nil
}

// UploadV210ToP210 uploads a CPU V210 buffer to a GPU P210 frame (lossless 10-bit).
func UploadV210ToP210(ctx *Context, dst *GPUFrame, v210 []byte, width, height int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 buffer too small: %d < %d", len(v210), expected)
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("v210_to_p210")
	if err != nil {
		return fmt.Errorf("gpu: V210->P210: %w", err)
	}

	// Upload V210 data to a temporary Metal buffer
	v210Buf, err := mtl.allocBuffer(expected)
	if err != nil {
		return fmt.Errorf("gpu: V210->P210 alloc: %w", err)
	}
	defer C.metal_buffer_free(v210Buf)
	C.memcpy(C.metal_buffer_contents(v210Buf), unsafe.Pointer(&v210[0]), C.size_t(expected))

	params := C.MetalV210P210Params{
		width:        C.uint32_t(width),
		height:       C.uint32_t(height),
		p210Pitch:    C.uint32_t(dst.Pitch),
		v210Stride32: C.uint32_t(v210Stride / 4),
	}

	rc := C.metal_v210_to_p210(mtl.queue, pipeline, dst.MetalBuf, v210Buf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: V210->P210 failed: %d", rc)
	}
	return nil
}

// DownloadP210ToV210 converts a P210 GPUFrame to V210 and downloads to CPU (lossless 10-bit).
func DownloadP210ToV210(ctx *Context, v210 []byte, src *GPUFrame, width, height int) error {
	if ctx == nil || ctx.mtl == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 download buffer too small: %d < %d", len(v210), expected)
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("p210_to_v210")
	if err != nil {
		return fmt.Errorf("gpu: P210->V210: %w", err)
	}

	v210Buf, err := mtl.allocBuffer(expected)
	if err != nil {
		return fmt.Errorf("gpu: P210->V210 alloc: %w", err)
	}
	defer C.metal_buffer_free(v210Buf)

	params := C.MetalV210P210Params{
		width:        C.uint32_t(width),
		height:       C.uint32_t(height),
		p210Pitch:    C.uint32_t(src.Pitch),
		v210Stride32: C.uint32_t(v210Stride / 4),
	}

	rc := C.metal_p210_to_v210(mtl.queue, pipeline, v210Buf, src.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: P210->V210 failed: %d", rc)
	}

	C.memcpy(unsafe.Pointer(&v210[0]), C.metal_buffer_contents(v210Buf), C.size_t(expected))
	return nil
}

// DSKCompositeFullFrameP210 composites a full-frame RGBA overlay (already on GPU)
// onto a P210 frame. This is the P210 counterpart to DSKCompositeFullFrame.
func DSKCompositeFullFrameP210(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, alphaScale float64) error {
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
	pipeline, err := mtl.getPipeline("dsk_overlay_p210")
	if err != nil {
		return fmt.Errorf("gpu: DSK full-frame p210: %w", err)
	}

	params := C.MetalDSKP210FullFrameParams{
		width:         C.uint32_t(frame.Width),
		height:        C.uint32_t(frame.Height),
		p210Pitch:     C.uint32_t(frame.Pitch),
		rgbaPitch:     C.uint32_t(overlay.Pitch),
		alphaScale256: C.int32_t(alphaScale256),
	}

	rc := C.metal_dsk_overlay_p210(mtl.queue, pipeline, frame.MetalBuf, overlay.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: DSK full-frame p210 composite failed: %d", rc)
	}
	return nil
}

// DSKCompositeRectP210 composites an RGBA overlay (already on GPU) into a
// rectangular region of a P210 frame. For now, this always dispatches as
// full-frame — the overlay is already sized to the target rect by the caller.
// TODO: Add a dedicated rect kernel for P210 when sub-frame overlays are common.
func DSKCompositeRectP210(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, rect Rect, alphaScale float64) error {
	// For rect compositing on P210, we dispatch the full-frame kernel since
	// the overlay is already clipped/positioned by the graphics compositor.
	// This is correct when overlay dimensions match the rect, which is the
	// typical case for DSK overlays.
	return DSKCompositeFullFrameP210(ctx, frame, overlay, alphaScale)
}

// DSKOverlayP210 alpha-blends an RGBA overlay onto a P210 frame.
// alphaScale256 is the global alpha scale (0-256).
func DSKOverlayP210(ctx *Context, frame *GPUFrame, rgba []byte, width, height, rgbaPitch, alphaScale256 int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("dsk_overlay_p210")
	if err != nil {
		return fmt.Errorf("gpu: dsk overlay p210: %w", err)
	}

	// Upload RGBA to a temporary Metal buffer
	rgbaSize := rgbaPitch * height
	if len(rgba) < rgbaSize {
		return fmt.Errorf("gpu: RGBA buffer too small: %d < %d", len(rgba), rgbaSize)
	}

	rgbaBuf, err := mtl.allocBuffer(rgbaSize)
	if err != nil {
		return fmt.Errorf("gpu: dsk overlay p210 alloc: %w", err)
	}
	defer C.metal_buffer_free(rgbaBuf)
	C.memcpy(C.metal_buffer_contents(rgbaBuf), unsafe.Pointer(&rgba[0]), C.size_t(rgbaSize))

	params := C.MetalDSKP210FullFrameParams{
		width:         C.uint32_t(width),
		height:        C.uint32_t(height),
		p210Pitch:     C.uint32_t(frame.Pitch),
		rgbaPitch:     C.uint32_t(rgbaPitch),
		alphaScale256: C.int32_t(alphaScale256),
	}

	rc := C.metal_dsk_overlay_p210(mtl.queue, pipeline, frame.MetalBuf, rgbaBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: dsk overlay p210 failed: %d", rc)
	}
	return nil
}

// STMapWarpP210Map warps a P210 frame using a pre-uploaded GPUSTMap.
// This is the P210 counterpart to STMapWarp (NV12), and is used by the
// stmap pipeline node which caches GPU-resident S/T maps.
func STMapWarpP210Map(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil || stmap == nil {
		return ErrGPUNotAvailable
	}
	if src.Width != stmap.Width || src.Height != stmap.Height {
		return fmt.Errorf("gpu: stmap p210: frame %dx%d doesn't match map %dx%d",
			src.Width, src.Height, stmap.Width, stmap.Height)
	}

	mtl := ctx.mtl
	width := stmap.Width
	height := stmap.Height

	// Warp Y plane
	yPipeline, err := mtl.getPipeline("stmap_warp_p210_y")
	if err != nil {
		return fmt.Errorf("gpu: stmap warp p210 Y: %w", err)
	}
	yParams := C.MetalSTMapP210Params{
		width:    C.uint32_t(width),
		height:   C.uint32_t(height),
		dstPitch: C.uint32_t(dst.Pitch),
		srcPitch: C.uint32_t(src.Pitch),
	}
	rc := C.metal_stmap_warp_p210_y(mtl.queue, yPipeline, src.MetalBuf, stmap.SBuf, stmap.TBuf, dst.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stmap warp p210 Y failed: %d", rc)
	}

	// Warp UV plane
	uvPipeline, err := mtl.getPipeline("stmap_warp_p210_uv")
	if err != nil {
		return fmt.Errorf("gpu: stmap warp p210 UV: %w", err)
	}
	chromaW := width / 2
	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)

	uvParams := C.MetalSTMapP210UVParams{
		lumaW:    C.uint32_t(width),
		lumaH:    C.uint32_t(height),
		chromaW:  C.uint32_t(chromaW),
		chromaH:  C.uint32_t(height), // P210: full height chroma
		dstPitch: C.uint32_t(dst.Pitch),
		srcPitch: C.uint32_t(src.Pitch),
	}
	uvBufs := [4]C.MetalBufferRef{src.MetalBuf, stmap.SBuf, stmap.TBuf, dst.MetalBuf}
	uvOffsets := [4]C.int64_t{srcUVOffset, 0, 0, dstUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 4,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 4,
		C.uint32_t(chromaW), C.uint32_t(height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stmap warp p210 UV failed: %d", rc)
	}

	return nil
}

// STMapWarpP210 warps a P210 frame using a float32 ST map.
func STMapWarpP210(ctx *Context, dst, src *GPUFrame, stmapS, stmapT []float32, width, height int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	stSize := width * height
	if len(stmapS) < stSize || len(stmapT) < stSize {
		return fmt.Errorf("gpu: stmap buffer too small: %d < %d", len(stmapS), stSize)
	}

	mtl := ctx.mtl

	// Upload ST maps to Metal buffers
	stBytes := stSize * 4
	sBuf, err := mtl.allocBuffer(stBytes)
	if err != nil {
		return fmt.Errorf("gpu: stmap S alloc: %w", err)
	}
	defer C.metal_buffer_free(sBuf)
	C.memcpy(C.metal_buffer_contents(sBuf), unsafe.Pointer(&stmapS[0]), C.size_t(stBytes))

	tBuf, err := mtl.allocBuffer(stBytes)
	if err != nil {
		return fmt.Errorf("gpu: stmap T alloc: %w", err)
	}
	defer C.metal_buffer_free(tBuf)
	C.memcpy(C.metal_buffer_contents(tBuf), unsafe.Pointer(&stmapT[0]), C.size_t(stBytes))

	// Warp Y plane
	yPipeline, err := mtl.getPipeline("stmap_warp_p210_y")
	if err != nil {
		return fmt.Errorf("gpu: stmap warp p210 Y: %w", err)
	}
	yParams := C.MetalSTMapP210Params{
		width:    C.uint32_t(width),
		height:   C.uint32_t(height),
		dstPitch: C.uint32_t(dst.Pitch),
		srcPitch: C.uint32_t(src.Pitch),
	}
	rc := C.metal_stmap_warp_p210_y(mtl.queue, yPipeline, src.MetalBuf, sBuf, tBuf, dst.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stmap warp p210 Y failed: %d", rc)
	}

	// Warp UV plane
	uvPipeline, err := mtl.getPipeline("stmap_warp_p210_uv")
	if err != nil {
		return fmt.Errorf("gpu: stmap warp p210 UV: %w", err)
	}
	chromaW := width / 2
	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)

	uvParams := C.MetalSTMapP210UVParams{
		lumaW:    C.uint32_t(width),
		lumaH:    C.uint32_t(height),
		chromaW:  C.uint32_t(chromaW),
		chromaH:  C.uint32_t(height), // P210: full height chroma
		dstPitch: C.uint32_t(dst.Pitch),
		srcPitch: C.uint32_t(src.Pitch),
	}
	uvBufs := [4]C.MetalBufferRef{src.MetalBuf, sBuf, tBuf, dst.MetalBuf}
	uvOffsets := [4]C.int64_t{srcUVOffset, 0, 0, dstUVOffset}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvPipeline,
		&uvBufs[0], &uvOffsets[0], 4,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 4,
		C.uint32_t(chromaW), C.uint32_t(height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stmap warp p210 UV failed: %d", rc)
	}

	return nil
}
