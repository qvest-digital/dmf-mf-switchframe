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
	defaultFmt := DefaultFormat
	s := &Switcher{
		log:           slog.With("component", "switcher"),
		sources:       make(map[string]*sourceState),
		programRelay:  programRelay,
		health:        newHealthMonitor(),
		videoProcCh:   make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
		framePool:     NewFramePool(4, 320, 240),
	}
	s.frameBudgetNs.Store(defaultFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&defaultFmt)
	s.delayBuffer = NewDelayBuffer(s)
	// Bypass pacer in tests for synchronous frame delivery.
	// Production uses the real frame duration (~33ms at 30fps).
	s.pacer = newBypassPacer(s.deliverPacedFrame)
	go s.videoProcessingLoop()
	return s
}
