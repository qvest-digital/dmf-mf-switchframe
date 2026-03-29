//go:build cgo && darwin

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

// FramePool manages pre-allocated GPU frames in Metal unified memory.
// Mirrors the CUDA FramePool API. Uses 256-byte row alignment.
// The format field determines the pixel layout and buffer sizing:
//   - GPUFormatNV12: pitch * height * 3/2
//   - GPUFormatP210: pitch * height * 2
type FramePool struct {
	ctx    *Context
	width  int
	height int
	pitch  int
	cap    int
	format GPUFrameFormat // pixel format (default: GPUFormatNV12)

	mu   sync.Mutex
	free []*GPUFrame
	all  []*GPUFrame

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

// allocFrame allocates a single frame in Metal unified memory.
// Buffer size depends on pool format: NV12 = pitch*h*3/2, P210 = pitch*h*2.
func (p *FramePool) allocFrame() (*GPUFrame, error) {
	var totalSize int
	switch p.format {
	case GPUFormatP210:
		totalSize = p.pitch * p.height * 2 // Y + UV (full height for 4:2:2)
	default:
		totalSize = p.pitch * p.height * 3 / 2 // NV12: Y + UV (half height for 4:2:0)
	}
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
		Format:   p.format,
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
// Resets Width/Height to pool defaults — callers may have temporarily
// changed them (e.g., stinger overlay at non-pipeline resolution,
// IngestYUV at source resolution). Without this reset, recycled frames
// carry stale dimensions that corrupt subsequent operations.
func (p *FramePool) release(frame *GPUFrame) {
	p.mu.Lock()
	frame.Width = p.width
	frame.Height = p.height
	frame.Format = p.format
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

// NewP210FramePool creates a pool of GPU P210 frames (10-bit 4:2:2 semi-planar).
// P210 pitch is computed as (width*2) rounded up to 256-byte alignment, and
// the total buffer size is pitch * height * 2 (Y + UV at full height).
func NewP210FramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}

	// P210: each sample is uint16 (2 bytes), so row width in bytes = width * 2.
	// Round up to 256-byte alignment.
	rawPitch := width * 2
	pitch := (rawPitch + metalPitchAlignment - 1) &^ (metalPitchAlignment - 1)

	p := &FramePool{
		ctx:    ctx,
		width:  width,
		height: height,
		pitch:  pitch,
		cap:    initialSize,
		format: GPUFormatP210,
	}

	p.free = make([]*GPUFrame, 0, initialSize)
	for i := 0; i < initialSize; i++ {
		frame, err := p.allocFrame()
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("gpu: pre-allocate P210 frame %d: %w", i, err)
		}
		p.free = append(p.free, frame)
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

// AllDevPtrs returns CPU-accessible pointers for all pre-allocated frames.
// On Metal, these are unified memory pointers (not CUDA IPC handles).
func (p *FramePool) AllDevPtrs() []uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	ptrs := make([]uintptr, len(p.all))
	for i, f := range p.all {
		ptrs[i] = f.DevPtr
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
