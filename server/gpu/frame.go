//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"log/slog"
	"sync/atomic"
	"unsafe"
)

// GPUFrame holds a single NV12 frame in GPU VRAM.
// The memory layout is: Y plane (pitch * height) followed by UV plane (pitch * height/2).
// Pitch is 256-byte aligned for NVENC compatibility.
type GPUFrame struct {
	DevPtr C.CUdeviceptr // NV12 data in VRAM
	Pitch  int           // row pitch in bytes (256-byte aligned)
	Width  int
	Height int
	PTS    int64 // 90kHz MPEG-TS timestamp

	refs atomic.Int32 // reference counting
	pool *FramePool   // return-to-pool on last release
}

// Ref increments the reference count.
func (f *GPUFrame) Ref() {
	f.refs.Add(1)
}

// Release decrements the reference count. When it reaches 0, the frame
// is returned to the pool (if pooled) or the GPU memory is freed.
func (f *GPUFrame) Release() {
	if f == nil {
		return
	}
	new := f.refs.Add(-1)
	if new > 0 {
		return
	}
	if new < 0 {
		// Log and leak rather than double-freeing GPU memory. A leaked
		// buffer is invisible to the viewer; a double-free corrupts VRAM.
		slog.Error("GPUFrame.Release: refcount underflow (double release)",
			"refs", new)
		return
	}
	if f.pool != nil {
		f.pool.release(f)
	} else if f.DevPtr != 0 {
		C.cudaFree(unsafe.Pointer(uintptr(f.DevPtr)))
		f.DevPtr = 0
	}
}

// YPlaneOffset returns the byte offset of the Y plane (always 0).
func (f *GPUFrame) YPlaneOffset() int {
	return 0
}

// UVPlaneOffset returns the byte offset of the UV plane.
func (f *GPUFrame) UVPlaneOffset() int {
	return f.Pitch * f.Height
}

// NV12Size returns the total byte size of the NV12 frame.
func (f *GPUFrame) NV12Size() int {
	return f.Pitch * f.Height * 3 / 2
}
