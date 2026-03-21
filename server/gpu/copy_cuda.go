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

