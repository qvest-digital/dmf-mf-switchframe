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
// Uses CUDA Runtime API (cudaMalloc/cudaMemcpy) for thread-safe operation
// across cgo calls. Allocates temporary device buffers, launches the
// NV12→YUV420p conversion kernel, then copies planes to host.
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

	// Allocate temporary GPU buffers for the 3 planar outputs (Runtime API)
	var devY, devCb, devCr unsafe.Pointer
	if rc := C.cudaMalloc(&devY, C.size_t(ySize)); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: alloc Y failed: %d", rc)
	}
	defer C.cudaFree(devY)

	if rc := C.cudaMalloc(&devCb, C.size_t(cbSize)); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: alloc Cb failed: %d", rc)
	}
	defer C.cudaFree(devCb)

	if rc := C.cudaMalloc(&devCr, C.size_t(crSize)); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: alloc Cr failed: %d", rc)
	}
	defer C.cudaFree(devCr)

	// Launch conversion kernel: NV12 → YUV420p
	cerr := C.nv12_to_yuv420p(
		(*C.uint8_t)(devY),
		(*C.uint8_t)(devCb),
		(*C.uint8_t)(devCr),
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(width),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: download: nv12_to_yuv420p kernel failed: %d", cerr)
	}

	// Synchronize before reading back
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: stream sync failed: %d", rc)
	}

	// Copy planar data from device to host (Runtime API)
	if rc := C.cudaMemcpy(unsafe.Pointer(&yuv[0]), devY, C.size_t(ySize), C.cudaMemcpyDeviceToHost); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: memcpy Y failed: %d", rc)
	}
	if rc := C.cudaMemcpy(unsafe.Pointer(&yuv[ySize]), devCb, C.size_t(cbSize), C.cudaMemcpyDeviceToHost); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: memcpy Cb failed: %d", rc)
	}
	if rc := C.cudaMemcpy(unsafe.Pointer(&yuv[ySize+cbSize]), devCr, C.size_t(crSize), C.cudaMemcpyDeviceToHost); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: memcpy Cr failed: %d", rc)
	}

	return nil
}
