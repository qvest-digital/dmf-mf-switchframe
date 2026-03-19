package switcher

import (
	"sync"
	"time"

	"github.com/zsiec/prism/media"
)

// pacerSnapshot holds diagnostic counters for the frame pacer.
type pacerSnapshot struct {
	paced      int64 // frames delivered on tick
	emptyTicks int64 // ticks with no pending frame
	replaced   int64 // always 0 for FIFO pacer (kept for interface compat)
	queueDepth int64 // current queue depth
}

// framePacer smooths frame delivery using a FIFO queue with a fixed-cadence
// ticker. Every submitted frame is delivered in order — none are dropped.
// This is critical for H.264 where P-frames depend on previous reference
// frames; dropping any frame breaks the decode chain.
//
// When bypass is true, submit() calls deliver() synchronously (no ticker
// goroutine). Used in tests for deterministic frame delivery.
type framePacer struct {
	deliver  func(f *media.VideoFrame)
	interval time.Duration
	bypass   bool

	mu    sync.Mutex
	queue []*media.VideoFrame

	// diagnostics (accessed under mu)
	pacedCount int64
	emptyCount int64

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
	close(p.doneCh)
	return p
}

func (p *framePacer) run() {
	defer close(p.doneCh)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			// Drain all remaining queued frames on stop
			p.mu.Lock()
			remaining := p.queue
			p.queue = nil
			p.mu.Unlock()
			for _, f := range remaining {
				p.deliver(f)
				p.mu.Lock()
				p.pacedCount++
				p.mu.Unlock()
			}
			return
		case <-ticker.C:
			p.mu.Lock()
			if len(p.queue) > 0 {
				f := p.queue[0]
				p.queue = p.queue[1:]
				p.pacedCount++
				p.mu.Unlock()
				p.deliver(f)
			} else {
				p.emptyCount++
				p.mu.Unlock()
			}
		}
	}
}

// submit enqueues a frame for delivery on the next tick. All frames are
// delivered in FIFO order — none are dropped or replaced.
func (p *framePacer) submit(f *media.VideoFrame) {
	if p.bypass {
		p.deliver(f)
		p.mu.Lock()
		p.pacedCount++
		p.mu.Unlock()
		return
	}
	select {
	case <-p.stopCh:
		return // pacer stopped, discard
	default:
	}
	p.mu.Lock()
	p.queue = append(p.queue, f)
	p.mu.Unlock()
}

func (p *framePacer) stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
	})
}

func (p *framePacer) snapshot() pacerSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pacerSnapshot{
		paced:      p.pacedCount,
		emptyTicks: p.emptyCount,
		replaced:   0,
		queueDepth: int64(len(p.queue)),
	}
}
