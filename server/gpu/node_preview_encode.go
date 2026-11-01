//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuPreviewEncodeNode scales and hardware-encodes program frames to a
// lower resolution for browser multiview — entirely on the GPU. This
// eliminates the CPU download + software encode round-trip that previously
// consumed ~44% CPU.
//
// Encoding runs asynchronously in a background goroutine to avoid blocking
// the pipeline. ProcessGPU refs the frame and sends it to a channel (newest-
// wins, capacity 1). The background goroutine scales + encodes + broadcasts.
// This mirrors the per-source previewWorker pattern.
type gpuPreviewEncodeNode struct {
	ctx       *Context
	enc       *PreviewEncoder
	onEncoded func(data []byte, isIDR bool, pts int64)

	previewW int
	previewH int
	bitrate  int
	fpsNum   int
	fpsDen   int

	forceIDR atomic.Bool
	active   atomic.Bool
	lastErr  atomic.Value

	// Async encode channel + goroutine
	frameCh chan *GPUFrame
	done    chan struct{}

	// Diagnostic counters (atomic, lock-free).
	framesIn      atomic.Int64 // frames received by ProcessGPU
	framesDropped atomic.Int64 // frames dropped (newest-wins channel full)
	framesEncoded atomic.Int64 // frames successfully encoded + broadcast
	encodeErrors  atomic.Int64 // encode failures
	lastEncodeNs  atomic.Int64 // last encode duration (scale + NVENC)
	maxEncodeNs   atomic.Int64 // max encode duration seen
}

// NewGPUPreviewEncodeNode creates a GPU preview encode pipeline node.
// Returns nil if ctx or onEncoded is nil (caller checks for nil nodes).
func NewGPUPreviewEncodeNode(ctx *Context, previewW, previewH, bitrate, fpsNum, fpsDen int,
	onEncoded func(data []byte, isIDR bool, pts int64)) GPUPipelineNode {
	if ctx == nil || onEncoded == nil {
		return nil
	}
	return &gpuPreviewEncodeNode{
		ctx:       ctx,
		onEncoded: onEncoded,
		previewW:  previewW,
		previewH:  previewH,
		bitrate:   bitrate,
		fpsNum:    fpsNum,
		fpsDen:    fpsDen,
	}
}

func (n *gpuPreviewEncodeNode) Name() string { return "gpu_preview_encode" }

// Configure creates the PreviewEncoder now that the pipeline's source
// dimensions are known. Called once when the pipeline is built.
func (n *gpuPreviewEncodeNode) Configure(width, height, pitch int) error {
	// Close any previous encoder (pipeline reconfiguration).
	n.stopLoop()
	if n.enc != nil {
		n.enc.Close()
		n.enc = nil
		n.active.Store(false)
	}

	enc, err := NewPreviewEncoder(n.ctx, width, height, n.previewW, n.previewH,
		n.bitrate, n.fpsNum, n.fpsDen)
	if err != nil {
		slog.Error("gpu_preview_encode: failed to create encoder",
			"src", [2]int{width, height},
			"dst", [2]int{n.previewW, n.previewH},
			"error", err)
		n.lastErr.Store(err)
		return err
	}

	n.enc = enc
	n.active.Store(true)

	// Start async encode goroutine.
	n.frameCh = make(chan *GPUFrame, 1)
	n.done = make(chan struct{})
	go n.encodeLoop()

	slog.Info("gpu_preview_encode: configured",
		"src", [2]int{width, height},
		"dst", [2]int{n.previewW, n.previewH},
		"bitrate", n.bitrate)
	return nil
}

// Active returns true only after Configure succeeds and creates the encoder.
func (n *gpuPreviewEncodeNode) Active() bool {
	return n.active.Load()
}

func (n *gpuPreviewEncodeNode) Latency() time.Duration { return time.Millisecond }

func (n *gpuPreviewEncodeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// ProcessGPU refs the frame and dispatches it to the async encode goroutine.
// Non-blocking: if the encode goroutine is busy, the old frame is dropped
// (newest-wins policy, same as per-source previewWorker).
func (n *gpuPreviewEncodeNode) ProcessGPU(frame *GPUFrame) error {
	if n.enc == nil || n.frameCh == nil {
		return nil
	}

	n.framesIn.Add(1)

	// Ref the frame so it's not recycled before the encode goroutine reads it.
	frame.Ref()

	// Newest-wins: drop old frame if channel is full.
	select {
	case old := <-n.frameCh:
		old.Release()
		n.framesDropped.Add(1)
	default:
	}
	n.frameCh <- frame

	return nil
}

// encodeLoop runs in a background goroutine, encoding frames as they arrive.
func (n *gpuPreviewEncodeNode) encodeLoop() {
	defer close(n.done)
	for frame := range n.frameCh {
		forceIDR := n.forceIDR.Swap(false)
		pts := frame.PTS // capture before Release

		t0 := time.Now()
		data, isIDR, err := n.enc.Encode(frame, forceIDR)
		encNs := time.Since(t0).Nanoseconds()
		frame.Release()

		n.lastEncodeNs.Store(encNs)
		if mx := n.maxEncodeNs.Load(); encNs > mx {
			n.maxEncodeNs.Store(encNs)
		}

		if err != nil {
			n.lastErr.Store(err)
			n.encodeErrors.Add(1)
			continue
		}
		if len(data) > 0 {
			n.framesEncoded.Add(1)
			n.onEncoded(data, isIDR, pts)
		}
	}
}

// stopLoop shuts down the async encode goroutine if running.
func (n *gpuPreviewEncodeNode) stopLoop() {
	if n.frameCh != nil {
		close(n.frameCh)
		<-n.done
		n.frameCh = nil
		n.done = nil
	}
}

// ForceIDR requests that the next encoded frame be an IDR keyframe.
// Safe to call from any goroutine.
func (n *gpuPreviewEncodeNode) ForceIDR() {
	n.forceIDR.Store(true)
}

// DiagSnapshot returns diagnostic counters for this node.
func (n *gpuPreviewEncodeNode) DiagSnapshot() map[string]any {
	return map[string]any{
		"frames_in":      n.framesIn.Load(),
		"frames_dropped": n.framesDropped.Load(),
		"frames_encoded": n.framesEncoded.Load(),
		"encode_errors":  n.encodeErrors.Load(),
		"last_encode_ns": n.lastEncodeNs.Load(),
		"max_encode_ns":  n.maxEncodeNs.Load(),
	}
}

func (n *gpuPreviewEncodeNode) Close() error {
	n.stopLoop()
	if n.enc != nil {
		n.enc.Close()
		n.enc = nil
	}
	n.active.Store(false)
	return nil
}
