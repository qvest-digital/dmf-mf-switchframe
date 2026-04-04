//go:build cgo && darwin

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

// --- gpuEncodeNode: async H.264 encode via VideoToolbox ---
//
// VideoToolbox encodes asynchronously via GCD, but the semaphore-based
// collection in metal_vt_encode blocks the caller until the callback fires.
// On Apple Silicon, this blocking wait has high variance (7ms–238ms observed)
// due to GCD scheduling, causing pipeline stalls and output FPS drops.
//
// To decouple the pipeline from VT's variable latency, ProcessGPU refs the
// frame and sends it to a background goroutine via a buffered channel. The
// goroutine calls EncodeGPU (blocking) and delivers results via onEncoded.
// The pipeline returns immediately, allowing the next source frame to be
// processed while VT finishes the encode.
//
// Channel capacity 2 provides ~80ms of buffering at 24fps, absorbing p95
// encode spikes without frame drops. If VT falls behind (channel full),
// ProcessGPU blocks — no worse than the previous synchronous behavior.

type gpuEncodeNode struct {
	ctx       *Context
	encoder   *GPUEncoder
	forceIDR  *atomic.Bool
	onEncoded func(data []byte, isIDR bool, pts int64)
	lastErr   atomic.Value

	// Async encode channel + goroutine
	frameCh       chan vtEncodeReq
	done          chan struct{}
	framesIn      atomic.Int64
	framesDropped atomic.Int64
}

type vtEncodeReq struct {
	frame    *GPUFrame
	forceIDR bool
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

func (n *gpuEncodeNode) Name() string { return "gpu_encode" }

func (n *gpuEncodeNode) Configure(width, height, pitch int) error {
	n.stopLoop()
	n.frameCh = make(chan vtEncodeReq, 2)
	n.done = make(chan struct{})
	go n.encodeLoop()
	return nil
}

func (n *gpuEncodeNode) Active() bool           { return n.encoder != nil }
func (n *gpuEncodeNode) Latency() time.Duration { return 2 * time.Millisecond }

func (n *gpuEncodeNode) Close() error {
	n.stopLoop()
	return nil
}

func (n *gpuEncodeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// ProcessGPU refs the frame and dispatches it to the async encode goroutine.
// Non-blocking: if the encode goroutine is backed up (channel full), the oldest
// queued frame is dropped (newest-wins policy). This prevents VideoToolbox
// stalls from back-pressuring the entire GPU pipeline.
func (n *gpuEncodeNode) ProcessGPU(frame *GPUFrame) error {
	if n.encoder == nil || n.frameCh == nil {
		return nil
	}

	n.framesIn.Add(1)

	idr := false
	if n.forceIDR != nil {
		idr = n.forceIDR.Swap(false)
	}

	frame.Ref()

	// Newest-wins: drop oldest queued frame if channel is full.
	select {
	case old := <-n.frameCh:
		// Preserve IDR request if the dropped frame had one.
		if old.forceIDR {
			idr = true
		}
		old.frame.Release()
		n.framesDropped.Add(1)
	default:
	}
	n.frameCh <- vtEncodeReq{frame: frame, forceIDR: idr}
	return nil
}

// encodeLoop runs in a background goroutine, encoding frames as they arrive.
// Frames are processed in FIFO order — PTS ordering is preserved.
func (n *gpuEncodeNode) encodeLoop() {
	defer close(n.done)
	for req := range n.frameCh {
		pts := req.frame.PTS
		data, isIDR, err := n.encoder.EncodeGPU(req.frame, req.forceIDR)
		req.frame.Release()

		if err != nil {
			n.lastErr.Store(err)
			slog.Warn("gpu_encode: VT encode error", "err", err)
			continue
		}
		if len(data) > 0 && n.onEncoded != nil {
			n.onEncoded(data, isIDR, pts)
		}
	}
}

// stopLoop shuts down the async encode goroutine if running.
// Blocks until all queued frames are encoded and delivered.
func (n *gpuEncodeNode) stopLoop() {
	if n.frameCh != nil {
		close(n.frameCh)
		<-n.done
		n.frameCh = nil
		n.done = nil
	}
}

// --- gpuRawSinkNode: download to CPU only when sinks are active ---

type gpuRawSinkNode struct {
	ctx    *Context
	sink   *atomic.Pointer[RawSinkFunc]
	cpuBuf [2][]byte // double-buffer to prevent aliasing if callback retains slice
	bufIdx int
	width  int
	height int
	format GPUFrameFormat // tracked for lazy buffer resize on format change
}

// NewGPURawSinkNode creates a node that downloads GPU frames to CPU.
func NewGPURawSinkNode(ctx *Context, sink *atomic.Pointer[RawSinkFunc]) GPUPipelineNode {
	return &gpuRawSinkNode{ctx: ctx, sink: sink}
}

func (n *gpuRawSinkNode) Name() string { return "gpu_raw_sink" }
func (n *gpuRawSinkNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	// Default to NV12 buffer size; will be resized on first ProcessGPU
	// if the frame format differs.
	size := video.YUV420FrameSize(width, height)
	n.cpuBuf[0] = make([]byte, size)
	n.cpuBuf[1] = make([]byte, size)
	return nil
}

// configureForFormat resizes CPU buffers based on the frame format.
// Called lazily when the frame format changes from the default NV12.
func (n *gpuRawSinkNode) configureForFormat(width, height int, format GPUFrameFormat) {
	size := cpuFrameSize(width, height, format)
	if len(n.cpuBuf[0]) != size {
		n.cpuBuf[0] = make([]byte, size)
		n.cpuBuf[1] = make([]byte, size)
	}
	n.format = format
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
	// Lazily resize CPU buffers when frame format changes (e.g., NV12→P210).
	if frame.Format != n.format {
		n.configureForFormat(n.width, n.height, frame.Format)
	}
	buf := n.cpuBuf[n.bufIdx]
	n.bufIdx ^= 1 // flip 0↔1
	if err := GPUDownloadDispatch(n.ctx, frame, buf, n.width, n.height); err != nil {
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
