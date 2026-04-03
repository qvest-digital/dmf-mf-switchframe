//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

// Forward declarations (defined in cuda/convert.cu)
cudaError_t yuv422p10_to_p210(
    uint8_t* dst, const uint8_t* src,
    int width, int height, int dstPitch, int srcStride,
    cudaStream_t stream);
cudaError_t p210_to_yuv422p10(
    uint8_t* dst, const uint8_t* src,
    int width, int height, int srcPitch, int dstStride,
    cudaStream_t stream);
cudaError_t p210_fill(
    uint8_t* p210, int width, int height, int pitch,
    uint16_t yVal, uint16_t cbVal, uint16_t crVal, cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Upload10 transfers a CPU YUV422P10LE frame to a GPU P210 frame.
//
// YUV422P10LE layout (planar): Y plane (w*h uint16 LE) + Cb plane (w/2*h uint16 LE) + Cr plane (w/2*h uint16 LE).
// P210 layout (semi-planar): Y plane (pitch*height bytes, uint16 per sample) + UV plane (pitch*height bytes, interleaved Cb/Cr uint16 pairs).
//
// On CUDA, this uploads planar data to a device staging buffer, then runs a
// GPU kernel to interleave Cb/Cr into the semi-planar UV format.
func Upload10(ctx *Context, frame *GPUFrame, yuv422p10 []byte, width, height int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	chromaW := width / 2
	ySize := width * height * 2    // uint16 per sample
	cbSize := chromaW * height * 2 // uint16 per sample, full height (4:2:2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv422p10) < expectedSize {
		return fmt.Errorf("gpu: upload10: YUV422P10LE buffer too small: %d < %d", len(yuv422p10), expectedSize)
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Use a single staging buffer for the entire planar frame.
	// We reuse stagingV210 since Upload10 and UploadV210 are never concurrent
	// on the same context (they both hold ctx.mu).
	if ctx.stagingV210Size < expectedSize {
		if ctx.stagingV210 != nil {
			C.cudaFree(ctx.stagingV210)
			ctx.stagingV210 = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingV210, C.size_t(expectedSize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: upload10: alloc staging failed: %d", rc)
		}
		ctx.stagingV210Size = expectedSize
	}

	// Copy entire planar frame to device staging
	if rc := C.cudaMemcpyAsync(ctx.stagingV210, unsafe.Pointer(&yuv422p10[0]), C.size_t(expectedSize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: upload10: memcpy failed: %d", rc)
	}

	// Run YUV422P10LE -> P210 conversion kernel
	cerr := C.yuv422p10_to_p210(
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		(*C.uint8_t)(ctx.stagingV210),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(width),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: upload10: yuv422p10_to_p210 kernel failed: %d", cerr)
	}

	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: upload10: stream sync failed: %d", rc)
	}
	return nil
}

// Download10 transfers a GPU P210 frame to a CPU YUV422P10LE buffer.
func Download10(ctx *Context, frame *GPUFrame, dst []byte, width, height int) error {
	if ctx == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	chromaW := width / 2
	ySize := width * height * 2
	cbSize := chromaW * height * 2
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(dst) < expectedSize {
		return fmt.Errorf("gpu: download10: YUV422P10LE buffer too small: %d < %d", len(dst), expectedSize)
	}

	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Allocate staging buffer for the planar output
	if ctx.stagingV210Size < expectedSize {
		if ctx.stagingV210 != nil {
			C.cudaFree(ctx.stagingV210)
			ctx.stagingV210 = nil
		}
		if rc := C.cudaMalloc(&ctx.stagingV210, C.size_t(expectedSize)); rc != C.cudaSuccess {
			return fmt.Errorf("gpu: download10: alloc staging failed: %d", rc)
		}
		ctx.stagingV210Size = expectedSize
	}

	// Run P210 -> YUV422P10LE conversion kernel into staging
	cerr := C.p210_to_yuv422p10(
		(*C.uint8_t)(ctx.stagingV210),
		(*C.uint8_t)(unsafe.Pointer(uintptr(frame.DevPtr))),
		C.int(width), C.int(height),
		C.int(frame.Pitch), C.int(width),
		ctx.stream,
	)
	if cerr != C.cudaSuccess {
		return fmt.Errorf("gpu: download10: p210_to_yuv422p10 kernel failed: %d", cerr)
	}

	// Copy planar result from device to host
	if rc := C.cudaMemcpyAsync(unsafe.Pointer(&dst[0]), ctx.stagingV210, C.size_t(expectedSize), C.cudaMemcpyDeviceToHost, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download10: memcpy failed: %d", rc)
	}

	if rc := C.cudaStreamSynchronize(ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: download10: stream sync failed: %d", rc)
	}
	return nil
}

// UploadYUV420PToP210 is not supported on CUDA (requires unified memory).
// Use Upload to NV12 temp + ConvertNV12ToP210 instead.
func UploadYUV420PToP210(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	return fmt.Errorf("gpu: UploadYUV420PToP210 not supported on CUDA (use NV12→P210 kernel)")
}
