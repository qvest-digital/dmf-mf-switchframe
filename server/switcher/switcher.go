package switcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/transition"
)

const (
	maxDelayMs      = 500  // maximum source delay for lip-sync correction
	defaultFTBDurMs = 1000 // default FTB transition duration in milliseconds
)

// Sentinel errors for the switcher package.
var (
	ErrSourceNotFound   = errors.New("switcher: source not found")
	ErrAlreadyOnProgram = errors.New("switcher: already on program")
	ErrInvalidDelay     = errors.New("switcher: delay must be 0-500ms")
	ErrInvalidPosition  = errors.New("switcher: position must be >= 1")
	ErrNoTransition          = errors.New("switcher: no active transition")
	ErrFormatDuringTransition = errors.New("switcher: cannot change pipeline format during active transition")
)

// updateAtomicMax atomically updates field to val if val > current.
func updateAtomicMax(field *atomic.Int64, val int64) {
	for {
		cur := field.Load()
		if val <= cur {
			return
		}
		if field.CompareAndSwap(cur, val) {
			return
		}
	}
}

// SwitcherState represents the global state of the switching engine.
// It replaces the implicit (inTransition, ftbActive) boolean pair with an
// explicit enum that makes every valid state and transition auditable.
type SwitcherState int

const (
	StateIdle             SwitcherState = iota // No transition, normal frame routing
	StateTransitioning                         // Mix/dip/wipe in progress
	StateFTBTransitioning                      // FTB forward in progress (transitioning to black)
	StateFTB                                   // Faded to black (holding black)
	StateFTBReversing                          // Reversing FTB (fading back in)
)

// String returns the human-readable name of the switcher state.
func (s SwitcherState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateTransitioning:
		return "transitioning"
	case StateFTBTransitioning:
		return "ftb_transitioning"
	case StateFTB:
		return "ftb"
	case StateFTBReversing:
		return "ftb_reversing"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// isInTransition returns true if the switcher is in any transitioning state
// (mix/dip/wipe, FTB forward, or FTB reverse). This maps to the
// ControlRoomState.InTransition API field.
func (s SwitcherState) isInTransition() bool {
	return s == StateTransitioning || s == StateFTBTransitioning || s == StateFTBReversing
}

// isFTBActive returns true if the switcher is in any FTB-related state
// (transitioning to black, holding at black, or reversing from black).
// This maps to the ControlRoomState.FTBActive API field.
func (s SwitcherState) isFTBActive() bool {
	return s == StateFTBTransitioning || s == StateFTB || s == StateFTBReversing
}

// validTransitions defines the allowed state transitions. Any transition not
// in this map is logged as a warning but still executed (no panics in production).
var validTransitions = map[SwitcherState][]SwitcherState{
	StateIdle:             {StateTransitioning, StateFTBTransitioning},
	StateTransitioning:    {StateIdle},
	StateFTBTransitioning: {StateFTB, StateIdle},
	StateFTB:              {StateFTBReversing},
	StateFTBReversing:     {StateFTB, StateIdle},
}

// transitionState changes the switcher state, logging a warning if the transition
// is not in the valid transitions map. Never panics in production.
// Caller must hold s.mu (write lock).
func (s *Switcher) transitionState(to SwitcherState) {
	from := s.state
	if from == to {
		return
	}
	if !slices.Contains(validTransitions[from], to) {
		s.log.Warn("invalid state transition",
			"from", from.String(), "to", to.String())
	}
	s.state = to
}

// audioStateProvider is the interface the Switcher needs from the AudioMixer
// to populate audio fields in state broadcasts.
type audioStateProvider interface {
	ProgramPeak() [2]float64
	ChannelStates() map[string]internal.AudioChannel
	MasterLevel() float64
	GainReduction() float64
	MomentaryLUFS() float64
	ShortTermLUFS() float64
	IntegratedLUFS() float64
}

// audioCutHandler is called during a cut to trigger audio crossfade.
type audioCutHandler interface {
	OnCut(oldSource, newSource string)
	OnProgramChange(newProgramSource string)
}

// audioTransitionHandler is called during transitions to sync audio crossfade
// with video dissolve progress.
type audioTransitionHandler interface {
	OnTransitionStart(oldSource, newSource string, mode audio.AudioTransitionMode, durationMs int)
	OnTransitionPosition(position float64)
	OnTransitionComplete()
	SetProgramMute(muted bool)
}

// RawVideoSink receives a deep copy of the processed YUV420p frame
// after all video processing (keying, compositor) but before H.264 encode.
// Used by MXL output to write raw video to shared memory.
type RawVideoSink func(pf *ProcessingFrame)

// TransitionConfig holds the codec factories needed to create TransitionEngines.
type TransitionConfig struct {
	DecoderFactory transition.DecoderFactory
}

// TransitionOption configures optional parameters for StartTransition.
type TransitionOption func(*transitionOpts)

type transitionOpts struct {
	stingerData *transition.StingerData
	easing      *transition.EasingCurve
}

// WithStingerData sets the stinger overlay data for a stinger transition.
func WithStingerData(sd *transition.StingerData) TransitionOption {
	return func(o *transitionOpts) { o.stingerData = sd }
}

// WithEasing sets the easing curve for the transition.
func WithEasing(ec *transition.EasingCurve) TransitionOption {
	return func(o *transitionOpts) { o.easing = ec }
}

// sourceState tracks a registered source and its Relay/viewer pair.
type sourceState struct {
	key        string
	label      string
	relay      *distribution.Relay
	viewer     *sourceViewer
	isVirtual      bool // true for virtual sources (replay, etc.)
	isMXL          bool // true for MXL raw YUV sources (no H.264 decode/IDR gating)
	useRawPipeline bool // true when source has a per-source decoder (always-decode mode)
	position       int  // display order in the UI (1-based)

	// Rolling frame statistics for dynamic encoder parameters.
	// Updated on every video frame from a single goroutine (source viewer).
	// Used to estimate bitrate/fps for the transition encoder so it
	// matches the source stream quality.
	avgFrameSize float64       // exponential moving average of len(WireData) in bytes
	avgFPS       float64       // exponential moving average of fps from PTS deltas
	lastPTS      int64         // PTS of the most recent video frame (microseconds)
	frameCount   int           // total video frames received (for EMA warmup)
	lastGroupID  atomic.Uint32 // most recent GroupID from this source's video frames
}

// Switcher is the central switching engine. It manages which source is
// on-program (live output) and which is on-preview, maintains tally state,
// and routes frames from the program source to the program Relay.
type Switcher struct {
	log             *slog.Logger
	mu              sync.RWMutex
	sources         map[string]*sourceState
	programSource   string
	previewSource   string
	programRelay    *distribution.Relay
	seq             uint64 // always use atomic ops, even under s.mu, to prevent races on lock-free read paths
	stateCallbacks  []func(internal.ControlRoomState)
	health          *healthMonitor
	audioHandler    func(sourceKey string, frame *media.AudioFrame)
	mixer           audioStateProvider
	audioCut        audioCutHandler
	transConfig     *TransitionConfig
	transEngine     *transition.TransitionEngine
	state           SwitcherState
	audioTransition audioTransitionHandler
	delayBuffer *DelayBuffer
	frameSync       *FrameSynchronizer
	frameSyncActive bool

	// DSK graphics compositor — applies overlay in YUV420 domain.
	compositorRef *graphics.Compositor

	// Upstream key bridge — applies chroma/luma keys in YUV420 domain.
	keyBridge *graphics.KeyProcessorBridge

	// Per-source decoder factory — when set, RegisterSource creates a
	// sourceDecoder for each source that decodes H.264 to YUV at ingest time.
	// This eliminates keyframe wait on cuts/transitions (always-decode mode).
	sourceDecoderFactory transition.DecoderFactory

	// Global pipeline format (resolution + frame rate). Atomic pointer for
	// lock-free reads on the hot path (frame budget check, encoder FPS).
	pipelineFormat atomic.Pointer[PipelineFormat]

	// Pipeline codec pool — shared decoder/encoder for the video processing chain.
	// Used when any YUV processor (compositor, key bridge) is active or when
	// the transition engine outputs raw YUV.
	pipeCodecs *pipelineCodecs

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// programGroupID tracks the last GroupID broadcast to the program relay.
	// Ensures monotonically increasing GroupIDs across source switches and
	// transition boundaries. Atomic for lock-free access from broadcastToProgram.
	programGroupID atomic.Uint32

	// Debug instrumentation counters (atomic, lock-free)
	cutsTotal            atomic.Int64
	transitionsStarted   atomic.Int64
	transitionsCompleted atomic.Int64

	// Video pipeline timing diagnostics (atomic, lock-free)
	videoProcCount      atomic.Int64 // total frames processed through pipeline
	videoProcMaxNano    atomic.Int64 // max video processing time (ns)
	videoProcLastNano   atomic.Int64 // last video processing time (ns)
	videoBroadcastCount atomic.Int64 // frames sent to program relay
	videoProcDropped    atomic.Int64 // frames dropped due to full channel

	// Per-stage pipeline timing (nanoseconds, atomic, lock-free)
	pipeKeyLastNano atomic.Int64
	pipeKeyMaxNano        atomic.Int64
	pipeCompositeLastNano atomic.Int64
	pipeCompositeMaxNano  atomic.Int64
	pipeEncodeLastNano    atomic.Int64
	pipeEncodeMaxNano     atomic.Int64

	// Output FPS tracking (atomic, lock-free)
	outputFPSCount       atomic.Int64 // frames in current 1-second window
	outputFPSLastSecond  atomic.Int64 // FPS computed from previous second
	outputFPSWindowStart atomic.Int64 // UnixNano start of current window

	// Frame loss diagnostic counters (atomic, lock-free).
	pipeEncodeNil    atomic.Int64 // encoder returned nil (HW warmup)
	transOutputCount atomic.Int64 // frames output by transition engine

	// Last broadcast PTS for replay PTS anchoring (atomic, lock-free).
	lastBroadcastPTS atomic.Int64

	// Broadcast interval diagnostics (atomic, lock-free).
	lastBroadcastNano        atomic.Int64 // UnixNano of last program broadcast
	maxBroadcastIntervalNano atomic.Int64 // max gap between consecutive broadcasts (ns)

	// Per-transition gap tracking: measures gap between last transition
	// frame and first post-transition frame (the "transition seam").
	transSeamStartNano atomic.Int64 // set when transition completes (last engine output time)
	transSeamMaxNano   atomic.Int64 // max transition seam gap seen
	transSeamLastNano  atomic.Int64 // most recent transition seam gap
	transSeamCount     atomic.Int64 // number of transition seams measured

	// Frame routing counters (atomic, lock-free).
	routeToEngine     atomic.Int64 // frames routed to transition engine
	routeToIdleEngine atomic.Int64 // frames routed to engine but engine was idle (dropped)
	routeToPipeline   atomic.Int64 // frames routed to normal pipeline
	routeFiltered     atomic.Int64 // frames filtered (non-program, FTB, etc.)

	// Frame deadline monitor: tracks pipeline latency violations.
	frameBudgetNs      atomic.Int64 // frame budget in nanoseconds (33ms for 30fps)
	deadlineViolations atomic.Int64 // count of frames that exceeded budget

	// forceNextIDR is set when a new output viewer joins the program relay
	// (e.g., SRT output starts). The next encode call forces an IDR keyframe
	// so the TSMuxer can initialize immediately instead of waiting up to
	// one full GOP interval (~2 seconds).
	forceNextIDR atomic.Bool

	// Raw video output tap — receives deep copy of YUV after processing,
	// before encode. Used by MXL output to write raw video to shared memory.
	rawVideoSink atomic.Pointer[RawVideoSink]

	// Raw monitor output — sends YUV copy to program-raw MoQ track.
	rawMonitorSink atomic.Pointer[RawVideoSink]

	// Async video processing: frames are sent to videoProcCh and processed
	// in a dedicated goroutine, decoupling the source relay's delivery
	// goroutine from the 30-100ms decode+encode overhead. Without this,
	// audio delivery from the same goroutine gets starved.
	videoProcCh   chan videoProcWork
	videoProcDone chan struct{}
}

// videoProcWork represents a unit of work for the async video processing
// goroutine. Contains a pre-decoded YUV frame needing encode + broadcast.
type videoProcWork struct {
	// yuvFrame: pre-decoded YUV from source decoder or transition engine, needing encode only
	yuvFrame *ProcessingFrame
}

// Compile-time check that Switcher implements the frameHandler interface.
var _ frameHandler = (*Switcher)(nil)

// New creates a Switcher that forwards program frames to programRelay.
func New(programRelay *distribution.Relay) *Switcher {
	defaultFmt := DefaultFormat
	s := &Switcher{
		log:          slog.With("component", "switcher"),
		sources:      make(map[string]*sourceState),
		programRelay: programRelay,
		health:       newHealthMonitor(),
		videoProcCh:  make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
	}
	s.frameBudgetNs.Store(defaultFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&defaultFmt)
	s.delayBuffer = NewDelayBuffer(s)
	go s.videoProcessingLoop()
	return s
}

// SetMixer attaches an audio mixer to the switcher for state broadcasts.
// When set, buildStateLocked will include audio channel states, master level,
// and program peak levels in the ControlRoomState. If the mixer also implements
// audioCutHandler, crossfade and AFV program changes are triggered automatically
// on Cut().
func (s *Switcher) SetMixer(m audioStateProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mixer = m
	if handler, ok := m.(audioCutHandler); ok {
		s.audioCut = handler
	}
	if handler, ok := m.(audioTransitionHandler); ok {
		s.audioTransition = handler
	}
}

// SetMetrics attaches Prometheus metrics to the switcher for production
// observability. When set, the switcher increments counters for cuts,
// transitions, and IDR gate events alongside the existing atomic debug counters.
func (s *Switcher) SetMetrics(m *metrics.Metrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promMetrics = m
}

// SetRawVideoSink sets or clears the raw video output tap.
// The sink receives a deep copy of each processed YUV420p frame after all
// video processing (keying, compositor) but before H.264 encode. This is
// used by MXL output to write raw video to shared memory. Pass nil to disable.
func (s *Switcher) SetRawVideoSink(sink RawVideoSink) {
	if sink != nil {
		s.rawVideoSink.Store(&sink)
	} else {
		s.rawVideoSink.Store(nil)
	}
}

// SetRawMonitorSink sets or clears the raw monitor output tap.
// Like RawVideoSink, it receives a deep copy of each processed YUV420p frame
// after all video processing but before H.264 encode.
func (s *Switcher) SetRawMonitorSink(sink RawVideoSink) {
	if sink != nil {
		s.rawMonitorSink.Store(&sink)
	} else {
		s.rawMonitorSink.Store(nil)
	}
}

// Close stops the health monitor, delay buffer, frame sync, and unregisters all sources.
func (s *Switcher) Close() {
	s.health.stop()
	s.delayBuffer.Close()
	// Shut down async video processing goroutine.
	close(s.videoProcCh)
	<-s.videoProcDone
	s.mu.Lock()
	if s.frameSync != nil {
		s.frameSync.Stop()
	}
	pipeCodecs := s.pipeCodecs
	s.mu.Unlock()
	if pipeCodecs != nil {
		pipeCodecs.close()
	}
	s.mu.Lock()
	keys := make([]string, 0, len(s.sources))
	for k := range s.sources {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	for _, k := range keys {
		s.UnregisterSource(k)
	}
}

// OnStateChange registers a callback invoked whenever the switcher state
// changes. Multiple callbacks may be registered; they are called in order.
// Callbacks are called outside the lock so they may safely perform slow
// operations (JSON marshal, network I/O).
func (s *Switcher) OnStateChange(cb func(internal.ControlRoomState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateCallbacks = append(s.stateCallbacks, cb)
}

// SetAudioHandler registers a handler that receives audio frames from ALL
// sources. When set, the handler (typically an audio mixer) is responsible
// for deciding which audio reaches the program output. When no handler is
// set, the original behavior (only program source audio forwarded) is used.
func (s *Switcher) SetAudioHandler(handler func(sourceKey string, frame *media.AudioFrame)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioHandler = handler
}

// SetTransitionConfig stores the transition codec configuration under lock.
func (s *Switcher) SetTransitionConfig(config TransitionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transConfig = &config
}

// SetAudioTransition attaches an audio transition handler for dissolve sync.
func (s *Switcher) SetAudioTransition(handler audioTransitionHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audioTransition = handler
}

// RequestKeyframe forces the next encoded frame to be an IDR keyframe.
// Called when a new output viewer joins (e.g., SRT output starts) so the
// TSMuxer can initialize immediately without waiting for the next GOP boundary.
func (s *Switcher) RequestKeyframe() {
	s.forceNextIDR.Store(true)
}

// SetCompositor attaches the DSK graphics compositor. The compositor's
// ProcessYUV method is called in the video processing pipeline when active.
func (s *Switcher) SetCompositor(c *graphics.Compositor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compositorRef = c
}

// SetKeyBridge attaches the upstream key bridge for chroma/luma keying.
// The bridge's ProcessYUV method is called in the video processing pipeline.
func (s *Switcher) SetKeyBridge(kb *graphics.KeyProcessorBridge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyBridge = kb
}

// SetSourceDecoderFactory enables always-decode mode. When set, RegisterSource
// creates a per-source decoder that decodes H.264 to raw YUV at ingest time,
// eliminating keyframe waits on cuts and transitions. Must be called before
// any sources are registered.
func (s *Switcher) SetSourceDecoderFactory(factory transition.DecoderFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sourceDecoderFactory = factory
	s.log.Info("always-decode mode enabled")
}

// SetPipelineCodecs creates the shared pipeline encoder for the video
// processing chain. Called from app.go during initialization.
func (s *Switcher) SetPipelineCodecs(encoderFactory transition.EncoderFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipeCodecs = &pipelineCodecs{
		encoderFactory: encoderFactory,
		formatRef:      &s.pipelineFormat,
	}
}

// SetPipelineVideoInfoCallback sets the callback invoked when the pipeline
// encoder produces a keyframe with new SPS/PPS parameters.
func (s *Switcher) SetPipelineVideoInfoCallback(cb func(sps, pps []byte, width, height int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipeCodecs != nil {
		s.pipeCodecs.onVideoInfoChange = cb
	}
}

// SetFrameSync enables or disables the freerun frame synchronizer. When
// enabled, all source video and audio frames are buffered and released at
// a common tick rate (program frame rate) instead of flowing through the
// per-source delay buffer. This ensures frame-aligned output across sources.
//
// The tickRate parameter sets the release interval (e.g., 33ms for 30fps).
// Passing 0 uses the default of 33.333ms (30fps).
//
// When enabled, existing source viewers are re-wired to route through the
// FrameSynchronizer. When disabled, they revert to the delay buffer.
func (s *Switcher) SetFrameSync(enabled bool, tickRate time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if enabled == s.frameSyncActive {
		return
	}

	if enabled {
		if tickRate <= 0 {
			if f := s.pipelineFormat.Load(); f != nil {
				tickRate = f.FrameDuration()
			} else {
				tickRate = 33333 * time.Microsecond
			}
		}
		fs := NewFrameSynchronizer(tickRate,
			func(sourceKey string, frame media.VideoFrame) {
				s.handleVideoFrame(sourceKey, &frame)
			},
			func(sourceKey string, frame media.AudioFrame) {
				s.handleAudioFrame(sourceKey, &frame)
			},
		)
		fs.onRawVideo = func(sourceKey string, pf *ProcessingFrame) {
			s.handleRawVideoFrame(sourceKey, pf)
		}
		s.frameSync = fs

		// Wire all existing source viewers to the frame sync.
		for key, ss := range s.sources {
			if ss.viewer != nil {
				ss.viewer.frameSync.Store(fs)
				ss.viewer.delayBuffer.Store(nil) // bypass delay buffer
			}
			fs.AddSource(key)
		}
		fs.Start()
		s.log.Info("frame sync enabled", "tick_rate", tickRate)
	} else {
		if s.frameSync != nil {
			s.frameSync.Stop()
		}
		s.frameSync = nil

		// Revert all source viewers to the delay buffer.
		for _, ss := range s.sources {
			if ss.viewer != nil {
				ss.viewer.frameSync.Store(nil)
				ss.viewer.delayBuffer.Store(s.delayBuffer)
			}
		}
		s.log.Info("frame sync disabled")
	}
	s.frameSyncActive = enabled
}

// SetFRCQuality sets the frame rate conversion quality for all sources.
// Only effective when frame sync is enabled.
func (s *Switcher) SetFRCQuality(q FRCQuality) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frameSync != nil {
		s.frameSync.SetFRCQuality(q)
	}
	s.log.Info("FRC quality set", "quality", q.String())
}

// SetFrameBudget sets the per-frame processing time budget in nanoseconds.
// When pipeline latency exceeds this budget, deadlineViolations is incremented.
// Default is 33ms (30fps). Call with 16_666_666 for 60fps sources.
func (s *Switcher) SetFrameBudget(ns int64) {
	s.frameBudgetNs.Store(ns)
}

// PipelineFormat returns the current pipeline format.
func (s *Switcher) PipelineFormat() PipelineFormat {
	if p := s.pipelineFormat.Load(); p != nil {
		return *p
	}
	return DefaultFormat
}

// SetPipelineFormat changes the global pipeline format at runtime.
// Returns error if a transition is currently active.
// Propagates change to: frame budget, frame sync tick rate, encoder.
func (s *Switcher) SetPipelineFormat(f PipelineFormat) error {
	s.mu.Lock()

	if s.state.isInTransition() {
		s.mu.Unlock()
		return ErrFormatDuringTransition
	}

	s.pipelineFormat.Store(&f)
	s.frameBudgetNs.Store(f.FrameBudgetNs())

	// Update frame sync tick rate if active
	if s.frameSyncActive && s.frameSync != nil {
		s.frameSync.SetTickRate(f.FrameDuration())
	}

	// Force encoder recreation on next frame
	if s.pipeCodecs != nil {
		s.pipeCodecs.invalidateEncoder()
	}

	s.log.Info("pipeline format changed",
		"name", f.Name,
		"width", f.Width,
		"height", f.Height,
		"fps", fmt.Sprintf("%d/%d", f.FPSNum, f.FPSDen))

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// LastBroadcastVideoPTS returns the PTS of the most recently broadcast video
// frame to the program relay. Used by the replay system to anchor its output
// PTS to the program timeline.
func (s *Switcher) LastBroadcastVideoPTS() int64 {
	return s.lastBroadcastPTS.Load()
}

// ProgramSource returns the key of the current program source.
func (s *Switcher) ProgramSource() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.programSource
}

// trackBroadcastInterval records the time gap since the last program broadcast
// and logs a warning when the gap exceeds 100ms, helping diagnose fps drops.
func (s *Switcher) trackBroadcastInterval() {
	now := time.Now().UnixNano()
	prev := s.lastBroadcastNano.Swap(now)
	if prev == 0 {
		return // first broadcast
	}
	gap := now - prev
	updateAtomicMax(&s.maxBroadcastIntervalNano, gap)
	// Log when gap exceeds 100ms (>2 frame times at 24fps) to pinpoint stalls
	if gap > 100_000_000 { // 100ms in nanoseconds
		s.mu.RLock()
		programSrc := s.programSource
		hasEngine := s.transEngine != nil
		state := s.state
		s.mu.RUnlock()
		s.log.Warn("program broadcast gap",
			"gap_ms", float64(gap)/1e6,
			"state", state.String(),
			"program_source", programSrc,
			"has_engine", hasEngine,
			"idle_engine_drops", s.routeToIdleEngine.Load(),
		)
	}
}

// trackOutputFPS maintains a 1-second sliding window to compute output FPS.
func (s *Switcher) trackOutputFPS() {
	now := time.Now().UnixNano()
	windowStart := s.outputFPSWindowStart.Load()
	if windowStart == 0 {
		s.outputFPSWindowStart.Store(now)
		s.outputFPSCount.Store(1)
		return
	}
	elapsed := now - windowStart
	if elapsed >= 1_000_000_000 { // 1 second
		count := s.outputFPSCount.Swap(1)
		s.outputFPSLastSecond.Store(count)
		s.outputFPSWindowStart.Store(now)
	} else {
		s.outputFPSCount.Add(1)
	}
}

// measureTransSeam checks if a transition seam measurement is pending and
// records the gap. Called on every program broadcast to capture the time
// between transition completion and first post-transition frame output.
func (s *Switcher) measureTransSeam() {
	start := s.transSeamStartNano.Swap(0)
	if start == 0 {
		return
	}
	gap := time.Now().UnixNano() - start
	s.transSeamLastNano.Store(gap)
	updateAtomicMax(&s.transSeamMaxNano, gap)
	s.transSeamCount.Add(1)
	s.log.Info("transition seam measured", "gap_ms", float64(gap)/1e6)
}

// broadcastToProgram sends a video frame to the program relay with a
// monotonically increasing GroupID. Uses a shallow struct copy to avoid
// mutating the caller's frame (which may be shared with other viewers).
// Uses atomic operations for programGroupID so it can be called while
// s.mu is held.
func (s *Switcher) broadcastToProgram(frame *media.VideoFrame) {
	f := *frame // shallow struct copy — avoids mutating shared frame
	if f.IsKeyframe {
		f.GroupID = s.programGroupID.Add(1)
	} else {
		f.GroupID = s.programGroupID.Load()
	}
	s.lastBroadcastPTS.Store(f.PTS)
	s.measureTransSeam()
	s.trackBroadcastInterval()
	s.trackOutputFPS()
	s.videoBroadcastCount.Add(1)
	s.programRelay.BroadcastVideo(&f)
}

// broadcastOwnedToProgram sends an owned frame (safe to mutate) to the
// program relay with a monotonically increasing GroupID. Use for frames
// from pipelineCodecs.encode() or GOP replay deep copies.
func (s *Switcher) broadcastOwnedToProgram(frame *media.VideoFrame) {
	if frame.IsKeyframe {
		frame.GroupID = s.programGroupID.Add(1)
	} else {
		frame.GroupID = s.programGroupID.Load()
	}
	s.lastBroadcastPTS.Store(frame.PTS)
	s.measureTransSeam()
	s.trackBroadcastInterval()
	s.trackOutputFPS()
	s.videoBroadcastCount.Add(1)
	s.programRelay.BroadcastVideo(frame)
}


// enqueueVideoWork sends a work item to the async video processing goroutine
// with newest-wins drop policy when the channel is full.
func (s *Switcher) enqueueVideoWork(work videoProcWork) {
	select {
	case s.videoProcCh <- work:
	default:
		// Channel full — drop oldest, enqueue new (newest-wins).
		select {
		case <-s.videoProcCh:
		default:
		}
		select {
		case s.videoProcCh <- work:
		default:
		}
		s.videoProcDropped.Add(1)
	}
}

// videoProcessingLoop runs in a dedicated goroutine, draining videoProcCh
// and running each frame through the encode pipeline. This prevents
// the source relay's delivery goroutine from blocking on video processing,
// which would starve audio delivery.
func (s *Switcher) videoProcessingLoop() {
	defer close(s.videoProcDone)
	for work := range s.videoProcCh {
		if work.yuvFrame != nil {
			s.encodeAndBroadcastTransition(work.yuvFrame)
		}
	}
}


// broadcastProcessed handles frames that are already decoded to YUV
// (e.g., from the transition engine). Runs YUV processors, then encodes once.
func (s *Switcher) broadcastProcessed(yuv []byte, width, height int, pts int64, isKeyframe bool) {
	s.transOutputCount.Add(1)
	s.mu.RLock()
	keyBridge := s.keyBridge
	compositor := s.compositorRef
	hasPipeline := s.pipeCodecs != nil
	var groupID uint32
	if ss, ok := s.sources[s.programSource]; ok {
		groupID = ss.lastGroupID.Load()
	}
	s.mu.RUnlock()

	if !hasPipeline {
		return
	}

	// Run YUV processors synchronously (fast, sub-millisecond).
	if keyBridge != nil && keyBridge.HasEnabledKeysWithFills() {
		yuv = keyBridge.ProcessYUV(yuv, width, height)
	}
	if compositor != nil && compositor.IsActive() {
		yuv = compositor.ProcessYUV(yuv, width, height)
	}

	// Deep-copy YUV before async enqueue: the transition engine's FrameBlender
	// reuses its output buffer, so the next IngestFrame overwrites it. The
	// async encoder must operate on its own copy.
	buf := getYUVBuffer(len(yuv))
	copy(buf, yuv)

	pf := &ProcessingFrame{
		YUV: buf, Width: width, Height: height,
		PTS: pts, DTS: pts, IsKeyframe: isKeyframe,
		Codec:   "h264", // only codec supported today
		GroupID: groupID,
	}
	s.enqueueVideoWork(videoProcWork{yuvFrame: pf})
}

// broadcastProcessedFromPF handles a ProcessingFrame from the always-decode
// pipeline. Runs YUV processors (keying, compositor), then enqueues for
// async encode + broadcast. Similar to broadcastProcessed but takes a
// ProcessingFrame directly (no separate yuv/w/h/pts args).
func (s *Switcher) broadcastProcessedFromPF(pf *ProcessingFrame) {
	s.mu.RLock()
	keyBridge := s.keyBridge
	compositor := s.compositorRef
	hasPipeline := s.pipeCodecs != nil
	s.mu.RUnlock()

	if !hasPipeline {
		return
	}

	// Deep-copy YUV BEFORE in-place processing. The frame sync and FRC
	// retain references to the original buffer for repeated/interpolated
	// frames. Without this copy, the compositor bakes the overlay into
	// the retained buffer, causing progressive opacity accumulation on
	// repeated frames (visible as overlay blinking/pulsing).
	cp := pf.DeepCopy()

	// Run YUV processors synchronously on the copy (fast, sub-millisecond).
	if keyBridge != nil && keyBridge.HasEnabledKeysWithFills() {
		cp.YUV = keyBridge.ProcessYUV(cp.YUV, cp.Width, cp.Height)
	}
	if compositor != nil && compositor.IsActive() {
		cp.YUV = compositor.ProcessYUV(cp.YUV, cp.Width, cp.Height)
	}

	s.enqueueVideoWork(videoProcWork{yuvFrame: cp})
}

// encodeAndBroadcastTransition encodes a pre-decoded YUV frame from the
// transition engine and broadcasts it to the program relay. Called from
// the videoProcessingLoop goroutine.
func (s *Switcher) encodeAndBroadcastTransition(pf *ProcessingFrame) {
	start := time.Now()
	defer pf.ReleaseYUV()
	defer func() {
		dur := time.Since(start).Nanoseconds()
		s.videoProcLastNano.Store(dur)
		s.videoProcCount.Add(1)
		updateAtomicMax(&s.videoProcMaxNano, dur)
		if dur > s.frameBudgetNs.Load() {
			s.deadlineViolations.Add(1)
		}
	}()

	s.mu.RLock()
	pipeCodecs := s.pipeCodecs
	s.mu.RUnlock()

	if pipeCodecs == nil {
		return
	}

	// MXL output tap — deep copy YUV after all processing, before encode
	if sinkPtr := s.rawVideoSink.Load(); sinkPtr != nil {
		cp := pf.DeepCopy()
		(*sinkPtr)(cp)
	}

	// Raw monitor tap — deep copy YUV for low-latency program monitor
	if sinkPtr := s.rawMonitorSink.Load(); sinkPtr != nil {
		cp := pf.DeepCopy()
		(*sinkPtr)(cp)
	}

	encStart := time.Now()
	forceIDR := pf.IsKeyframe || s.forceNextIDR.CompareAndSwap(true, false)
	frame, err := pipeCodecs.encode(pf, forceIDR)
	encDur := time.Since(encStart).Nanoseconds()
	s.pipeEncodeLastNano.Store(encDur)
	updateAtomicMax(&s.pipeEncodeMaxNano, encDur)
	if s.promMetrics != nil {
		s.promMetrics.PipelineEncodeDuration.Observe(float64(encDur) / 1e9)
	}
	if err != nil {
		s.log.Warn("pipeline encode failed, dropping frame", "error", err, "path", "transition")
		if s.promMetrics != nil {
			s.promMetrics.PipelineEncodeErrorsTotal.Inc()
		}
		return
	}
	if frame == nil {
		// Encoder buffering (e.g. VideoToolbox warmup) — no output yet.
		s.pipeEncodeNil.Add(1)
		return
	}

	if s.promMetrics != nil {
		s.promMetrics.PipelineFramesProcessed.Inc()
	}
	s.broadcastOwnedToProgram(frame)
	// NOTE: Do NOT putAVC1Buffer(frame.WireData) here. BroadcastVideo fans
	// out the frame pointer to viewers via buffered channels. Those viewers
	// (output muxer, SRT destinations, WebTransport) process frames
	// asynchronously. Recycling the backing buffer immediately would let
	// the next encode overwrite WireData while viewers still reference it.
	// Let GC reclaim the buffer after all viewers release the frame.
}

// StartTransition begins a mix/dip/wipe/stinger transition from the current
// program source to the given target source. Frames from both sources are
// routed to the TransitionEngine which produces blended output on the program
// relay. wipeDirection is only used when transType is "wipe"; pass empty
// string otherwise.
//
// The ctx parameter is checked before the expensive codec initialization phase;
// a cancelled context will abort the transition early and roll back state.
func (s *Switcher) StartTransition(ctx context.Context, sourceKey string, transType string, durationMs int, wipeDirection string, opts ...TransitionOption) error {
	// Phase 1: Validate and read state under write lock. Set state to
	// StateTransitioning to prevent concurrent starts, then release the lock
	// so warmup can proceed without blocking frame routing.
	s.mu.Lock()

	if s.transConfig == nil {
		s.mu.Unlock()
		return fmt.Errorf("transition not configured")
	}
	if s.state.isInTransition() {
		s.mu.Unlock()
		return fmt.Errorf("transition: %w", transition.ErrTransitionActive)
	}
	if s.state.isFTBActive() {
		s.mu.Unlock()
		return fmt.Errorf("cannot start transition: %w", transition.ErrFTBActive)
	}
	if s.programSource == "" {
		s.mu.Unlock()
		return fmt.Errorf("no program source set")
	}
	if sourceKey == "" {
		s.mu.Unlock()
		return fmt.Errorf("no target source specified")
	}
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}

	if s.programSource == sourceKey {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrAlreadyOnProgram)
	}

	// Apply options
	var topts transitionOpts
	for _, opt := range opts {
		opt(&topts)
	}

	tt := transition.TransitionType(transType)
	if tt != transition.TransitionMix && tt != transition.TransitionDip && tt != transition.TransitionWipe && tt != transition.TransitionStinger {
		s.mu.Unlock()
		return fmt.Errorf("unsupported transition type: %q", transType)
	}

	if tt == transition.TransitionStinger && topts.stingerData == nil {
		s.mu.Unlock()
		return fmt.Errorf("stinger transition requires stinger data")
	}

	// Validate wipe direction when type is wipe
	var wipeDir transition.WipeDirection
	if tt == transition.TransitionWipe {
		wipeDir = transition.WipeDirection(wipeDirection)
		if !transition.ValidWipeDirections[wipeDir] {
			s.mu.Unlock()
			return fmt.Errorf("invalid wipe direction: %q", wipeDirection)
		}
	}

	fromSource := s.programSource

	// Capture pipeline dimensions before releasing lock.
	hintW, hintH := s.pipeCodecs.dimensions()

	// Mark transition as starting to prevent concurrent StartTransition/FTB calls.
	// handleVideoFrame checks transEngine != nil to route frames, so setting
	// StateTransitioning without transEngine is safe — frames won't route to
	// the engine and normal frame processing continues for the current program source.
	s.transitionState(StateTransitioning)
	s.mu.Unlock()

	// Capture decoder factory — still needed for virtual/legacy sources
	// that may send H.264 via IngestFrame during transitions.
	decoderFactory := s.transConfig.DecoderFactory

	// Phase 2: Create engine and start it with NO lock held.
	// In always-decode mode, most sources provide raw YUV via IngestRawFrame.
	// The decoder factory is passed for backward compatibility with virtual
	// sources that may still send H.264 via handleVideoFrame → IngestFrame.

	// Check for cancellation before engine creation.
	if err := ctx.Err(); err != nil {
		s.mu.Lock()
		s.transitionState(StateIdle)
		s.mu.Unlock()
		return fmt.Errorf("start transition: %w", err)
	}

	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: decoderFactory,
		WipeDirection:  wipeDir,
		Stinger:        topts.stingerData,
		Easing:         topts.easing,
		HintWidth:      hintW,
		HintHeight:     hintH,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			s.broadcastProcessed(yuv, width, height, pts, isKeyframe)
		},
		OnComplete: func(aborted bool) {
			s.handleTransitionComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, sourceKey, tt, durationMs); err != nil {
		// Roll back state since we failed to start.
		s.mu.Lock()
		s.transitionState(StateIdle)
		s.mu.Unlock()
		return fmt.Errorf("start transition: %w", err)
	}

	// Phase 3: Start audio crossfade BEFORE publishing the engine.
	// This eliminates a race where video frames see the engine and call
	// OnTransitionPosition before the mixer receives OnTransitionStart,
	// causing a gain discontinuity (audible pop).
	s.mu.Lock()
	audioHandler := s.audioTransition
	s.mu.Unlock()

	if audioHandler != nil {
		audioMode := audio.AudioCrossfade
		if tt == transition.TransitionDip {
			audioMode = audio.AudioDipToSilence
		}
		audioHandler.OnTransitionStart(fromSource, sourceKey, audioMode, durationMs)
	}

	// Now publish the engine — audio crossfade is already active, so the
	// first OnTransitionPosition from a video frame will be handled.
	s.mu.Lock()
	s.transEngine = engine
	s.previewSource = sourceKey
	s.transitionsStarted.Add(1)

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.log.Info("transition started",
		"type", string(tt), "from", fromSource, "to", sourceKey, "duration_ms", durationMs)

	s.notifyStateChange(snapshot)
	return nil
}

// SetTransitionPosition sets the T-bar position during an active transition.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) SetTransitionPosition(ctx context.Context, position float64) error {
	s.mu.RLock()
	engine := s.transEngine
	inTrans := s.state.isInTransition()
	audioHandler := s.audioTransition
	s.mu.RUnlock()

	if !inTrans || engine == nil {
		return ErrNoTransition
	}

	engine.SetPosition(position)

	if audioHandler != nil {
		audioHandler.OnTransitionPosition(position)
	}
	return nil
}

// FadeToBlack starts or toggles a Fade to Black transition. If FTB is already
// active and no transition is running, it toggles off (restores normal output).
// If a non-FTB transition is active, FTB is rejected.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) FadeToBlack(ctx context.Context) error {
	s.mu.Lock()

	if s.transConfig == nil {
		s.mu.Unlock()
		return fmt.Errorf("transition not configured")
	}

	// Reject if a non-FTB transition is active (mix/dip/wipe)
	if s.state == StateTransitioning {
		s.mu.Unlock()
		return fmt.Errorf("cannot FTB while mix/dip transition is active: %w", transition.ErrTransitionActive)
	}

	// Toggle off: FTB is active but transition is complete (fully black).
	// Start a reverse FTB transition to fade back from black.
	if s.state == StateFTB {
		if s.programSource == "" {
			s.mu.Unlock()
			return fmt.Errorf("no program source set")
		}

		fromSource := s.programSource
		ftbHintW, ftbHintH := s.pipeCodecs.dimensions()
		ftbRevDecoderFactory := s.transConfig.DecoderFactory

		// Mark transition as starting, then release lock.
		s.transitionState(StateFTBReversing)
		s.mu.Unlock()

		// No decoder warmup needed — sources provide raw YUV.
		engine := transition.NewTransitionEngine(transition.EngineConfig{
			DecoderFactory: ftbRevDecoderFactory,
			HintWidth:      ftbHintW,
			HintHeight:     ftbHintH,
			Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
				s.broadcastProcessed(yuv, width, height, pts, isKeyframe)
			},
			OnComplete: func(aborted bool) {
				s.handleFTBReverseComplete(aborted)
			},
		})

		if err := engine.Start(fromSource, "", transition.TransitionFTBReverse, defaultFTBDurMs); err != nil {
			s.mu.Lock()
			s.transitionState(StateFTB) // Roll back to StateFTB
			s.mu.Unlock()
			return fmt.Errorf("start FTB reverse: %w", err)
		}

		// Start audio BEFORE publishing engine (prevents position race).
		s.mu.RLock()
		audioHandler := s.audioTransition
		s.mu.RUnlock()
		if audioHandler != nil {
			audioHandler.SetProgramMute(false)
			audioHandler.OnTransitionStart(fromSource, "", audio.AudioFadeIn, defaultFTBDurMs)
		}

		// Now publish the engine.
		s.mu.Lock()
		s.transEngine = engine
		s.transitionsStarted.Add(1)

		atomic.AddUint64(&s.seq, 1)
		snapshot := s.buildStateLocked()
		s.mu.Unlock()

		s.log.Info("transition started",
			"type", "ftb_reverse", "from", fromSource, "to", "", "duration_ms", defaultFTBDurMs)

		s.notifyStateChange(snapshot)
		return nil
	}

	if s.programSource == "" {
		s.mu.Unlock()
		return fmt.Errorf("no program source set")
	}

	fromSource := s.programSource
	ftbFwdHintW, ftbFwdHintH := s.pipeCodecs.dimensions()
	ftbFwdDecoderFactory := s.transConfig.DecoderFactory

	// Mark transition as starting, then release lock.
	s.transitionState(StateFTBTransitioning)
	s.mu.Unlock()

	// No decoder warmup needed — sources provide raw YUV.
	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: ftbFwdDecoderFactory,
		HintWidth:      ftbFwdHintW,
		HintHeight:     ftbFwdHintH,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			s.broadcastProcessed(yuv, width, height, pts, isKeyframe)
		},
		OnComplete: func(aborted bool) {
			s.handleFTBComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, "", transition.TransitionFTB, defaultFTBDurMs); err != nil {
		s.mu.Lock()
		s.transitionState(StateIdle)
		s.mu.Unlock()
		return fmt.Errorf("start FTB: %w", err)
	}

	// Start audio BEFORE publishing engine (prevents position race).
	s.mu.RLock()
	audioHandler := s.audioTransition
	s.mu.RUnlock()
	if audioHandler != nil {
		audioHandler.OnTransitionStart(fromSource, "", audio.AudioFadeOut, defaultFTBDurMs)
	}

	// Now publish the engine.
	s.mu.Lock()
	s.transEngine = engine
	s.transitionsStarted.Add(1)

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.log.Info("transition started",
		"type", "ftb", "from", fromSource, "to", "", "duration_ms", defaultFTBDurMs)

	s.notifyStateChange(snapshot)
	return nil
}

// AbortTransition stops any active transition and restores normal frame routing.
func (s *Switcher) AbortTransition() {
	s.mu.Lock()
	engine := s.transEngine
	wasActive := s.state.isInTransition()
	audioHandler := s.audioTransition
	var transType string

	if wasActive {
		if engine != nil {
			transType = string(engine.TransitionType())
		}
		// When aborting a reverse FTB, keep in FTB state (screen stays black).
		// For all other transitions (including forward FTB), return to idle.
		if s.state == StateFTBReversing {
			s.transitionState(StateFTB)
		} else {
			s.transitionState(StateIdle)
		}
		s.transEngine = nil
		atomic.AddUint64(&s.seq, 1)
	}
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if engine != nil {
		engine.Stop()
	}
	if wasActive {
		s.log.Warn("transition aborted", "type", transType, "reason", "manual abort")

		if audioHandler != nil {
			audioHandler.OnTransitionComplete()
		}
		s.notifyStateChange(snapshot)
	}
}

// handleTransitionComplete is called by the TransitionEngine when a mix/dip
// transition finishes. If completed (not aborted), it swaps program/preview
// sources. All sources use the raw pipeline (always-decode), so no GOP
// replay or IDR gating is needed — frames flow immediately.
func (s *Switcher) handleTransitionComplete(aborted bool) {
	completeStart := time.Now()

	s.mu.Lock()
	if !s.state.isInTransition() {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	var audioCut audioCutHandler
	var newProgram, oldProgram string

	if !aborted && s.transEngine != nil {
		newProgram = s.transEngine.ToSource()
		oldProgram = s.programSource
		if newProgram != "" {
			audioCut = s.audioCut
		}
	}

	transType := ""
	if s.transEngine != nil {
		transType = string(s.transEngine.TransitionType())
	}

	if newProgram != "" {
		s.programSource = newProgram
		s.previewSource = oldProgram
	}

	// Record transition seam start — the gap from now until the first
	// post-transition frame reaches broadcastToProgram.
	if !aborted {
		s.transSeamStartNano.Store(time.Now().UnixNano())
	}

	s.transitionState(StateIdle)
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues(transType).Inc()
	}
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	completeDur := time.Since(completeStart)
	if aborted {
		s.log.Warn("transition aborted", "type", transType, "reason", "engine aborted",
			"complete_ms", completeDur.Milliseconds())
	} else {
		s.log.Info("transition completed", "type", transType,
			"complete_ms", completeDur.Milliseconds(),
			"idle_engine_drops", s.routeToIdleEngine.Load())
	}

	if audioHandler != nil {
		audioHandler.OnTransitionComplete()
	}
	if !aborted && audioCut != nil {
		audioCut.OnProgramChange(snapshot.ProgramSource)
	}
	s.notifyStateChange(snapshot)
}

// handleFTBComplete is called by the TransitionEngine when an FTB transition
// finishes. FTB stays active (screen is black) unless aborted.
func (s *Switcher) handleFTBComplete(aborted bool) {
	s.mu.Lock()
	if !s.state.isInTransition() {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	if aborted {
		s.transitionState(StateIdle) // Aborted — return to idle
	} else {
		s.transitionState(StateFTB) // Completed — hold at black
	}
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb").Inc()
	}
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if aborted {
		s.log.Warn("transition aborted", "type", "ftb", "reason", "engine aborted")
	} else {
		s.log.Info("FTB activated")
	}

	if audioHandler != nil {
		audioHandler.OnTransitionComplete()
		if !aborted {
			// FTB completed — screen is black, mute program audio
			audioHandler.SetProgramMute(true)
		}
	}
	s.notifyStateChange(snapshot)
}

// handleFTBReverseComplete is called by the TransitionEngine when a reverse
// FTB transition finishes. If completed (not aborted), it transitions to
// StateIdle (screen is now fully visible). If aborted, it transitions to
// StateFTB (screen stays black). All sources use the raw pipeline
// (always-decode), so no GOP replay or IDR gating is needed.
func (s *Switcher) handleFTBReverseComplete(aborted bool) {
	s.mu.Lock()
	if !s.state.isInTransition() {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition

	if aborted {
		s.transitionState(StateFTB) // Aborted — screen stays black
	} else {
		// Record transition seam start for FTB reverse.
		s.transSeamStartNano.Store(time.Now().UnixNano())
		s.transitionState(StateIdle) // Completed — screen is visible
	}
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb_reverse").Inc()
	}

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if aborted {
		s.log.Warn("transition aborted", "type", "ftb_reverse", "reason", "engine aborted")
	} else {
		s.log.Info("FTB deactivated")
	}

	if audioHandler != nil {
		audioHandler.OnTransitionComplete()
		if aborted {
			// FTB reverse aborted — screen stays black, re-mute audio
			audioHandler.SetProgramMute(true)
		}
	}
	s.notifyStateChange(snapshot)
}

// RegisterSource adds a source to the switcher. A sourceViewer proxy is
// created and attached to the source's Relay so that frames flow into the
// Switcher's handleVideoFrame/handleAudioFrame methods tagged with the
// source key. When frame sync is active, frames route through the
// FrameSynchronizer; otherwise the delay buffer is attached for per-source
// lip-sync compensation.
func (s *Switcher) RegisterSource(key string, relay *distribution.Relay) {
	s.mu.Lock()
	viewer := newSourceViewer(key, s)
	useRaw := false

	// Always-decode mode: create per-source decoder that converts H.264 → YUV
	// at ingest time. Decoded frames route through frameSync/delayBuffer via callback.
	if s.sourceDecoderFactory != nil {
		cb := s.makeDecoderCallback(key)
		sd := newSourceDecoder(key, s.sourceDecoderFactory, cb)
		if sd != nil {
			viewer.srcDecoder.Store(sd)
			useRaw = true
		}
	}

	if s.frameSyncActive && s.frameSync != nil {
		viewer.frameSync.Store(s.frameSync)
		s.frameSync.AddSource(key)
	} else {
		viewer.delayBuffer.Store(s.delayBuffer)
	}
	relay.AddViewer(viewer)
	s.sources[key] = &sourceState{key: key, relay: relay, viewer: viewer, useRawPipeline: useRaw, position: len(s.sources) + 1}
	s.health.registerSource(key)
	// Count active decoders for memory warning.
	decoderCount := 0
	for _, ss := range s.sources {
		if ss.viewer != nil && ss.viewer.srcDecoder.Load() != nil {
			decoderCount++
		}
	}
	s.mu.Unlock()

	s.log.Info("source registered", "source_key", key, "raw_pipeline", useRaw)

	// Warn when estimated YUV memory exceeds 256MB (~85 1080p sources).
	// Each 1080p decoder holds ~3MB of YUV buffer.
	if estimatedMB := decoderCount * 3; estimatedMB > 256 {
		s.log.Warn("high source decoder memory usage",
			"active_decoders", decoderCount,
			"estimated_yuv_mb", estimatedMB)
	}
}

// makeDecoderCallback creates the callback function for a sourceDecoder.
// Decoded YUV frames route through frameSync or delayBuffer, same as H.264
// frames but using the raw video path.
func (s *Switcher) makeDecoderCallback(key string) func(string, *ProcessingFrame) {
	return func(sourceKey string, pf *ProcessingFrame) {
		// Route through frame sync or delay buffer (same as SendVideo
		// would for the legacy path, but for decoded YUV).
		s.mu.RLock()
		ss := s.sources[sourceKey]
		s.mu.RUnlock()
		if ss == nil || ss.viewer == nil {
			return
		}
		if fs := ss.viewer.frameSync.Load(); fs != nil {
			fs.IngestRawVideo(sourceKey, pf)
			return
		}
		if db := ss.viewer.delayBuffer.Load(); db != nil {
			db.handleRawVideoFrame(sourceKey, pf)
			return
		}
		s.handleRawVideoFrame(sourceKey, pf)
	}
}

// RegisterVirtualSource registers a transient internal source (e.g. replay).
// Virtual sources skip delay buffer, frame sync, and replay buffering.
// Safe to call if the key already exists — cleans up the old viewer first.
func (s *Switcher) RegisterVirtualSource(key string, relay *distribution.Relay) {
	s.mu.Lock()
	// Clean up existing registration to prevent viewer leak on rapid re-register.
	if old, exists := s.sources[key]; exists {
		old.relay.RemoveViewer(old.viewer.ID())
		delete(s.sources, key)
		s.health.removeSource(key)
	}
	viewer := newSourceViewer(key, s)
	relay.AddViewer(viewer)
	s.sources[key] = &sourceState{
		key:       key,
		label:     strings.ToUpper(key),
		relay:     relay,
		viewer:    viewer,
		isVirtual: true,
		position:  len(s.sources) + 1,
	}
	s.health.registerSource(key)
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()
	s.log.Info("virtual source registered", "source_key", key)
	s.notifyStateChange(snapshot)
}

// RegisterMXLSource registers a source that provides raw YUV420p frames
// directly (no Prism relay/viewer). Used for MXL shared-memory sources.
func (s *Switcher) RegisterMXLSource(key string) {
	s.mu.Lock()
	s.sources[key] = &sourceState{
		key:      key,
		label:    strings.ToUpper(key),
		position: len(s.sources) + 1,
		isMXL:    true,
	}
	s.health.registerSource(key)
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()
	s.log.Info("MXL source registered", "source_key", key)
	s.notifyStateChange(snapshot)
}

// RegisterReplaySource registers a transient replay source that receives
// raw YUV frames via IngestReplayVideo. Like virtual sources, replay
// sources skip delay buffer, frame sync, and replay buffering.
// Safe to call if the key already exists — cleans up the old registration.
func (s *Switcher) RegisterReplaySource(key string) {
	s.mu.Lock()
	if old, exists := s.sources[key]; exists {
		if old.relay != nil && old.viewer != nil {
			old.relay.RemoveViewer(old.viewer.ID())
		}
		delete(s.sources, key)
		s.health.removeSource(key)
	}
	s.sources[key] = &sourceState{
		key:       key,
		label:     strings.ToUpper(key),
		position:  len(s.sources) + 1,
		isVirtual: true,
	}
	s.health.registerSource(key)
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()
	s.log.Info("replay source registered", "source_key", key)
	s.notifyStateChange(snapshot)
}

// IngestReplayVideo accepts a raw YUV420p frame from the replay player.
// Routes through the full video processing pipeline (keying, compositor,
// encode) — identical to always-decode live sources.
func (s *Switcher) IngestReplayVideo(sourceKey string, pf *ProcessingFrame) {
	s.handleRawVideoFrame(sourceKey, pf)
}

// IngestRawVideo accepts a raw YUV420p frame from an MXL source.
// Delegates to handleRawVideoFrame which handles the full pipeline:
// health tracking → key fill ingest → transition routing → keying →
// compositor → encode → program relay. The srcDecoder stats block in
// handleRawVideoFrame safely handles nil viewers (MXL sources have none).
func (s *Switcher) IngestRawVideo(sourceKey string, pf *ProcessingFrame) {
	s.handleRawVideoFrame(sourceKey, pf)
}

// UnregisterSource removes a source from the switcher and detaches its
// viewer from the source Relay. If the removed source was on program or
// preview, those fields are cleared.
func (s *Switcher) UnregisterSource(key string) {
	s.mu.Lock()
	ss, ok := s.sources[key]
	if !ok {
		s.mu.Unlock()
		return
	}
	// Close per-source decoder if present (always-decode mode).
	var srcDec *sourceDecoder
	if ss.viewer != nil {
		srcDec = ss.viewer.srcDecoder.Swap(nil)
	}
	if ss.relay != nil && ss.viewer != nil {
		ss.relay.RemoveViewer(ss.viewer.ID())
	}
	delete(s.sources, key)
	s.health.removeSource(key)
	s.delayBuffer.RemoveSource(key)
	if s.frameSync != nil {
		s.frameSync.RemoveSource(key)
	}
	if s.programSource == key {
		s.programSource = ""
	}
	if s.previewSource == key {
		s.previewSource = ""
	}
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	// Close decoder outside the lock (may take time to drain).
	if srcDec != nil {
		srcDec.Close()
	}

	s.log.Info("source unregistered", "source_key", key)
	s.notifyStateChange(snapshot)
}

// Cut performs a hard cut to the named source, making it the program output.
// The previous program source is automatically moved to preview. If the
// source is already on program, Cut is a no-op (Seq is not incremented).
// When an audioCutHandler (mixer) is attached, Cut triggers an audio crossfade
// and AFV program change automatically.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) Cut(ctx context.Context, sourceKey string) error {
	var snapshot internal.ControlRoomState
	var oldProgram string
	var audioCut audioCutHandler
	changed := false

	s.mu.Lock()
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	if s.programSource != sourceKey {
		oldProgram = s.programSource
		s.programSource = sourceKey
		// All sources use the raw pipeline (always-decode or MXL) —
		// no IDR gating needed. Frames flow immediately after cut.
		s.cutsTotal.Add(1)
		if s.promMetrics != nil {
			s.promMetrics.CutsTotal.Inc()
		}
		if oldProgram != "" {
			s.previewSource = oldProgram
		}
		audioCut = s.audioCut
		atomic.AddUint64(&s.seq, 1)
		snapshot = s.buildStateLocked()
		changed = true
	}
	s.mu.Unlock()

	if changed {
		s.log.Info("cut executed", "source", sourceKey, "previous_source", oldProgram)

		// Record transition seam for cut timing diagnostics.
		s.transSeamStartNano.Store(time.Now().UnixNano())

		// Notify mixer of program change (AFV + crossfade) outside the lock.
		if audioCut != nil {
			if oldProgram != "" {
				audioCut.OnCut(oldProgram, sourceKey)
			}
			audioCut.OnProgramChange(sourceKey)
		}
		s.notifyStateChange(snapshot)
	}
	return nil
}

// SetPreview sets the preview source. This does not affect the program output.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) SetPreview(ctx context.Context, sourceKey string) error {
	s.mu.Lock()
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	s.previewSource = sourceKey
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// SetLabel sets a human-readable label for the given source.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) SetLabel(ctx context.Context, sourceKey, label string) error {
	s.mu.Lock()
	ss, ok := s.sources[sourceKey]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	ss.label = label
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// SetSourcePosition sets the display position for a source. Sources are
// ordered by position in the UI. If another source already occupies the
// target position, they swap positions.
func (s *Switcher) SetSourcePosition(sourceKey string, position int) error {
	if position < 1 {
		return fmt.Errorf("position %d: %w", position, ErrInvalidPosition)
	}
	s.mu.Lock()
	ss, ok := s.sources[sourceKey]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	// Swap with any source already at the target position
	for _, other := range s.sources {
		if other.key != sourceKey && other.position == position {
			other.position = ss.position
			break
		}
	}
	ss.position = position
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// SetSourceDelay sets the input delay for a source in milliseconds (0-500).
// A delay of 0 means no buffering (passthrough). Non-zero delays are used
// for lip-sync compensation. Returns ErrSourceNotFound if the source is not
// registered, or ErrInvalidDelay if the value is out of range.
func (s *Switcher) SetSourceDelay(sourceKey string, delayMs int) error {
	if delayMs < 0 || delayMs > maxDelayMs {
		return ErrInvalidDelay
	}
	s.mu.Lock()
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	s.mu.Unlock()

	s.delayBuffer.SetDelay(sourceKey, time.Duration(delayMs)*time.Millisecond)

	s.log.Info("source delay set", "source_key", sourceKey, "delay_ms", delayMs)

	s.mu.Lock()
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// GetSourceDelay returns the configured input delay in milliseconds for a
// source, or 0 if the source has no delay configured.
func (s *Switcher) GetSourceDelay(sourceKey string) int {
	return int(s.delayBuffer.GetDelay(sourceKey) / time.Millisecond)
}

// StartHealthMonitor begins periodic health checking at the given interval.
// When any source's health status changes, a state snapshot is published
// to all registered state-change callbacks.
func (s *Switcher) StartHealthMonitor(interval time.Duration) {
	s.health.start(interval, func() {
		snapshot := s.State()
		s.notifyStateChange(snapshot)
	})
}

// State returns a snapshot of the current control room state.
func (s *Switcher) State() internal.ControlRoomState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buildStateLocked()
}

// DebugSnapshot returns a map of debug instrumentation data for diagnostics.
func (s *Switcher) DebugSnapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sources := make(map[string]any, len(s.sources))
	activeDecoders := 0
	for key, ss := range s.sources {
		var videoIn, audioIn int64
		if ss.viewer != nil {
			videoIn = ss.viewer.videoSent.Load()
			audioIn = ss.viewer.audioSent.Load()
			if ss.viewer.srcDecoder.Load() != nil {
				activeDecoders++
			}
		}
		sources[key] = map[string]any{
			"video_frames_in":   videoIn,
			"audio_frames_in":   audioIn,
			"health_status":     string(s.health.rawStatus(key)),
			"last_frame_ago_ms": s.health.lastFrameAgoMs(key),
			"raw_pipeline":      ss.useRawPipeline,
		}
	}

	// Estimate ~3MB per 1080p YUV420 decoder output buffer.
	estimatedYUVMB := activeDecoders * 3

	result := map[string]any{
		"program_source":            s.programSource,
		"preview_source":            s.previewSource,
		"state":                     s.state.String(),
		"in_transition":             s.state.isInTransition(),
		"ftb_active":                s.state.isFTBActive(),
		"seq":                       atomic.LoadUint64(&s.seq),
		"sources":                   sources,
		"source_decoders": map[string]any{
			"active_count":     activeDecoders,
			"estimated_yuv_mb": estimatedYUVMB,
		},
		"cuts_total":            s.cutsTotal.Load(),
		"transitions_started":   s.transitionsStarted.Load(),
		"transitions_completed": s.transitionsCompleted.Load(),
		"deadline_violations":   s.deadlineViolations.Load(),
		"frame_budget_ms":       float64(s.frameBudgetNs.Load()) / 1e6,
		"video_pipeline": map[string]any{
			"frames_processed":     s.videoProcCount.Load(),
			"frames_broadcast":     s.videoBroadcastCount.Load(),
			"frames_dropped":       s.videoProcDropped.Load(),
			"encode_nil":           s.pipeEncodeNil.Load(),
			"trans_output":         s.transOutputCount.Load(),
			"last_proc_time_ms":    float64(s.videoProcLastNano.Load()) / 1e6,
			"max_proc_time_ms":     float64(s.videoProcMaxNano.Load()) / 1e6,
			"max_broadcast_gap_ms": float64(s.maxBroadcastIntervalNano.Load()) / 1e6,
			"route_to_engine":      s.routeToEngine.Load(),
			"route_to_idle_engine": s.routeToIdleEngine.Load(),
			"route_to_pipeline":    s.routeToPipeline.Load(),
			"route_filtered":       s.routeFiltered.Load(),
			"queue_len":            len(s.videoProcCh),
			"output_fps":           s.outputFPSLastSecond.Load(),
			"key_last_ms":          float64(s.pipeKeyLastNano.Load()) / 1e6,
			"key_max_ms":           float64(s.pipeKeyMaxNano.Load()) / 1e6,
			"composite_last_ms":    float64(s.pipeCompositeLastNano.Load()) / 1e6,
			"composite_max_ms":     float64(s.pipeCompositeMaxNano.Load()) / 1e6,
			"encode_last_ms":       float64(s.pipeEncodeLastNano.Load()) / 1e6,
			"encode_max_ms":        float64(s.pipeEncodeMaxNano.Load()) / 1e6,
			"trans_seam_last_ms":   float64(s.transSeamLastNano.Load()) / 1e6,
			"trans_seam_max_ms":    float64(s.transSeamMaxNano.Load()) / 1e6,
			"trans_seam_count":     s.transSeamCount.Load(),
		},
	}

	// Include transition engine timing when active
	if s.state.isInTransition() && s.transEngine != nil {
		result["transition_engine"] = s.transEngine.Timing()
	}

	// Include FRC info when frame sync is active.
	if s.frameSync != nil {
		result["frame_rate_converter"] = map[string]any{
			"quality": s.frameSync.FRCQuality().String(),
		}
	}

	// Program relay viewer stats — reveals MoQ channel drops.
	programViewers := s.programRelay.ViewerStatsAll()
	if len(programViewers) > 0 {
		pvs := make([]map[string]any, len(programViewers))
		for i, vs := range programViewers {
			pvs[i] = map[string]any{
				"id":               vs.ID,
				"video_sent":       vs.VideoSent,
				"video_dropped":    vs.VideoDropped,
				"audio_sent":       vs.AudioSent,
				"audio_dropped":    vs.AudioDropped,
				"bytes_sent":       vs.BytesSent,
				"last_video_ts_ms": vs.LastVideoTsMS,
			}
		}
		result["program_relay_viewers"] = pvs
	}

	// Per-source relay viewer stats for the same purpose.
	sourceViewers := make(map[string]any)
	for key, ss := range s.sources {
		if ss.relay == nil {
			continue
		}
		svs := ss.relay.ViewerStatsAll()
		if len(svs) == 0 {
			continue
		}
		viewers := make([]map[string]any, len(svs))
		for i, vs := range svs {
			viewers[i] = map[string]any{
				"id":            vs.ID,
				"video_sent":    vs.VideoSent,
				"video_dropped": vs.VideoDropped,
				"audio_sent":    vs.AudioSent,
				"audio_dropped": vs.AudioDropped,
				"bytes_sent":    vs.BytesSent,
			}
		}
		sourceViewers[key] = viewers
	}
	if len(sourceViewers) > 0 {
		result["source_relay_viewers"] = sourceViewers
	}

	return result
}

// SourceKeys returns the keys of all registered sources.
func (s *Switcher) SourceKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.sources))
	for k := range s.sources {
		keys = append(keys, k)
	}
	return keys
}

// buildStateLocked constructs a ControlRoomState snapshot. Caller must hold
// at least s.mu.RLock().
func (s *Switcher) buildStateLocked() internal.ControlRoomState {
	tally := make(map[string]string, len(s.sources))
	sources := make(map[string]internal.SourceInfo, len(s.sources))
	for key, ss := range s.sources {
		tally[key] = string(TallyIdle)
		sources[key] = internal.SourceInfo{
			Key:       key,
			Label:     ss.label,
			Status:    string(s.health.status(key)),
			Position:  ss.position,
			DelayMs:   int(s.delayBuffer.GetDelay(key) / time.Millisecond),
			IsVirtual: ss.isVirtual,
		}
	}
	if s.programSource != "" {
		tally[s.programSource] = string(TallyProgram)
	}
	if s.previewSource != "" && s.previewSource != s.programSource {
		tally[s.previewSource] = string(TallyPreview)
	}
	transType := "cut"
	if s.state.isInTransition() && s.transEngine != nil {
		transType = string(s.transEngine.TransitionType())
	}
	state := internal.ControlRoomState{
		ProgramSource:  s.programSource,
		PreviewSource:  s.previewSource,
		TransitionType: transType,
		InTransition:   s.state.isInTransition(),
		FTBActive:      s.state.isFTBActive(),
		TallyState:     tally,
		Sources:        sources,
		Seq:            atomic.LoadUint64(&s.seq),
		Timestamp:      time.Now().UnixMilli(),
	}
	if s.state.isInTransition() && s.transEngine != nil {
		state.TransitionPosition = s.transEngine.Position()
		state.TransitionEasing = string(s.transEngine.Easing())
	}

	// Populate audio state from mixer if available.
	if s.mixer != nil {
		state.AudioChannels = s.mixer.ChannelStates()
		state.MasterLevel = s.mixer.MasterLevel()
		state.ProgramPeak = s.mixer.ProgramPeak()
		state.GainReduction = s.mixer.GainReduction()
		state.MomentaryLUFS = s.mixer.MomentaryLUFS()
		state.ShortTermLUFS = s.mixer.ShortTermLUFS()
		state.IntegratedLUFS = s.mixer.IntegratedLUFS()
	}

	// Include pipeline format
	if f := s.pipelineFormat.Load(); f != nil {
		state.PipelineFormat = &internal.PipelineFormatInfo{
			Width:  f.Width,
			Height: f.Height,
			FPSNum: f.FPSNum,
			FPSDen: f.FPSDen,
			Name:   f.Name,
		}
	}

	return state
}

// notifyStateChange calls all registered state callbacks.
// Must be called WITHOUT holding s.mu to avoid blocking frame handlers.
func (s *Switcher) notifyStateChange(snapshot internal.ControlRoomState) {
	s.mu.RLock()
	cbs := s.stateCallbacks
	s.mu.RUnlock()
	for _, cb := range cbs {
		cb(snapshot)
	}
}

// updateFrameStats updates the rolling frame size and FPS estimates for a
// source. Called on every video frame from the source's viewer goroutine
// (single-writer). Uses an exponential moving average with alpha=0.1.
func (s *Switcher) updateFrameStats(ss *sourceState, frame *media.VideoFrame) {
	const alpha = 0.1 // EMA smoothing factor

	frameSize := float64(len(frame.WireData))
	ss.frameCount++

	if ss.frameCount == 1 {
		// First frame — seed the averages
		ss.avgFrameSize = frameSize
		ss.lastPTS = frame.PTS
		return
	}

	// Update frame size EMA
	ss.avgFrameSize = alpha*frameSize + (1-alpha)*ss.avgFrameSize

	// Update FPS EMA from PTS delta
	if frame.PTS > ss.lastPTS {
		deltaPTS := frame.PTS - ss.lastPTS
		// PTS is in microseconds (90kHz clock is common, but Prism uses µs)
		// Protect against unreasonable deltas (>1 second or negative)
		if deltaPTS > 0 && deltaPTS < 1_000_000 {
			instantFPS := 1_000_000.0 / float64(deltaPTS)
			if ss.avgFPS == 0 {
				ss.avgFPS = instantFPS
			} else {
				ss.avgFPS = alpha*instantFPS + (1-alpha)*ss.avgFPS
			}
		}
	}
	ss.lastPTS = frame.PTS
	if frame.GroupID > ss.lastGroupID.Load() {
		ss.lastGroupID.Store(frame.GroupID)
	}
}

// handleVideoFrame implements frameHandler. It is called by sourceViewers
// when a video frame arrives from a source (legacy H.264 path). In
// always-decode mode, most sources use handleRawVideoFrame instead.
// This handler is still used for virtual sources (replay) which produce
// pre-encoded H.264 output.
func (s *Switcher) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	s.health.recordFrame(sourceKey, time.Now())

	// Single RLock to snapshot all state needed for this frame.
	s.mu.RLock()
	ss := s.sources[sourceKey]
	isProgramSource := sourceKey == s.programSource
	engine := s.transEngine
	inTrans := s.state.isInTransition()
	isFTB := s.state.isFTBActive()
	audioHandler := s.audioTransition
	s.mu.RUnlock()

	// Update per-source frame statistics (single-writer per source viewer).
	if ss != nil {
		s.updateFrameStats(ss, frame)
	}

	// Check if transition is active — route both sources to engine.
	// Non-virtual sources should have per-source decoders and use
	// handleRawVideoFrame, but the engine can accept H.264 from
	// virtual sources via IngestFrame.
	if inTrans && engine != nil {
		if engine.State() != transition.StateActive {
			s.routeToIdleEngine.Add(1)
			return
		}
		s.routeToEngine.Add(1)
		// WireData is AVC1 (length-prefixed); decoder expects Annex B.
		annexB := codec.AVC1ToAnnexB(frame.WireData)
		if frame.IsKeyframe {
			annexB = codec.PrependSPSPPS(frame.SPS, frame.PPS, annexB)
		}
		engine.IngestFrame(sourceKey, annexB, frame.PTS, frame.IsKeyframe)

		// Sync audio crossfade position with video on every frame.
		if audioHandler != nil {
			audioHandler.OnTransitionPosition(engine.Position())
		}
		return
	}

	// Normal frame routing.
	if ss == nil || !isProgramSource || isFTB {
		s.routeFiltered.Add(1)
		return
	}

	s.routeToPipeline.Add(1)
	// Virtual sources (replay) and fallback non-raw sources pass through
	// directly. In always-decode mode, non-virtual sources normally use
	// handleRawVideoFrame, but this path handles legacy/test scenarios.
	s.broadcastToProgram(frame)
}

// handleAudioFrame implements frameHandler. It is called by sourceViewers
// when an audio frame arrives from a source. If an audio handler (mixer)
// is set, ALL source audio is forwarded to it — the mixer decides routing.
// Otherwise, only the current program source's audio is forwarded to the
// program Relay.
func (s *Switcher) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	s.health.recordFrame(sourceKey, time.Now())

	s.mu.RLock()
	handler := s.audioHandler
	_, ok := s.sources[sourceKey]
	isProgram := ok && s.programSource == sourceKey
	s.mu.RUnlock()

	// Route to audio handler (mixer) if set — ALL sources
	if handler != nil {
		handler(sourceKey, frame)
		return
	}

	// Fallback: original behavior (only program source)
	if !isProgram {
		return
	}
	s.programRelay.BroadcastAudio(frame)
}

// handleRawVideoFrame implements frameHandler for decoded YUV frames
// from the per-source sourceDecoder pipeline. This is the always-decode
// path — frames arrive as raw YUV420 and flow directly through the
// processing pipeline without H.264 decode/IDR gating.
//
// Routing logic mirrors IngestRawVideo:
//  1. Health tracking
//  2. During transition → engine.IngestRawFrame
//  3. Program source → broadcastProcessedFromPF (key→compositor→encode→relay)
//  4. Non-program → filtered (dropped)
func (s *Switcher) handleRawVideoFrame(sourceKey string, pf *ProcessingFrame) {
	s.health.recordFrame(sourceKey, time.Now())

	s.mu.RLock()
	ss, ok := s.sources[sourceKey]
	programSource := s.programSource
	fTBActive := s.state.isFTBActive()
	inTrans := s.state.isInTransition()
	engine := s.transEngine
	audioHandler := s.audioTransition
	keyBridge := s.keyBridge
	s.mu.RUnlock()

	if !ok {
		s.routeFiltered.Add(1)
		return
	}

	// Update source stats from the sourceDecoder for encoder parameter derivation.
	// Guard against nil viewer — MXL and replay sources have no viewer.
	if ss != nil {
		if ss.viewer != nil {
			if dec := ss.viewer.srcDecoder.Load(); dec != nil {
				avgSize, avgFPS := dec.Stats()
				if sourceKey == programSource {
					s.mu.RLock()
					pipeCodecs := s.pipeCodecs
					s.mu.RUnlock()
					if pipeCodecs != nil {
						pipeCodecs.updateSourceStats(avgSize, avgFPS)
					}
				}
			}
		}
		ss.lastGroupID.Store(pf.GroupID)
	}

	// Feed key fill bridge with decoded YUV (for upstream keying).
	// Use HasEnabledKeys (not HasEnabledKeysWithFills) to avoid chicken-and-egg:
	// fills can't exist until IngestFillYUV is called, which requires this guard
	// to pass. IngestFillYUV has its own per-source config check internally.
	if keyBridge != nil {
		keyBridge.IngestFillYUV(sourceKey, pf.YUV, pf.Width, pf.Height)
	}

	// During transition (including FTB): route to engine for blending.
	// This must come BEFORE the FTB filter — FTB transitions need frames
	// from the program source to produce the fade-to-black blend.
	if inTrans && engine != nil {
		if engine.State() != transition.StateActive {
			s.routeToIdleEngine.Add(1)
			return
		}
		s.routeToEngine.Add(1)
		engine.IngestRawFrame(sourceKey, pf.YUV, pf.Width, pf.Height, pf.PTS)
		if audioHandler != nil {
			audioHandler.OnTransitionPosition(engine.Position())
		}
		return
	}

	// Normal: only program source passes through. FTB hold (StateFTB)
	// filters all frames — screen stays black until FTB is toggled off.
	if sourceKey != programSource || fTBActive {
		s.routeFiltered.Add(1)
		return
	}

	s.routeToPipeline.Add(1)

	// Enqueue as yuvFrame — the processing loop handles key→compositor→encode→broadcast.
	s.broadcastProcessedFromPF(pf)
}

// handleCaptionFrame implements frameHandler. Only the current program
// source's captions are forwarded to the program Relay.
func (s *Switcher) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	s.mu.RLock()
	_, ok := s.sources[sourceKey]
	isProgram := ok && s.programSource == sourceKey
	s.mu.RUnlock()

	if !isProgram {
		return
	}
	s.programRelay.BroadcastCaptions(frame)
}
