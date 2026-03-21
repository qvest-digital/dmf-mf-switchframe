//go:build darwin || (cgo && cuda)

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
// The node creates a PreviewEncoder lazily in Configure() once the
// pipeline dimensions are known. ProcessGPU hands each frame to the
// encoder (which scales + encodes on its own GPUWorkQueue) and delivers
// the encoded bitstream via the onEncoded callback.
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

// ProcessGPU scales and encodes the frame on the GPU, then delivers the
// encoded bitstream via the onEncoded callback. The source frame is not
// modified (PreviewEncoder.Encode reads but does not write to src).
func (n *gpuPreviewEncodeNode) ProcessGPU(frame *GPUFrame) error {
	if n.enc == nil {
		return nil
	}

	forceIDR := n.forceIDR.Swap(false)

	data, isIDR, err := n.enc.Encode(frame, forceIDR)
	if err != nil {
		n.lastErr.Store(err)
		return err
	}

	if len(data) > 0 {
		n.onEncoded(data, isIDR, frame.PTS)
	}
	return nil
}

// ForceIDR requests that the next encoded frame be an IDR keyframe.
// Safe to call from any goroutine.
func (n *gpuPreviewEncodeNode) ForceIDR() {
	n.forceIDR.Store(true)
}

func (n *gpuPreviewEncodeNode) Close() error {
	if n.enc != nil {
		n.enc.Close()
		n.enc = nil
	}
	n.active.Store(false)
	return nil
}
