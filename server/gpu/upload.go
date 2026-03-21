//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

// Forward declarations (cgo preambles are file-scoped)
cudaError_t yuv420p_to_nv12(
    uint8_t* nv12, const uint8_t* y, const uint8_t* cb, const uint8_t* cr,
    int width, int height, int nv12_pitch, int src_stride, cudaStream_t stream);
cudaError_t nv12_fill(
    uint8_t* nv12, int width, int height, int pitch,
    uint8_t yVal, uint8_t cbVal, uint8_t crVal, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Upload transfers a CPU YUV420p frame to a GPU NV12 frame.
//
// Uses persistent staging device buffers stored in Context to avoid
// cudaMalloc/cudaFree on every call (~180 allocs/sec at 30fps with 3 planes).
// Staging buffers are lazily allocated on first use and freed in Close().
// ctx.mu serializes access to the staging buffers.
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

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Lazily allocate or grow persistent staging buffers.
	if ctx.stagingSize < ySize {
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
			return fmt.Errorf("gpu: upload: alloc staging Y failed: %d", rc)
		}
		if rc := C.cudaMalloc(&ctx.stagingCb, C.size_t(cbSize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: upload: alloc staging Cb failed: %d", rc)
		}
		if rc := C.cudaMalloc(&ctx.stagingCr, C.size_t(crSize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: upload: alloc staging Cr failed: %d", rc)
		}
		ctx.stagingSize = ySize
	}

	// Copy planar data from host to persistent staging buffers (async so the
	// subsequent kernel launch can overlap with the DMA transfer on the stream).
	if rc := C.cudaMemcpyAsync(ctx.stagingY, unsafe.Pointer(&yData[0]), C.size_t(ySize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: upload: memcpy Y failed: %d", rc)
	}
	if rc := C.cudaMemcpyAsync(ctx.stagingCb, unsafe.Pointer(&cbData[0]), C.size_t(cbSize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: upload: memcpy Cb failed: %d", rc)
	}
	if rc := C.cudaMemcpyAsync(ctx.stagingCr, unsafe.Pointer(&crData[0]), C.size_t(crSize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: upload: memcpy Cr failed: %d", rc)
	}

	// Launch conversion kernel: YUV420p → NV12
	cerr := C.yuv420p_to_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(ctx.stagingY),
		(*C.uint8_t)(ctx.stagingCb),
		(*C.uint8_t)(ctx.stagingCr),
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
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
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
