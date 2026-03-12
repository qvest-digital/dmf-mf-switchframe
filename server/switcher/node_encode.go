package switcher

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/metrics"
)

var _ PipelineNode = (*encodeNode)(nil)

// encodeWork holds a frame and encode parameters for async processing.
type encodeWork struct {
	frame    *ProcessingFrame
	forceIDR bool
}

type encodeNode struct {
	codecs      *pipelineCodecs
	forceIDR    *atomic.Bool
	promMetrics *metrics.Metrics
	lastErr     atomic.Value // stores error; safe for concurrent Snapshot() reads

	// Output callback -- called with encoded H.264 frame.
	onEncoded func(frame *media.VideoFrame)

	// Diagnostic counter for hardware encoder warmup (nil frame returns)
	encodeNilCount *atomic.Int64

	// Async encode goroutine. When encodeCh is non-nil, Process() enqueues
	// work and returns immediately — the encode happens in a background
	// goroutine. This decouples encode latency from the pipeline loop,
	// allowing raw sinks (which run before encode) to deliver frames at
	// full rate even when encode is slow (e.g., stinger transitions).
	encodeCh  chan encodeWork
	wg        sync.WaitGroup
	closeOnce sync.Once

	// Diagnostic counter: frames dropped because the encoder goroutine
	// was still busy with the previous frame.
	encodeDropCount *atomic.Int64
}

func (n *encodeNode) Name() string                          { return "h264-encode" }
func (n *encodeNode) Configure(format PipelineFormat) error { return nil }
func (n *encodeNode) Active() bool                          { return true }
func (n *encodeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}
func (n *encodeNode) Latency() time.Duration { return 10 * time.Millisecond }

// start launches the async encode goroutine. Must be called before Process()
// for async operation. If not called, Process() falls back to synchronous encode.
func (n *encodeNode) start() {
	// Buffer of 2: absorbs one frame of encode latency spike without
	// dropping. At 30fps (33ms budget), this allows one frame where
	// encode takes up to 66ms before drops occur. Keeps latency bounded
	// to one extra frame (33ms) in the worst case.
	n.encodeCh = make(chan encodeWork, 2)
	n.wg.Add(1)
	go n.encodeLoop()
}

// Close stops the async encode goroutine and waits for pending work to drain.
// Safe to call multiple times (idempotent via sync.Once).
//
// Thread safety with Process(): Pipeline.Close() calls inflight.Wait() before
// closing nodes, guaranteeing no concurrent Process() call is in progress when
// Close() runs. This is enforced by the Pipeline.Run()/Close() contract.
func (n *encodeNode) Close() error {
	n.closeOnce.Do(func() {
		if n.encodeCh != nil {
			close(n.encodeCh)
			n.wg.Wait()
		}
	})
	return nil
}

// encodeLoop processes encode work items from the channel.
// Runs in a dedicated goroutine for the lifetime of the node.
func (n *encodeNode) encodeLoop() {
	defer n.wg.Done()
	for work := range n.encodeCh {
		n.processWorkItem(work)
	}
}

// processWorkItem encodes a single frame with panic recovery.
// A panic in the encoder (e.g., cgo FFmpeg crash) must not kill the
// goroutine — that would silently disable all H.264 output.
func (n *encodeNode) processWorkItem(work encodeWork) {
	defer func() {
		if r := recover(); r != nil {
			n.lastErr.Store(fmt.Errorf("encode panic: %v", r))
			slog.Error("encode goroutine recovered from panic",
				"panic", r,
				"stack", string(debug.Stack()))
			// Invalidate the encoder — a panic (e.g., cgo crash) likely
			// left it in a corrupt state. Force recreation on next frame.
			n.codecs.invalidateEncoder()
		}
		work.frame.ReleaseYUV() // always release the async ref
	}()
	n.doEncode(work.frame, work.forceIDR)
}

// doEncode performs the actual H.264 encode with timing, metrics, and error handling.
// Called from processWorkItem (async) or Process (sync fallback).
func (n *encodeNode) doEncode(src *ProcessingFrame, forceIDR bool) {
	encStart := time.Now().UnixNano()
	frame, err := n.codecs.encode(src, forceIDR)
	encDur := time.Now().UnixNano() - encStart

	if n.promMetrics != nil {
		n.promMetrics.PipelineEncodeDuration.Observe(float64(encDur) / 1e9)
	}

	if err != nil {
		n.lastErr.Store(err)
		if n.promMetrics != nil {
			n.promMetrics.PipelineEncodeErrorsTotal.Inc()
		}
		return
	}
	if frame == nil {
		if n.encodeNilCount != nil {
			n.encodeNilCount.Add(1)
		}
		return
	}
	if n.promMetrics != nil {
		n.promMetrics.PipelineFramesProcessed.Inc()
	}
	if n.onEncoded != nil {
		n.onEncoded(frame)
	}
}

func (n *encodeNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	forceIDR := src.IsKeyframe
	if n.forceIDR != nil {
		forceIDR = forceIDR || n.forceIDR.CompareAndSwap(true, false)
	}

	// Async path: enqueue work for the background goroutine.
	// Only use async when the frame has ref tracking (managed frames).
	// Unmanaged frames (refs == nil, e.g. test code) can't safely Ref(),
	// so they fall through to synchronous encode.
	if n.encodeCh != nil && src.Refs() > 0 {
		src.Ref() // +1 for async encode goroutine
		select {
		case n.encodeCh <- encodeWork{frame: src, forceIDR: forceIDR}:
			// queued for async encode
		default:
			// Encoder goroutine still busy — drop this frame from H.264 output.
			// Raw YUV sinks already received this frame (they run before encode
			// in the pipeline). H.264 viewers will experience the same drop they
			// would have seen from videoProcCh backpressure before this change.
			src.ReleaseYUV()
			if forceIDR && n.forceIDR != nil {
				// Re-arm so the next frame carries the IDR request.
				n.forceIDR.Store(true)
			}
			if n.encodeDropCount != nil {
				n.encodeDropCount.Add(1)
			}
		}
		return src
	}

	// Synchronous fallback: no start() called, or frame is unmanaged
	// (no ref tracking). Used in tests and for transient frames.
	n.doEncode(src, forceIDR)
	return src
}
