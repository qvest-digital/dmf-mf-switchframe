//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

// Forward declaration (cgo preambles are file-scoped)
cudaError_t nv12_to_yuv420p(
    uint8_t* y, uint8_t* cb, uint8_t* cr, const uint8_t* nv12,
    int width, int height, int nv12_pitch, int dst_stride, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Download transfers a GPU NV12 frame to a CPU YUV420p buffer.
//
// The process:
// 1. Launch nv12_to_yuv420p kernel to convert NV12 → planar YUV420p in GPU memory
// 2. Copy the 3 planes from GPU to CPU
//
// The destination buffer must be at least width*height*3/2 bytes.
func Download(ctx *Context, yuv []byte, frame *GPUFrame, width, height int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv) < expectedSize {
		return fmt.Errorf("gpu: download: YUV buffer too small: %d < %d", len(yuv), expectedSize)
	}

	// Allocate temporary GPU buffers for the 3 planar outputs
	var devY, devCb, devCr C.CUdeviceptr
	rc := C.cuMemAlloc(&devY, C.size_t(ySize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: download: alloc Y failed: %d", rc)
	}
	defer C.cuMemFree(devY)

	rc = C.cuMemAlloc(&devCb, C.size_t(cbSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: download: alloc Cb failed: %d", rc)
	}
	defer C.cuMemFree(devCb)

	rc = C.cuMemAlloc(&devCr, C.size_t(crSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: download: alloc Cr failed: %d", rc)
	}
	defer C.cuMemFree(devCr)

	// Launch conversion kernel: NV12 → YUV420p
	// CUdeviceptr is uint64 (device address), cast via uintptr → unsafe.Pointer
	cerr := C.nv12_to_yuv420p(
		(*C.uint8_t)(unsafe.Pointer(uintptr(devY))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(devCb))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(devCr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(width),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: download: nv12_to_yuv420p kernel failed: %d", cerr)
	}

	// Synchronize before reading back
	if syncRc := C.cudaStreamSynchronize(ctx.stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: stream sync failed: %d", syncRc)
	}

	// Copy planar data from device to host
	rc = C.cuMemcpyDtoH(unsafe.Pointer(&yuv[0]), devY, C.size_t(ySize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: download: memcpy Y failed: %d", rc)
	}
	rc = C.cuMemcpyDtoH(unsafe.Pointer(&yuv[ySize]), devCb, C.size_t(cbSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: download: memcpy Cb failed: %d", rc)
	}
	rc = C.cuMemcpyDtoH(unsafe.Pointer(&yuv[ySize+cbSize]), devCr, C.size_t(crSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: download: memcpy Cr failed: %d", rc)
	}

	return nil
}
