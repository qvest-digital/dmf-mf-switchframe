//go:build cgo && darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"errors"
	"fmt"
)

// CopyGPUFrame copies frame data from src to dst using unified memory memcpy.
// On Apple Silicon, contentsPtr() returns a CPU-accessible pointer to the
// Metal buffer's unified memory, so a simple memcpy suffices.
// The copy size is format-aware: NV12 = pitch*h*3/2, P210 = pitch*h*2.
func CopyGPUFrame(dst, src *GPUFrame) error {
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		return fmt.Errorf("CopyGPUFrame: dimension mismatch: dst=%dx%d p=%d src=%dx%d p=%d",
			dst.Width, dst.Height, dst.Pitch, src.Width, src.Height, src.Pitch)
	}
	size := C.size_t(src.FrameSize())
	C.memcpy(dst.contentsPtr(), src.contentsPtr(), size)
	return nil
}

// CopyNV12FromDevice is a no-op on darwin (Apple Silicon uses Metal, not CUDA).
// NVDEC zero-copy decode is only available on Linux with NVIDIA GPUs.
func CopyNV12FromDevice(_ *GPUFrame, _, _ uintptr, _, _, _ int) error {
	return errors.New("CopyNV12FromDevice: not supported on darwin (CUDA-only)")
}

// CopyP016ToNV12FromDevice is a no-op on darwin (NVDEC 10-bit decode is CUDA-only).
func CopyP016ToNV12FromDevice(_ *GPUFrame, _, _ uintptr, _, _, _ int) error {
	return errors.New("CopyP016ToNV12FromDevice: not supported on darwin (CUDA-only)")
}

// CopyGPUFrameOn copies frame data from src to dst. On Apple Silicon, unified
// memory means a simple memcpy suffices — the work queue is irrelevant.
// The copy size is format-aware: NV12 = pitch*h*3/2, P210 = pitch*h*2.
func CopyGPUFrameOn(dst, src *GPUFrame, q *GPUWorkQueue) error {
	if dst == nil || src == nil {
		return fmt.Errorf("CopyGPUFrameOn: nil frame")
	}
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		return fmt.Errorf("CopyGPUFrameOn: dimension mismatch: dst=%dx%d p=%d src=%dx%d p=%d",
			dst.Width, dst.Height, dst.Pitch, src.Width, src.Height, src.Pitch)
	}
	size := C.size_t(src.FrameSize())
	C.memcpy(dst.contentsPtr(), src.contentsPtr(), size)
	return nil
}
