package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/graphics"
)

var _ PipelineNode = (*compositorNode)(nil)

type compositorNode struct {
	compositor    *graphics.Compositor
	blendScratch  []byte
	colLUTScratch []int
}

func (n *compositorNode) Name() string                          { return "compositor" }
func (n *compositorNode) Configure(format PipelineFormat) error { return nil }
func (n *compositorNode) Active() bool {
	// Always active when attached. ProcessYUV has its own fast path
	// (RLock + hasVisibleLayers check) that returns in <1µs when no
	// layers are visible. Keeping the node always active avoids pipeline
	// rebuilds on graphics toggle, which cause a single-frame stutter
	// from encode goroutine handoff (pipeCodecs.mu contention).
	return n.compositor != nil
}
func (n *compositorNode) Err() error             { return nil }
func (n *compositorNode) Latency() time.Duration { return 200 * time.Microsecond }
func (n *compositorNode) Close() error           { return nil }

func (n *compositorNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	src.YUV = n.compositor.ProcessYUV(src.YUV, src.Width, src.Height, &n.blendScratch, &n.colLUTScratch)
	return src
}
