package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/layout"
)

// Compile-time interface check.
var _ PipelineNode = (*layoutCompositorNode)(nil)

// layoutCompositorNode composites PIP/split-screen overlays onto the program frame.
// Wraps layout.Compositor as a PipelineNode. In-place: modifies src.YUV via
// compositor.ProcessFrame and returns src.
//
// Active only when the compositor has an active layout with enabled slots.
// When inactive, the pipeline skips this node entirely (zero overhead).
type layoutCompositorNode struct {
	compositor *layout.Compositor
}

func (n *layoutCompositorNode) Name() string                          { return "layout-compositor" }
func (n *layoutCompositorNode) Configure(format PipelineFormat) error { return nil }
func (n *layoutCompositorNode) Active() bool {
	return n.compositor != nil && n.compositor.Active()
}
func (n *layoutCompositorNode) Err() error             { return nil }
func (n *layoutCompositorNode) Latency() time.Duration { return time.Millisecond }
func (n *layoutCompositorNode) Close() error           { return nil }

func (n *layoutCompositorNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	src.YUV = n.compositor.ProcessFrame(src.YUV, src.Width, src.Height)
	return src
}
