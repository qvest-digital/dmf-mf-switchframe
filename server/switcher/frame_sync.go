package switcher

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// syncRingSize is how many frames each source buffers beyond its delay and
// its hold. A tick reads clock.ReaderDelayGrains behind the clock, so a
// source at k times the program rate needs k*(ReaderDelayGrains+1) frames
// for one to be due; 16 covers five times the program rate and a reader's
// burst. Frames older than the due one are released on every tick, so a
// source at the program rate holds about ReaderDelayGrains+1 of them. A
// source placed ahead of its arrival, as an SRT stream is by its
// clock.TrackingAnchor, holds the frames that arrive in its hold as well.
const syncRingSize = 16

// FrameSynchronizer buffers each source's decoded frames by media time.
// Every tick takes, per source, the newest frame whose PTS is at or before
// the tick's read point; older frames are released and newer ones wait for
// a later tick. A source that delivers nothing keeps its last frame.
type FrameSynchronizer struct {
	mu      sync.Mutex
	sources map[string]*syncSource
	log     *slog.Logger
}

type syncSource struct {
	mu sync.Mutex
	// ring holds frames in ascending PTS order.
	ring []*ProcessingFrame
	// lastRawVideo is the frame the last tick took, kept for repeats.
	lastRawVideo *ProcessingFrame
	// lostAt is when a tick first found nothing new from the source since
	// it last delivered; zero while it delivers.
	lostAt time.Time
	// delay is the source's extra delay in 90 kHz ticks (SetSourceDelay).
	delay atomic.Int64
	// capacity is syncRingSize plus the frames the delay holds back; 0
	// means syncRingSize.
	capacity int
	// hold adds the frames the source's placement ahead of its arrival
	// holds back.
	hold clock.SourceHold

	overflows atomic.Int64 // frames released unread because the ring was full
	reordered atomic.Int64 // frames whose PTS did not follow the newest buffered
}

// NewFrameSynchronizer returns an empty synchronizer.
func NewFrameSynchronizer() *FrameSynchronizer {
	return &FrameSynchronizer{
		sources: make(map[string]*syncSource),
		log:     slog.With("component", "framesync"),
	}
}

// AddSource registers a source. Frames of unregistered sources are dropped.
func (fs *FrameSynchronizer) AddSource(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.sources[key]; !ok {
		fs.sources[key] = &syncSource{ring: make([]*ProcessingFrame, 0, syncRingSize), capacity: syncRingSize}
	}
}

// RemoveSource unregisters a source and releases its frames.
func (fs *FrameSynchronizer) RemoveSource(key string) {
	fs.mu.Lock()
	ss := fs.sources[key]
	delete(fs.sources, key)
	fs.mu.Unlock()
	if ss == nil {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, f := range ss.ring {
		f.ReleaseYUV()
	}
	ss.ring = nil
	if ss.lastRawVideo != nil {
		ss.lastRawVideo.ReleaseYUV()
		ss.lastRawVideo = nil
	}
}

// ReleaseFrames releases a source's buffered and last frames and keeps
// it registered with its delay: it shows black until it delivers again.
func (fs *FrameSynchronizer) ReleaseFrames(key string) {
	ss := fs.source(key)
	if ss == nil {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, f := range ss.ring {
		f.ReleaseYUV()
	}
	ss.ring = ss.ring[:0]
	if ss.lastRawVideo != nil {
		ss.lastRawVideo.ReleaseYUV()
		ss.lastRawVideo = nil
	}
}

func (fs *FrameSynchronizer) source(key string) *syncSource {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.sources[key]
}

// IngestRawVideo buffers a frame stamped with its media time. It consumes
// pf: the synchronizer releases it once a later frame has replaced it.
func (fs *FrameSynchronizer) IngestRawVideo(sourceKey string, pf *ProcessingFrame) {
	ss := fs.source(sourceKey)
	if ss == nil {
		pf.ReleaseYUV()
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.push(pf)
}

// SetSourceDelay delays a source by delayMs: ticks read it that much
// further behind the clock. The ring grows by the frames, at frameDur
// each, that the delay holds back.
func (fs *FrameSynchronizer) SetSourceDelay(sourceKey string, delayMs int, frameDur time.Duration) bool {
	ss := fs.source(sourceKey)
	if ss == nil {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.delay.Store(int64(delayMs) * 90)
	ss.capacity = syncRingSize
	if frameDur > 0 {
		ss.capacity += int((time.Duration(delayMs)*time.Millisecond + frameDur - 1) / frameDur)
	}
	return true
}

// push inserts pf in PTS order. A frame not after the newest buffered one
// means the source's timeline stepped back, as when its producer
// restarted: what was buffered from the old timeline is dropped. A full
// ring, at its capacity plus the source's hold, drops its oldest frame.
func (ss *syncSource) push(pf *ProcessingFrame) {
	if n := len(ss.ring); n > 0 && pf.PTS <= ss.ring[n-1].PTS {
		ss.reordered.Add(1)
		for _, f := range ss.ring {
			f.ReleaseYUV()
		}
		ss.ring = ss.ring[:0]
	}
	limit := ss.capacity
	if limit == 0 {
		limit = syncRingSize
	}
	limit += ss.hold.Frames(pf.PTS)
	for len(ss.ring) >= limit {
		ss.ring[0].ReleaseYUV()
		n := copy(ss.ring, ss.ring[1:])
		ss.ring = ss.ring[:n]
		ss.overflows.Add(1)
	}
	ss.ring = append(ss.ring, pf)
}

// popAt returns the newest buffered frame with PTS <= limit and releases
// the older ones, or nil when none is due. Newer frames stay buffered.
func (ss *syncSource) popAt(limit int64) *ProcessingFrame {
	i := -1
	for j, f := range ss.ring {
		if f.PTS > limit {
			break
		}
		i = j
	}
	if i < 0 {
		return nil
	}
	pf := ss.ring[i]
	for _, f := range ss.ring[:i] {
		f.ReleaseYUV()
	}
	n := copy(ss.ring, ss.ring[i+1:])
	ss.ring = ss.ring[:n]
	return pf
}

// DebugSnapshot reports each source's buffer state.
func (fs *FrameSynchronizer) DebugSnapshot() map[string]any {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	sources := make(map[string]any, len(fs.sources))
	for key, ss := range fs.sources {
		ss.mu.Lock()
		info := map[string]any{
			"buffered":  len(ss.ring),
			"delay_90k": ss.delay.Load(),
			"overflows": ss.overflows.Load(),
			"reordered": ss.reordered.Load(),
		}
		if n := len(ss.ring); n > 0 {
			info["oldest_pts"] = ss.ring[0].PTS
			info["newest_pts"] = ss.ring[n-1].PTS
		}
		ss.mu.Unlock()
		sources[key] = info
	}
	return map[string]any{"sources": sources}
}

// Buffered returns how many frames a source has waiting.
func (fs *FrameSynchronizer) Buffered(sourceKey string) int {
	ss := fs.source(sourceKey)
	if ss == nil {
		return 0
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.ring)
}

// SourceCount returns the number of registered sources.
func (fs *FrameSynchronizer) SourceCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.sources)
}
