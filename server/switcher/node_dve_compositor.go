package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/dve"
)

// Compile-time interface check.
var _ PipelineNode = (*dveCompositorNode)(nil)

// dveCompositorNode composites DVE-transformed PIP/split-screen overlays onto
// the program frame. Wraps dve.Compositor as a PipelineNode. In-place: modifies
// src.Data via compositor.ProcessFrame and returns src.
//
// Active only when the compositor has an active layout with enabled slots.
// When inactive, the pipeline skips this node entirely (zero overhead).
type dveCompositorNode struct {
	compositor *dve.Compositor
}

func (n *dveCompositorNode) Name() string { return "dve-compositor" }
func (n *dveCompositorNode) Configure(format PipelineFormat) error {
	if n.compositor != nil {
		n.compositor.UpdateFormat(format.Width, format.Height)
	}
	return nil
}
func (n *dveCompositorNode) Active() bool {
	return n.compositor != nil && n.compositor.Active()
}
func (n *dveCompositorNode) Err() error             { return nil }
func (n *dveCompositorNode) Latency() time.Duration { return time.Millisecond }
func (n *dveCompositorNode) Close() error           { return nil }

func (n *dveCompositorNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	if src.Format == ColorYUV422_10bit {
		// 10-bit path not yet implemented for DVE compositor — pass through.
		return src
	}
	src.Data = n.compositor.ProcessFrame(src.Data, src.Width, src.Height)
	return src
}
