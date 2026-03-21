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

// FramePool manages pre-allocated NV12 frames in GPU VRAM.
// Mirrors switcher.FramePool but for GPU memory. Uses cudaMallocPitch
// for 256-byte row alignment (NVENC requirement). LIFO free list with mutex.
type FramePool struct {
	ctx    *Context
	width  int
	height int
	pitch  int // row pitch from cudaMallocPitch (256-byte aligned)
	cap    int // total capacity

	mu   sync.Mutex
	free []*GPUFrame

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
	for i := 0; i < initialSize; i++ {
		frame, err := p.allocFrame()
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu: pre-allocate frame %d: %w", i, err)
		}
		p.free = append(p.free, frame)
	}

	return p, nil
}

// determinePitch allocates a temporary pitched buffer to discover the
// pitch that cudaMallocPitch returns for our dimensions, then frees it.
func (p *FramePool) determinePitch() (int, error) {
	var devPtr unsafe.Pointer
	var pitch C.size_t
	// NV12 total height: height + height/2 (Y + UV planes)
	totalHeight := C.size_t(p.height * 3 / 2)
	rc := C.cudaMallocPitch(
		&devPtr,
		&pitch,
		C.size_t(p.width),
		totalHeight,
	)
	if rc != C.cudaSuccess {
		return 0, fmt.Errorf("cudaMallocPitch probe failed: %d", rc)
	}
	C.cudaFree(devPtr)
	return int(pitch), nil
}

// allocFrame allocates a single NV12 frame on the GPU.
func (p *FramePool) allocFrame() (*GPUFrame, error) {
	var devPtr unsafe.Pointer
	var pitch C.size_t
	totalHeight := C.size_t(p.height * 3 / 2)
	rc := C.cudaMallocPitch(
		&devPtr,
		&pitch,
		C.size_t(p.width),
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
