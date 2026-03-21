//go:build darwin

package gpu

import (
	"sync/atomic"
	"time"
)

// --- gpuEncodeNode: H.264 encode via VideoToolbox ---

type gpuEncodeNode struct {
	ctx       *Context
	encoder   *GPUEncoder
	forceIDR  *atomic.Bool
	onEncoded func(data []byte, isIDR bool, pts int64)
	lastErr   atomic.Value
}

// NewGPUEncodeNode creates an encode pipeline node.
func NewGPUEncodeNode(ctx *Context, encoder *GPUEncoder, forceIDR *atomic.Bool, onEncoded func([]byte, bool, int64)) GPUPipelineNode {
	return &gpuEncodeNode{
		ctx:       ctx,
		encoder:   encoder,
		forceIDR:  forceIDR,
		onEncoded: onEncoded,
	}
}

func (n *gpuEncodeNode) Name() string                             { return "gpu_encode" }
func (n *gpuEncodeNode) Configure(width, height, pitch int) error { return nil }
func (n *gpuEncodeNode) Active() bool                             { return n.encoder != nil }
func (n *gpuEncodeNode) Latency() time.Duration                   { return 2 * time.Millisecond }
func (n *gpuEncodeNode) Close() error                             { return nil }

func (n *gpuEncodeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuEncodeNode) ProcessGPU(frame *GPUFrame) error {
	idr := false
	if n.forceIDR != nil {
		idr = n.forceIDR.Swap(false)
	}
	data, isIDR, err := n.encoder.EncodeGPU(frame, idr)
	if err != nil {
		n.lastErr.Store(err)
		return err
	}
	if len(data) > 0 && n.onEncoded != nil {
		n.onEncoded(data, isIDR, frame.PTS)
	}
	return nil
}

// --- gpuRawSinkNode: download to CPU only when sinks are active ---

type gpuRawSinkNode struct {
	ctx    *Context
	sink   *atomic.Pointer[RawSinkFunc]
	cpuBuf []byte
	width  int
	height int
}

// NewGPURawSinkNode creates a node that downloads GPU frames to CPU.
func NewGPURawSinkNode(ctx *Context, sink *atomic.Pointer[RawSinkFunc]) GPUPipelineNode {
	return &gpuRawSinkNode{ctx: ctx, sink: sink}
}

func (n *gpuRawSinkNode) Name() string { return "gpu_raw_sink" }
func (n *gpuRawSinkNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	n.cpuBuf = make([]byte, width*height*3/2)
	return nil
}
func (n *gpuRawSinkNode) Active() bool {
	return n.sink != nil && n.sink.Load() != nil
}
func (n *gpuRawSinkNode) Latency() time.Duration { return 500 * time.Microsecond }
func (n *gpuRawSinkNode) Err() error             { return nil }
func (n *gpuRawSinkNode) Close() error           { return nil }

func (n *gpuRawSinkNode) ProcessGPU(frame *GPUFrame) error {
	fn := n.sink.Load()
	if fn == nil {
		return nil
	}
	if err := Download(n.ctx, n.cpuBuf, frame, n.width, n.height); err != nil {
		return err
	}
	(*fn)(n.cpuBuf, n.width, n.height)
	return nil
}

// --- gpuPassthroughNode: placeholder for nodes not yet GPU-accelerated ---

type gpuPassthroughNode struct {
	name   string
	active bool
}

// NewGPUPassthroughNode creates a no-op node.
func NewGPUPassthroughNode(name string, active bool) GPUPipelineNode {
	return &gpuPassthroughNode{name: name, active: active}
}

func (n *gpuPassthroughNode) Name() string                             { return n.name }
func (n *gpuPassthroughNode) Configure(width, height, pitch int) error { return nil }
func (n *gpuPassthroughNode) Active() bool                             { return n.active }
func (n *gpuPassthroughNode) ProcessGPU(frame *GPUFrame) error         { return nil }
func (n *gpuPassthroughNode) Err() error                               { return nil }
func (n *gpuPassthroughNode) Latency() time.Duration                   { return 0 }
func (n *gpuPassthroughNode) Close() error                             { return nil }
