package switcher

import (
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
)

// mpegtsClock is the MPEG-TS 90 kHz clock rate used for PTS values.
const mpegtsClock = 90000

const (
	// syncRingSize is the number of slots in the per-source ring buffer.
	// Two slots allows one frame to be consumed while the next arrives,
	// preventing jitter from causing drops under normal conditions.
	syncRingSize = 2
)

// pendingRelease holds a frame pair collected under the lock for delivery
// outside the lock. The slice is reused across ticks to avoid allocation.
type pendingRelease struct {
	sourceKey string
	ss        *syncSource // for PTS tracking during delivery
	video     *media.VideoFrame
	rawVideo  *ProcessingFrame
	audio     *media.AudioFrame
}

// syncSource holds per-source buffering state for the FrameSynchronizer.
type syncSource struct {
	mu sync.Mutex // per-source lock; protects ring buffers and last-frame state

	// pendingVideo is a fixed-size ring buffer for incoming video frames.
	pendingVideo [syncRingSize]*media.VideoFrame
	videoHead    int // write index into pendingVideo
	videoCount   int // number of valid frames in ring

	// pendingAudio mirrors video buffering for audio frames.
	pendingAudio [syncRingSize]*media.AudioFrame
	audioHead    int
	audioCount   int

	// pendingRawVideo is a ring buffer for decoded YUV frames (from sourceDecoder).
	pendingRawVideo [syncRingSize]*ProcessingFrame
	rawVideoHead    int
	rawVideoCount   int

	// lastVideo/lastAudio/lastRawVideo are the most recently released frames.
	// Used for freeze behavior: if no new frame arrived since last tick,
	// the last frame is repeated to maintain continuous output.
	lastVideo    *media.VideoFrame
	lastRawVideo *ProcessingFrame
	lastAudio    *media.AudioFrame

	// audioMissCount tracks consecutive ticks with no new audio frame.
	// After 2 repeated frames, audio emission stops to prevent a glitch loop
	// with encoded AAC (which sounds worse than silence).
	audioMissCount int

	// lastReleasedPTS tracks the PTS of the last video frame released by this
	// source. Used to generate monotonic PTS for repeated/frozen frames while
	// preserving original source PTS for fresh frames (A/V sync with audio).
	lastReleasedPTS int64
	ptsInitialized  bool

	// frc holds per-source frame rate conversion state. nil when FRC is disabled.
	frc *frcSource
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

// pushRawVideo adds a decoded YUV frame to the ring buffer.
func (ss *syncSource) pushRawVideo(pf *ProcessingFrame) {
	ss.pendingRawVideo[ss.rawVideoHead] = pf
	ss.rawVideoHead = (ss.rawVideoHead + 1) % syncRingSize
	if ss.rawVideoCount < syncRingSize {
		ss.rawVideoCount++
	}
}

// popNewestRawVideo returns the most recently pushed raw video frame.
func (ss *syncSource) popNewestRawVideo() *ProcessingFrame {
	if ss.rawVideoCount == 0 {
		return nil
	}
	newest := (ss.rawVideoHead - 1 + syncRingSize) % syncRingSize
	frame := ss.pendingRawVideo[newest]
	for i := range ss.pendingRawVideo {
		ss.pendingRawVideo[i] = nil
	}
	ss.rawVideoHead = 0
	ss.rawVideoCount = 0
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
	log        *slog.Logger
	mu         sync.Mutex
	sources    map[string]*syncSource
	tickRate   time.Duration
	fpsNum     int // rational FPS numerator (e.g. 30000 for 29.97fps)
	fpsDen     int // rational FPS denominator (e.g. 1001 for 29.97fps)
	onVideo    func(sourceKey string, frame media.VideoFrame)
	onRawVideo func(sourceKey string, pf *ProcessingFrame)
	onAudio    func(sourceKey string, frame media.AudioFrame)
	done       chan struct{}
	wg         sync.WaitGroup
	started    bool
	stopped    bool
	tickNum    int64            // monotonic tick counter for PTS generation
	releases   []pendingRelease // reused across ticks to avoid allocation
	frcQuality FRCQuality       // FRC quality level for new sources
}

// NewFrameSynchronizer creates a FrameSynchronizer with the given tick rate
// and output callbacks. The ticker is NOT started automatically — call
// Start() to begin releasing frames.
func NewFrameSynchronizer(
	tickRate time.Duration,
	onVideo func(sourceKey string, frame media.VideoFrame),
	onAudio func(sourceKey string, frame media.AudioFrame),
) *FrameSynchronizer {
	// Derive rational FPS from tickRate as default. Callers should use
	// SetTickRateRational for exact values when a PipelineFormat is available.
	fpsNum, fpsDen := tickRateToRational(tickRate)
	return &FrameSynchronizer{
		log:      slog.With("component", "framesync"),
		sources:  make(map[string]*syncSource),
		tickRate: tickRate,
		fpsNum:   fpsNum,
		fpsDen:   fpsDen,
		onVideo:  onVideo,
		onAudio:  onAudio,
		done:     make(chan struct{}),
	}
}

// tickRateToRational maps a tick duration to the nearest standard broadcast
// frame rate rational. Falls back to direct computation for non-standard rates.
func tickRateToRational(d time.Duration) (int, int) {
	type rate struct {
		num, den int
		ns       int64 // time.Duration(den) * time.Second / time.Duration(num)
	}
	standards := []rate{
		{24000, 1001, 41708333},
		{24, 1, 41666666},
		{25, 1, 40000000},
		{30000, 1001, 33366666},
		{30, 1, 33333333},
		{50, 1, 20000000},
		{60000, 1001, 16683333},
		{60, 1, 16666666},
	}
	ns := d.Nanoseconds()
	for _, s := range standards {
		// Match within 1µs to handle truncation from FrameDuration().
		if diff := ns - s.ns; diff >= -1000 && diff <= 1000 {
			return s.num, s.den
		}
	}
	// Non-standard rate: approximate as integer FPS.
	if ns > 0 {
		fps := int((int64(time.Second) + ns/2) / ns)
		if fps < 1 {
			fps = 1
		}
		return fps, 1
	}
	return 30, 1
}

// AddSource registers a source for frame synchronization. Safe to call
// while the ticker is running.
func (fs *FrameSynchronizer) AddSource(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, exists := fs.sources[key]; exists {
		return
	}
	ss := &syncSource{}
	if fs.frcQuality != FRCNone {
		ss.frc = newFRCSource(fs.frcQuality, fs.tickPTSInterval())
	}
	fs.sources[key] = ss
	fs.log.Debug("source added", "key", key)
}

// RemoveSource unregisters a source and discards any buffered frames.
func (fs *FrameSynchronizer) RemoveSource(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.sources, key)
	fs.log.Debug("source removed", "key", key)
}

// IngestVideo buffers an incoming video frame for the specified source.
// If the source is not registered, the frame is silently dropped.
// Takes a pointer to avoid value copy heap escape on the hot path.
func (fs *FrameSynchronizer) IngestVideo(sourceKey string, frame *media.VideoFrame) {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	fs.mu.Unlock()
	if !ok {
		return
	}
	ss.mu.Lock()
	ss.pushVideo(frame)
	ss.mu.Unlock()
}

// IngestAudio buffers an incoming audio frame for the specified source.
// Takes a pointer to avoid value copy heap escape on the hot path.
func (fs *FrameSynchronizer) IngestAudio(sourceKey string, frame *media.AudioFrame) {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	fs.mu.Unlock()
	if !ok {
		return
	}
	ss.mu.Lock()
	ss.pushAudio(frame)
	ss.mu.Unlock()
}

// IngestRawVideo buffers a decoded YUV frame for the specified source.
func (fs *FrameSynchronizer) IngestRawVideo(sourceKey string, pf *ProcessingFrame) {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	fs.mu.Unlock()
	if !ok {
		return
	}
	ss.mu.Lock()
	ss.pushRawVideo(pf)
	if ss.frc != nil {
		ss.frc.ingest(pf)
	}
	ss.mu.Unlock()
}

// tickPTSInterval returns the tick interval in 90 kHz PTS units using
// rational arithmetic. Must be called with fs.mu held.
func (fs *FrameSynchronizer) tickPTSInterval() int64 {
	if fs.fpsNum > 0 {
		return int64(mpegtsClock) * int64(fs.fpsDen) / int64(fs.fpsNum)
	}
	// Fallback (should not happen): derive from tickRate.
	return int64(fs.tickRate) * mpegtsClock / int64(time.Second)
}

// SetTickRate updates the tick rate. Takes effect on the next tick cycle.
// This is used when auto-detecting frame rate from source streams.
func (fs *FrameSynchronizer) SetTickRate(d time.Duration) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.tickRate = d
	fs.fpsNum, fs.fpsDen = tickRateToRational(d)
	fs.log.Debug("tick rate updated", "rate", d)
}

// SetFRCQuality sets the frame rate conversion quality for all sources.
// FRCNone disables FRC and removes frcSource instances. Other values
// create or update frcSource on each syncSource.
func (fs *FrameSynchronizer) SetFRCQuality(q FRCQuality) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.frcQuality = q
	for _, ss := range fs.sources {
		ss.mu.Lock()
		if q == FRCNone {
			if ss.frc != nil {
				ss.frc.reset()
				ss.frc = nil
			}
		} else if ss.frc == nil {
			ss.frc = newFRCSource(q, fs.tickPTSInterval())
		} else {
			ss.frc.requestedQuality = q
			ss.frc.effectiveQuality = q
		}
		ss.mu.Unlock()
	}
}

// FRCQuality returns the current FRC quality level.
func (fs *FrameSynchronizer) FRCQuality() FRCQuality {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.frcQuality
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
	fs.wg.Add(1)
	go fs.tickLoop()
}

// Stop halts the background ticker. Safe to call multiple times.
func (fs *FrameSynchronizer) Stop() {
	fs.mu.Lock()
	if fs.stopped {
		fs.mu.Unlock()
		return
	}
	fs.stopped = true
	close(fs.done)
	fs.mu.Unlock()

	// Wait for the tickLoop goroutine to exit.
	fs.wg.Wait()
}

// tickLoop is the background goroutine that runs the ticker.
// Uses a monotonic deadline loop: nextTick advances by fixed intervals from
// the previous *target* time, not from time.Now(). If a tick handler takes
// variable time, the next tick fires earlier to compensate, preventing drift.
func (fs *FrameSynchronizer) tickLoop() {
	defer fs.wg.Done()
	fs.mu.Lock()
	rate := fs.tickRate
	fs.mu.Unlock()

	timer := time.NewTimer(rate)
	defer timer.Stop()
	nextTick := time.Now().Add(rate)
	for {
		sleepDur := time.Until(nextTick)
		if sleepDur > 0 {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(sleepDur)
			select {
			case <-timer.C:
			case <-fs.done:
				return
			}
		} else {
			select {
			case <-fs.done:
				return
			default:
			}
		}

		fs.mu.Lock()
		newRate := fs.tickRate
		fs.mu.Unlock()

		if newRate != rate {
			rate = newRate
			nextTick = time.Now().Add(rate)
		} else {
			nextTick = nextTick.Add(rate)
		}

		fs.releaseTick()
	}
}

// releaseTick releases one frame per source. For each source:
// - If new frames are buffered, release the newest and update lastFrame.
// - If no new frames, repeat the last frame (freeze).
// - If no frame has ever been received, skip.
//
// Fresh source frames preserve their original PTS (A/V sync with audio).
// Repeated/frozen/interpolated frames advance PTS by one tick interval to
// maintain monotonic output for downstream decoders.
func (fs *FrameSynchronizer) releaseTick() {
	fs.mu.Lock()
	fs.tickNum++
	// Tick interval in 90 kHz PTS units (e.g., 3003 for 29.97fps).
	tickIntervalPTS := fs.tickPTSInterval()

	// Reuse the releases slice from previous ticks to avoid allocation.
	fs.releases = fs.releases[:0]

	for key, ss := range fs.sources {
		var releaseVideo *media.VideoFrame
		var releaseRawVideo *ProcessingFrame
		var releaseAudio *media.AudioFrame

		ss.mu.Lock()

		// Raw video: pop newest from ring, or repeat last.
		// Raw video takes priority over H.264 video — sources with a
		// sourceDecoder produce raw frames; H.264 frames are for legacy path.
		if newest := ss.popNewestRawVideo(); newest != nil {
			ss.lastRawVideo = newest
			releaseRawVideo = newest
			// Reset FRC interpolation counter — fresh frame arrived
			if ss.frc != nil {
				ss.frc.ticksSinceLastFresh = 0
			}
		} else if ss.frc != nil && ss.frc.canInterpolate() {
			// FRC: synthesize interpolated frame on the source PTS timeline.
			// Advance from the last released PTS by one tick interval per missed tick.
			ss.frc.ticksSinceLastFresh++
			frcPTS := ss.lastReleasedPTS + int64(ss.frc.ticksSinceLastFresh)*ss.frc.tickIntervalPTS
			releaseRawVideo = ss.frc.emit(frcPTS)
		} else if ss.lastRawVideo != nil {
			releaseRawVideo = ss.lastRawVideo
		}

		// H.264 video: only if no raw video frame was released.
		if releaseRawVideo == nil {
			if newest := ss.popNewestVideo(); newest != nil {
				ss.lastVideo = newest
				releaseVideo = newest
			} else if ss.lastVideo != nil {
				releaseVideo = ss.lastVideo
			}
		}

		// Audio: pop newest from ring, or repeat last (max 2 repeats to avoid glitch loop).
		// Repeating encoded AAC frames produces an audible stutter; after 2 repeats
		// we stop emitting and let downstream handle silence instead.
		if newest := ss.popNewestAudio(); newest != nil {
			ss.lastAudio = newest
			ss.audioMissCount = 0
			releaseAudio = newest
		} else if ss.lastAudio != nil {
			ss.audioMissCount++
			if ss.audioMissCount <= 2 {
				releaseAudio = ss.lastAudio
			}
		}

		ss.mu.Unlock()

		if releaseVideo != nil || releaseRawVideo != nil || releaseAudio != nil {
			fs.releases = append(fs.releases, pendingRelease{
				sourceKey: key,
				ss:        ss,
				video:     releaseVideo,
				rawVideo:  releaseRawVideo,
				audio:     releaseAudio,
			})
		}
	}
	fs.mu.Unlock()

	// Deliver outside the lock to prevent deadlocks with downstream handlers.
	//
	// PTS strategy (broadcast-correct A/V sync):
	// - Fresh source frames: preserve original PTS (same timeline as audio)
	// - Repeated/frozen frames: advance PTS by one tick interval (monotonic for decoders)
	// - FRC-interpolated frames: use tick PTS (no source PTS exists)
	//
	// Audio bypasses frame sync entirely (continuous sample stream), so keeping
	// video PTS on the source timeline maintains A/V sync in the muxer.
	for _, r := range fs.releases {
		if r.rawVideo != nil {
			pf := *r.rawVideo
			if r.ss != nil {
				if pf.PTS != r.ss.lastReleasedPTS || !r.ss.ptsInitialized {
					// Fresh frame or FRC-interpolated: preserve source PTS
					r.ss.lastReleasedPTS = pf.PTS
					r.ss.ptsInitialized = true
				} else {
					// Repeated frame (freeze): advance by tick interval for monotonic output
					r.ss.lastReleasedPTS += tickIntervalPTS
					pf.PTS = r.ss.lastReleasedPTS
				}
			}
			if fs.onRawVideo != nil {
				fs.onRawVideo(r.sourceKey, &pf)
			}
		} else if r.video != nil {
			vf := *r.video
			if r.ss != nil {
				if vf.PTS != r.ss.lastReleasedPTS || !r.ss.ptsInitialized {
					r.ss.lastReleasedPTS = vf.PTS
					r.ss.ptsInitialized = true
				} else {
					r.ss.lastReleasedPTS += tickIntervalPTS
					vf.PTS = r.ss.lastReleasedPTS
				}
			}
			fs.onVideo(r.sourceKey, vf)
		}
		if r.audio != nil {
			af := *r.audio
			fs.onAudio(r.sourceKey, af)
		}
	}
}
