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

// GPUFrame holds a single frame in GPU VRAM.
// The memory layout depends on Format:
//   - NV12: Y plane (pitch * height) + UV plane (pitch * height/2)
//   - P210: Y plane (pitch * height) + UV plane (pitch * height)
//
// Pitch is 256-byte aligned for NVENC compatibility.
type GPUFrame struct {
	DevPtr C.CUdeviceptr // pixel data in VRAM
	Pitch  int           // row pitch in bytes (256-byte aligned)
	Width  int
	Height int
	PTS    int64          // 90kHz MPEG-TS timestamp
	Format GPUFrameFormat // pixel format (default: GPUFormatNV12)

	refs atomic.Int32 // reference counting
	pool *FramePool   // return-to-pool on last release

	// releaseFn, when non-nil, is invoked exactly once when the refcount
	// reaches zero, instead of returning the frame to a pool or cudaFree'ing
	// DevPtr. Used for externally managed memory such as CUDA IPC imports,
	// which must be unmapped with cuIpcCloseMemHandle (not cudaFree) and only
	// after the last reader has released its reference.
	releaseFn func()
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
	if f.releaseFn != nil {
		fn := f.releaseFn
		f.releaseFn = nil
		f.DevPtr = 0
		fn()
	} else if f.pool != nil {
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

// NV12Size returns the total byte size assuming NV12 layout.
// Deprecated: Use FrameSize() which is format-aware.
func (f *GPUFrame) NV12Size() int {
	return f.Pitch * f.Height * 3 / 2
}

// FrameSize returns the total byte size of the frame based on its format.
// NV12: pitch * height * 3/2 (half-height UV plane).
// P210: pitch * height * 2 (full-height UV plane).
func (f *GPUFrame) FrameSize() int {
	switch f.Format {
	case GPUFormatP210:
		return f.Pitch * f.Height * 2
	default: // GPUFormatNV12
		return f.Pitch * f.Height * 3 / 2
	}
}

// UVPlaneHeight returns the height of the UV/chroma plane.
// NV12 (4:2:0): height/2. P210 (4:2:2): height.
func (f *GPUFrame) UVPlaneHeight() int {
	switch f.Format {
	case GPUFormatP210:
		return f.Height
	default: // GPUFormatNV12
		return f.Height / 2
	}
}

// NewGPUFrameFromDevPtr creates a GPUFrame wrapping an external device pointer.
// Used by the sidecar binary to wrap IPC-imported frame pointers without
// needing to cast through the gpu package's cgo CUdeviceptr type.
func NewGPUFrameFromDevPtr(devPtr uintptr, width, height, pitch int, pts int64, format GPUFrameFormat) *GPUFrame {
	f := &GPUFrame{
		DevPtr: C.CUdeviceptr(devPtr),
		Width:  width,
		Height: height,
		Pitch:  pitch,
		PTS:    pts,
		Format: format,
	}
	f.refs.Store(1)
	return f
}
