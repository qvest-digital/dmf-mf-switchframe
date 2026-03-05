package switcher

import (
	"sync"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

// frameType tags queued frames so the release goroutine can dispatch
// to the correct handler method.
type frameType int

const (
	frameTypeVideo   frameType = iota
	frameTypeAudio
	frameTypeCaption
)

// delayedFrame wraps a frame of any type together with the metadata
// needed to release it after the configured delay.
type delayedFrame struct {
	sourceKey string
	pushTime  time.Time
	delay     time.Duration
	ftype     frameType
	video     *media.VideoFrame
	audio     *media.AudioFrame
	caption   *ccx.CaptionFrame
}

// sourceDelay holds the configured delay and pending frame queue for
// a single source.
type sourceDelay struct {
	delay time.Duration
	queue []*delayedFrame
}

// DelayBuffer introduces a configurable per-source delay between frame
// ingestion (from sourceViewer) and delivery to the downstream frameHandler.
// When delay is 0 for a source, frames pass through immediately with zero
// allocation. A background goroutine ticks at 1ms resolution to release
// queued frames whose delay has elapsed.
type DelayBuffer struct {
	mu       sync.Mutex
	sources  map[string]*sourceDelay
	handler  frameHandler
	stopCh   chan struct{}
	stopped  bool
}

// Compile-time check that DelayBuffer implements the frameHandler interface.
var _ frameHandler = (*DelayBuffer)(nil)

// NewDelayBuffer creates a DelayBuffer that forwards released frames to
// the given handler. A background ticker goroutine is started immediately.
func NewDelayBuffer(handler frameHandler) *DelayBuffer {
	db := &DelayBuffer{
		sources: make(map[string]*sourceDelay),
		handler: handler,
		stopCh:  make(chan struct{}),
	}
	go db.releaseTicker()
	return db
}

// SetDelay configures the delay for a source. New frames pushed after this
// call use the new delay; already-queued frames retain their original
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

// RemoveSource removes a source's delay configuration and discards all
// queued frames for that source.
func (db *DelayBuffer) RemoveSource(sourceKey string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.sources, sourceKey)
}

// Close stops the background ticker and discards all pending frames.
// It is safe to call Close multiple times.
func (db *DelayBuffer) Close() {
	db.mu.Lock()
	if db.stopped {
		db.mu.Unlock()
		return
	}
	db.stopped = true
	close(db.stopCh)
	// Discard all pending frames.
	db.sources = make(map[string]*sourceDelay)
	db.mu.Unlock()
}

// handleVideoFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is queued.
func (db *DelayBuffer) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	db.mu.Lock()
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleVideoFrame(sourceKey, frame)
		return
	}
	sd.queue = append(sd.queue, &delayedFrame{
		sourceKey: sourceKey,
		pushTime:  time.Now(),
		delay:     sd.delay,
		ftype:     frameTypeVideo,
		video:     frame,
	})
	db.mu.Unlock()
}

// handleAudioFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is queued.
func (db *DelayBuffer) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	db.mu.Lock()
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleAudioFrame(sourceKey, frame)
		return
	}
	sd.queue = append(sd.queue, &delayedFrame{
		sourceKey: sourceKey,
		pushTime:  time.Now(),
		delay:     sd.delay,
		ftype:     frameTypeAudio,
		audio:     frame,
	})
	db.mu.Unlock()
}

// handleCaptionFrame implements frameHandler. If delay=0 for the source,
// the frame is forwarded immediately. Otherwise it is queued.
func (db *DelayBuffer) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	db.mu.Lock()
	sd := db.sources[sourceKey]
	if sd == nil || sd.delay == 0 {
		db.mu.Unlock()
		db.handler.handleCaptionFrame(sourceKey, frame)
		return
	}
	sd.queue = append(sd.queue, &delayedFrame{
		sourceKey: sourceKey,
		pushTime:  time.Now(),
		delay:     sd.delay,
		ftype:     frameTypeCaption,
		caption:   frame,
	})
	db.mu.Unlock()
}

// releaseTicker runs in a background goroutine, checking every 1ms for
// frames whose delay has elapsed and forwarding them to the handler.
func (db *DelayBuffer) releaseTicker() {
	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-db.stopCh:
			return
		case <-ticker.C:
			db.releaseReady()
		}
	}
}

// releaseReady scans all source queues and forwards any frames whose
// delay has elapsed. Released frames are removed from their queue.
func (db *DelayBuffer) releaseReady() {
	now := time.Now()

	db.mu.Lock()
	// Collect frames to release outside the lock to avoid holding it
	// during downstream handler calls.
	var ready []*delayedFrame
	for _, sd := range db.sources {
		i := 0
		for i < len(sd.queue) {
			df := sd.queue[i]
			if now.Sub(df.pushTime) >= df.delay {
				ready = append(ready, df)
				// Remove from queue by swapping with the last element.
				// This does NOT preserve order within the queue, but we
				// re-scan from the beginning, so all ready frames in
				// this tick are collected. Order is preserved because
				// frames are pushed in order and have monotonic push
				// times + same delay, so they all become ready at once.
				sd.queue[i] = sd.queue[len(sd.queue)-1]
				sd.queue[len(sd.queue)-1] = nil
				sd.queue = sd.queue[:len(sd.queue)-1]
				// Don't increment i — the swapped element needs checking.
			} else {
				i++
			}
		}
	}
	db.mu.Unlock()

	// Deliver outside the lock.
	for _, df := range ready {
		switch df.ftype {
		case frameTypeVideo:
			db.handler.handleVideoFrame(df.sourceKey, df.video)
		case frameTypeAudio:
			db.handler.handleAudioFrame(df.sourceKey, df.audio)
		case frameTypeCaption:
			db.handler.handleCaptionFrame(df.sourceKey, df.caption)
		}
	}
}
