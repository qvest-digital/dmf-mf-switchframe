//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
	"unsafe"
)

// FramePool manages pre-allocated GPU frames in GPU VRAM.
// Mirrors switcher.FramePool but for GPU memory. Uses cudaMallocPitch
// for 256-byte row alignment (NVENC requirement). LIFO free list with mutex.
// The format field determines the pixel layout and buffer sizing:
//   - GPUFormatNV12: pitch * height * 3/2
//   - GPUFormatP210: pitch * height * 2
type FramePool struct {
	ctx    *Context
	width  int
	height int
	pitch  int            // row pitch from cudaMallocPitch (256-byte aligned)
	cap    int            // total capacity
	format GPUFrameFormat // pixel format (default: GPUFormatNV12)

	mu   sync.Mutex
	free []*GPUFrame
	all  []*GPUFrame // all pre-allocated frames (for IPC export)

	// Diagnostics
	hits   uint64
	misses uint64
}

// NewFramePool creates a pool of GPU NV12 frames.
// Uses cudaMallocPitch to determine the optimal pitch, then pre-allocates
// initialSize frames in VRAM.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}

	p := &FramePool{
		ctx:    ctx,
		width:  width,
		height: height,
		cap:    initialSize,
	}

	// Determine pitch by allocating one temporary frame
	pitch, err := p.determinePitch()
	if err != nil {
		return nil, fmt.Errorf("gpu: determine pitch: %w", err)
	}
	p.pitch = pitch

	// Pre-allocate initial frames
	p.free = make([]*GPUFrame, 0, initialSize)
	p.all = make([]*GPUFrame, 0, initialSize)
	for i := 0; i < initialSize; i++ {
		frame, err := p.allocFrame()
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu: pre-allocate frame %d: %w", i, err)
		}
		p.free = append(p.free, frame)
		p.all = append(p.all, frame)
	}

	return p, nil
}

// determinePitch allocates a temporary pitched buffer to discover the
// pitch that cudaMallocPitch returns for our dimensions, then frees it.
func (p *FramePool) determinePitch() (int, error) {
	var devPtr unsafe.Pointer
	var pitch C.size_t
	var totalHeight C.size_t
	var allocWidth C.size_t
	switch p.format {
	case GPUFormatP210:
		totalHeight = C.size_t(p.height * 2)
		allocWidth = C.size_t(p.width * 2)
	default:
		totalHeight = C.size_t(p.height * 3 / 2)
		allocWidth = C.size_t(p.width)
	}
	rc := C.cudaMallocPitch(
		&devPtr,
		&pitch,
		allocWidth,
		totalHeight,
	)
	if rc != C.cudaSuccess {
		return 0, fmt.Errorf("cudaMallocPitch probe failed: %d", rc)
	}
	C.cudaFree(devPtr)
	return int(pitch), nil
}

// allocFrame allocates a single frame on the GPU.
// Buffer height depends on pool format: NV12 = h*3/2, P210 = h*2.
func (p *FramePool) allocFrame() (*GPUFrame, error) {
	var devPtr unsafe.Pointer
	var pitch C.size_t
	var totalHeight C.size_t
	var allocWidth C.size_t
	switch p.format {
	case GPUFormatP210:
		totalHeight = C.size_t(p.height * 2) // Y + UV (full height for 4:2:2)
		allocWidth = C.size_t(p.width * 2)   // uint16 per sample
	default:
		totalHeight = C.size_t(p.height * 3 / 2)
		allocWidth = C.size_t(p.width)
	}
	rc := C.cudaMallocPitch(
		&devPtr,
		&pitch,
		allocWidth,
		totalHeight,
	)
	if rc != C.cudaSuccess {
		return nil, fmt.Errorf("cudaMallocPitch failed: %d", rc)
	}

	frame := &GPUFrame{
		DevPtr: C.CUdeviceptr(uintptr(devPtr)),
		Pitch:  int(pitch),
		Width:  p.width,
		Height: p.height,
		Format: p.format,
		pool:   p,
	}
	frame.refs.Store(1)
	return frame, nil
}

// Acquire returns a GPU frame from the pool. If the pool is exhausted,
// allocates a new frame (logged as a pool miss). The returned frame has
// refs=1; call Release() when done.
func (p *FramePool) Acquire() (*GPUFrame, error) {
	p.mu.Lock()
	if len(p.free) > 0 {
		frame := p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		p.hits++
		p.mu.Unlock()
		frame.refs.Store(1)
		frame.PTS = 0
		return frame, nil
	}
	p.misses++
	p.mu.Unlock()

	slog.Debug("gpu: frame pool miss, allocating new frame")
	return p.allocFrame()
}

// release returns a frame to the pool's free list.
// Called by GPUFrame.Release() when refcount reaches 0.
func (p *FramePool) release(frame *GPUFrame) {
	// Reset dimensions and format to pool defaults — callers may have
	// temporarily changed them (e.g., stinger overlay, IngestYUV).
	frame.Width = p.width
	frame.Height = p.height
	frame.Format = p.format

	p.mu.Lock()
	if len(p.free) < p.cap {
		p.free = append(p.free, frame)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	// Pool full — free GPU memory
	if frame.DevPtr != 0 {
		C.cudaFree(unsafe.Pointer(uintptr(frame.DevPtr)))
		frame.DevPtr = 0
	}
}

// Close frees all pooled GPU frames.
func (p *FramePool) Close() {
	p.mu.Lock()
	for _, frame := range p.free {
		if frame.DevPtr != 0 {
			C.cudaFree(unsafe.Pointer(uintptr(frame.DevPtr)))
			frame.DevPtr = 0
		}
	}
	p.free = p.free[:0]
	p.mu.Unlock()
}

// NewP210FramePool creates a pool of GPU P210 frames (10-bit 4:2:2 semi-planar).
func NewP210FramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}

	p := &FramePool{
		ctx:    ctx,
		width:  width,
		height: height,
		cap:    initialSize,
		format: GPUFormatP210,
	}

	// Determine pitch by allocating one temporary frame
	pitch, err := p.determinePitch()
	if err != nil {
		return nil, fmt.Errorf("gpu: determine P210 pitch: %w", err)
	}
	p.pitch = pitch

	// Pre-allocate initial frames
	p.free = make([]*GPUFrame, 0, initialSize)
	p.all = make([]*GPUFrame, 0, initialSize)
	for i := 0; i < initialSize; i++ {
		frame, err := p.allocFrame()
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu: pre-allocate P210 frame %d: %w", i, err)
		}
		p.free = append(p.free, frame)
		p.all = append(p.all, frame)
	}

	return p, nil
}

// Stats returns hit/miss counts for diagnostics.
func (p *FramePool) Stats() (hits, misses uint64) {
	p.mu.Lock()
	h, m := p.hits, p.misses
	p.mu.Unlock()
	return h, m
}

// Pitch returns the row pitch for frames in this pool.
func (p *FramePool) Pitch() int {
	return p.pitch
}

// Format returns the pixel format of frames in this pool.
func (p *FramePool) Format() GPUFrameFormat {
	return p.format
}

// AllDevPtrs returns the CUDA device pointers for all pre-allocated frames.
// Used to create a CUDAGPUPool that exports these pointers via IPC handles
// for zero-copy sharing with sidecar processes.
func (p *FramePool) AllDevPtrs() []uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	ptrs := make([]uintptr, len(p.all))
	for i, f := range p.all {
		ptrs[i] = uintptr(f.DevPtr)
	}
	return ptrs
}

// AllFrames returns the pre-allocated GPUFrame objects.
// Used by InferenceProxy to build a DevPtr→GPUFrame map for Ref/Release
// of frames while the sidecar processes them asynchronously.
func (p *FramePool) AllFrames() []*GPUFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*GPUFrame, len(p.all))
	copy(out, p.all)
	return out
}
