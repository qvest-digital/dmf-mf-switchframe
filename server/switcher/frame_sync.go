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

	// aacSamplesPerFrame is the number of PCM samples per AAC-LC frame.
	aacSamplesPerFrame = 1024

	// defaultAudioSampleRate is the standard audio sample rate for broadcast.
	defaultAudioSampleRate = 48000

	// audioFramePTS is the PTS interval for one AAC frame in 90 kHz MPEG-TS
	// clock units: 1024 samples * 90000 Hz / 48000 Hz = 1920 ticks.
	// Used for advancing repeated audio frame PTS (instead of video tick interval).
	audioFramePTS = int64(aacSamplesPerFrame) * int64(mpegtsClock) / int64(defaultAudioSampleRate)
)

// pendingRelease holds a frame pair collected under the lock for delivery
// outside the lock. The slice is reused across ticks to avoid allocation.
// rawVideo is stored by value (not pointer) to prevent races with
// frcSource.ingest() which may concurrently modify the original struct.
type pendingRelease struct {
	sourceKey   string
	ss          *syncSource // for PTS tracking during delivery
	video       *media.VideoFrame
	rawVideo    ProcessingFrame // value copy — safe from concurrent modification
	hasRawVideo bool            // true when rawVideo is set
	freshVideo  bool            // true when a new frame was popped from ring (not repeated)
	audio       *media.AudioFrame
	freshAudio  bool // true when a new audio frame was popped from ring (not repeated)
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
	// When a fresh frame arrives after freeze with PTS <= lastReleasedPTS, the
	// PTS is clamped forward to maintain monotonicity for downstream decoders.
	lastReleasedPTS int64
	ptsInitialized  bool

	// lastReleasedAudioPTS tracks audio PTS separately from video.
	// Repeated audio frames get advancing PTS to avoid duplicate timestamps
	// in the MPEG-TS muxer.
	lastReleasedAudioPTS int64
	audioPTSInitialized  bool

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
// When no FRC is active and the ring is full, the overwritten frame's
// pool buffer is released. With FRC, frame releases are handled by
// frcSource.ingest/reset instead.
func (ss *syncSource) pushRawVideo(pf *ProcessingFrame) {
	if ss.frc == nil && ss.rawVideoCount >= syncRingSize {
		if old := ss.pendingRawVideo[ss.rawVideoHead]; old != nil {
			old.ReleaseYUV()
		}
	}
	ss.pendingRawVideo[ss.rawVideoHead] = pf
	ss.rawVideoHead = (ss.rawVideoHead + 1) % syncRingSize
	if ss.rawVideoCount < syncRingSize {
		ss.rawVideoCount++
	}
}

// popNewestRawVideo returns the most recently pushed raw video frame.
// When no FRC is active, non-newest frames' pool buffers are released.
// With FRC, frame releases are handled by frcSource.ingest/reset instead.
func (ss *syncSource) popNewestRawVideo() *ProcessingFrame {
	if ss.rawVideoCount == 0 {
		return nil
	}
	newest := (ss.rawVideoHead - 1 + syncRingSize) % syncRingSize
	frame := ss.pendingRawVideo[newest]
	for i := range ss.pendingRawVideo {
		if ss.frc == nil && i != newest && ss.pendingRawVideo[i] != nil {
			ss.pendingRawVideo[i].ReleaseYUV()
		}
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
// PTS strategy: fresh source frames preserve their original PTS (maintaining
// A/V sync with passthrough audio). Repeated/frozen frames advance PTS by
// one tick interval for monotonic output. If a fresh frame arrives after a
// freeze with PTS behind the accumulated freeze PTS, it is clamped forward
// to prevent backward PTS in the MPEG-TS output.
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
	framePool  *FramePool       // pool reference for FRC-emitted frames
}

// NewFrameSynchronizer creates a FrameSynchronizer with the given tick rate
// and output callbacks. The ticker is NOT started automatically — call
// Start() to begin releasing frames.
func NewFrameSynchronizer(
	tickRate time.Duration,
	onVideo func(sourceKey string, frame media.VideoFrame),
	onAudio func(sourceKey string, frame media.AudioFrame),
) *FrameSynchronizer {
	// Derive rational FPS from tickRate as default. SetTickRate calls
	// tickRateToRational which maps to standard broadcast frame rates.
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
		frc := newFRCSource(fs.frcQuality, fs.tickPTSInterval())
		frc.pool = fs.framePool
		ss.frc = frc
	}
	fs.sources[key] = ss
	fs.log.Debug("source added", "key", key)
}

// RemoveSource unregisters a source and releases any buffered frames.
// Pool buffers held by raw video ring slots, lastRawVideo, and FRC state
// are explicitly released to prevent FramePool starvation.
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

	// Release raw video frames in the ring buffer.
	for i := range ss.pendingRawVideo {
		if ss.pendingRawVideo[i] != nil {
			ss.pendingRawVideo[i].ReleaseYUV()
			ss.pendingRawVideo[i] = nil
		}
	}
	// Release the last-released raw frame (used for freeze repeats).
	if ss.lastRawVideo != nil {
		ss.lastRawVideo.ReleaseYUV()
		ss.lastRawVideo = nil
	}
	// Release FRC state (holds its own pool buffers).
	if ss.frc != nil {
		ss.frc.reset()
		ss.frc = nil
	}

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
			frc := newFRCSource(q, fs.tickPTSInterval())
			frc.pool = fs.framePool
			ss.frc = frc
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
// Releases all pool buffers held by sources (pendingRawVideo, lastRawVideo,
// FRC state) to prevent FramePool starvation.
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

	// Release all pool buffers held by sources. Safe after wg.Wait()
	// guarantees the tick loop is no longer accessing source state.
	fs.mu.Lock()
	for _, ss := range fs.sources {
		ss.mu.Lock()
		for i := range ss.pendingRawVideo {
			if ss.pendingRawVideo[i] != nil {
				ss.pendingRawVideo[i].ReleaseYUV()
				ss.pendingRawVideo[i] = nil
			}
		}
		if ss.lastRawVideo != nil {
			ss.lastRawVideo.ReleaseYUV()
			ss.lastRawVideo = nil
		}
		if ss.frc != nil {
			ss.frc.reset()
		}
		ss.mu.Unlock()
	}
	fs.mu.Unlock()
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
		var releaseRawVideo ProcessingFrame
		var hasRawVideo bool
		var releaseAudio *media.AudioFrame

		ss.mu.Lock()

		var freshVideo bool

		// Raw video: pop newest from ring, or repeat last.
		// Raw video takes priority over H.264 video — sources with a
		// sourceDecoder produce raw frames; H.264 frames are for legacy path.
		if newest := ss.popNewestRawVideo(); newest != nil {
			// Release the old lastRawVideo's pool buffer before replacing.
			// The previous tick's delivery used a value copy, so this is safe.
			if ss.lastRawVideo != nil && ss.lastRawVideo != newest {
				ss.lastRawVideo.ReleaseYUV()
			}
			ss.lastRawVideo = newest
			releaseRawVideo = *newest // value copy under lock — safe from concurrent frc.ingest
			hasRawVideo = true
			freshVideo = true
			// Reset FRC interpolation counter — fresh frame arrived
			if ss.frc != nil {
				ss.frc.ticksSinceLastFresh = 0
			}
		} else if ss.frc != nil && ss.frc.canInterpolate() {
			// FRC: synthesize interpolated frame on the source PTS timeline.
			// Advance from the last released PTS by one tick interval per missed tick.
			ss.frc.ticksSinceLastFresh++
			frcPTS := ss.lastReleasedPTS + int64(ss.frc.ticksSinceLastFresh)*ss.frc.tickIntervalPTS
			if emitted := ss.frc.emit(frcPTS); emitted != nil {
				releaseRawVideo = *emitted // value copy under lock
				hasRawVideo = true
				freshVideo = true // FRC frames have unique PTS, treat as fresh
			}
		} else if ss.lastRawVideo != nil {
			releaseRawVideo = *ss.lastRawVideo // value copy under lock
			hasRawVideo = true
		}

		// H.264 video: only if no raw video frame was released.
		if !hasRawVideo {
			if newest := ss.popNewestVideo(); newest != nil {
				ss.lastVideo = newest
				releaseVideo = newest
				freshVideo = true
			} else if ss.lastVideo != nil {
				releaseVideo = ss.lastVideo
			}
		}

		// Audio: pop newest from ring, or repeat last (max 2 repeats to avoid glitch loop).
		// Repeating encoded AAC frames produces an audible stutter; after 2 repeats
		// we stop emitting and let downstream handle silence instead.
		var freshAudio bool
		if newest := ss.popNewestAudio(); newest != nil {
			ss.lastAudio = newest
			ss.audioMissCount = 0
			releaseAudio = newest
			freshAudio = true
		} else if ss.lastAudio != nil {
			ss.audioMissCount++
			if ss.audioMissCount <= 2 {
				releaseAudio = ss.lastAudio
			}
		}

		ss.mu.Unlock()

		if releaseVideo != nil || hasRawVideo || releaseAudio != nil {
			fs.releases = append(fs.releases, pendingRelease{
				sourceKey:   key,
				ss:          ss,
				video:       releaseVideo,
				rawVideo:    releaseRawVideo,
				hasRawVideo: hasRawVideo,
				freshVideo:  freshVideo,
				audio:       releaseAudio,
				freshAudio:  freshAudio,
			})
		}
	}
	fs.mu.Unlock()

	// Deliver outside the lock to prevent deadlocks with downstream handlers.
	//
	// PTS strategy (broadcast-correct monotonic output):
	// - Fresh source frames: preserve original PTS (A/V sync with passthrough audio),
	//   but clamp forward if behind accumulated freeze PTS (prevents backward PTS
	//   in MPEG-TS output that would confuse downstream decoders).
	// - Repeated/frozen frames: advance PTS by one tick interval (monotonic).
	// - FRC-interpolated frames: use computed PTS (no source PTS exists).
	// - Audio: same strategy — fresh preserves source PTS, repeats advance.
	for i := range fs.releases {
		r := &fs.releases[i]
		if r.hasRawVideo {
			pf := r.rawVideo // already a value copy from under the lock
			if r.ss != nil {
				if r.freshVideo || !r.ss.ptsInitialized {
					// Fresh frame: preserve source PTS, but clamp forward
					// if behind accumulated freeze PTS to prevent backward
					// PTS in the MPEG-TS output.
					if r.ss.ptsInitialized && pf.PTS <= r.ss.lastReleasedPTS {
						r.ss.lastReleasedPTS += tickIntervalPTS
						pf.PTS = r.ss.lastReleasedPTS
					} else {
						r.ss.lastReleasedPTS = pf.PTS
					}
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
				if r.freshVideo || !r.ss.ptsInitialized {
					if r.ss.ptsInitialized && vf.PTS <= r.ss.lastReleasedPTS {
						r.ss.lastReleasedPTS += tickIntervalPTS
						vf.PTS = r.ss.lastReleasedPTS
					} else {
						r.ss.lastReleasedPTS = vf.PTS
					}
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
			if r.ss != nil {
				if r.freshAudio || !r.ss.audioPTSInitialized {
					if r.ss.audioPTSInitialized && af.PTS <= r.ss.lastReleasedAudioPTS {
						r.ss.lastReleasedAudioPTS += audioFramePTS
						af.PTS = r.ss.lastReleasedAudioPTS
					} else {
						r.ss.lastReleasedAudioPTS = af.PTS
					}
					r.ss.audioPTSInitialized = true
				} else {
					// Repeated audio frame (freeze): advance by audio frame duration,
					// not video tick interval. AAC frames are 1024 samples at 48kHz
					// = 1920 ticks at 90kHz, regardless of video frame rate.
					r.ss.lastReleasedAudioPTS += audioFramePTS
					af.PTS = r.ss.lastReleasedAudioPTS
				}
			}
			fs.onAudio(r.sourceKey, af)
		}
	}
}
