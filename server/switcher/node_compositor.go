package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/graphics"
)

type compositorNode struct {
	compositor *graphics.Compositor
	lastErr    error
}

func (n *compositorNode) Name() string                         { return "compositor" }
func (n *compositorNode) Configure(format PipelineFormat) error { return nil }
func (n *compositorNode) Active() bool {
	return n.compositor != nil && n.compositor.IsActive()
}
func (n *compositorNode) Err() error             { return n.lastErr }
func (n *compositorNode) Latency() time.Duration { return 200 * time.Microsecond }
func (n *compositorNode) Close() error           { return nil }

func (n *compositorNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	src.YUV = n.compositor.ProcessYUV(src.YUV, src.Width, src.Height)
	return src
}
