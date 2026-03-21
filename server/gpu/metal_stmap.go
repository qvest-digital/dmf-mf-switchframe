//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// UploadSTMap uploads S and T coordinate arrays to GPU memory.
func UploadSTMap(ctx *Context, s, t []float32, width, height int) (*GPUSTMap, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}
	expected := width * height
	if len(s) < expected || len(t) < expected {
		return nil, fmt.Errorf("gpu: stmap: S/T arrays too small: %d < %d", len(s), expected)
	}

	sz := expected * 4 // float32 = 4 bytes
	sBuf, err := ctx.mtl.allocBuffer(sz)
	if err != nil {
		return nil, fmt.Errorf("gpu: stmap: alloc S failed: %w", err)
	}
	tBuf, err := ctx.mtl.allocBuffer(sz)
	if err != nil {
		C.metal_buffer_free(sBuf)
		return nil, fmt.Errorf("gpu: stmap: alloc T failed: %w", err)
	}

	C.memcpy(C.metal_buffer_contents(sBuf), unsafe.Pointer(&s[0]), C.size_t(sz))
	C.memcpy(C.metal_buffer_contents(tBuf), unsafe.Pointer(&t[0]), C.size_t(sz))

	return &GPUSTMap{
		SBuf:   sBuf,
		TBuf:   tBuf,
		Width:  width,
		Height: height,
	}, nil
}

// Free releases GPU memory for the ST map.
func (m *GPUSTMap) Free() {
	if m != nil {
		if m.SBuf != nil {
			C.metal_buffer_free(m.SBuf)
			m.SBuf = nil
		}
		if m.TBuf != nil {
			C.metal_buffer_free(m.TBuf)
			m.TBuf = nil
		}
	}
}

// STMapWarp applies an ST map warp to a GPU NV12 frame.
func STMapWarp(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil || stmap == nil {
		return ErrGPUNotAvailable
	}
	if src.Width != stmap.Width || src.Height != stmap.Height {
		return fmt.Errorf("gpu: stmap: frame %dx%d doesn't match map %dx%d",
			src.Width, src.Height, stmap.Width, stmap.Height)
	}

	mtl := ctx.mtl

	// Y plane warp
	yPipeline, err := mtl.getPipeline("stmap_warp_y")
	if err != nil {
		return fmt.Errorf("gpu: stmap warp Y: %w", err)
	}
	yParams := C.MetalSTMapParams{
		width:    C.uint32_t(stmap.Width),
		height:   C.uint32_t(stmap.Height),
		dstPitch: C.uint32_t(dst.Pitch),
		srcPitch: C.uint32_t(src.Pitch),
	}
	rc := C.metal_stmap_warp_y(mtl.queue, yPipeline, src.MetalBuf, stmap.SBuf, stmap.TBuf, dst.MetalBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stmap warp Y failed: %d", rc)
	}

	// UV plane warp
	uvPipeline, err := mtl.getPipeline("stmap_warp_uv")
	if err != nil {
		return fmt.Errorf("gpu: stmap warp UV: %w", err)
	}
	chromaW := stmap.Width / 2
	chromaH := stmap.Height / 2
	uvParams := C.MetalSTMapUVParams{
		lumaW:    C.uint32_t(stmap.Width),
		lumaH:    C.uint32_t(stmap.Height),
		chromaW:  C.uint32_t(chromaW),
		chromaH:  C.uint32_t(chromaH),
		dstPitch: C.uint32_t(dst.Pitch),
		srcPitch: C.uint32_t(src.Pitch),
	}
	// Note: For UV plane, we need to pass buffers with offsets for the UV portion.
	// Metal buffers contain both Y and UV planes. The kernel needs to know the UV offset.
	// TODO: Implement buffer offset support for UV-plane kernels.
	rc = C.metal_stmap_warp_uv(mtl.queue, uvPipeline, src.MetalBuf, stmap.SBuf, stmap.TBuf, dst.MetalBuf, &uvParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: stmap warp UV failed: %d", rc)
	}

	return nil
}

// STMapWarpGlobalMem is the same as STMapWarp on Metal (no texture distinction).
func STMapWarpGlobalMem(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	return STMapWarp(ctx, dst, src, stmap)
}

// NewGPUAnimatedSTMap uploads a sequence of ST map frames to GPU memory.
func NewGPUAnimatedSTMap(ctx *Context, sMaps, tMaps [][]float32, width, height, fps int) (*GPUAnimatedSTMap, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}
	if len(sMaps) != len(tMaps) || len(sMaps) == 0 {
		return nil, fmt.Errorf("gpu: animated stmap: mismatched or empty frame count")
	}

	anim := &GPUAnimatedSTMap{
		frames: make([]*GPUSTMap, len(sMaps)),
		Width:  width,
		Height: height,
		FPS:    fps,
	}

	for i := range sMaps {
		m, err := UploadSTMap(ctx, sMaps[i], tMaps[i], width, height)
		if err != nil {
			anim.Free()
			return nil, fmt.Errorf("gpu: animated stmap frame %d: %w", i, err)
		}
		anim.frames[i] = m
	}

	return anim, nil
}

// CurrentFrame returns the current ST map frame and advances the counter.
func (a *GPUAnimatedSTMap) CurrentFrame() *GPUSTMap {
	idx := a.index.Add(1) - 1
	return a.frames[idx%int64(len(a.frames))]
}

// FrameCount returns the number of animation frames.
func (a *GPUAnimatedSTMap) FrameCount() int {
	return len(a.frames)
}

// Free releases all GPU memory for the animated ST map.
func (a *GPUAnimatedSTMap) Free() {
	if a == nil {
		return
	}
	for _, m := range a.frames {
		if m != nil {
			m.Free()
		}
	}
	a.frames = nil
}

// Ensure atomic is used.
var _ atomic.Int64
