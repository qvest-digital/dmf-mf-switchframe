package transition

import (
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/internal/video"
)

// renderJob is one frame to draw: copies of both sources, so the producer
// can go on overwriting its own buffers.
type renderJob struct {
	a, b  []byte
	w, h  int
	f     video.Format
	pos   float64
	pts   int64
	kf    bool
	final bool
}

// asyncRender draws Render frames on a goroutine of its own. A CPU DVE
// frame takes longer than the gap between source frames leaves the
// producer, and drawing it on the incoming source's goroutine slowed that
// source's reader below the frame rate. Producers hand frames over through
// a one-slot mailbox instead: a frame that arrives while the renderer is
// busy replaces the waiting one, so a slow renderer drops frames and the
// sources keep their rate.
type asyncRender struct {
	e     *Engine
	ttype Type
	wake  chan struct{}
	stop  chan struct{}
	once  sync.Once
	done  chan struct{} // closed when the goroutine exits

	mu      sync.Mutex
	pending *renderJob
	spare   *renderJob

	dst     []byte
	blender *FrameBlender // mix for a frame Render declines
}

func newAsyncRender(e *Engine, ttype Type) *asyncRender {
	r := &asyncRender{
		e:     e,
		ttype: ttype,
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go r.loop()
	return r
}

// submit queues a frame, replacing a waiting one unless that one is the
// final frame, which is always drawn.
func (r *asyncRender) submit(a, b []byte, w, h int, f video.Format, pos float64, pts int64, kf, final bool) {
	r.mu.Lock()
	if r.pending != nil && r.pending.final {
		r.mu.Unlock()
		return
	}
	j := r.pending
	if j == nil {
		j, r.spare = r.spare, nil
	}
	if j == nil {
		j = &renderJob{}
	}
	j.a = append(j.a[:0], a...)
	j.b = append(j.b[:0], b...)
	j.w, j.h, j.f, j.pos, j.pts, j.kf, j.final = w, h, f, pos, pts, kf, final
	if r.pending != nil && r.pending.kf {
		j.kf = true // a dropped frame's keyframe flag moves to its replacement
	}
	r.pending = j
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// shutdown stops the goroutine without drawing what is waiting. It does
// not wait: the goroutine may be inside an Output callback that needs the
// engine lock the caller holds.
func (r *asyncRender) shutdown() {
	r.once.Do(func() { close(r.stop) })
}

func (r *asyncRender) loop() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case <-r.wake:
		}
		r.mu.Lock()
		j := r.pending
		r.pending = nil
		r.mu.Unlock()
		if j == nil {
			continue
		}
		frame := r.draw(j)
		select {
		case <-r.stop:
			return // aborted while drawing: the frame belongs to no transition
		default:
		}
		if out := r.e.config.Output; out != nil {
			out(frame, j.w, j.h, j.pts, j.kf)
		}
		if j.final {
			if cb := r.e.config.OnComplete; cb != nil {
				cb(false)
			}
			return
		}
		r.mu.Lock()
		r.spare = j
		r.mu.Unlock()
	}
}

// draw renders a job, or mixes it when Render declines the frame.
func (r *asyncRender) draw(j *renderJob) []byte {
	start := time.Now()
	r.dst = growBuf(r.dst, j.f.FrameSize(j.w, j.h))
	if !r.e.timedRender(r.ttype, r.dst, j.a, j.b, j.w, j.h, j.f, j.pos) {
		r.mix(j)
	}
	d := time.Since(start).Nanoseconds()
	r.e.blendLastNano.Store(d)
	atomicutil.UpdateMax(&r.e.blendMaxNano, d)
	r.e.framesBlended.Add(1)
	return r.dst
}

func (r *asyncRender) mix(j *renderJob) {
	pos256 := min(max(int(j.pos*256), 0), 256)
	if j.f == video.YUV422_10bit {
		_ = BlendMix10(r.dst, j.a, j.b, j.w, j.h, pos256)
		return
	}
	if r.blender == nil || r.blender.width != j.w || r.blender.height != j.h {
		b, err := NewFrameBlender(j.w, j.h)
		if err != nil {
			copy(r.dst, j.a)
			return
		}
		r.blender = b
	}
	copy(r.dst, r.blender.BlendMix(j.a, j.b, j.pos))
}

// running reports whether the render goroutine is alive.
func (r *asyncRender) running() bool {
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}
