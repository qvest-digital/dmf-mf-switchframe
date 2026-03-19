package switcher

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
)

// pacerSnapshot holds diagnostic counters for the frame pacer.
type pacerSnapshot struct {
	paced      int64 // frames delivered on tick
	emptyTicks int64 // ticks with no pending frame
	replaced   int64 // frames replaced before delivery (newest-wins)
}

// framePacer smooths frame delivery by holding a single pending frame
// and releasing it on a fixed-cadence ticker. This prevents bursty
// encode timing from causing uneven frame delivery to subscribers.
//
// When bypass is true, submit() calls deliver() synchronously (no
// ticker goroutine). Used in tests for deterministic frame delivery.
type framePacer struct {
	pending  atomic.Pointer[media.VideoFrame]
	deliver  func(f *media.VideoFrame)
	interval time.Duration
	bypass   bool

	// diagnostics
	pacedCount    atomic.Int64
	emptyTicks    atomic.Int64
	replacedCount atomic.Int64

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func newFramePacer(interval time.Duration, deliver func(f *media.VideoFrame)) *framePacer {
	p := &framePacer{
		deliver:  deliver,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go p.run()
	return p
}

// newBypassPacer creates a pacer that delivers frames synchronously in
// submit(). No ticker goroutine is started. Used in tests for deterministic
// frame delivery without timing dependencies.
func newBypassPacer(deliver func(f *media.VideoFrame)) *framePacer {
	p := &framePacer{
		deliver: deliver,
		bypass:  true,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	close(p.doneCh) // already "done" — no goroutine to wait for
	return p
}

func (p *framePacer) run() {
	defer close(p.doneCh)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			// Deliver any final pending frame
			if f := p.pending.Swap(nil); f != nil {
				p.deliver(f)
				p.pacedCount.Add(1)
			}
			return
		case <-ticker.C:
			f := p.pending.Swap(nil)
			if f != nil {
				p.deliver(f)
				p.pacedCount.Add(1)
			} else {
				p.emptyTicks.Add(1)
			}
		}
	}
}

// submit stores a frame for delivery on the next tick. If a frame is
// already pending, it is replaced (newest-wins, no lock needed).
// In bypass mode, delivers synchronously.
func (p *framePacer) submit(f *media.VideoFrame) {
	if p.bypass {
		p.deliver(f)
		p.pacedCount.Add(1)
		return
	}
	old := p.pending.Swap(f)
	if old != nil {
		p.replacedCount.Add(1)
	}
}

func (p *framePacer) stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
	})
}

func (p *framePacer) snapshot() pacerSnapshot {
	return pacerSnapshot{
		paced:      p.pacedCount.Load(),
		emptyTicks: p.emptyTicks.Load(),
		replaced:   p.replacedCount.Load(),
	}
}
