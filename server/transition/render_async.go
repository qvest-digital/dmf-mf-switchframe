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
	final bool
}

// asyncRender draws Render frames on a goroutine of its own. A CPU DVE
// frame takes longer than the gap between source frames leaves the
// producer, and drawing it on the incoming source's goroutine slowed that
// source's reader below the frame rate. Producers hand frames over through
// a one-slot mailbox instead: a frame that arrives while the renderer is
// busy replaces the waiting one, so a slow renderer drops frames and the
// sources keep their rate.
//
// Output keeps the sources' rate too. Every source frame opens an output
// slot carrying its pts, and every slot gets one frame, in order: the
// renderer fills the open slot with the frame it finishes, and a slot it
// leaves empty gets a copy of the last finished frame when the next source
// frame arrives. A frame finished into a slot that is already filled is
// kept as the last frame without going out.
type asyncRender struct {
	e     *Engine
	ttype Type
	wake  chan struct{}
	stop  chan struct{} // closed by shutdown
	done  chan struct{} // closed when the goroutine exits

	mu        sync.Mutex
	stopped   bool
	pending   *renderJob
	spare     *renderJob
	final     bool  // the final frame is submitted; nothing follows it
	claimed   bool  // the goroutine calls OnComplete for the final frame
	slotPTS   int64 // pts of the newest slot
	slotOpen  bool
	slotFull  bool
	keyed     bool // a frame flagged as keyframe went out
	haveLast  bool // last holds a finished frame
	repeat    bool // the slot at repeatPTS waits for a copy of last
	repeatPTS int64

	// outMu orders Output calls, so slots go out in pts order, and guards
	// last and rep. last is never handed to Output: a consumer may process
	// an output buffer in place, and a repeat must carry the frame as drawn.
	outMu        sync.Mutex
	last, rep    []byte
	lastW, lastH int

	dst     []byte        // render goroutine only
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

// submit queues a frame, replacing a waiting one, and opens its output
// slot. It reports whether the slot before it was left empty and now waits
// for a repeat, which the caller sends with flush once it holds no engine
// lock. Nothing is queued after the final frame.
func (r *asyncRender) submit(a, b []byte, w, h int, f video.Format, pos float64, pts int64, final bool) (repeat bool) {
	r.mu.Lock()
	if r.stopped || r.final {
		r.mu.Unlock()
		return false
	}
	if r.slotOpen && !r.slotFull && r.haveLast {
		r.repeat, r.repeatPTS = true, r.slotPTS
		repeat = true
	}
	r.slotPTS, r.slotOpen, r.slotFull = pts, true, false
	r.final = final
	j := r.pending
	if j == nil {
		j, r.spare = r.spare, nil
	}
	if j == nil {
		j = &renderJob{}
	}
	j.a = append(j.a[:0], a...)
	j.b = append(j.b[:0], b...)
	j.w, j.h, j.f, j.pos, j.pts, j.final = w, h, f, pos, pts, final
	r.pending = j
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return repeat
}

// flush sends a repeat that submit left waiting.
func (r *asyncRender) flush() {
	r.outMu.Lock()
	r.flushLocked()
	r.outMu.Unlock()
}

// flushLocked sends a waiting repeat, a copy of the last finished frame.
// Caller holds outMu. Whichever of producer and renderer takes outMu first
// sends it, so it always goes out ahead of the slot after it.
func (r *asyncRender) flushLocked() {
	r.mu.Lock()
	due, pts, stopped := r.repeat, r.repeatPTS, r.stopped
	r.repeat = false
	r.mu.Unlock()
	if !due || stopped {
		return
	}
	r.rep = growBuf(r.rep, len(r.last))
	copy(r.rep, r.last)
	r.e.framesRepeated.Add(1)
	// Like the renderer's, this stop check races a shutdown landing just
	// after it; see loop.
	if out := r.e.config.Output; out != nil {
		out(r.rep, r.lastW, r.lastH, pts, false)
	}
}

// shutdown stops the goroutine without drawing or sending what is waiting.
// It reports whether the final frame was submitted and its OnComplete has
// not been claimed: the caller then owns the transition's completion. It
// does not wait: the goroutine may be inside an Output callback that needs
// the engine lock the caller holds.
func (r *asyncRender) shutdown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	r.stopped = true
	close(r.stop)
	return r.final && !r.claimed
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

		r.outMu.Lock()
		r.flushLocked()
		r.mu.Lock()
		if r.stopped {
			// Stopped while drawing: the frame belongs to no transition.
			r.mu.Unlock()
			r.outMu.Unlock()
			return
		}
		send := j.final || (r.slotOpen && !r.slotFull && !r.final)
		r.slotFull = r.slotFull || send
		kf := send && !r.keyed
		r.keyed = r.keyed || kf
		pts := r.slotPTS
		if !j.final {
			r.haveLast = true
		}
		r.mu.Unlock()
		if !j.final {
			r.last = growBuf(r.last, len(frame))
			copy(r.last, frame)
			r.lastW, r.lastH = j.w, j.h
		}
		// A shutdown landing between the stop check above and this call
		// still lets the frame out. Holding r.mu across Output would close
		// that window of microseconds, but Output can need the engine lock
		// that shutdown's caller holds.
		if out := r.e.config.Output; send && out != nil {
			out(frame, j.w, j.h, pts, kf)
		}
		r.outMu.Unlock()

		if j.final {
			r.complete()
			return
		}
		r.mu.Lock()
		r.spare = j
		r.mu.Unlock()
	}
}

// complete calls OnComplete for the final frame unless a shutdown came
// first; that one decides about the completion.
func (r *asyncRender) complete() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.claimed = true
	r.mu.Unlock()
	if cb := r.e.config.OnComplete; cb != nil {
		cb(false)
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
