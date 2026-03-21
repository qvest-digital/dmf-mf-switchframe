//go:build cgo && cuda

package gpu

import (
	"sync/atomic"
	"time"
)

// --- gpuEncodeNode: H.264 encode via NVENC ---

type gpuEncodeNode struct {
	ctx       *Context
	encoder   *GPUEncoder
	forceIDR  *atomic.Bool
	onEncoded func(data []byte, isIDR bool, pts int64) // callback with encoded H.264
	lastErr   atomic.Value
}

// NewGPUEncodeNode creates an encode pipeline node.
// onEncoded is called with the encoded H.264 bitstream for each frame.
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
func (n *gpuEncodeNode) Close() error                             { return nil } // encoder lifecycle managed externally

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

// RawSinkFunc is a callback that receives decoded YUV420p on the CPU.
type RawSinkFunc func(yuv []byte, width, height int)

type gpuRawSinkNode struct {
	ctx    *Context
	sink   *atomic.Pointer[RawSinkFunc]
	cpuBuf [2][]byte // double-buffer to prevent aliasing if callback retains slice
	bufIdx int
	width  int
	height int
}

// NewGPURawSinkNode creates a node that downloads GPU frames to CPU
// only when a raw sink callback is registered.
func NewGPURawSinkNode(ctx *Context, sink *atomic.Pointer[RawSinkFunc]) GPUPipelineNode {
	return &gpuRawSinkNode{ctx: ctx, sink: sink}
}

func (n *gpuRawSinkNode) Name() string { return "gpu_raw_sink" }
func (n *gpuRawSinkNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	size := width * height * 3 / 2
	n.cpuBuf[0] = make([]byte, size)
	n.cpuBuf[1] = make([]byte, size)
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
		return nil // no sink — skip download entirely (zero cost)
	}
	buf := n.cpuBuf[n.bufIdx]
	n.bufIdx ^= 1 // flip 0↔1
	if err := Download(n.ctx, buf, frame, n.width, n.height); err != nil {
		return err
	}
	(*fn)(buf, n.width, n.height)
	return nil
}

// --- gpuPassthroughNode: placeholder for nodes not yet GPU-accelerated ---

type gpuPassthroughNode struct {
	name   string
	active bool
}

// NewGPUPassthroughNode creates a no-op node for pipeline stages that
// don't yet have GPU implementations (keying, layout, DSK, stmap).
// These are wired as placeholders so the pipeline structure is complete.
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
