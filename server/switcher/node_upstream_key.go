package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/graphics"
)

// Compile-time interface check.
var _ PipelineNode = (*upstreamKeyNode)(nil)

// upstreamKeyNode applies upstream chroma/luma keying to the program frame.
// Wraps graphics.KeyProcessorBridge as a PipelineNode. In-place: modifies
// src.YUV via bridge.ProcessYUV and returns src.
//
// Active only when the bridge has enabled keys with cached fill frames.
// When inactive, the pipeline skips this node entirely (zero overhead).
type upstreamKeyNode struct {
	bridge *graphics.KeyProcessorBridge
}

func (n *upstreamKeyNode) Name() string                          { return "upstream-key" }
func (n *upstreamKeyNode) Configure(format PipelineFormat) error { return nil }
func (n *upstreamKeyNode) Active() bool {
	return n.bridge != nil && n.bridge.HasEnabledKeysWithFills()
}
func (n *upstreamKeyNode) Err() error             { return nil }
func (n *upstreamKeyNode) Latency() time.Duration { return 100 * time.Microsecond }
func (n *upstreamKeyNode) Close() error           { return nil }

func (n *upstreamKeyNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	src.YUV = n.bridge.ProcessYUV(src.YUV, src.Width, src.Height)
	return src
}
