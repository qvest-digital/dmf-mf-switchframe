//go:build (!cgo || !cuda) && !darwin

package gpu

import (
	"sync/atomic"
	"time"
)

// GPUPipelineNode is the interface for GPU pipeline processing nodes.
type GPUPipelineNode interface {
	Name() string
	Configure(width, height, pitch int) error
	Active() bool
	ProcessGPU(frame *GPUFrame) error
	Err() error
	Latency() time.Duration
	Close() error
}

// GPUPipeline is a stub for non-GPU builds.
type GPUPipeline struct{}

// NewGPUPipeline returns a stub on non-GPU builds.
func NewGPUPipeline(ctx *Context, pool *FramePool) *GPUPipeline { return &GPUPipeline{} }

// Pool returns nil on non-GPU builds.
func (p *GPUPipeline) Pool() *FramePool { return nil }

// Build returns ErrGPUNotAvailable on non-GPU builds.
func (p *GPUPipeline) Build(width, height, pitch int, nodes []GPUPipelineNode) error {
	return ErrGPUNotAvailable
}

// Run returns ErrGPUNotAvailable on non-GPU builds.
func (p *GPUPipeline) Run(frame *GPUFrame) error { return ErrGPUNotAvailable }

// RunWithUpload returns ErrGPUNotAvailable on non-GPU builds.
func (p *GPUPipeline) RunWithUpload(yuv []byte, width, height int, pts int64) (*GPUFrame, error) {
	return nil, ErrGPUNotAvailable
}

// Snapshot returns empty stats on non-GPU builds.
func (p *GPUPipeline) Snapshot() map[string]any { return map[string]any{"gpu": false} }

// Wait is a no-op on non-GPU builds.
func (p *GPUPipeline) Wait() {}

// Close is a no-op on non-GPU builds.
func (p *GPUPipeline) Close() error { return nil }

// RawSinkFunc is a callback for raw YUV420p frames.
type RawSinkFunc func(yuv []byte, width, height int)

// NewGPURawSinkNode returns nil on non-GPU builds.
func NewGPURawSinkNode(ctx *Context, sink *atomic.Pointer[RawSinkFunc]) GPUPipelineNode {
	return nil
}

// NewGPUEncodeNode returns nil on non-GPU builds.
func NewGPUEncodeNode(ctx *Context, encoder *GPUEncoder, forceIDR *atomic.Bool, onEncoded func([]byte, bool, int64)) GPUPipelineNode {
	return nil
}

// NewGPUPassthroughNode returns nil on non-GPU builds.
func NewGPUPassthroughNode(name string, active bool) GPUPipelineNode { return nil }
