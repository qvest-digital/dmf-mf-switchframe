//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t scale_bilinear_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream);

cudaError_t scale_lanczos3_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    float* tmpBuf,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// ScaleQuality selects the scaling algorithm.
type ScaleQuality int

const (
	ScaleQualityBilinear ScaleQuality = iota
	ScaleQualityLanczos
)

// ScaleBilinear scales an NV12 GPU frame using bilinear interpolation.
// Both Y and UV planes are scaled independently using a custom CUDA kernel
// with 16.16 fixed-point sub-pixel accuracy.
func ScaleBilinear(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	rc := C.scale_bilinear_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale bilinear failed: %d", rc)
	}

	if syncRc := C.cudaStreamSynchronize(ctx.stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale sync failed: %d", syncRc)
	}
	return nil
}

// ScaleLanczos3 scales an NV12 GPU frame using a two-pass separable Lanczos-3
// kernel. It allocates (and caches on the Context) a temporary float device
// buffer sized dstW * srcH floats, sufficient for both Y and UV passes.
func ScaleLanczos3(ctx *Context, dst, src *GPUFrame) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}

	// Ensure temp buffer is large enough: dstW * srcH floats cover both passes.
	needed := dst.Width * src.Height
	if err := ctx.ensureLanczosTemp(needed); err != nil {
		return err
	}

	rc := C.scale_lanczos3_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		C.int(dst.Width), C.int(dst.Height), C.int(dst.Pitch),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch),
		ctx.lanczosTmp,
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale lanczos3 failed: %d", rc)
	}

	if syncRc := C.cudaStreamSynchronize(ctx.stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: scale lanczos3 sync failed: %d", syncRc)
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
