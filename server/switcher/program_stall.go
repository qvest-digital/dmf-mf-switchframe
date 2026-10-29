package switcher

import (
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

// stallFrames is how many frame periods the pipeline may go without a
// frame before the program counts as stalled. One late frame is jitter;
// two missing ones are not.
const stallFrames = 2

// programStall keeps program output running while the program source
// delivers no frames. The pipeline runs once per program frame, so a stalled
// source (an MXL flow with no grains, a source gone offline, an FTB hold,
// which filters every frame) stopped the encoder and with it every output:
// MXL, SRT, recording and the browser preview. A recording started then
// wrote nothing.
//
// When no frame has entered the pipeline for stallFrames periods, the filler
// feeds the last program frame through the pipeline again, once per period
// on the grid of the last frame, and after sourceLossTimeout broadcast black
// in the pipeline's format, as the reference clock's sync loop does. During
// an FTB hold it feeds black at once. The first frame from a source stops
// it. It does nothing during a transition, and nothing in reference clock
// mode, where the sync loop paces output itself.
//
// It runs only with wall-clock video PTS, which renumbers every program
// frame on one monotonic grid; a resumed source therefore continues after
// the filled frames whatever PTS it carries.
type programStall struct {
	mu sync.Mutex
	// lastFrame is when a frame last entered the pipeline, and epoch the
	// program epoch it belonged to; a frame of an older epoch is a frame
	// of another program source.
	lastFrame time.Time
	lastPTS   int64
	epoch     uint64
	// held is a reference to the last program source frame, kept
	// unmodified: the pipeline makes a shared frame writable by copying it,
	// so holding a reference costs that copy and keeps these pixels intact.
	held      *ProcessingFrame
	heldEpoch uint64
	stalled   bool
	// blackAfter is how long a stall shows the held frame before black;
	// sourceLossTimeout when zero.
	blackAfter time.Duration

	stop chan struct{}
	wg   sync.WaitGroup
}

// noteFrame records a frame about to enter the pipeline. A program source
// frame (sourceKey set) is also held for a freeze, replacing the previous
// one. Called on the processing goroutine.
func (st *programStall) noteFrame(pf *ProcessingFrame, sourceKey string, epoch uint64, now time.Time) (resumed bool) {
	var old *ProcessingFrame
	st.mu.Lock()
	st.lastFrame = now
	st.lastPTS = pf.PTS
	st.epoch = epoch
	if sourceKey != "" && pf.refs != nil {
		old = st.held
		keep := *pf
		keep.Ref()
		st.held = &keep
		st.heldEpoch = epoch
	}
	resumed = st.stalled
	st.stalled = false
	st.mu.Unlock()
	if old != nil {
		old.ReleaseYUV()
	}
	return resumed
}

// release drops the held frame.
func (st *programStall) release() {
	st.mu.Lock()
	old := st.held
	st.held = nil
	st.mu.Unlock()
	if old != nil {
		old.ReleaseYUV()
	}
}

// startProgramStallFiller starts the filler goroutine. Stopped by Close
// before the processing channel closes.
func (s *Switcher) startProgramStallFiller() {
	stop := make(chan struct{})
	s.stall.stop = stop
	s.stall.wg.Add(1)
	go s.programStallLoop(stop)
}

func (s *Switcher) stopProgramStallFiller() {
	if s.stall.stop == nil {
		return
	}
	close(s.stall.stop)
	s.stall.stop = nil
	s.stall.wg.Wait()
	s.stall.release()
}

// framePeriod is the pipeline's frame period.
func (s *Switcher) framePeriod() time.Duration {
	if pf := s.pipelineFormat.Load(); pf != nil && pf.FPSNum > 0 && pf.FPSDen > 0 {
		return time.Duration(int64(time.Second) * int64(pf.FPSDen) / int64(pf.FPSNum))
	}
	return time.Second / 30
}

// framePTSTicks is the pipeline's frame period in 90 kHz ticks.
func (s *Switcher) framePTSTicks() int64 {
	if pf := s.pipelineFormat.Load(); pf != nil && pf.FPSNum > 0 && pf.FPSDen > 0 {
		return int64(90000) * int64(pf.FPSDen) / int64(pf.FPSNum)
	}
	return 3000
}

// programStallLoop sleeps until stallFrames periods after the last frame
// and, if none arrived meanwhile, feeds one frame per period. While frames
// flow it only wakes to find the deadline moved and goes back to sleep: it
// feeds nothing then, so it cannot beat against the source.
func (s *Switcher) programStallLoop(stop <-chan struct{}) {
	defer s.stall.wg.Done()
	timer := time.NewTimer(s.framePeriod())
	defer timer.Stop()
	var seenEpoch uint64
	var epochSince time.Time
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
		}
		period := s.framePeriod()
		now := time.Now()
		timer.Reset(s.fillStep(now, period, &seenEpoch, &epochSince))
	}
}

// fillStep feeds a frame if the program is stalled at now and returns how
// long to sleep until the next decision.
func (s *Switcher) fillStep(now time.Time, period time.Duration, seenEpoch *uint64, epochSince *time.Time) time.Duration {
	hs := s.hotState.Load()
	if s.useRefClock.Load() || !hs.wallClockVideoEnabled || s.pipeline.Load() == nil ||
		hs.state.isInTransition() || (hs.programSource == "" && hs.state != StateFTB) {
		return period
	}

	// A new program epoch (a cut, a completed transition) gives the new
	// source stallFrames periods of its own before anything is filled, so
	// no filled frame can land before its first frame.
	epoch := s.programEpoch.Load()
	if epoch != *seenEpoch {
		*seenEpoch = epoch
		*epochSince = now
	}

	st := &s.stall
	st.mu.Lock()
	since := st.lastFrame
	if st.epoch != epoch || since.Before(*epochSince) {
		since = *epochSince
	}
	stallAt := since.Add(stallFrames * period)
	if now.Before(stallAt) {
		st.mu.Unlock()
		return stallAt.Sub(now)
	}
	if !st.stalled {
		st.stalled = true
		s.log.Warn("program source stalled, holding its last frame",
			"program_source", hs.programSource, "since_ms", now.Sub(since).Milliseconds())
	}
	lastPTS := st.lastPTS
	blackAfter := st.blackAfter
	if blackAfter == 0 {
		blackAfter = sourceLossTimeout
	}
	var frame *ProcessingFrame
	if hs.state != StateFTB && now.Sub(since) < blackAfter && st.held != nil && st.heldEpoch == epoch {
		keep := *st.held
		keep.Ref()
		frame = &keep
	}
	st.mu.Unlock()

	if frame == nil {
		frame = s.pipelineBlack()
	}
	elapsed := now.Sub(since)
	// wallClockVideoPTS renumbers this like any program frame. A built-in
	// source keeps the CFR PTS of its own producer instead, so a frame
	// filled for one (an FTB hold on BLACK or BARS) continues that grid.
	frame.PTS = lastPTS + int64(elapsed/period)*s.framePTSTicks()
	frame.DTS = frame.PTS
	s.stallFillFrames.Add(1)
	s.enqueueVideoWork(videoProcWork{
		yuvFrame:    frame,
		epoch:       epoch,
		enqueueNano: now.UnixNano(),
		fill:        true,
		keepPTS:     isBuiltinSource(hs.programSource),
	})

	// Next frame on the grid of the last real one.
	next := since.Add((elapsed/period + 1) * period)
	return next.Sub(now)
}

// pipelineBlack returns a black frame in the pipeline's format and size.
func (s *Switcher) pipelineBlack() *ProcessingFrame {
	pf := s.pipelineFormat.Load()
	w, h, cf := 1920, 1080, ColorYUV420_8bit
	if pf != nil {
		w, h, cf = pf.Width, pf.Height, pf.Color.ColorFormat
	}
	pool := s.framePool.Load()
	size := cf.FrameSize(w, h)
	var buf []byte
	var owner *FramePool
	if pool != nil && size <= pool.BufSize() {
		buf, owner = pool.Acquire()[:size], pool
	} else {
		buf = make([]byte, size)
	}
	video.FillBlack(buf, cf, w, h)
	frame := &ProcessingFrame{Data: buf, Width: w, Height: h, Format: cf, IsKeyframe: true, pool: owner}
	if pf != nil {
		frame.Codec = pf.Color.Codec.String()
	}
	frame.SetRefs(1)
	return frame
}
