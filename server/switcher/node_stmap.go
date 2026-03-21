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
	n.buf = make([]byte, format.Width*format.Height*3/2)
	return nil
}

func (n *stmapProgramNode) Active() bool {
	return n.registry != nil && n.registry.HasProgramMap()
}

func (n *stmapProgramNode) Err() error             { return nil }
func (n *stmapProgramNode) Latency() time.Duration { return 8 * time.Millisecond }
func (n *stmapProgramNode) Close() error           { return nil }

func (n *stmapProgramNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	// Try animated map first (checked via registry — returns nil if not animated).
	if anim := n.registry.ProgramAnimatedFrame(); anim != nil {
		idx := anim.AdvanceIndex()
		proc := anim.ProcessorAt(idx)
		proc.ProcessYUV(n.buf, src.YUV, src.Width, src.Height)
		copy(src.YUV, n.buf)
		return src
	}

	// Static map path.
	if proc := n.registry.ProgramProcessor(); proc != nil && proc.Active() {
		proc.ProcessYUV(n.buf, src.YUV, src.Width, src.Height)
		copy(src.YUV, n.buf)
	}

	return src
}
