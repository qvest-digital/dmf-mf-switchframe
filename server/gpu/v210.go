//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t v210_to_nv12(
    uint8_t* nv12, const uint32_t* v210,
    int width, int height, int nv12Pitch, int v210StrideBytes,
    cudaStream_t stream);
cudaError_t nv12_to_v210(
    uint32_t* v210, const uint8_t* nv12,
    int width, int height, int nv12Pitch, int v210StrideBytes,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// V210LineStride returns the byte stride for one line of V210 data.
// Each group of 6 pixels = 16 bytes (4 x 32-bit words), padded to 128 bytes.
func V210LineStride(width int) int {
	groups := (width + 5) / 6
	rawBytes := groups * 16
	return (rawBytes + 127) &^ 127
}

// V210ToNV12 converts a GPU-resident V210 buffer to an NV12 GPUFrame.
// v210DevPtr is a CUdeviceptr to the V210 data in VRAM.
// width must be divisible by 6, height must be even.
func V210ToNV12(ctx *Context, dst *GPUFrame, v210DevPtr uintptr, v210Stride, width, height int) error {
	if ctx == nil || dst == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}
	if height%2 != 0 {
		return fmt.Errorf("gpu: V210 height %d not even", height)
	}

	rc := C.v210_to_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint32_t)(unsafe.Pointer(v210DevPtr)),
		C.int(width), C.int(height),
		C.int(dst.Pitch), C.int(v210Stride),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210→NV12 failed: %d", rc)
	}
	return ctx.Sync()
}

// NV12ToV210 converts an NV12 GPUFrame to a GPU-resident V210 buffer.
// v210DevPtr is a CUdeviceptr to the destination V210 buffer in VRAM.
// width must be divisible by 6, height must be even.
func NV12ToV210(ctx *Context, v210DevPtr uintptr, v210Stride int, src *GPUFrame, width, height int) error {
	if ctx == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}
	if height%2 != 0 {
		return fmt.Errorf("gpu: V210 height %d not even", height)
	}

	rc := C.nv12_to_v210(
		(*C.uint32_t)(unsafe.Pointer(v210DevPtr)),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(width), C.int(height),
		C.int(src.Pitch), C.int(v210Stride),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: NV12→V210 failed: %d", rc)
	}
	return ctx.Sync()
}

// UploadV210 uploads a CPU V210 buffer to GPU memory and converts to NV12.
// This is the common path for MXL sources: V210 arrives from shared memory,
// gets uploaded and converted in one step.
//
// Uses a persistent V210 staging buffer in Context to avoid per-call
// cudaMalloc/cudaFree at 30fps. ctx.mu serializes access.
func UploadV210(ctx *Context, dst *GPUFrame, v210 []byte, width, height int) error {
	if ctx == nil || dst == nil {
		return ErrGPUNotAvailable
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 buffer too small: %d < %d", len(v210), expected)
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Lazily allocate or grow the persistent V210 staging buffer.
	if ctx.stagingV210Size < expected {
		if ctx.stagingV210 != nil {
			C.cudaFree(ctx.stagingV210)
			ctx.stagingV210 = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingV210, C.size_t(expected)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: V210 upload alloc failed: %d", rc)
		}
		ctx.stagingV210Size = expected
	}

	if rc := C.cudaMemcpy(ctx.stagingV210, unsafe.Pointer(&v210[0]), C.size_t(expected), C.cudaMemcpyHostToDevice); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210 upload memcpy failed: %d", rc)
	}

	// V210ToNV12 calls ctx.Sync() which requires the stream — call the kernel
	// directly here since we already hold ctx.mu and can't recurse into
	// V210ToNV12 (it doesn't acquire ctx.mu, but we keep the call path simple).
	rc := C.v210_to_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint32_t)(ctx.stagingV210),
		C.int(width), C.int(height),
		C.int(dst.Pitch), C.int(v210Stride),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210→NV12 failed: %d", rc)
	}
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210 upload sync failed: %d", rc)
	}
	return nil
}

// DownloadV210 converts an NV12 GPUFrame to V210 and downloads to CPU.
// This is the output path for MXL: NV12 program frame → V210 for shared memory.
//
// Uses a persistent V210 staging buffer in Context to avoid per-call
// cudaMalloc/cudaFree at 30fps. ctx.mu serializes access.
func DownloadV210(ctx *Context, v210 []byte, src *GPUFrame, width, height int) error {
	if ctx == nil || src == nil {
		return ErrGPUNotAvailable
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 download buffer too small: %d < %d", len(v210), expected)
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Lazily allocate or grow the persistent V210 staging buffer.
	if ctx.stagingV210Size < expected {
		if ctx.stagingV210 != nil {
			C.cudaFree(ctx.stagingV210)
			ctx.stagingV210 = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingV210, C.size_t(expected)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: V210 download alloc failed: %d", rc)
		}
		ctx.stagingV210Size = expected
	}

	// Run the NV12→V210 kernel into the persistent staging buffer.
	rc := C.nv12_to_v210(
		(*C.uint32_t)(ctx.stagingV210),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		C.int(width), C.int(height),
		C.int(src.Pitch), C.int(v210Stride),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: NV12→V210 failed: %d", rc)
	}
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210 download sync failed: %d", rc)
	}

	if rc := C.cudaMemcpy(unsafe.Pointer(&v210[0]), ctx.stagingV210, C.size_t(expected), C.cudaMemcpyDeviceToHost); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: V210 download memcpy failed: %d", rc)
	}
	return nil
}
