package switcher

import (
	"log/slog"

	"github.com/zsiec/prism/distribution"
)

// NewTestSwitcher creates a Switcher with a tiny frame pool (4 × 320×240)
// suitable for testing. The production New() allocates 512 × 1080p buffers
// (~1.5 GB) which causes OOM kills when many tests each create one.
//
// This function is intended for use in other packages' tests (e.g.,
// control API tests) that need a Switcher but don't exercise video
// processing at full resolution.
func NewTestSwitcher(programRelay *distribution.Relay) *Switcher {
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
		log:           slog.With("component", "switcher"),
		sources:       make(map[string]*sourceState),
		programRelay:  programRelay,
		health:        newHealthMonitor(),
		videoProcCh:   make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
		framePool:     NewFramePool(4, 320, 240),
	}
	s.frameBudgetNs.Store(testFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&testFmt)
	s.delayBuffer = NewDelayBuffer(s)
	go s.videoProcessingLoop()
	return s
}
