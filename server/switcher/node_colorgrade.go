package switcher

import (
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/colorgrade"
)

// ProgramGrader supplies the program color grade (look and corrector baked
// into one LUT) to the CPU pipeline.
type ProgramGrader interface {
	// ProgramGrader returns the grader and its intensity, or a nil grader
	// while grading is off. Safe for concurrent use.
	ProgramGrader() (*colorgrade.Grader, float32)
}

// SourceGrader supplies per-source color correction to CPU ingest.
type SourceGrader interface {
	// SourceGrader returns the grader for a source, or nil when the source
	// has no correction. Safe for concurrent use.
	SourceGrader(sourceKey string) *colorgrade.Grader
}

// Compile-time interface check.
var _ PipelineNode = (*colorGradeNode)(nil)

// colorGradeNode applies the program color grade to the composited frame,
// the CPU counterpart of the GPU pipeline's gpu_colorgrade node and in the
// same place: after the downstream keyers, before the program ST map.
//
// It stays in the pipeline whenever a program grader is attached and asks
// the grader per frame, so turning grading on or off needs no pipeline
// rebuild, and no rebuild can race the change and leave the node out.
type colorGradeNode struct {
	grader  ProgramGrader
	lastErr atomic.Pointer[error]
}

func (n *colorGradeNode) Name() string                          { return "colorgrade" }
func (n *colorGradeNode) Configure(format PipelineFormat) error { return nil }
func (n *colorGradeNode) Active() bool                          { return n.grader != nil }

func (n *colorGradeNode) Err() error {
	if p := n.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Latency is zero: the node is in every CPU pipeline with a grader and
// passes frames through untouched while grading is off. A grade takes a
// few milliseconds of the frame period when on.
func (n *colorGradeNode) Latency() time.Duration { return 0 }
func (n *colorGradeNode) Close() error           { return nil }

func (n *colorGradeNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	g, intensity := n.grader.ProgramGrader()
	if g == nil || intensity <= 0 {
		return src
	}
	if err := gradeFrame(g, src, intensity); err != nil {
		n.lastErr.Store(&err)
	}
	return src
}

// gradeFrame grades a frame in place according to its pixel format.
func gradeFrame(g *colorgrade.Grader, pf *ProcessingFrame, intensity float32) error {
	if pf.Format == ColorYUV422_10bit {
		return g.ApplyYUV422P10(pf.Data, pf.Width, pf.Height, intensity)
	}
	return g.ApplyYUV420(pf.Data, pf.Width, pf.Height, intensity)
}

// gradeSourceFrame applies a source's color correction on the CPU, the
// counterpart of the GPU source manager's per-source grade. It grades a
// copy: producers may hand the same buffer in again (replay repeats a frame
// for slow motion) or keep it (clip rings), and grading in place would
// grade it twice. Consumes pf when it returns a different frame.
func (s *Switcher) gradeSourceFrame(sourceKey string, pf *ProcessingFrame) *ProcessingFrame {
	sg := s.sourceGrader.Load()
	if sg == nil || len(pf.Data) == 0 {
		return pf
	}
	g := (*sg).SourceGrader(sourceKey)
	if g == nil {
		return pf
	}
	out := *pf
	pool := s.framePool.Load()
	if pool != nil && len(pf.Data) <= pool.bufSize {
		out.Data = pool.Acquire()[:len(pf.Data)]
		out.pool = pool
	} else {
		out.Data = make([]byte, len(pf.Data))
		out.pool = nil
	}
	copy(out.Data, pf.Data)
	// The copy has one owner, whatever pf's refcount was.
	out.refs = nil
	if out.pool != nil {
		out.SetRefs(1)
	}
	out.GPUData = nil
	if err := gradeFrame(g, &out, 1); err != nil {
		if _, logged := s.sourceGradeErrLogged.LoadOrStore(sourceKey, true); !logged {
			s.log.Warn("source color correction skipped", "source", sourceKey, "err", err)
		}
		out.ReleaseYUV()
		return pf
	}
	pf.ReleaseYUV()
	return &out
}
