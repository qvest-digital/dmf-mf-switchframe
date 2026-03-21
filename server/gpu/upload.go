//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Upload transfers a CPU YUV420p frame to a GPU NV12 frame.
//
// The process:
// 1. Copy YUV420p planes to pinned (page-locked) host memory
// 2. Async memcpy from pinned host to GPU staging area
// 3. Launch yuv420p_to_nv12 kernel to convert and place in pitched NV12
//
// For the initial implementation, we use a simpler approach:
// 1. cudaMemcpy the 3 planes to separate GPU buffers
// 2. Launch conversion kernel
// The pinned-memory optimization can be added in Phase 14.
func Upload(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv) < expectedSize {
		return fmt.Errorf("gpu: upload: YUV buffer too small: %d < %d", len(yuv), expectedSize)
	}

	yData := yuv[:ySize]
	cbData := yuv[ySize : ySize+cbSize]
	crData := yuv[ySize+cbSize : ySize+cbSize+crSize]

	// Allocate temporary GPU buffers for the 3 planar inputs
	var devY, devCb, devCr C.CUdeviceptr
	rc := C.cuMemAlloc(&devY, C.size_t(ySize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: upload: alloc Y failed: %d", rc)
	}
	defer C.cuMemFree(devY)

	rc = C.cuMemAlloc(&devCb, C.size_t(cbSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: upload: alloc Cb failed: %d", rc)
	}
	defer C.cuMemFree(devCb)

	rc = C.cuMemAlloc(&devCr, C.size_t(crSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: upload: alloc Cr failed: %d", rc)
	}
	defer C.cuMemFree(devCr)

	// Copy planar data from host to device
	rc = C.cuMemcpyHtoD(devY, unsafe.Pointer(&yData[0]), C.size_t(ySize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: upload: memcpy Y failed: %d", rc)
	}
	rc = C.cuMemcpyHtoD(devCb, unsafe.Pointer(&cbData[0]), C.size_t(cbSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: upload: memcpy Cb failed: %d", rc)
	}
	rc = C.cuMemcpyHtoD(devCr, unsafe.Pointer(&crData[0]), C.size_t(crSize))
	if rc != C.CUDA_SUCCESS {
		return fmt.Errorf("gpu: upload: memcpy Cr failed: %d", rc)
	}

	// Launch conversion kernel: YUV420p → NV12
	cerr := C.yuv420p_to_nv12(
		(*C.uint8_t)(unsafe.Pointer(frame.DevPtr)),
		(*C.uint8_t)(unsafe.Pointer(devY)),
		(*C.uint8_t)(unsafe.Pointer(devCb)),
		(*C.uint8_t)(unsafe.Pointer(devCr)),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(width),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: upload: yuv420p_to_nv12 kernel failed: %d", cerr)
	}

	// Synchronize to ensure conversion completes before returning
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: upload: stream sync failed: %d", rc)
	}

	return nil
}

// FillBlack fills a GPU NV12 frame with black (Y=16, Cb=128, Cr=128 for BT.709 limited range).
func FillBlack(ctx *Context, frame *GPUFrame) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	cerr := C.nv12_fill(
		(*C.uint8_t)(unsafe.Pointer(frame.DevPtr)),
		C.int(frame.Width), C.int(frame.Height), C.int(frame.Pitch),
		C.uint8_t(16), C.uint8_t(128), C.uint8_t(128),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: fill black: kernel failed: %d", cerr)
	}

	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: fill black: sync failed: %d", rc)
	}

	return nil
}
