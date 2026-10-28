package dve

import (
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
