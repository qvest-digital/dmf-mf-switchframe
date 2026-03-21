//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t alpha_blend_rgba_nv12(
    uint8_t* nv12, const uint8_t* rgba,
    int width, int height, int nv12Pitch, int rgbaPitch,
    int alphaScale256, cudaStream_t stream);
cudaError_t alpha_blend_rgba_rect_nv12(
    uint8_t* nv12,
    const uint8_t* rgba, int overlayW, int overlayH, int rgbaPitch,
    int nv12Pitch, int frameW, int frameH,
    int rectX, int rectY, int rectW, int rectH,
    int alphaScale256, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// GPUOverlay holds an RGBA overlay uploaded to GPU memory.
type GPUOverlay struct {
	DevPtr unsafe.Pointer // RGBA data in VRAM
	Width  int
	Height int
	Pitch  int // row pitch (bytes per row, >= width*4)
}

// UploadOverlay uploads an RGBA image to GPU memory for DSK compositing.
// The returned GPUOverlay must be freed with FreeOverlay when no longer needed.
func UploadOverlay(ctx *Context, rgba []byte, width, height int) (*GPUOverlay, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}
	if len(rgba) < width*height*4 {
		return nil, fmt.Errorf("gpu: RGBA buffer too small: %d < %d", len(rgba), width*height*4)
	}

	// Allocate pitched GPU memory for RGBA
	var devPtr unsafe.Pointer
	var pitch C.size_t
	rc := C.cudaMallocPitch(&devPtr, &pitch, C.size_t(width*4), C.size_t(height))
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: overlay alloc failed: %d", rc)
	}

	// Copy RGBA to GPU (row by row for pitched alignment)
	rc = C.cudaMemcpy2D(
		devPtr, pitch,
		unsafe.Pointer(&rgba[0]), C.size_t(width*4),
		C.size_t(width*4), C.size_t(height),
		C.cudaMemcpyHostToDevice,
	)
	if rc != C.cudaSuccess {
		C.cudaFree(devPtr)
		return nil, fmt.Errorf("gpu: overlay upload failed: %d", rc)
	}

	return &GPUOverlay{
		DevPtr: devPtr,
		Width:  width,
		Height: height,
		Pitch:  int(pitch),
	}, nil
}

// FreeOverlay releases GPU memory for an overlay.
func FreeOverlay(overlay *GPUOverlay) {
	if overlay != nil && overlay.DevPtr != nil {
		C.cudaFree(overlay.DevPtr)
		overlay.DevPtr = nil
	}
}

// DSKCompositeFullFrame composites a full-frame RGBA overlay onto an NV12 GPU frame.
// alphaScale is 0.0 (fully transparent) to 1.0 (use per-pixel alpha as-is).
func DSKCompositeFullFrame(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, alphaScale float64) error {
	if ctx == nil || frame == nil || overlay == nil {
		return ErrGPUNotAvailable
	}

	alphaScale256 := int(alphaScale * 256.0)
	if alphaScale256 < 0 {
		alphaScale256 = 0
	} else if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	rc := C.alpha_blend_rgba_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(overlay.DevPtr),
		C.int(frame.Width), C.int(frame.Height),
		C.int(frame.Pitch), C.int(overlay.Pitch),
		C.int(alphaScale256),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: DSK full-frame composite failed: %d", rc)
	}
	return ctx.Sync()
}

// DSKCompositeRect composites an RGBA overlay into a rectangular region of
// an NV12 GPU frame. Uses nearest-neighbor scaling if overlay size differs from rect.
func DSKCompositeRect(ctx *Context, frame *GPUFrame, overlay *GPUOverlay, rect Rect, alphaScale float64) error {
	if ctx == nil || frame == nil || overlay == nil {
		return ErrGPUNotAvailable
	}

	alphaScale256 := int(alphaScale * 256.0)
	if alphaScale256 < 0 {
		alphaScale256 = 0
	} else if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	rc := C.alpha_blend_rgba_rect_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(overlay.DevPtr),
		C.int(overlay.Width), C.int(overlay.Height), C.int(overlay.Pitch),
		C.int(frame.Pitch), C.int(frame.Width), C.int(frame.Height),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(alphaScale256),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: DSK rect composite failed: %d", rc)
	}
	return ctx.Sync()
}
