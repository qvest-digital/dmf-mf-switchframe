//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"log/slog"
	"sync/atomic"
	"time"
	"unsafe"
)

// Ensure time is used (referenced by GPUPipelineNode interface).
var _ = time.Duration(0)

// DeviceProperties holds GPU device information.
type DeviceProperties struct {
	Name                string
	ComputeCapability   [2]int // Not applicable for Metal; [0]=0,[1]=0
	TotalMemory         int64
	MultiprocessorCount int // Not applicable for Metal
	MaxThreadsPerBlock  int // Not applicable for Metal
}

// MemoryStats holds GPU memory usage information.
type MemoryStats struct {
	TotalMB int
	FreeMB  int
	UsedMB  int
}

// Context manages the shared Metal context for all GPU operations.
// Wraps metalContext to match the CUDA API surface.
type Context struct {
	mtl *metalContext
}

// NewContext initializes Metal and creates a shared context.
func NewContext() (*Context, error) {
	mtl, err := newMetalContext()
	if err != nil {
		return nil, err
	}
	return &Context{mtl: mtl}, nil
}

// Close releases all Metal resources.
func (c *Context) Close() error {
	if c == nil || c.mtl == nil {
		return nil
	}
	return c.mtl.Close()
}

// Backend returns the GPU backend name ("metal").
func (c *Context) Backend() string {
	if c == nil || c.mtl == nil {
		return ""
	}
	return c.mtl.Backend()
}

// DeviceName returns the GPU device name (e.g., "Apple M2 Max").
func (c *Context) DeviceName() string {
	if c == nil || c.mtl == nil {
		return ""
	}
	return c.mtl.DeviceName()
}

// DeviceProperties returns the GPU device properties.
func (c *Context) DeviceProperties() DeviceProperties {
	if c == nil || c.mtl == nil {
		return DeviceProperties{}
	}
	return DeviceProperties{
		Name:        c.mtl.DeviceName(),
		TotalMemory: c.mtl.DeviceMemory(),
	}
}

// Stream returns 0 (Metal uses command queues, not streams).
func (c *Context) Stream() uintptr { return 0 }

// EncStream returns 0 (Metal uses command queues, not streams).
func (c *Context) EncStream() uintptr { return 0 }

// MemoryStats returns current GPU memory usage.
// On Apple Silicon with unified memory, we report system memory.
func (c *Context) MemoryStats() MemoryStats {
	if c == nil || c.mtl == nil {
		return MemoryStats{}
	}
	totalBytes := c.mtl.DeviceMemory()
	totalMB := int(totalBytes) / (1024 * 1024)
	return MemoryStats{
		TotalMB: totalMB,
		FreeMB:  totalMB, // Unified memory — always "available"
		UsedMB:  0,
	}
}

// Sync synchronizes the GPU, blocking until all operations complete.
func (c *Context) Sync() error {
	if c == nil || c.mtl == nil {
		return nil
	}
	return c.mtl.Sync()
}

// SetPool associates a FramePool with this context.
func (c *Context) SetPool(pool *FramePool) {
	if c != nil && c.mtl != nil {
		c.mtl.SetPool(pool)
	}
}

// Pool returns the associated FramePool, if any.
func (c *Context) Pool() *FramePool {
	if c == nil || c.mtl == nil {
		return nil
	}
	return c.mtl.Pool()
}

// GPUFrame holds a single NV12 frame in Metal GPU memory.
// On Apple Silicon, the buffer is in unified memory — CPU and GPU
// can both access it directly with zero-copy.
type GPUFrame struct {
	MetalBuf C.MetalBufferRef // NV12 data in unified memory
	DevPtr   uintptr          // CPU-accessible pointer to the same memory
	Pitch    int              // row pitch in bytes (256-byte aligned)
	Width    int
	Height   int
	PTS      int64 // 90kHz MPEG-TS timestamp

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
		slog.Error("GPUFrame.Release: refcount underflow (double release)",
			"refs", new)
		return
	}
	if f.pool != nil {
		f.pool.release(f)
	} else if f.MetalBuf != nil {
		C.metal_buffer_free(f.MetalBuf)
		f.MetalBuf = nil
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

// contentsPtr returns a Go-accessible unsafe.Pointer to the Metal buffer contents.
func (f *GPUFrame) contentsPtr() unsafe.Pointer {
	return unsafe.Pointer(f.DevPtr)
}

// ScaleQuality selects the scaling algorithm.
type ScaleQuality int

const (
	ScaleQualityBilinear ScaleQuality = iota
	ScaleQualityLanczos
)

// WipeDirection matches transition.WipeDirection values.
type WipeDirection int

const (
	WipeHLeft     WipeDirection = 0
	WipeHRight    WipeDirection = 1
	WipeVTop      WipeDirection = 2
	WipeVBottom   WipeDirection = 3
	WipeBoxCenter WipeDirection = 4
	WipeBoxEdges  WipeDirection = 5
)

// ChromaKeyConfig holds parameters for GPU chroma keying.
type ChromaKeyConfig struct {
	KeyCb, KeyCr   uint8
	Similarity     float32
	Smoothness     float32
	SpillSuppress  float32
	SpillReplaceCb uint8
	SpillReplaceCr uint8
}

// Rect defines a rectangle for compositing.
type Rect struct {
	X, Y, W, H int
}

// YUVColor defines a color in YCbCr space.
type YUVColor struct {
	Y, Cb, Cr uint8
}

// ColorBlack is BT.709 limited-range black.
var ColorBlack = YUVColor{16, 128, 128}

// GPUOverlay holds an RGBA overlay uploaded to GPU memory.
type GPUOverlay struct {
	MetalBuf C.MetalBufferRef
	Width    int
	Height   int
	Pitch    int
}

// GPUSTMap holds an ST map uploaded to GPU memory.
type GPUSTMap struct {
	SBuf   C.MetalBufferRef
	TBuf   C.MetalBufferRef
	Width  int
	Height int
}

// GPUAnimatedSTMap holds a sequence of ST maps for animated warp effects.
type GPUAnimatedSTMap struct {
	frames []*GPUSTMap
	Width  int
	Height int
	FPS    int
	index  atomic.Int64
}

// RawSinkFunc is a callback for raw YUV420p frames.
type RawSinkFunc func(yuv []byte, width, height int)
