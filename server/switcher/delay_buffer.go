package switcher

import (
	"sync"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

// sourceDelay holds the configured delay for a single source.
// The generation counter is incremented on RemoveSource so that
// in-flight time.AfterFunc callbacks for removed sources are discarded.
type sourceDelay struct {
	delay      time.Duration
	generation uint64
}

// DelayBuffer introduces a configurable per-source delay between frame
// ingestion (from sourceViewer) and delivery to the downstream frameHandler.
// When delay is 0 for a source, frames pass through immediately with zero
// allocation. Delayed frames are scheduled via time.AfterFunc, eliminating
// the need for a background polling goroutine.
type DelayBuffer struct {
	mu      sync.Mutex
	sources map[string]*sourceDelay
	handler frameHandler
	done    chan struct{} // closed on Close() for compatibility
	stopped bool
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
		sd.generation++ // invalidate in-flight timers
	}
	delete(db.sources, sourceKey)
}

// Close marks the buffer as stopped. Any in-flight time.AfterFunc callbacks
// will check the stopped flag and discard frames. It is safe to call Close
// multiple times.
func (db *DelayBuffer) Close() {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.stopped {
		return
	}
	db.stopped = true
	db.sources = make(map[string]*sourceDelay)
	close(db.done)
}

// handleVideoFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is scheduled via
// time.AfterFunc for release after the configured delay.
func (db *DelayBuffer) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	db.mu.Lock()
	if db.stopped {
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
	gen := sd.generation
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		db.mu.Lock()
		if db.stopped {
			db.mu.Unlock()
			return
		}
		curSD := db.sources[sourceKey]
		if curSD == nil || curSD.generation != gen {
			db.mu.Unlock()
			return
		}
		db.mu.Unlock()
		db.handler.handleVideoFrame(sourceKey, frame)
	})
}

// handleAudioFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is scheduled via
// time.AfterFunc for release after the configured delay.
func (db *DelayBuffer) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	db.mu.Lock()
	if db.stopped {
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
	gen := sd.generation
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		db.mu.Lock()
		if db.stopped {
			db.mu.Unlock()
			return
		}
		curSD := db.sources[sourceKey]
		if curSD == nil || curSD.generation != gen {
			db.mu.Unlock()
			return
		}
		db.mu.Unlock()
		db.handler.handleAudioFrame(sourceKey, frame)
	})
}

// handleCaptionFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is scheduled via
// time.AfterFunc for release after the configured delay.
func (db *DelayBuffer) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	db.mu.Lock()
	if db.stopped {
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
	gen := sd.generation
	db.mu.Unlock()

	time.AfterFunc(delay, func() {
		db.mu.Lock()
		if db.stopped {
			db.mu.Unlock()
			return
		}
		curSD := db.sources[sourceKey]
		if curSD == nil || curSD.generation != gen {
			db.mu.Unlock()
			return
		}
		db.mu.Unlock()
		db.handler.handleCaptionFrame(sourceKey, frame)
	})
}
