package dve

import (
	"maps"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// CPUTransitionForType returns the CPU renderer of a DVE transition type
// for transition.EngineConfig.Render, or nil for a type that has none.
//
// The frame is built as the GPU transition path builds it: broadcast black,
// then the incoming source through its transform, then the outgoing source
// on top. A source whose transform is 3D is not drawn yet, and a frame
// with one renders nothing.
func CPUTransitionForType(transitionType string) transition.RenderFunc {
	fn := DVETransitionForType(transitionType)
	if fn == nil {
		return nil
	}
	return func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
		size := f.FrameSize(w, h)
		if len(dst) < size || len(from) < size || (to != nil && len(to) < size) {
			return false
		}
		prog, prev := fn(pos, w, h)
		if prog.Is3D() || prev.Is3D() {
			return false
		}
		video.FillBlack(dst[:size], f, w, h)
		if to != nil {
			compositeTransitionSource(dst, to, w, h, f, prev)
		}
		compositeTransitionSource(dst, from, w, h, f, prog)
		return true
	}
}

// compositeTransitionSource draws one full-frame source through a 2D
// transform, as compositeDVESource does on the GPU.
func compositeTransitionSource(dst, src []byte, w, h int, f video.Format, t Transform) {
	if t.Opacity <= 0 {
		return
	}
	CompositeCPU(dst, w, h, src, w, h, f, t, ComputeInverseTransform(t, w, h), ComputeAABB(t, w, h, w, h))
}

// Reasons the CPU gives for transitions it does not draw.
const (
	reason3D   = "3D DVE transitions need a GPU engine"
	reasonPeel = "Peel needs a GPU engine: it takes about five cores at 1080p50 on the CPU"
)

// cpuRefused maps each DVE transition type the CPU does not draw to the
// reason. Ray casting a 1080p frame for a 3D type costs more than a 50 fps
// frame period even on eight cores; corner-peel fits the frame period on
// eight but takes about five cores for the length of the transition.
var cpuRefused = map[string]string{
	"dve-cube":        reason3D,
	"dve-cube-roll":   reason3D,
	"dve-flip":        reason3D,
	"dve-door":        reason3D,
	"dve-swing-door":  reason3D,
	"dve-fold":        reason3D,
	"dve-carousel":    reason3D,
	"dve-tumble":      reason3D,
	"dve-globe":       reason3D,
	"dve-page-turn":   reason3D,
	"dve-corner-peel": reasonPeel,
}

// CPURefusedTransitions returns the DVE transition types an engine without
// a GPU refuses, each with the reason.
func CPURefusedTransitions() map[string]string { return maps.Clone(cpuRefused) }

// CPURefusal returns the reason an engine without a GPU refuses a
// transition type, and whether it does.
func CPURefusal(transitionType string) (string, bool) {
	reason, ok := cpuRefused[transitionType]
	return reason, ok
}
