package switcher

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
)

// mpegtsClock is the MPEG-TS 90 kHz clock rate used for PTS values.
const mpegtsClock = 90000

// ptsMask33 masks a PTS value to 33 bits (MPEG-TS PTS range).
const ptsMask33 = (int64(1) << 33) - 1

// ptsAfter returns true if a is "after" b in 33-bit PTS space,
// handling wraparound. Uses signed comparison on the delta.
func ptsAfter(a, b int64) bool {
	delta := (a - b) & ptsMask33
	// If delta is in the lower half of the 33-bit range, a is after b.
	return delta > 0 && delta < (1<<32)
}

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

	// maxAudioQueueSize caps the per-source audio FIFO to prevent unbounded
	// growth if ticks stall or a source floods faster than drain rate.
	// 16 frames ≈ ~340ms at 48kHz/1024 samples — well above normal (1-2).
	maxAudioQueueSize = 16
)

// audioFramePTSForRate computes the PTS interval for one AAC frame at the
// given sample rate. If sampleRate is 0, defaultAudioSampleRate (48000) is used.
func audioFramePTSForRate(sampleRate int) int64 {
	if sampleRate <= 0 {
		sampleRate = defaultAudioSampleRate
	}
	return int64(aacSamplesPerFrame) * int64(mpegtsClock) / int64(sampleRate)
}

// pendingRelease holds a frame pair collected under the lock for delivery
// outside the lock. The slice is reused across ticks to avoid allocation.
// rawVideo is stored by value (not pointer) to prevent races with
// frcSource.ingest() which may concurrently modify the original struct.
type pendingRelease struct {
	sourceKey   string
	ss          *syncSource // for PTS tracking during delivery
	video       *media.VideoFrame
	rawVideo    ProcessingFrame     // value copy — safe from concurrent modification
	hasRawVideo bool                // true when rawVideo is set
	freshVideo  bool                // true when a new frame was popped from ring (not repeated)
	audio       *media.AudioFrame   // single audio frame (freeze/repeat or sole queued frame)
	audioQueue  []*media.AudioFrame // FIFO-drained audio frames (all fresh)
	freshAudio  bool                // true when audio frame(s) are fresh (not repeated)
}

// frcTask holds parameters for a deferred FRC computation that will be
// executed in parallel during Phase 2 of releaseTick.
type frcTask struct {
	releaseIdx int         // index in FrameSynchronizer.releases
	ss         *syncSource // source needing FRC
	frcPTS     int64       // target PTS for interpolation
}

// syncSource holds per-source buffering state for the FrameSynchronizer.
type syncSource struct {
	mu sync.Mutex // per-source lock; protects ring buffers and last-frame state

	// pendingVideo is a fixed-size ring buffer for incoming video frames.
	pendingVideo [syncRingSize]*media.VideoFrame
	videoHead    int // write index into pendingVideo
	videoCount   int // number of valid frames in ring

	// pendingAudio mirrors video buffering for audio frames (used for freeze repeat).
	pendingAudio [syncRingSize]*media.AudioFrame
	audioHead    int
	audioCount   int

	// audioQueue is a FIFO queue for incoming audio frames. Unlike video
	// (which uses a ring buffer with newest-wins), audio frames must never
	// be dropped. All queued frames are drained on each tick release.
	// Between 30fps ticks, ~1-2 audio frames accumulate (48kHz/1024 ≈ 47fps).
	audioQueue []*media.AudioFrame

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

	// lastAudioSampleRate tracks the sample rate of the most recently
	// ingested audio frame. Used to compute correct PTS increments for
	// repeated/frozen audio frames (e.g., 44.1kHz needs 2089 ticks vs
	// 48kHz needs 1920 ticks per AAC frame). Defaults to 0 (meaning
	// use defaultAudioSampleRate).
	lastAudioSampleRate int

	// Bresenham accumulator for sub-tick PTS remainder.
	// Prevents drift at NTSC rates where tickPTSInterval truncates
	// (e.g., 59.94fps: 90000*1001/60000 = 1501.5, truncates to 1501).
	ptsRemAccum int64 // accumulated remainder (numerator)

	// lastSourcePTS tracks the original PTS of the most recently ingested
	// fresh video frame (before any frame-sync rewriting). Used to compute
	// ptsCorrectionDelta for audio PTS alignment.
	lastSourcePTS int64

	// ptsCorrectionDelta stores the difference between the last released
	// (rewritten) video PTS and the last fresh source PTS. Audio frames
	// bypass the frame sync, so this delta is applied to audio PTS in the
	// switcher to maintain A/V sync. Stored atomically for lock-free reads
	// from the audio hot path.
	ptsCorrectionDelta atomic.Int64

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
	frcTasks   []frcTask        // reused across ticks for parallel FRC
	frcQuality FRCQuality       // FRC quality level for new sources
	framePool  *FramePool       // pool reference for FRC-emitted frames

	// Program-source-driven release: when the program source ingests a
	// fresh frame, the tick loop fires immediately instead of waiting for
	// the fixed-rate timer. This eliminates sync wait latency (~17ms→<1ms).
	programSource     string        // key of current program source
	programFrameReady chan struct{} // buffered(1) signal from IngestRawVideo

	// Observability counters for release trigger type.
	programDrivenReleases atomic.Int64
	timerDrivenReleases   atomic.Int64

	// Release FPS tracking — used by PerfSample() to compute frame sync output rate.
	// Only accessed under fs.mu (via ReleaseFPS which acquires the lock).
	releaseFPSLastTotal int64
	releaseFPSLastTime  time.Time
	releaseFPSCached    float64
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
		log:               slog.With("component", "framesync"),
		sources:           make(map[string]*syncSource),
		tickRate:          tickRate,
		fpsNum:            fpsNum,
		fpsDen:            fpsDen,
		onVideo:           onVideo,
		onAudio:           onAudio,
		done:              make(chan struct{}),
		programFrameReady: make(chan struct{}, 1),
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
// When FRC is enabled (quality >= FRCMCFI), the program source gets full
// quality while non-program sources get FRCNearest (near-zero CPU).
// For quality levels below FRCMCFI, all sources get the same quality.
func (fs *FrameSynchronizer) AddSource(key string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, exists := fs.sources[key]; exists {
		return
	}
	ss := &syncSource{}
	if fs.frcQuality != FRCNone {
		// Program source gets full quality; others get FRCNearest when
		// the global quality is MCFI (expensive). Below MCFI, all sources
		// get the same quality since they're cheap enough. When no program
		// source is set, all sources get full quality (backward compat).
		quality := fs.frcQuality
		if fs.frcQuality >= FRCMCFI && fs.programSource != "" && key != fs.programSource {
			quality = FRCNearest
		}
		frc := newFRCSource(quality, fs.tickPTSInterval())
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
// Audio frames are appended to a FIFO queue (never dropped) and also
// pushed into the ring buffer (for freeze/repeat behavior when the queue
// is empty). All queued frames are drained on the next tick release.
// Takes a pointer to avoid value copy heap escape on the hot path.
func (fs *FrameSynchronizer) IngestAudio(sourceKey string, frame *media.AudioFrame) {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	fs.mu.Unlock()
	if !ok {
		return
	}
	ss.mu.Lock()
	if len(ss.audioQueue) >= maxAudioQueueSize {
		// Drop the oldest frame to keep the queue bounded.
		ss.audioQueue = ss.audioQueue[1:]
	}
	ss.audioQueue = append(ss.audioQueue, frame)
	ss.pushAudio(frame) // ring buffer for freeze/repeat fallback
	if frame.SampleRate > 0 {
		ss.lastAudioSampleRate = frame.SampleRate
	}
	ss.mu.Unlock()
}

// IngestRawVideo buffers a decoded YUV frame for the specified source.
// When the source is the current program source, signals the tick loop to
// fire immediately (phase-lock). This works safely with FRC because each
// early release resets the timer deadline (nextTick = time.Now().Add(rate)),
// consuming a timer slot and preventing rate inflation. Between program
// source frames, the timer continues firing normally for FRC interpolation.
func (fs *FrameSynchronizer) IngestRawVideo(sourceKey string, pf *ProcessingFrame) {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	canSignal := sourceKey == fs.programSource
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

	// Signal tick loop for immediate release when program source has a fresh frame.
	// Non-blocking: if signal already pending, skip (buffered channel, size 1).
	if canSignal {
		select {
		case fs.programFrameReady <- struct{}{}:
		default:
		}
	}
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

// tickPTSWithRemainder returns the PTS interval for this tick, distributing
// the sub-tick remainder from integer truncation using a Bresenham-style
// accumulator. This prevents drift at NTSC rates (59.94fps, 23.976fps, etc.)
// where 90000*fpsDen/fpsNum has a non-zero remainder.
func tickPTSWithRemainder(ss *syncSource, baseInterval, remNum, remDen int64) int64 {
	interval := baseInterval
	ss.ptsRemAccum += remNum
	if ss.ptsRemAccum >= remDen {
		interval++
		ss.ptsRemAccum -= remDen
	}
	return interval
}

// SetTickRate updates the tick rate. Takes effect on the next tick cycle.
// This is used when auto-detecting frame rate from source streams.
// Also propagates the new tick interval to all existing FRC sources so
// their interpolation alpha computations use the correct PTS spacing.
func (fs *FrameSynchronizer) SetTickRate(d time.Duration) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.tickRate = d
	fs.fpsNum, fs.fpsDen = tickRateToRational(d)

	// Update per-source state for the new tick rate:
	// - Reset Bresenham accumulators to prevent stale remainder from the old
	//   rate bleeding into PTS intervals at the new rate.
	// - Propagate new tick interval to FRC sources.
	newInterval := fs.tickPTSInterval()
	for _, ss := range fs.sources {
		ss.mu.Lock()
		ss.ptsRemAccum = 0
		if ss.frc != nil {
			ss.frc.tickIntervalPTS = newInterval
		}
		ss.mu.Unlock()
	}

	fs.log.Debug("tick rate updated", "rate", d)
}

// SetFramePool updates the frame pool reference used by the FrameSynchronizer
// for FRC deep copies and new source FRC initialization. Called by
// SetPipelineFormat after creating a new pool at the updated dimensions.
// Also propagates the new pool to all existing FRC sources.
func (fs *FrameSynchronizer) SetFramePool(pool *FramePool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.framePool = pool
	for _, ss := range fs.sources {
		ss.mu.Lock()
		if ss.frc != nil {
			ss.frc.pool = pool
		}
		ss.mu.Unlock()
	}
}

// SetFRCQuality sets the frame rate conversion quality for all sources.
// FRCNone disables FRC and removes frcSource instances. When quality is
// FRCMCFI or above, the program source gets full quality while non-program
// sources get FRCNearest (near-zero CPU). Below FRCMCFI, all sources get
// the same quality since they're cheap enough.
func (fs *FrameSynchronizer) SetFRCQuality(q FRCQuality) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.frcQuality = q
	for key, ss := range fs.sources {
		ss.mu.Lock()
		if q == FRCNone {
			if ss.frc != nil {
				ss.frc.reset()
				ss.frc = nil
			}
		} else if ss.frc == nil {
			// Determine per-source quality: program gets full, others get nearest
			// when global quality is MCFI. When no program source is set, all
			// sources get full quality (backward compat).
			sourceQ := q
			if q >= FRCMCFI && fs.programSource != "" && key != fs.programSource {
				sourceQ = FRCNearest
			}
			frc := newFRCSource(sourceQ, fs.tickPTSInterval())
			frc.pool = fs.framePool
			ss.frc = frc
		} else {
			// Update existing: program gets full quality, others get nearest
			// when global quality is MCFI. When no program source is set, all
			// sources get full quality (backward compat).
			if q >= FRCMCFI && fs.programSource != "" && key != fs.programSource {
				ss.frc.requestedQuality = FRCNearest
				ss.frc.effectiveQuality = FRCNearest
			} else {
				ss.frc.requestedQuality = q
				ss.frc.effectiveQuality = q
			}
		}
		ss.mu.Unlock()
	}
}

// SetSourceFRCQuality sets the FRC quality for a specific source.
// Used to run full MCFI only on the program source while other sources
// use cheaper interpolation (e.g., FRCNearest).
func (fs *FrameSynchronizer) SetSourceFRCQuality(key string, q FRCQuality) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ss, ok := fs.sources[key]
	if !ok {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
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
}

// FRCQuality returns the current FRC quality level.
func (fs *FrameSynchronizer) FRCQuality() FRCQuality {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.frcQuality
}

// GetSourcePTSCorrection returns the PTS correction delta for a source.
// This is the amount by which the frame sync has shifted video PTS relative
// to the source's original PTS. Audio frames should have this delta added
// to their PTS to maintain A/V sync (since audio bypasses the frame sync).
// Returns 0 if the source is not found or no correction is needed.
func (fs *FrameSynchronizer) GetSourcePTSCorrection(sourceKey string) int64 {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	fs.mu.Unlock()
	if !ok {
		return 0
	}
	return ss.ptsCorrectionDelta.Load()
}

// SetProgramSource sets which source drives early release of the tick loop.
// When the program source ingests a fresh frame, the tick fires immediately
// instead of waiting for the fixed-rate timer.
//
// When FRC quality is FRCMCFI or above, this also demotes the old program
// source to FRCNearest and promotes the new program source to full quality.
// This ensures only the on-air source pays the MCFI CPU cost (~16% per source).
func (fs *FrameSynchronizer) SetProgramSource(key string) {
	fs.mu.Lock()
	oldProgram := fs.programSource
	fs.programSource = key

	// Auto-adjust FRC quality when MCFI is active: demote ALL non-program
	// sources to FRCNearest, promote the program source to full quality.
	// This handles both the initial case (sources added before program is set)
	// and subsequent program switches.
	if fs.frcQuality >= FRCMCFI && oldProgram != key {
		for srcKey, ss := range fs.sources {
			ss.mu.Lock()
			if ss.frc != nil {
				if srcKey == key {
					// Promote new program source to full quality.
					ss.frc.requestedQuality = fs.frcQuality
					ss.frc.effectiveQuality = fs.frcQuality
				} else {
					// Demote all non-program sources to FRCNearest.
					ss.frc.requestedQuality = FRCNearest
					ss.frc.effectiveQuality = FRCNearest
				}
			}
			ss.mu.Unlock()
		}
	}

	fs.mu.Unlock()
}

// DebugSnapshot returns a point-in-time snapshot of the frame synchronizer
// state for diagnostic display. Includes per-source buffer counts, audio
// miss counts, and FRC state (when enabled).
//
// Locking order: fs.mu → ss.mu (matches releasePending/Tick pattern).
func (fs *FrameSynchronizer) DebugSnapshot() map[string]any {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	sources := make(map[string]any, len(fs.sources))
	for key, ss := range fs.sources {
		ss.mu.Lock()
		info := map[string]any{
			"audio_miss_count": ss.audioMissCount,
			"video_count":      ss.videoCount,
			"audio_count":      ss.audioCount,
			"raw_video_count":  ss.rawVideoCount,
		}
		if ss.frc != nil {
			info["frc"] = map[string]any{
				"requested_quality": ss.frc.requestedQuality.String(),
				"effective_quality": ss.frc.effectiveQuality.String(),
				"scene_change":      ss.frc.sceneChange,
				"me_last_ns":        ss.frc.meLastNs,
				"has_two_frames":    ss.frc.hasTwo,
				"degraded":          !ss.frc.degradedSince.IsZero(),
			}
		}
		ss.mu.Unlock()
		sources[key] = info
	}
	return map[string]any{
		"sources":                 sources,
		"frc_quality":             fs.frcQuality.String(),
		"program_source":          fs.programSource,
		"program_driven_releases": fs.programDrivenReleases.Load(),
		"timer_driven_releases":   fs.timerDrivenReleases.Load(),
	}
}

// ReleaseFPS computes the frame sync output rate (releases per second)
// from the delta in total releases since the last call. Designed to be
// called once per second by the perf sampler. Must be called under the
// Switcher's RLock (fs is accessed via s.frameSync).
func (fs *FrameSynchronizer) ReleaseFPS() float64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	total := fs.programDrivenReleases.Load() + fs.timerDrivenReleases.Load()
	now := time.Now()
	if !fs.releaseFPSLastTime.IsZero() {
		elapsed := now.Sub(fs.releaseFPSLastTime).Seconds()
		if elapsed > 0 {
			delta := total - fs.releaseFPSLastTotal
			fs.releaseFPSCached = float64(delta) / elapsed
		}
	}
	fs.releaseFPSLastTotal = total
	fs.releaseFPSLastTime = now
	return fs.releaseFPSCached
}

// SourceCount returns the number of sources registered with the frame
// synchronizer.
func (fs *FrameSynchronizer) SourceCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.sources)
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
// Uses a monotonic deadline loop with program-source-driven early release:
// when the program source ingests a fresh frame, the tick fires immediately
// instead of waiting for the fixed-rate timer. The timer remains as fallback
// for FRC interpolation, freeze frames, and startup.
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
		var earlyRelease bool
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
				// Normal timer tick. Drain any pending program signal
				// to prevent double-fire on the next iteration.
				select {
				case <-fs.programFrameReady:
				default:
				}
				earlyRelease = false
			case <-fs.programFrameReady:
				// Program source has a fresh frame — release immediately.
				// Drain timer to prevent it firing after we proceed.
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				earlyRelease = true
			case <-fs.done:
				return
			}
		} else {
			// Tick is overdue.
			select {
			case <-fs.done:
				return
			default:
			}
			// Drain any stale program signal.
			select {
			case <-fs.programFrameReady:
			default:
			}
			earlyRelease = false
		}

		fs.mu.Lock()
		newRate := fs.tickRate
		fs.mu.Unlock()

		if newRate != rate {
			rate = newRate
			nextTick = time.Now().Add(rate)
		} else if earlyRelease {
			// Advance the tick grid forward by one period. The early release
			// consumed this tick slot; advancing ensures it replaces the next
			// timer tick rather than inserting an extra one. Without this,
			// a 24fps source driving a 60fps pipeline would inflate output to
			// ~72fps (early releases + unchanged timer grid = extra ticks).
			nextTick = nextTick.Add(rate)
			now := time.Now()
			for !nextTick.After(now) {
				nextTick = nextTick.Add(rate)
			}
		} else {
			// Monotonic advance from previous target (existing behavior).
			nextTick = nextTick.Add(rate)
		}

		// Track release type for diagnostics.
		if earlyRelease {
			fs.programDrivenReleases.Add(1)
		} else {
			fs.timerDrivenReleases.Add(1)
		}

		fs.releaseTick()
	}
}

// runFRCEmit executes a single FRC emit + deep copy. Called from releaseTick
// either inline (single task) or from a goroutine (multiple tasks in parallel).
// The result is written directly into the pendingRelease entry.
func runFRCEmit(task *frcTask, r *pendingRelease, framePool *FramePool) {
	task.ss.mu.Lock()
	emitted := task.ss.frc.emit(task.frcPTS)
	task.ss.mu.Unlock()

	if emitted == nil {
		return // fallback (frozen frame) already in release entry
	}

	// Deep-copy YUV to avoid aliasing FRC scratch buffers (nearestOut/blendOut).
	result := *emitted
	if framePool != nil && len(emitted.YUV) <= framePool.bufSize {
		yuvCopy := framePool.Acquire()[:len(emitted.YUV)]
		copy(yuvCopy, emitted.YUV)
		result.YUV = yuvCopy
		result.pool = framePool
	} else {
		yuvCopy := make([]byte, len(emitted.YUV))
		copy(yuvCopy, emitted.YUV)
		result.YUV = yuvCopy
	}

	r.rawVideo = result
	r.hasRawVideo = true
	r.freshVideo = true // FRC frames have unique PTS, treat as fresh
}

// releaseTick releases one frame per source using a three-phase approach:
//
// Phase 1 (under fs.mu + per-source ss.mu): Pop fresh frames from ring
// buffers. Sources needing FRC interpolation are identified but NOT computed
// here — they are deferred to Phase 2. fs.mu is released after this phase
// to unblock IngestRawVideo during FRC processing.
//
// Phase 2 (parallel goroutines under individual ss.mu, fs.mu NOT held):
// FRC emit + deep copy runs concurrently for all sources that need
// interpolation. This transforms tick time from O(sum(MCFI_times)) to
// O(max(MCFI_time)). Releasing fs.mu before this phase lets ingest calls
// land frames in the ring buffer during FRC, reducing sync wait.
//
// Phase 3 (no locks): Deliver frames to downstream callbacks with PTS handling.
//
// Fresh source frames preserve their original PTS (A/V sync with audio).
// Repeated/frozen/interpolated frames advance PTS by one tick interval to
// maintain monotonic output for downstream decoders.
func (fs *FrameSynchronizer) releaseTick() {
	fs.mu.Lock()
	fs.tickNum++
	// Tick interval in 90 kHz PTS units (e.g., 3003 for 29.97fps).
	tickIntervalPTS := fs.tickPTSInterval()
	// Bresenham remainder for sub-tick drift correction at NTSC rates.
	ptsRemNum := (int64(mpegtsClock) * int64(fs.fpsDen)) % int64(fs.fpsNum)
	ptsRemDen := int64(fs.fpsNum)

	// Reuse slices from previous ticks to avoid allocation.
	fs.releases = fs.releases[:0]
	fs.frcTasks = fs.frcTasks[:0]
	framePool := fs.framePool
	programSource := fs.programSource

	// programReleaseIdx tracks the index of the program source's fresh frame
	// in the releases slice. -1 means no early delivery (program source has
	// no fresh frame or needs FRC). Set during Phase 1, used in Phase 1.5.
	programReleaseIdx := -1

	// Phase 1: Pop fresh frames and identify FRC work.
	for key, ss := range fs.sources {
		var releaseVideo *media.VideoFrame
		var releaseRawVideo ProcessingFrame
		var hasRawVideo bool
		var releaseAudio *media.AudioFrame

		ss.mu.Lock()

		var freshVideo bool
		var needsFRC bool
		var frcPTS int64

		// Raw video: pop newest from ring, or defer FRC, or repeat last.
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
			// FRC: defer computation to Phase 2 for parallel execution.
			// Pre-populate release with frozen lastRawVideo as fallback
			// in case FRC emit returns nil.
			ss.frc.ticksSinceLastFresh++
			frcPTS = ss.lastReleasedPTS + int64(ss.frc.ticksSinceLastFresh)*ss.frc.tickIntervalPTS
			needsFRC = true
			if ss.lastRawVideo != nil {
				releaseRawVideo = *ss.lastRawVideo // frozen fallback
				hasRawVideo = true
			}
		} else if ss.lastRawVideo != nil {
			releaseRawVideo = *ss.lastRawVideo // value copy under lock
			hasRawVideo = true
		}

		// H.264 video: only if no raw video frame was released.
		// When FRC is pending without a frozen fallback (lastRawVideo nil),
		// H.264 serves as additional fallback — Phase 2 FRC result will
		// override via hasRawVideo if it succeeds.
		if !hasRawVideo {
			if newest := ss.popNewestVideo(); newest != nil {
				ss.lastVideo = newest
				releaseVideo = newest
				freshVideo = true
			} else if ss.lastVideo != nil {
				releaseVideo = ss.lastVideo
			}
		}

		// Audio: drain FIFO queue (all fresh frames), or repeat last from ring
		// (max 2 repeats to avoid glitch loop). The FIFO queue ensures no audio
		// frames are ever dropped — all are released on each tick in order.
		var freshAudio bool
		var audioQueue []*media.AudioFrame
		if len(ss.audioQueue) > 0 {
			// Drain the entire FIFO queue. Update lastAudio from the ring
			// buffer (which tracks the newest for freeze/repeat).
			audioQueue = make([]*media.AudioFrame, len(ss.audioQueue))
			copy(audioQueue, ss.audioQueue)
			ss.audioQueue = ss.audioQueue[:0]
			ss.audioMissCount = 0
			freshAudio = true
			// Update lastAudio from ring buffer for freeze/repeat fallback.
			if newest := ss.popNewestAudio(); newest != nil {
				ss.lastAudio = newest
			}
			releaseAudio = nil // all frames in audioQueue
		} else {
			// No fresh audio — try freeze/repeat from ring buffer.
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
		}

		ss.mu.Unlock()

		hasAudio := releaseAudio != nil || len(audioQueue) > 0
		if releaseVideo != nil || hasRawVideo || hasAudio || needsFRC {
			idx := len(fs.releases)
			fs.releases = append(fs.releases, pendingRelease{
				sourceKey:   key,
				ss:          ss,
				video:       releaseVideo,
				rawVideo:    releaseRawVideo,
				hasRawVideo: hasRawVideo,
				freshVideo:  freshVideo,
				audio:       releaseAudio,
				audioQueue:  audioQueue,
				freshAudio:  freshAudio,
			})
			if needsFRC {
				fs.frcTasks = append(fs.frcTasks, frcTask{
					releaseIdx: idx,
					ss:         ss,
					frcPTS:     frcPTS,
				})
			}
			// Track program source's fresh frame for early delivery (Phase 1.5).
			// Only when it has a fresh frame and doesn't need FRC.
			if key == programSource && freshVideo && !needsFRC {
				programReleaseIdx = idx
			}
		}
	}

	// Release fs.mu before Phase 1.5/2 to reduce lock contention on the ingest
	// hot path. Phase 2 only needs per-source ss.mu for FRC emit. Releasing
	// fs.mu here lets IngestRawVideo land frames in the ring buffer during
	// FRC processing instead of blocking, reducing sync wait by the FRC
	// duration (~3-5ms for MCFI).
	//
	// Safety: frcTasks and releases were built in Phase 1 and are only
	// accessed by this goroutine (tickLoop is single-threaded). Captured ss
	// pointers remain valid even if RemoveSource deletes from the map —
	// RemoveSource blocks on ss.mu until Phase 2 finishes.
	fs.mu.Unlock()

	// Phase 1.5: Deliver the program source's fresh frame immediately, before
	// Phase 2 FRC computation. This eliminates ~6.6ms of waiting for other
	// sources' MCFI to complete. The program source is the only one the
	// pipeline processes — other sources' frames are buffered for transitions.
	if programReleaseIdx >= 0 {
		fs.deliverRelease(&fs.releases[programReleaseIdx], tickIntervalPTS, ptsRemNum, ptsRemDen)
	}

	// Phase 2: Parallel FRC computation.
	// Each goroutine locks only its own ss.mu (no contention between sources).
	// Single task runs inline to avoid goroutine overhead.
	if len(fs.frcTasks) == 1 {
		task := &fs.frcTasks[0]
		runFRCEmit(task, &fs.releases[task.releaseIdx], framePool)
	} else if len(fs.frcTasks) > 1 {
		var wg sync.WaitGroup
		for i := range fs.frcTasks {
			wg.Add(1)
			go func(task *frcTask, r *pendingRelease) {
				defer wg.Done()
				runFRCEmit(task, r, framePool)
			}(&fs.frcTasks[i], &fs.releases[fs.frcTasks[i].releaseIdx])
		}
		wg.Wait()
	}

	// Phase 3: Deliver remaining sources (skip program source already delivered
	// in Phase 1.5). No locks held — prevents deadlocks with downstream handlers.
	for i := range fs.releases {
		if i == programReleaseIdx {
			continue // already delivered in Phase 1.5
		}
		fs.deliverRelease(&fs.releases[i], tickIntervalPTS, ptsRemNum, ptsRemDen)
	}
}

// deliverRelease handles PTS assignment and callback delivery for a single
// pendingRelease entry. Shared between Phase 1.5 (program source early delivery)
// and Phase 3 (remaining sources). Called from the single tickLoop goroutine.
//
// PTS strategy (broadcast-correct monotonic output):
//   - Fresh source frames: preserve original PTS (A/V sync with passthrough audio),
//     but clamp forward if behind accumulated freeze PTS (prevents backward PTS
//     in MPEG-TS output that would confuse downstream decoders).
//   - Repeated/frozen frames: advance PTS by one tick interval (monotonic).
//   - FRC-interpolated frames: use computed PTS (no source PTS exists).
//   - Audio: same strategy — fresh preserves source PTS, repeats advance.
func (fs *FrameSynchronizer) deliverRelease(r *pendingRelease, tickIntervalPTS, ptsRemNum, ptsRemDen int64) {
	if r.hasRawVideo {
		pf := r.rawVideo // already a value copy from under the lock

		// Non-fresh frames (frozen/repeated or FRC fallback) inherit stale
		// decode timestamps from the original frame. Clear them so the
		// downstream sync wait measurement stores 0 instead of a large
		// stale delta (e.g., 36ms from a frame decoded 36ms ago).
		if !r.freshVideo {
			pf.DecodeEndNano = 0
			pf.DecodeStartNano = 0
			pf.ArrivalNano = 0
		}

		if r.ss != nil {
			if r.freshVideo || !r.ss.ptsInitialized {
				// Fresh frame: update lastSourcePTS, preserve source PTS,
				// but clamp forward if behind accumulated freeze PTS to
				// prevent backward PTS in the MPEG-TS output.
				r.ss.lastSourcePTS = pf.PTS
				if r.ss.ptsInitialized && !ptsAfter(pf.PTS, r.ss.lastReleasedPTS) {
					r.ss.lastReleasedPTS += tickPTSWithRemainder(r.ss, tickIntervalPTS, ptsRemNum, ptsRemDen)
					r.ss.lastReleasedPTS &= ptsMask33
					pf.PTS = r.ss.lastReleasedPTS
				} else {
					r.ss.lastReleasedPTS = pf.PTS & ptsMask33
				}
				r.ss.ptsInitialized = true
			} else {
				// Repeated frame (freeze): advance by tick interval for monotonic output
				r.ss.lastReleasedPTS += tickPTSWithRemainder(r.ss, tickIntervalPTS, ptsRemNum, ptsRemDen)
				r.ss.lastReleasedPTS &= ptsMask33
				pf.PTS = r.ss.lastReleasedPTS
			}
			// Store correction delta atomically for audio PTS alignment.
			// Delta = how far frame sync has shifted video PTS beyond source PTS.
			// Mask to 33 bits since the values may be from different wrap epochs.
			r.ss.ptsCorrectionDelta.Store((r.ss.lastReleasedPTS - r.ss.lastSourcePTS) & ptsMask33)
		}
		pf.SyncReleaseNano = time.Now().UnixNano()
		if fs.onRawVideo != nil {
			fs.onRawVideo(r.sourceKey, &pf)
		}
	} else if r.video != nil {
		vf := *r.video
		if r.ss != nil {
			if r.freshVideo || !r.ss.ptsInitialized {
				r.ss.lastSourcePTS = vf.PTS
				if r.ss.ptsInitialized && !ptsAfter(vf.PTS, r.ss.lastReleasedPTS) {
					r.ss.lastReleasedPTS += tickPTSWithRemainder(r.ss, tickIntervalPTS, ptsRemNum, ptsRemDen)
					r.ss.lastReleasedPTS &= ptsMask33
					vf.PTS = r.ss.lastReleasedPTS
				} else {
					r.ss.lastReleasedPTS = vf.PTS & ptsMask33
				}
				r.ss.ptsInitialized = true
			} else {
				r.ss.lastReleasedPTS += tickPTSWithRemainder(r.ss, tickIntervalPTS, ptsRemNum, ptsRemDen)
				r.ss.lastReleasedPTS &= ptsMask33
				vf.PTS = r.ss.lastReleasedPTS
			}
			r.ss.ptsCorrectionDelta.Store((r.ss.lastReleasedPTS - r.ss.lastSourcePTS) & ptsMask33)
		}
		fs.onVideo(r.sourceKey, vf)
	}
	// Audio: drain FIFO queue first (all fresh), then single frame (freeze/repeat).
	if len(r.audioQueue) > 0 {
		// FIFO drain: deliver all queued audio frames in order.
		// Each frame preserves its original PTS (fresh frames).
		for _, qaf := range r.audioQueue {
			af := *qaf
			if r.ss != nil {
				if !r.ss.audioPTSInitialized {
					r.ss.lastReleasedAudioPTS = af.PTS & ptsMask33
					r.ss.audioPTSInitialized = true
				} else if !ptsAfter(af.PTS, r.ss.lastReleasedAudioPTS) {
					// PTS behind accumulated value — clamp forward.
					r.ss.lastReleasedAudioPTS += audioFramePTSForRate(r.ss.lastAudioSampleRate)
					r.ss.lastReleasedAudioPTS &= ptsMask33
					af.PTS = r.ss.lastReleasedAudioPTS
				} else {
					r.ss.lastReleasedAudioPTS = af.PTS & ptsMask33
				}
			}
			fs.onAudio(r.sourceKey, af)
		}
	} else if r.audio != nil {
		af := *r.audio
		if r.ss != nil {
			if r.freshAudio || !r.ss.audioPTSInitialized {
				if r.ss.audioPTSInitialized && !ptsAfter(af.PTS, r.ss.lastReleasedAudioPTS) {
					r.ss.lastReleasedAudioPTS += audioFramePTSForRate(r.ss.lastAudioSampleRate)
					r.ss.lastReleasedAudioPTS &= ptsMask33
					af.PTS = r.ss.lastReleasedAudioPTS
				} else {
					r.ss.lastReleasedAudioPTS = af.PTS & ptsMask33
				}
				r.ss.audioPTSInitialized = true
			} else {
				// Repeated audio frame (freeze): advance by audio frame duration,
				// not video tick interval. AAC frames are 1024 samples at the
				// source's sample rate, regardless of video frame rate.
				r.ss.lastReleasedAudioPTS += audioFramePTSForRate(r.ss.lastAudioSampleRate)
				r.ss.lastReleasedAudioPTS &= ptsMask33
				af.PTS = r.ss.lastReleasedAudioPTS
			}
		}
		fs.onAudio(r.sourceKey, af)
	}
}
