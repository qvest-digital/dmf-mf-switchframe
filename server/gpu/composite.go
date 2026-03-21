//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t pip_composite_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    int rectX, int rectY, int rectW, int rectH,
    int alpha256, cudaStream_t stream);
cudaError_t draw_border_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    int thickness, uint8_t colorY, uint8_t colorCb, uint8_t colorCr,
    cudaStream_t stream);
cudaError_t fill_rect_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    uint8_t colorY, uint8_t colorCb, uint8_t colorCr,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Rect defines a rectangle for PIP compositing.
type Rect struct {
	X, Y, W, H int
}

// YUVColor defines a color in YCbCr space.
type YUVColor struct {
	Y, Cb, Cr uint8
}

// ColorBlack is BT.709 limited-range black.
var ColorBlack = YUVColor{16, 128, 128}

// PIPComposite scales a source GPU frame and composites it into a destination
// frame at the specified rectangle. alpha is 0.0 (transparent) to 1.0 (opaque).
// Equivalent to PIPCompositeWithCrop with zero crop rect (full source).
func PIPComposite(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64) error {
	return PIPCompositeWithCrop(ctx, dst, src, rect, alpha, 0, 0, 0, 0)
}

// PIPCompositeWithCrop scales a source GPU frame (or a sub-region of it) and
// composites it into a destination frame at the specified rectangle.
// cropX/cropY/cropW/cropH define the source crop region; 0,0,0,0 means use
// the full source (backward compatible with PIPComposite).
// NOTE: CUDA path does not yet support crop — non-zero crop is ignored.
func PIPCompositeWithCrop(ctx *Context, dst, src *GPUFrame, rect Rect, alpha float64,
	cropX, cropY, cropW, cropH int) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	// CUDA kernel does not yet support crop params — ignored for now.
	_ = cropX
	_ = cropY
	_ = cropW
	_ = cropH

	alpha256 := int(alpha * 256.0)
	if alpha256 < 0 {
		alpha256 = 0
	} else if alpha256 > 256 {
		alpha256 = 256
	}

	rc := C.pip_composite_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(alpha256),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: pip composite failed: %d", rc)
	}
	return ctx.Sync()
}

// DrawBorder draws a colored border outside the given rectangle on a GPU frame.
func DrawBorder(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor, thickness int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rc := C.draw_border_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.int(thickness),
		C.uint8_t(color.Y), C.uint8_t(color.Cb), C.uint8_t(color.Cr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: draw border failed: %d", rc)
	}
	return ctx.Sync()
}

// FillRect fills a rectangular region with a constant color on a GPU frame.
func FillRect(ctx *Context, frame *GPUFrame, rect Rect, color YUVColor) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	rc := C.fill_rect_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.int(rect.X), C.int(rect.Y), C.int(rect.W), C.int(rect.H),
		C.uint8_t(color.Y), C.uint8_t(color.Cb), C.uint8_t(color.Cr),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: fill rect failed: %d", rc)
	}
	return ctx.Sync()
}
