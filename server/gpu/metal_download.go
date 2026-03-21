//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Download transfers a GPU NV12 frame to a CPU YUV420p buffer.
//
// On Apple Silicon with unified memory, this reads NV12 data directly from
// the frame's Metal buffer via CPU and deinterleaves UV to planar Cb/Cr.
// Each GPUFrame has its own MTLBuffer (StorageModeShared), so concurrent
// downloads from different frames are inherently race-free — no staging
// buffers needed.
//
// The NV12→YUV420p deinterleave is done in C where Clang auto-vectorizes
// the inner loop with ARM64 NEON vld2 instructions.
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

	// Apple Silicon unified memory: read NV12 directly from the frame's
	// Metal buffer via CPU and deinterleave to planar YUV420p.
	// No staging buffers, no GPU kernel needed.
	C.metal_nv12_to_yuv420p_cpu(
		frame.contentsPtr(),
		C.int(frame.Pitch),
		unsafe.Pointer(&yuv[0]),
		unsafe.Pointer(&yuv[ySize]),
		unsafe.Pointer(&yuv[ySize+cbSize]),
		C.int(width),
		C.int(height),
	)

	return nil
}
