package switcher

import (
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
)

const (
	// syncRingSize is the number of slots in the per-source ring buffer.
	// Two slots allows one frame to be consumed while the next arrives,
	// preventing jitter from causing drops under normal conditions.
	syncRingSize = 2
)

// syncSource holds per-source buffering state for the FrameSynchronizer.
type syncSource struct {
	// pendingVideo is a fixed-size ring buffer for incoming video frames.
	pendingVideo [syncRingSize]*media.VideoFrame
	videoHead    int // write index into pendingVideo
	videoCount   int // number of valid frames in ring

	// pendingAudio mirrors video buffering for audio frames.
	pendingAudio [syncRingSize]*media.AudioFrame
	audioHead    int
	audioCount   int

	// lastVideo/lastAudio are the most recently released frames.
	// Used for freeze behavior: if no new frame arrived since last tick,
	// the last frame is repeated to maintain continuous output.
	lastVideo *media.VideoFrame
	lastAudio *media.AudioFrame
}

// pushVideo adds a video frame to the ring buffer, overwriting the oldest
// if full.
func (ss *syncSource) pushVideo(frame *media.VideoFrame) {
	ss.pendingVideo[ss.videoHead] = frame
	ss.videoHead = (ss.videoHead + 1) % syncRingSize
	if ss.videoCount < syncRingSize {
		ss.videoCount++
	}
}

// popNewestVideo returns the most recently pushed video frame and clears
// the ring buffer. Returns nil if no frames are buffered.
func (ss *syncSource) popNewestVideo() *media.VideoFrame {
	if ss.videoCount == 0 {
		return nil
	}
	// The newest frame is at (videoHead - 1 + syncRingSize) % syncRingSize.
	newest := (ss.videoHead - 1 + syncRingSize) % syncRingSize
	frame := ss.pendingVideo[newest]
	// Clear the ring.
	for i := range ss.pendingVideo {
		ss.pendingVideo[i] = nil
	}
	ss.videoHead = 0
	ss.videoCount = 0
	return frame
}

// pushAudio adds an audio frame to the ring buffer.
func (ss *syncSource) pushAudio(frame *media.AudioFrame) {
	ss.pendingAudio[ss.audioHead] = frame
	ss.audioHead = (ss.audioHead + 1) % syncRingSize
	if ss.audioCount < syncRingSize {
		ss.audioCount++
	}
}

// popNewestAudio returns the most recently pushed audio frame and clears
// the ring buffer.
func (ss *syncSource) popNewestAudio() *media.AudioFrame {
	if ss.audioCount == 0 {
		return nil
	}
	newest := (ss.audioHead - 1 + syncRingSize) % syncRingSize
	frame := ss.pendingAudio[newest]
	for i := range ss.pendingAudio {
		ss.pendingAudio[i] = nil
	}
	ss.audioHead = 0
	ss.audioCount = 0
	return frame
}

// FrameSynchronizer aligns frames from multiple sources to a common frame
// boundary ("freerun sync"). Each source has a 2-frame ring buffer. A
// background ticker at the program frame rate releases the most recent
// buffered frame from each source on every tick. If no new frame arrived
// since the last tick, the previous frame is repeated (freeze behavior).
//
// Frame PTS values are rewritten to the tick timestamp to ensure consistent
// timing across all sources in the output.
type FrameSynchronizer struct {
	mu       sync.Mutex
	sources  map[string]*syncSource
	tickRate time.Duration
	onVideo  func(sourceKey string, frame media.VideoFrame)
	onAudio  func(sourceKey string, frame media.AudioFrame)
	done     chan struct{}
	started  bool
	stopped  bool
	tickNum  int64 // monotonic tick counter for PTS generation
}

// NewFrameSynchronizer creates a FrameSynchronizer with the given tick rate
// and output callbacks. The ticker is NOT started automatically — call
// Start() to begin releasing frames.
func NewFrameSynchronizer(
	tickRate time.Duration,
	onVideo func(sourceKey string, frame media.VideoFrame),
	onAudio func(sourceKey string, frame media.AudioFrame),
) *FrameSynchronizer {
	return &FrameSynchronizer{
		sources:  make(map[string]*syncSource),
		tickRate: tickRate,
		onVideo:  onVideo,
		onAudio:  onAudio,
		done:     make(chan struct{}),
	}
}

// AddSource registers a source for frame synchronization. Safe to call
// while the ticker is running.
func (fs *FrameSynchronizer) AddSource(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, exists := fs.sources[key]; exists {
		return
	}
	fs.sources[key] = &syncSource{}
	slog.Debug("frame_sync: source added", "key", key)
}

// RemoveSource unregisters a source and discards any buffered frames.
func (fs *FrameSynchronizer) RemoveSource(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.sources, key)
	slog.Debug("frame_sync: source removed", "key", key)
}

// IngestVideo buffers an incoming video frame for the specified source.
// If the source is not registered, the frame is silently dropped.
func (fs *FrameSynchronizer) IngestVideo(sourceKey string, frame media.VideoFrame) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ss, ok := fs.sources[sourceKey]
	if !ok {
		return
	}
	ss.pushVideo(&frame)
}

// IngestAudio buffers an incoming audio frame for the specified source.
func (fs *FrameSynchronizer) IngestAudio(sourceKey string, frame media.AudioFrame) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ss, ok := fs.sources[sourceKey]
	if !ok {
		return
	}
	ss.pushAudio(&frame)
}

// SetTickRate updates the tick rate. Takes effect on the next tick cycle.
// This is used when auto-detecting frame rate from source streams.
func (fs *FrameSynchronizer) SetTickRate(d time.Duration) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.tickRate = d
	slog.Debug("frame_sync: tick rate updated", "rate", d)
}

// Start begins the background ticker goroutine that releases frames at
// the configured tick rate. Calling Start multiple times is safe (no-op
// after first call).
func (fs *FrameSynchronizer) Start() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.started {
		return
	}
	fs.started = true
	go fs.tickLoop()
}

// Stop halts the background ticker. Safe to call multiple times.
func (fs *FrameSynchronizer) Stop() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.stopped {
		return
	}
	fs.stopped = true
	close(fs.done)
}

// tickLoop is the background goroutine that runs the ticker.
func (fs *FrameSynchronizer) tickLoop() {
	fs.mu.Lock()
	rate := fs.tickRate
	fs.mu.Unlock()

	ticker := time.NewTicker(rate)
	defer ticker.Stop()

	for {
		select {
		case <-fs.done:
			return
		case <-ticker.C:
			fs.mu.Lock()
			newRate := fs.tickRate
			fs.mu.Unlock()

			// If tick rate changed, reset the ticker.
			if newRate != rate {
				ticker.Stop()
				rate = newRate
				ticker = time.NewTicker(rate)
			}

			fs.releaseTick()
		}
	}
}

// releaseTick releases one frame per source. For each source:
// - If new frames are buffered, release the newest and update lastFrame.
// - If no new frames, repeat the last frame (freeze).
// - If no frame has ever been received, skip.
//
// PTS is rewritten to a monotonic tick-based timestamp.
func (fs *FrameSynchronizer) releaseTick() {
	fs.mu.Lock()
	fs.tickNum++
	tickPTS := fs.tickNum * int64(fs.tickRate)

	// Collect frames to release outside the lock.
	type pendingRelease struct {
		sourceKey string
		video     *media.VideoFrame
		audio     *media.AudioFrame
	}
	var releases []pendingRelease

	for key, ss := range fs.sources {
		var releaseVideo *media.VideoFrame
		var releaseAudio *media.AudioFrame

		// Video: pop newest from ring, or repeat last.
		if newest := ss.popNewestVideo(); newest != nil {
			ss.lastVideo = newest
			releaseVideo = newest
		} else if ss.lastVideo != nil {
			releaseVideo = ss.lastVideo
		}

		// Audio: pop newest from ring, or repeat last.
		if newest := ss.popNewestAudio(); newest != nil {
			ss.lastAudio = newest
			releaseAudio = newest
		} else if ss.lastAudio != nil {
			releaseAudio = ss.lastAudio
		}

		if releaseVideo != nil || releaseAudio != nil {
			releases = append(releases, pendingRelease{
				sourceKey: key,
				video:     releaseVideo,
				audio:     releaseAudio,
			})
		}
	}
	fs.mu.Unlock()

	// Deliver outside the lock to prevent deadlocks with downstream handlers.
	for _, r := range releases {
		if r.video != nil {
			// Copy the frame to rewrite PTS without mutating the original.
			vf := *r.video
			vf.PTS = tickPTS
			fs.onVideo(r.sourceKey, vf)
		}
		if r.audio != nil {
			af := *r.audio
			af.PTS = tickPTS
			fs.onAudio(r.sourceKey, af)
		}
	}
}
