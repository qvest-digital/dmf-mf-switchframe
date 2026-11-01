package gpu

import "github.com/zsiec/switchframe/server/clock"

// refCounted is a frame a store keeps a reference of its own to.
type refCounted interface {
	Ref()
	Release()
}

// recentFrames is a source's last published frames, oldest first, each
// under the PTS a tick looks it up by and with a reference of its own. The
// program is read behind a source's newest frame (clock.ReaderDelayGrains),
// so the frame a tick shows is usually one of these rather than the newest.
// A source placed ahead of its arrival, such as an SRT stream by its
// clock.TrackingAnchor, is read further behind its newest frame, and the
// store keeps the frames that arrive in that hold as well. It is not safe
// for concurrent use.
type recentFrames[F refCounted] struct {
	frames []recentFrame[F]
	hold   clock.SourceHold
	// delay is the source's delay (SetSourceDelay) in 90 kHz ticks.
	delay int64
}

type recentFrame[F refCounted] struct {
	f   F
	pts int64
}

// recentFramesDepth is how many frames a source's store keeps for a frame
// due on arrival: the reader delay plus a frame of slack. The source's hold
// adds the frames it holds back.
const recentFramesDepth = 4

// push keeps f under pts, taking a reference of its own, and returns the
// frames it no longer keeps. The caller releases those, outside any lock a
// reader takes.
func (r *recentFrames[F]) push(f F, pts int64) (evicted []F) {
	f.Ref()
	r.frames = append(r.frames, recentFrame[F]{f: f, pts: pts})
	if n := len(r.frames) - recentFramesDepth - r.hold.Frames(pts); n > 0 {
		for _, e := range r.frames[:n] {
			evicted = append(evicted, e.f)
		}
		k := copy(r.frames, r.frames[n:])
		clear(r.frames[k:])
		r.frames = r.frames[:k]
	}
	return evicted
}

// at returns the frame kept under pts with a reference the caller must
// Release, or false when none is.
func (r *recentFrames[F]) at(pts int64) (F, bool) {
	for i := len(r.frames) - 1; i >= 0; i-- {
		if e := r.frames[i]; e.pts == pts {
			e.f.Ref()
			return e.f, true
		}
	}
	var zero F
	return zero, false
}

// near returns, with a reference the caller must Release, the frame kept
// under pts, else the newest kept under an earlier PTS, else the oldest
// kept; false when the store is empty. It stands in for a frame that is no
// longer kept, for a frame without pixels of its own to upload.
func (r *recentFrames[F]) near(pts int64) (F, bool) {
	for i := len(r.frames) - 1; i >= 0; i-- {
		if e := r.frames[i]; e.pts <= pts {
			e.f.Ref()
			return e.f, true
		}
	}
	if len(r.frames) > 0 {
		e := r.frames[0]
		e.f.Ref()
		return e.f, true
	}
	var zero F
	return zero, false
}

// drain returns every kept frame and keeps none.
func (r *recentFrames[F]) drain() []F {
	out := make([]F, 0, len(r.frames))
	for _, e := range r.frames {
		out = append(out, e.f)
	}
	clear(r.frames)
	r.frames = r.frames[:0]
	return out
}
