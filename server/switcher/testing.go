package switcher

import (
	"github.com/zsiec/switchframe/server/clock"
	"log/slog"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

// NewTestSwitcher creates a Switcher with a tiny frame pool (4 x 320x240)
// suitable for testing, running on a realtime reference clock at 29.97
// fps like production. The production New() allocates 512 x 1080p buffers
// (~1.5 GB) which causes OOM kills when many tests each create one.
//
// Registers sw.Close via t.Cleanup so the clock, the sync loop and the
// built-in source producer shut down when the test returns. Close is
// idempotent, so callers that also want an explicit defer sw.Close()
// remain correct.
//
// This function is intended for use in other packages' tests (e.g.,
// control API tests) that need a Switcher but don't exercise video
// processing at full resolution.
func NewTestSwitcher(t *testing.T, programRelay *distribution.Relay) *Switcher {
	t.Helper()
	s := newTestSwitcherDirect(programRelay)
	s.Start()
	t.Cleanup(s.Close)
	return s
}

// newTestSwitcherDirect creates a Switcher with no reference clock. Tests
// drive it one tick at a time with tickNow, so nothing depends on
// goroutine scheduling.
func newTestSwitcherDirect(programRelay *distribution.Relay) *Switcher {
	// A small format matching the test frame pool (320x240), so that
	// transition hint dimensions don't create 1080p blenders.
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
		frameSync:                NewFrameSynchronizer(),
		ftbDurationMs:            defaultFTBDurationMs,
		transitionAbortThreshold: defaultTransitionAbortThreshold,
	}
	s.framePool.Store(NewFramePool(4, 320, 240, ColorYUV420_8bit))
	s.frameBudgetNs.Store(testFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&testFmt)
	s.delayBuffer = NewDelayBuffer(s)
	s.initAtomicSlots()
	s.registerBuiltinSourcesLocked()
	s.startBuiltinProducer()
	return s
}

// closeDirect shuts down a switcher created by newTestSwitcherDirect.
func (s *Switcher) closeDirect() {
	s.Close()
}

// tickNow runs one video tick whose read point is the current TAI, so
// every frame ingested before the call is due.
func (s *Switcher) tickNow() {
	pf := s.pipelineFormat.Load()
	r := clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}
	read := clock.TimestampToIndex(r, clock.TAINow()) + 1
	n := read + clock.ReaderDelayGrains
	s.processVideoTick(clock.Tick{
		Index:         n,
		VideoPTS:      clock.IndexPTS(r, n),
		ReadPTS:       clock.IndexPTS(r, read),
		FrameDuration: pf.FrameDuration(),
	})
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
