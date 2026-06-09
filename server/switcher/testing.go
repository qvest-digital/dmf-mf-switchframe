package switcher

import (
	"log/slog"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

// NewTestSwitcher creates a Switcher with a tiny frame pool (4 × 320×240)
// suitable for testing. The production New() allocates 512 × 1080p buffers
// (~1.5 GB) which causes OOM kills when many tests each create one.
//
// Registers sw.Close via t.Cleanup so the built-in source producer
// goroutine (and everything else Close tears down) shuts down when the test
// returns. Close is idempotent, so callers that also want an explicit
// defer sw.Close() remain correct.
//
// This function is intended for use in other packages' tests (e.g.,
// control API tests) that need a Switcher but don't exercise video
// processing at full resolution.
func NewTestSwitcher(t *testing.T, programRelay *distribution.Relay) *Switcher {
	t.Helper()
	// Use a small format matching the test frame pool (320×240) so that
	// transition hint dimensions don't create 1080p blenders. The default
	// format is 1080p which causes "frame exceeds pool buffer size" drops
	// when the transition engine tries to broadcast 1920×1080 frames into
	// the tiny test pool.
	testFmt := PipelineFormat{
		Name:   "test",
		Width:  320,
		Height: 240,
		FPSNum: 30000,
		FPSDen: 1001,
	}
	s := &Switcher{
		log:                      slog.With("component", "switcher"),
		programRelay:             programRelay,
		health:                   newHealthMonitor(),
		videoProcCh:              make(chan videoProcWork, 8),
		videoProcDone:            make(chan struct{}),
		ftbDurationMs:            defaultFTBDurationMs,
		transitionAbortThreshold: defaultTransitionAbortThreshold,
	}
	s.framePool.Store(NewFramePool(4, 320, 240, ColorYUV420_8bit))
	s.frameBudgetNs.Store(testFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&testFmt)
	s.delayBuffer = NewDelayBuffer(s)
	s.initAtomicSlots()
	s.registerBuiltinSourcesLocked()
	go s.videoProcessingLoop()
	s.startBuiltinProducer()
	t.Cleanup(s.Close)
	return s
}

// newTestSwitcherDirect creates a Switcher WITHOUT starting the
// videoProcessingLoop goroutine. This allows tests to read frames from
// videoProcCh directly and run them through the pipeline synchronously,
// eliminating goroutine scheduling dependencies that cause flaky timeouts
// on slow CI runners.
//
// Callers must drain videoProcCh manually (see runPipelineDirect).
func newTestSwitcherDirect(programRelay *distribution.Relay) *Switcher {
	testFmt := PipelineFormat{
		Name:   "test",
		Width:  320,
		Height: 240,
		FPSNum: 30000,
		FPSDen: 1001,
	}
	s := &Switcher{
		log:                      slog.With("component", "switcher"),
		programRelay:             programRelay,
		health:                   newHealthMonitor(),
		videoProcCh:              make(chan videoProcWork, 8),
		videoProcDone:            make(chan struct{}),
		ftbDurationMs:            defaultFTBDurationMs,
		transitionAbortThreshold: defaultTransitionAbortThreshold,
	}
	s.framePool.Store(NewFramePool(4, 320, 240, ColorYUV420_8bit))
	s.frameBudgetNs.Store(testFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&testFmt)
	s.delayBuffer = NewDelayBuffer(s)
	s.initAtomicSlots()
	s.registerBuiltinSourcesLocked()
	// No videoProcessingLoop started — caller drains videoProcCh directly.
	return s
}

// closeDirect shuts down a switcher created by newTestSwitcherDirect.
// Unlike Close(), it does not wait on videoProcDone since no processing
// loop was started.
func (s *Switcher) closeDirect() {
	s.stopBuiltinProducer()
	s.health.stop()
	s.delayBuffer.Close()
	if fp := s.framePool.Load(); fp != nil {
		fp.Close()
	}
	if p := s.pipeline.Swap(nil); p != nil {
		p.Wait()
		_ = p.Close()
	}
	s.drainWg.Wait()
	s.configMu.Lock()
	pipeCodecs := s.pipeCodecs
	s.configMu.Unlock()
	if pipeCodecs != nil {
		pipeCodecs.close()
	}
}

// runPipelineDirect reads a single work item from videoProcCh and runs it
// through the pipeline synchronously. Returns the ProcessingFrame after
// pipeline processing. The frame's raw sink callback fires during Run().
//
// This is the deterministic alternative to waiting for videoProcessingLoop
// to process the frame asynchronously.
func (s *Switcher) runPipelineDirect(t *testing.T) *ProcessingFrame {
	t.Helper()
	var work videoProcWork
	select {
	case work = <-s.videoProcCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no frame enqueued to videoProcCh")
		return nil
	}
	if work.yuvFrame == nil {
		t.Fatal("enqueued work has nil frame")
		return nil
	}
	p := s.pipeline.Load()
	if p == nil {
		t.Fatal("no pipeline configured")
		return nil
	}
	frame := p.Run(work.yuvFrame)
	return frame
}

// SetTransitionEngine sets the transition engine and transition state for
// testing the synchronous (ref clock) transition path from external packages.
// This bypasses the async StartTransition flow which spawns goroutines.
func (s *Switcher) SetTransitionEngine(engine *transition.Engine) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.transConfig = &TransitionConfig{}
	s.transEngine = engine
	s.transitionState(StateTransitioning)
}
