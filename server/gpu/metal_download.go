//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Download transfers a GPU NV12 frame to a CPU YUV420p buffer.
//
// On Apple Silicon with unified memory, this dispatches the NV12->YUV420p
// conversion kernel, then reads the result directly from the staging
// buffers (zero-copy -- GPU and CPU see the same memory).
//
// Staging buffers are cached per-dimension to avoid per-call allocation.
// The lock is only held during staging buffer access, not the kernel dispatch.
func Download(ctx *Context, yuv []byte, frame *GPUFrame, width, height int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv) < expectedSize {
		return fmt.Errorf("gpu: download: YUV buffer too small: %d < %d", len(yuv), expectedSize)
	}

	mtl := ctx.mtl

	// Get or create cached staging buffers (lock held briefly)
	staging, err := mtl.getOrCreateStagingBuffers(width, height)
	if err != nil {
		return fmt.Errorf("gpu: download: %w", err)
	}

	// Launch conversion kernel: NV12 -> YUV420p (no lock — Metal command queues are thread-safe)
	pipeline, err := mtl.getPipeline("nv12_to_yuv420p")
	if err != nil {
		return fmt.Errorf("gpu: download: %w", err)
	}

	params := C.MetalConvertParams{
		width:     C.uint32_t(width),
		height:    C.uint32_t(height),
		nv12Pitch: C.uint32_t(frame.Pitch),
		srcStride: C.uint32_t(width),
	}

	rc := C.metal_nv12_to_yuv420p(mtl.queue, pipeline, staging.yBuf, staging.cbBuf, staging.crBuf, frame.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: download: nv12_to_yuv420p kernel failed: %d", rc)
	}

	// Copy planar data from Metal buffers to Go byte slice (zero-copy read)
	C.memcpy(unsafe.Pointer(&yuv[0]), C.metal_buffer_contents(staging.yBuf), C.size_t(ySize))
	C.memcpy(unsafe.Pointer(&yuv[ySize]), C.metal_buffer_contents(staging.cbBuf), C.size_t(cbSize))
	C.memcpy(unsafe.Pointer(&yuv[ySize+cbSize]), C.metal_buffer_contents(staging.crBuf), C.size_t(crSize))

	return nil
}
