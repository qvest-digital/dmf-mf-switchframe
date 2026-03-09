package switcher

import (
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/metrics"
)

var _ PipelineNode = (*encodeNode)(nil)

type encodeNode struct {
	codecs      *pipelineCodecs
	forceIDR    *atomic.Bool
	promMetrics *metrics.Metrics
	lastErr     atomic.Value // stores error; safe for concurrent Snapshot() reads

	// Output callback -- called with encoded H.264 frame.
	onEncoded func(frame *media.VideoFrame)

	// Diagnostic counter for hardware encoder warmup (nil frame returns)
	encodeNilCount *atomic.Int64
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
func (n *encodeNode) Latency() time.Duration                { return 10 * time.Millisecond }
func (n *encodeNode) Close() error                          { return nil }

func (n *encodeNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	forceIDR := src.IsKeyframe
	if n.forceIDR != nil {
		forceIDR = forceIDR || n.forceIDR.CompareAndSwap(true, false)
	}

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
		return src
	}
	if frame == nil {
		if n.encodeNilCount != nil {
			n.encodeNilCount.Add(1)
		}
		return src
	}
	if n.promMetrics != nil {
		n.promMetrics.PipelineFramesProcessed.Inc()
	}
	if n.onEncoded != nil {
		n.onEncoded(frame)
	}
	return src
}
