//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
)

// CopyGPUFrame copies NV12 data from src to dst using unified memory memcpy.
// Both frames must have the same dimensions and pitch. No-ops with an error
// log if dimensions mismatch.
//
// On Apple Silicon, contentsPtr() returns a CPU-accessible pointer to the
// Metal buffer's unified memory, so a simple memcpy suffices.
func CopyGPUFrame(dst, src *GPUFrame) {
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		slog.Error("CopyGPUFrame: dimension mismatch",
			"dst", fmt.Sprintf("%dx%d p=%d", dst.Width, dst.Height, dst.Pitch),
			"src", fmt.Sprintf("%dx%d p=%d", src.Width, src.Height, src.Pitch))
		return
	}
	size := C.size_t(src.Pitch * src.Height * 3 / 2)
	C.memcpy(dst.contentsPtr(), src.contentsPtr(), size)
}

// LockGPUOp / UnlockGPUOp are no-ops on Metal.
// Metal command queues handle serialization internally.
func LockGPUOp()   {}
func UnlockGPUOp() {}
