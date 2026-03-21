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
	"sync"
	"unsafe"
)

const metalPitchAlignment = 256 // Match NVENC 256-byte alignment

// FramePool manages pre-allocated NV12 frames in Metal unified memory.
// Mirrors the CUDA FramePool API. Uses 256-byte row alignment.
type FramePool struct {
	ctx    *Context
	width  int
	height int
	pitch  int
	cap    int

	mu   sync.Mutex
	free []*GPUFrame

	hits   uint64
	misses uint64
}

// NewFramePool creates a pool of GPU NV12 frames.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}

	// Compute 256-byte aligned pitch
	pitch := (width + metalPitchAlignment - 1) &^ (metalPitchAlignment - 1)

	p := &FramePool{
		ctx:    ctx,
		width:  width,
		height: height,
		pitch:  pitch,
		cap:    initialSize,
	}

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

// allocFrame allocates a single NV12 frame in Metal unified memory.
func (p *FramePool) allocFrame() (*GPUFrame, error) {
	totalSize := p.pitch * p.height * 3 / 2 // NV12: Y + UV planes
	buf, err := p.ctx.mtl.allocBufferAligned(totalSize, metalPitchAlignment)
	if err != nil {
		return nil, err
	}

	contents := C.metal_buffer_contents(buf)
	frame := &GPUFrame{
		MetalBuf: buf,
		DevPtr:   uintptr(contents),
		Pitch:    p.pitch,
		Width:    p.width,
		Height:   p.height,
		pool:     p,
	}
	frame.refs.Store(1)

	// Zero the buffer
	C.memset(unsafe.Pointer(contents), 0, C.size_t(totalSize))

	return frame, nil
}

// Acquire returns a GPU frame from the pool.
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
func (p *FramePool) release(frame *GPUFrame) {
	p.mu.Lock()
	if len(p.free) < p.cap {
		p.free = append(p.free, frame)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	// Pool full — free buffer
	if frame.MetalBuf != nil {
		C.metal_buffer_free(frame.MetalBuf)
		frame.MetalBuf = nil
		frame.DevPtr = 0
	}
}

// Close frees all pooled GPU frames.
func (p *FramePool) Close() {
	p.mu.Lock()
	for _, frame := range p.free {
		if frame.MetalBuf != nil {
			C.metal_buffer_free(frame.MetalBuf)
			frame.MetalBuf = nil
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
