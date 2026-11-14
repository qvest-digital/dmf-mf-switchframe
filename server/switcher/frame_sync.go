package switcher

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// syncRingSize is how many frames each source buffers beyond its delay and
// its hold while no realtime clock has set the program rate.
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
	// delayFrames is how many frames the delay holds back at the program
	// rate, counted while no realtime clock has set it.
	delayFrames int
	// hold adds the frames not yet read: those the source's delay and its
	// placement ahead of its arrival hold back, counted while no realtime
	// clock has set the program rate.
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
		fs.sources[key] = &syncSource{ring: make([]*ProcessingFrame, 0, syncRingSize)}
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
	ss.delayFrames = 0
	if frameDur > 0 {
		ss.delayFrames = int((time.Duration(delayMs)*time.Millisecond + frameDur - 1) / frameDur)
	}
	return true
}

// push inserts pf in PTS order. A frame not after the newest buffered one
// means the source's timeline stepped back: its producer restarted, or its
// TrackingAnchor lowered its offset. The buffered frames placed at or after
// pf came before it and are dropped, so the source's frames stay in their
// own order; those placed before it are still shown before it, so a small
// step costs the frames it skips and not a wait for pf.
//
// Once a realtime clock runs, the ring keeps the frames a tick still to
// come may show: the newest one at or before the oldest read point such a
// tick has, the current time less the read lag and the source's delay,
// and every one after it. The clock delivers a tick late after a stall,
// but the sync loop sheds the video of a tick taken after its grain ended,
// so no tick reads further back. A full ring drops its oldest frame. It
// holds, at the faster of the program rate and the source's, which the
// ring's PTS give, the frames from that read point to the furthest a
// TrackingAnchor places a frame after its arrival, clock.MaxSourceHold
// past the current time, and one more. Before a realtime clock sets the
// program rate it holds syncRingSize frames, those the delay holds back
// and those the source's hold counts.
func (ss *syncSource) push(pf *ProcessingFrame) {
	if n := len(ss.ring); n > 0 && pf.PTS <= ss.ring[n-1].PTS {
		ss.reordered.Add(1)
		i := n
		for i > 0 && ss.ring[i-1].PTS >= pf.PTS {
			i--
		}
		for _, f := range ss.ring[i:] {
			f.ReleaseYUV()
		}
		clear(ss.ring[i:])
		ss.ring = ss.ring[:i]
	}
	ss.ring = append(ss.ring, pf)
	delay := ss.delay.Load()
	var limit int
	if lag, period := clock.ReadLag(), clock.ProgramPeriod(); lag > 0 && period > 0 {
		oldest := clock.NowPTS() - lag - delay
		i := 0
		for i+1 < len(ss.ring) && ss.ring[i+1].PTS <= oldest {
			i++
		}
		ss.release(i)
		window := lag + delay + clock.MaxSourceHold
		frames := (window + period - 1) / period
		if n := len(ss.ring); n > 1 {
			if span := ss.ring[n-1].PTS - ss.ring[0].PTS; span > 0 {
				frames = max(frames, (window*int64(n-1)+span-1)/span)
			}
		}
		limit = int(frames) + 1
	} else {
		limit = syncRingSize + ss.delayFrames + ss.hold.Frames(pf.PTS, delay)
	}
	for len(ss.ring) > limit {
		ss.release(1)
		ss.overflows.Add(1)
	}
}

// release releases the ring's oldest n frames.
func (ss *syncSource) release(n int) {
	for _, f := range ss.ring[:n] {
		f.ReleaseYUV()
	}
	k := copy(ss.ring, ss.ring[n:])
	clear(ss.ring[k:])
	ss.ring = ss.ring[:k]
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
