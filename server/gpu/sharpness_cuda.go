//go:build cgo && cuda

package gpu

/*
#include <cuda_runtime.h>
#include <stdint.h>

// Forward declaration for CUDA kernel (implemented in cuda/sharpness.cu)
cudaError_t launch_sharpen_luma(
    uint8_t* frame, int width, int height, int pitch,
    float amount, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// SharpenLuma applies unsharp mask sharpening to the Y plane of an NV12 frame.
// amount controls intensity: 0.0 = no sharpening, 1.0 = full.
// The UV plane is not modified — sharpening chroma causes color artifacts.
func SharpenLuma(ctx *Context, frame *GPUFrame, amount float32) error {
	if amount <= 0 {
		return nil
	}
	rc := C.launch_sharpen_luma(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(frame.Width),
		C.int(frame.Height),
		C.int(frame.Pitch),
		C.float(amount),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: sharpen luma failed: %d", rc)
	}
	return ctx.Sync()
}
