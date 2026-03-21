//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t stmap_warp_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* src, int srcPitch,
    const float* stS, const float* stT,
    int width, int height, cudaStream_t stream);
cudaError_t stmap_warp_nv12_tex(
    uint8_t* dst, int dstPitch,
    const uint8_t* src, int srcPitch,
    const float* stS, const float* stT,
    int width, int height, cudaStream_t stream);
cudaError_t stmap_upload(
    float** devS, float** devT, int width, int height,
    const float* hostS, const float* hostT);
void stmap_free(float* devS, float* devT);
*/
import "C"

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

// GPUSTMap holds an ST map uploaded to GPU memory.
// S and T are separate float arrays in VRAM, each width*height elements.
type GPUSTMap struct {
	DevS   *C.float // S coordinates in VRAM
	DevT   *C.float // T coordinates in VRAM
	Width  int
	Height int
}

// UploadSTMap uploads S and T coordinate arrays to GPU memory.
// s and t must each have width*height float32 elements with normalized
// coordinates (0.0-1.0).
func UploadSTMap(ctx *Context, s, t []float32, width, height int) (*GPUSTMap, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}
	expected := width * height
	if len(s) < expected || len(t) < expected {
		return nil, fmt.Errorf("gpu: stmap: S/T arrays too small: %d < %d", len(s), expected)
	}

	var devS, devT *C.float
	rc := C.stmap_upload(
		&devS, &devT,
		C.int(width), C.int(height),
		(*C.float)(unsafe.Pointer(&s[0])),
		(*C.float)(unsafe.Pointer(&t[0])),
	)
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: stmap upload failed: %d", rc)
	}

	return &GPUSTMap{
		DevS:   devS,
		DevT:   devT,
		Width:  width,
		Height: height,
	}, nil
}

// Free releases GPU memory for the ST map.
func (m *GPUSTMap) Free() {
	if m != nil {
		C.stmap_free(m.DevS, m.DevT)
		m.DevS = nil
		m.DevT = nil
	}
}

// STMapWarp applies an ST map warp to a GPU NV12 frame.
// Uses CUDA texture memory for hardware bilinear interpolation on the Y plane
// (single tex2D fetch replaces 4 global reads + 3 multiply-adds per pixel).
// Falls back to global memory automatically if texture creation fails.
// Both frames must have matching dimensions (same as the ST map).
func STMapWarp(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	if ctx == nil || dst == nil || src == nil || stmap == nil {
		return ErrGPUNotAvailable
	}
	if src.Width != stmap.Width || src.Height != stmap.Height {
		return fmt.Errorf("gpu: stmap: frame %dx%d doesn't match map %dx%d",
			src.Width, src.Height, stmap.Width, stmap.Height)
	}

	// Use texture-based path (hardware bilinear for Y, global mem for UV).
	// Falls back to full global memory internally if texture creation fails.
	rc := C.stmap_warp_nv12_tex(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Pitch),
		stmap.DevS, stmap.DevT,
		C.int(stmap.Width), C.int(stmap.Height),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stmap warp failed: %d", rc)
	}
	return ctx.Sync()
}

// STMapWarpGlobalMem applies an ST map warp using only global memory
// (no texture objects). Provided for benchmarking comparison.
func STMapWarpGlobalMem(ctx *Context, dst, src *GPUFrame, stmap *GPUSTMap) error {
	if ctx == nil || dst == nil || src == nil || stmap == nil {
		return ErrGPUNotAvailable
	}
	if src.Width != stmap.Width || src.Height != stmap.Height {
		return fmt.Errorf("gpu: stmap: frame %dx%d doesn't match map %dx%d",
			src.Width, src.Height, stmap.Width, stmap.Height)
	}

	rc := C.stmap_warp_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Pitch),
		stmap.DevS, stmap.DevT,
		C.int(stmap.Width), C.int(stmap.Height),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stmap warp (global mem) failed: %d", rc)
	}
	return ctx.Sync()
}

// GPUAnimatedSTMap holds a sequence of ST maps pre-uploaded to GPU memory
// for animated warp effects (e.g., flowing water, heat shimmer).
type GPUAnimatedSTMap struct {
	frames []*GPUSTMap
	Width  int
	Height int
	FPS    int
	index  atomic.Int64
}

// NewGPUAnimatedSTMap uploads a sequence of ST map frames to GPU memory.
func NewGPUAnimatedSTMap(ctx *Context, sMaps, tMaps [][]float32, width, height, fps int) (*GPUAnimatedSTMap, error) {
	if ctx == nil {
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
