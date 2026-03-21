//go:build cgo && cuda && !darwin

package gpu

/*
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// CopyGPUFrame copies NV12 data from src to dst on the default CUDA stream.
// Both frames must have the same dimensions and pitch.
//
// Uses cudaMemcpyAsync on defaultCUDAStream (the context's main processing
// stream) followed by cudaStreamSynchronize to ensure the copy completes
// before returning. This replaces the old cudaMemcpy (null stream) approach
// which could race with kernel launches on non-blocking streams.
//
func CopyGPUFrame(dst, src *GPUFrame) error {
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		return fmt.Errorf("CopyGPUFrame: dimension mismatch: dst=%dx%d p=%d src=%dx%d p=%d",
			dst.Width, dst.Height, dst.Pitch, src.Width, src.Height, src.Pitch)
	}

	size := C.size_t(src.Pitch * src.Height * 3 / 2)
	rc := C.cudaMemcpyAsync(
		unsafe.Pointer(uintptr(dst.DevPtr)),
		unsafe.Pointer(uintptr(src.DevPtr)),
		size,
		C.cudaMemcpyDeviceToDevice,
		defaultCUDAStream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("CopyGPUFrame: cudaMemcpyAsync failed: %d", rc)
	}
	if rc := C.cudaStreamSynchronize(defaultCUDAStream); rc != C.cudaSuccess {
		return fmt.Errorf("CopyGPUFrame: stream sync failed: %d", rc)
	}
	return nil
}

// CopyGPUFrameOn copies NV12 data from src to dst using the specified work
// queue's CUDA stream. If q is nil, the default CUDA stream is used.
// The copy is synchronous — it blocks until the copy completes.
func CopyGPUFrameOn(dst, src *GPUFrame, q *GPUWorkQueue) error {
	if dst == nil || src == nil {
		return fmt.Errorf("CopyGPUFrameOn: nil frame")
	}
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		return fmt.Errorf("CopyGPUFrameOn: dimension mismatch: dst=%dx%d p=%d src=%dx%d p=%d",
			dst.Width, dst.Height, dst.Pitch, src.Width, src.Height, src.Pitch)
	}

	stream := cudaStream(q)
	if stream == nil {
		stream = defaultCUDAStream
	}

	size := C.size_t(src.Pitch * src.Height * 3 / 2)
	rc := C.cudaMemcpyAsync(
		unsafe.Pointer(uintptr(dst.DevPtr)),
		unsafe.Pointer(uintptr(src.DevPtr)),
		size,
		C.cudaMemcpyDeviceToDevice,
		stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("CopyGPUFrameOn: cudaMemcpyAsync failed: %d", rc)
	}
	if syncRc := C.cudaStreamSynchronize(stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("CopyGPUFrameOn: stream sync failed: %d", syncRc)
	}
	return nil
}

// CopyNV12FromDevice copies NV12 data from an external CUDA device pointer
// (e.g., NVDEC output) into a pool GPUFrame. Handles pitch mismatch between
// source and destination via cudaMemcpy2DAsync.
//
// srcDevPtr points to the start of the NV12 data (Y plane).
// srcPitch is the source row pitch in bytes.
// width/height are the frame dimensions in pixels.
func CopyNV12FromDevice(dst *GPUFrame, srcDevPtr uintptr, srcPitch, width, height int) error {
	if dst == nil {
		return fmt.Errorf("CopyNV12FromDevice: nil destination frame")
	}
	if srcDevPtr == 0 {
		return fmt.Errorf("CopyNV12FromDevice: nil source device pointer")
	}

	// Y plane: width bytes per row, height rows.
	rc := C.cudaMemcpy2DAsync(
		unsafe.Pointer(uintptr(dst.DevPtr)),   // dst
		C.size_t(dst.Pitch),                    // dpitch
		unsafe.Pointer(srcDevPtr),              // src
		C.size_t(srcPitch),                     // spitch
		C.size_t(width),                        // width (bytes to copy per row)
		C.size_t(height),                       // height (number of rows)
		C.cudaMemcpyDeviceToDevice,
		defaultCUDAStream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("CopyNV12FromDevice: Y plane cudaMemcpy2DAsync failed: %d", rc)
	}

	// UV plane: width bytes per row, height/2 rows.
	// NV12 interleaved UV follows immediately after the Y plane in both src and dst.
	srcUV := srcDevPtr + uintptr(srcPitch*height)
	dstUV := uintptr(dst.DevPtr) + uintptr(dst.Pitch*dst.Height)

	rc = C.cudaMemcpy2DAsync(
		unsafe.Pointer(dstUV),                  // dst
		C.size_t(dst.Pitch),                    // dpitch
		unsafe.Pointer(srcUV),                  // src
		C.size_t(srcPitch),                     // spitch
		C.size_t(width),                        // width (bytes to copy per row)
		C.size_t(height/2),                     // height (number of rows)
		C.cudaMemcpyDeviceToDevice,
		defaultCUDAStream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("CopyNV12FromDevice: UV plane cudaMemcpy2DAsync failed: %d", rc)
	}

	// Synchronize — must complete before NVDEC reclaims the surface.
	if rc := C.cudaStreamSynchronize(defaultCUDAStream); rc != C.cudaSuccess {
		return fmt.Errorf("CopyNV12FromDevice: stream sync failed: %d", rc)
	}

	dst.Width = width
	dst.Height = height
	return nil
}

