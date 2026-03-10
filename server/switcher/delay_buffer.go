package switcher

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

// sourceDelay holds the configured delay for a single source.
// The generation counter is incremented on RemoveSource so that
// in-flight time.AfterFunc callbacks for removed sources are discarded.
// generation is atomic so callbacks can check it without locking.
type sourceDelay struct {
	delay      time.Duration
	generation atomic.Uint64
}

// DelayBuffer introduces a configurable per-source delay between frame
// ingestion (from sourceViewer) and delivery to the downstream frameHandler.
// When delay is 0 for a source, frames pass through immediately with zero
// allocation. Delayed frames are scheduled via time.AfterFunc, eliminating
// the need for a background polling goroutine.
type DelayBuffer struct {
	hasAnyDelay atomic.Bool
	stopped     atomic.Bool
	mu          sync.Mutex
	sources     map[string]*sourceDelay
	handler     frameHandler
	done        chan struct{} // closed on Close() for compatibility
}

// Compile-time check that DelayBuffer implements the frameHandler interface.
var _ frameHandler = (*DelayBuffer)(nil)

// NewDelayBuffer creates a DelayBuffer that forwards released frames to
// the given handler. No background goroutine is started; delayed frames
// are scheduled individually via time.AfterFunc.
func NewDelayBuffer(handler frameHandler) *DelayBuffer {
	return &DelayBuffer{
		sources: make(map[string]*sourceDelay),
		handler: handler,
		done:    make(chan struct{}),
	}
}

// updateHasAnyDelay scans all sources and sets the atomic flag. Must be
// called with db.mu held.
func (db *DelayBuffer) updateHasAnyDelay() {
	for _, sd := range db.sources {
		if sd.delay > 0 {
			db.hasAnyDelay.Store(true)
			return
		}
	}
	db.hasAnyDelay.Store(false)
}

// SetDelay configures the delay for a source. New frames pushed after this
// call use the new delay; already-scheduled frames retain their original
// scheduled release time.
func (db *DelayBuffer) SetDelay(sourceKey string, delay time.Duration) {
	db.mu.Lock()
	defer db.mu.Unlock()
	sd, ok := db.sources[sourceKey]
	if !ok {
		sd = &sourceDelay{}
		db.sources[sourceKey] = sd
	}
	sd.delay = delay
	db.updateHasAnyDelay()
}

// GetDelay returns the configured delay for a source, or 0 if not set.
func (db *DelayBuffer) GetDelay(sourceKey string) time.Duration {
	db.mu.Lock()
	defer db.mu.Unlock()
	sd, ok := db.sources[sourceKey]
	if !ok {
		return 0
	}
	return sd.delay
}

// RemoveSource removes a source's delay configuration. Any in-flight
// time.AfterFunc callbacks for this source will detect the generation
// mismatch and discard the frame.
func (db *DelayBuffer) RemoveSource(sourceKey string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if sd, ok := db.sources[sourceKey]; ok {
		sd.generation.Add(1)
	}
	delete(db.sources, sourceKey)
	db.updateHasAnyDelay()
}

// Close marks the buffer as stopped. Any in-flight time.AfterFunc callbacks
// will check the stopped flag and discard frames. It is safe to call Close
// multiple times.
func (db *DelayBuffer) Close() {
	if !db.stopped.CompareAndSwap(false, true) {
		return
	}
	db.mu.Lock()
	db.sources = make(map[string]*sourceDelay)
	db.mu.Unlock()
	close(db.done)
}

// handleVideoFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is scheduled via
// time.AfterFunc for release after the configured delay.
func (db *DelayBuffer) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	if !db.hasAnyDelay.Load() {
		db.handler.handleVideoFrame(sourceKey, frame)
		return
	}
	db.mu.Lock()
	if db.stopped.Load() {
		db.mu.Unlock()
		return
	}
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleVideoFrame(sourceKey, frame)
		return
	}
	delay := sd.delay
	gen := sd.generation.Load()
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		if db.stopped.Load() {
			return
		}
		if sd.generation.Load() != gen {
			return
		}
		db.handler.handleVideoFrame(sourceKey, frame)
	})
}

// handleRawVideoFrame implements frameHandler for decoded YUV frames.
// Uses the same delay mechanism as handleVideoFrame.
// Releases the pool buffer after delivery or on early return (stopped/stale).
func (db *DelayBuffer) handleRawVideoFrame(sourceKey string, pf *ProcessingFrame) {
	if !db.hasAnyDelay.Load() {
		db.handler.handleRawVideoFrame(sourceKey, pf)
		pf.ReleaseYUV()
		return
	}
	db.mu.Lock()
	if db.stopped.Load() {
		db.mu.Unlock()
		pf.ReleaseYUV()
		return
	}
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleRawVideoFrame(sourceKey, pf)
		pf.ReleaseYUV()
		return
	}
	delay := sd.delay
	gen := sd.generation.Load()
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		if db.stopped.Load() {
			pf.ReleaseYUV()
			return
		}
		if sd.generation.Load() != gen {
			pf.ReleaseYUV()
			return
		}
		db.handler.handleRawVideoFrame(sourceKey, pf)
		pf.ReleaseYUV()
	})
}

// handleAudioFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is scheduled via
// time.AfterFunc for release after the configured delay.
func (db *DelayBuffer) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	if !db.hasAnyDelay.Load() {
		db.handler.handleAudioFrame(sourceKey, frame)
		return
	}
	db.mu.Lock()
	if db.stopped.Load() {
		db.mu.Unlock()
		return
	}
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleAudioFrame(sourceKey, frame)
		return
	}
	delay := sd.delay
	gen := sd.generation.Load()
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		if db.stopped.Load() {
			return
		}
		if sd.generation.Load() != gen {
			return
		}
		db.handler.handleAudioFrame(sourceKey, frame)
	})
}

// handleCaptionFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is scheduled via
// time.AfterFunc for release after the configured delay.
func (db *DelayBuffer) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	if !db.hasAnyDelay.Load() {
		db.handler.handleCaptionFrame(sourceKey, frame)
		return
	}
	db.mu.Lock()
	if db.stopped.Load() {
		db.mu.Unlock()
		return
	}
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleCaptionFrame(sourceKey, frame)
		return
	}
	delay := sd.delay
	gen := sd.generation.Load()
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		if db.stopped.Load() {
			return
		}
		if sd.generation.Load() != gen {
			return
		}
		db.handler.handleCaptionFrame(sourceKey, frame)
	})
}
