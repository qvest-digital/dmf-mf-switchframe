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
// Uses persistent staging device buffers stored in Context to avoid
// cudaMalloc/cudaFree on every call (~180 allocs/sec at 30fps with 3 planes).
// Staging buffers are lazily allocated on first use and freed in Close().
// ctx.mu serializes access to the staging buffers.
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

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Lazily allocate or grow persistent staging buffers.
	// Track both Y and chroma sizes separately for safety.
	if ctx.stagingSize < ySize || ctx.stagingCbSize < cbSize {
		if ctx.stagingY != nil {
			C.cudaFree(ctx.stagingY)
			ctx.stagingY = nil
		}
		if ctx.stagingCb != nil {
			C.cudaFree(ctx.stagingCb)
			ctx.stagingCb = nil
		}
		if ctx.stagingCr != nil {
			C.cudaFree(ctx.stagingCr)
			ctx.stagingCr = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingY, C.size_t(ySize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: download: alloc staging Y failed: %d", rc)
		}
		if rc := C.cudaMalloc(&ctx.stagingCb, C.size_t(cbSize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: download: alloc staging Cb failed: %d", rc)
		}
		if rc := C.cudaMalloc(&ctx.stagingCr, C.size_t(crSize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: download: alloc staging Cr failed: %d", rc)
		}
		ctx.stagingSize = ySize
		ctx.stagingCbSize = cbSize
	}

	// Launch conversion kernel: NV12 → YUV420p into persistent staging buffers.
	cerr := C.nv12_to_yuv420p(
		(*C.uint8_t)(ctx.stagingY),
		(*C.uint8_t)(ctx.stagingCb),
		(*C.uint8_t)(ctx.stagingCr),
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(width),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: download: nv12_to_yuv420p kernel failed: %d", cerr)
	}

	// Queue async device→host copies after the kernel on the same stream.
	// A single cudaStreamSynchronize below ensures all three transfers complete
	// before we return the data to the caller.
	if rc := C.cudaMemcpyAsync(unsafe.Pointer(&yuv[0]), ctx.stagingY, C.size_t(ySize), C.cudaMemcpyDeviceToHost, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: memcpy Y failed: %d", rc)
	}
	if rc := C.cudaMemcpyAsync(unsafe.Pointer(&yuv[ySize]), ctx.stagingCb, C.size_t(cbSize), C.cudaMemcpyDeviceToHost, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: memcpy Cb failed: %d", rc)
	}
	if rc := C.cudaMemcpyAsync(unsafe.Pointer(&yuv[ySize+cbSize]), ctx.stagingCr, C.size_t(crSize), C.cudaMemcpyDeviceToHost, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: memcpy Cr failed: %d", rc)
	}

	// Synchronize once after all async operations — kernel + three DMA transfers.
	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download: stream sync failed: %d", rc)
	}

	return nil
}
