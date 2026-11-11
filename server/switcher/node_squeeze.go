package switcher

import (
	"time"

	"github.com/zsiec/switchframe/server/dve"
)

var _ PipelineNode = (*squeezeNode)(nil)

// SetSqueeze sets the squeeze-back stage for L-frames.
func (s *Switcher) SetSqueeze(sq *dve.Squeeze) {
	if sq != nil {
		// An arrival changes animating and, after an Off, tally. It is
		// reported on the pipeline goroutine, which must not wait on the
		// state lock. Set before the stage is published to the pipeline.
		sq.SetOnArrive(func() { go func() { s.notifyStateChange(s.State()) }() })
	}
	s.configMu.Lock()
	s.squeeze = sq
	s.updateGraphicsHandlers(func(gh *graphicsHandlers) { gh.squeeze = sq })
	s.configMu.Unlock()
	s.rebuildPipeline()
}

// squeezeNode shrinks the composited program into the squeeze-back rect
// over its background. It sits after the DVE compositor and before the
// downstream graphics, so keys and PiPs shrink with the program and
// graphics stay full frame.
type squeezeNode struct {
	squeeze *dve.Squeeze
}

func (n *squeezeNode) Name() string { return "squeezeback" }
func (n *squeezeNode) Configure(format PipelineFormat) error {
	if n.squeeze != nil {
		n.squeeze.UpdateFormat(format.Width, format.Height)
	}
	return nil
}

// Active is true whenever attached, as for the graphics compositor: a
// rebuild on every on and off would stutter, and Process returns at once
// while the program fills the frame.
func (n *squeezeNode) Active() bool           { return n.squeeze != nil }
func (n *squeezeNode) Err() error             { return nil }
func (n *squeezeNode) Latency() time.Duration { return time.Millisecond }
func (n *squeezeNode) Close() error           { return nil }

func (n *squeezeNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	if src.Format == ColorYUV422_10bit {
		src.Data = n.squeeze.ProcessFrame10(src.Data, src.Width, src.Height)
	} else {
		src.Data = n.squeeze.ProcessFrame(src.Data, src.Width, src.Height)
	}
	return src
}
