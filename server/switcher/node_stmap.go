package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/stmap"
)

// Compile-time interface check.
var _ PipelineNode = (*stmapProgramNode)(nil)

// stmapProgramNode applies a program-wide ST map warp to the composited frame.
// Active only when registry.HasProgramMap() is true (atomic, lock-free check).
// Inserted after DSK compositor, before raw sinks.
type stmapProgramNode struct {
	registry *stmap.Registry
	buf      []byte // preallocated dst buffer, sized in Configure()
}

func (n *stmapProgramNode) Name() string { return "stmap-program" }

func (n *stmapProgramNode) Configure(format PipelineFormat) error {
	n.buf = make([]byte, format.Color.ColorFormat.FrameSize(format.Width, format.Height))
	return nil
}

func (n *stmapProgramNode) Active() bool {
	return n.registry != nil && n.registry.HasProgramMap()
}

func (n *stmapProgramNode) Err() error             { return nil }
func (n *stmapProgramNode) Latency() time.Duration { return 8 * time.Millisecond }
func (n *stmapProgramNode) Close() error           { return nil }

func (n *stmapProgramNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	proc, anim, release := n.registry.ProgramSnapshot()
	defer release()

	if anim != nil {
		idx := anim.AdvanceIndex()
		p := anim.ProcessorAt(idx)
		n.warpProcessor(p, src)
		return src
	}

	if proc != nil && proc.Active() {
		n.warpProcessor(proc, src)
	}

	return src
}

// warpProcessor dispatches to the appropriate warp path based on frame format.
func (n *stmapProgramNode) warpProcessor(p *stmap.Processor, src *ProcessingFrame) {
	switch src.Format {
	case ColorYUV422_10bit:
		if err := p.Warp10(n.buf, src.Data, src.Width, src.Height); err == nil {
			copy(src.Data, n.buf)
		}
	default:
		p.ProcessYUV(n.buf, src.Data, src.Width, src.Height)
		copy(src.Data, n.buf)
	}
}
