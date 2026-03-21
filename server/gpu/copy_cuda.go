//go:build cgo && cuda && !darwin

package gpu

/*
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"unsafe"
)

// CopyGPUFrame copies NV12 data from src to dst using cudaMemcpy.
// Both frames must have the same dimensions and pitch. No-ops with an error
// log if dimensions mismatch.
//
// On CUDA, GPU frames live in device memory (not CPU-accessible), so we
// use cudaMemcpy with DeviceToDevice transfer.
func CopyGPUFrame(dst, src *GPUFrame) {
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		slog.Error("CopyGPUFrame: dimension mismatch",
			"dst", fmt.Sprintf("%dx%d p=%d", dst.Width, dst.Height, dst.Pitch),
			"src", fmt.Sprintf("%dx%d p=%d", src.Width, src.Height, src.Pitch))
		return
	}
	size := C.size_t(src.Pitch * src.Height * 3 / 2)
	rc := C.cudaMemcpy(
		unsafe.Pointer(uintptr(dst.DevPtr)),
		unsafe.Pointer(uintptr(src.DevPtr)),
		size,
		C.cudaMemcpyDeviceToDevice,
	)
	if rc != C.cudaSuccess {
		slog.Error("CopyGPUFrame: cudaMemcpy failed",
			"error", int(rc))
	}
}
