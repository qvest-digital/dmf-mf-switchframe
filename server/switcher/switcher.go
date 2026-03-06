package switcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// Sentinel errors for the switcher package.
var ErrSourceNotFound = errors.New("switcher: source not found")
var ErrAlreadyOnProgram = errors.New("switcher: already on program")
var ErrInvalidDelay = errors.New("switcher: delay must be 0-500ms")
var ErrNoTransition = errors.New("switcher: no active transition")

// SwitcherState represents the global state of the switching engine.
// It replaces the implicit (inTransition, ftbActive) boolean pair with an
// explicit enum that makes every valid state and transition auditable.
type SwitcherState int

const (
	StateIdle             SwitcherState = iota // No transition, normal passthrough
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
	valid := false
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			valid = true
			break
		}
	}
	if !valid {
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

// TransitionConfig holds the codec factories needed to create TransitionEngines.
type TransitionConfig struct {
	DecoderFactory transition.DecoderFactory
	EncoderFactory transition.EncoderFactory // convenience: passed to SetPipelineCodecs, not used by engine
}

// TransitionOption configures optional parameters for StartTransition.
type TransitionOption func(*transitionOpts)

type transitionOpts struct {
	stingerData *transition.StingerData
}

// WithStingerData sets the stinger overlay data for a stinger transition.
func WithStingerData(sd *transition.StingerData) TransitionOption {
	return func(o *transitionOpts) { o.stingerData = sd }
}

// sourceState tracks a registered source and its Relay/viewer pair.
type sourceState struct {
	key        string
	label      string
	relay      *distribution.Relay
	viewer     *sourceViewer
	pendingIDR bool // true after a cut until first keyframe from this source
	isVirtual  bool // true for virtual sources (replay, etc.)

	// Rolling frame statistics for dynamic encoder parameters.
	// Updated on every video frame. Used to estimate bitrate/fps for
	// the transition encoder so it matches the source stream quality.
	avgFrameSize float64 // exponential moving average of len(WireData) in bytes
	avgFPS       float64 // exponential moving average of fps from PTS deltas
	lastPTS      int64   // PTS of the most recent video frame (microseconds)
	frameCount   int     // total video frames received (for EMA warmup)
	lastGroupID  uint32  // most recent GroupID from this source's video frames
}

// Switcher is the central switching engine. It manages which source is
// on-program (live output) and which is on-preview, maintains tally state,
// and routes frames from the program source to the program Relay.
type Switcher struct {
	log            *slog.Logger
	mu             sync.RWMutex
	sources        map[string]*sourceState
	programSource  string
	previewSource  string
	programRelay   *distribution.Relay
	seq            uint64 // always use atomic ops, even under s.mu, to prevent races on lock-free read paths
	stateCallbacks []func(internal.ControlRoomState)
	health         *healthMonitor
	audioHandler    func(sourceKey string, frame *media.AudioFrame)
	mixer           audioStateProvider
	audioCut        audioCutHandler
	transConfig     *TransitionConfig
	transEngine     *transition.TransitionEngine
	state           SwitcherState
	audioTransition audioTransitionHandler
	gopCache        *gopCache
	delayBuffer     *DelayBuffer
	frameSync       *FrameSynchronizer
	frameSyncActive bool

	// DSK graphics compositor — applies overlay in YUV420 domain.
	compositorRef *graphics.Compositor

	// Upstream key bridge — applies chroma/luma keys in YUV420 domain.
	keyBridge *graphics.KeyProcessorBridge

	// Fill frame ingestor for upstream keying. When set, source video frames
	// are forwarded here so the bridge can decode and cache YUV for keyed sources.
	keyFillIngestor func(sourceKey string, frame *media.VideoFrame)

	// Pipeline codec pool — shared decoder/encoder for the video processing chain.
	// Used when any YUV processor (compositor, key bridge) is active or when
	// the transition engine outputs raw YUV.
	pipeCodecs *pipelineCodecs

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// Debug instrumentation counters (atomic, lock-free)
	idrGateEvents         atomic.Int64 // number of cuts (pendingIDR set)
	idrGateStartNano      atomic.Int64 // when current IDR gate started (UnixNano)
	lastIDRGateDurationMs atomic.Int64 // duration of last IDR gate
	transitionsStarted    atomic.Int64
	transitionsCompleted  atomic.Int64
}

// Compile-time check that Switcher implements the frameHandler interface.
var _ frameHandler = (*Switcher)(nil)

// New creates a Switcher that forwards program frames to programRelay.
func New(programRelay *distribution.Relay) *Switcher {
	s := &Switcher{
		log:          slog.With("component", "switcher"),
		sources:      make(map[string]*sourceState),
		programRelay: programRelay,
		health:       newHealthMonitor(),
		gopCache:     newGOPCache(),
	}
	s.delayBuffer = NewDelayBuffer(s)
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

// Close stops the health monitor, delay buffer, frame sync, and unregisters all sources.
func (s *Switcher) Close() {
	s.health.stop()
	s.delayBuffer.Close()
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

// SetKeyFillIngestor sets the function that receives source video frames
// for upstream key fill decoding. Called from handleVideoFrame on every
// source frame; the ingestor decides which sources to actually decode.
func (s *Switcher) SetKeyFillIngestor(fn func(sourceKey string, frame *media.VideoFrame)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyFillIngestor = fn
}

// SetPipelineCodecs creates the shared pipeline codec pool for the video
// processing chain. Called from app.go during initialization.
func (s *Switcher) SetPipelineCodecs(decoderFactory transition.DecoderFactory, encoderFactory transition.EncoderFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipeCodecs = &pipelineCodecs{
		decoderFactory: decoderFactory,
		encoderFactory: encoderFactory,
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
			tickRate = 33333 * time.Microsecond // ~30fps default
		}
		fs := NewFrameSynchronizer(tickRate,
			func(sourceKey string, frame media.VideoFrame) {
				s.handleVideoFrame(sourceKey, &frame)
			},
			func(sourceKey string, frame media.AudioFrame) {
				s.handleAudioFrame(sourceKey, &frame)
			},
		)
		s.frameSync = fs

		// Wire all existing source viewers to the frame sync.
		for key, ss := range s.sources {
			ss.viewer.frameSync.Store(fs)
			ss.viewer.delayBuffer.Store(nil) // bypass delay buffer
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
			ss.viewer.frameSync.Store(nil)
			ss.viewer.delayBuffer.Store(s.delayBuffer)
		}
		s.log.Info("frame sync disabled")
	}
	s.frameSyncActive = enabled
}

// broadcastVideo sends a video frame to the program relay. When YUV
// processors (upstream keying, DSK compositor) are active, the frame is
// decoded once, run through the YUV processor chain, and re-encoded once.
// When no processors are active, the compressed frame passes through
// untouched (zero CPU).
func (s *Switcher) broadcastVideo(frame *media.VideoFrame) {
	s.mu.RLock()
	keyBridge := s.keyBridge
	compositor := s.compositorRef
	pipeCodecs := s.pipeCodecs
	s.mu.RUnlock()

	// Fast path: no active processors → passthrough (zero CPU)
	keyActive := keyBridge != nil && keyBridge.HasEnabledKeysWithFills()
	vidActive := compositor != nil && compositor.IsActive()

	if (!keyActive && !vidActive) || pipeCodecs == nil {
		s.programRelay.BroadcastVideo(frame)
		return
	}

	// Slow path: decode once
	pf, err := pipeCodecs.decode(frame)
	if err != nil {
		// Can't decode → pass through compressed (graceful degradation)
		s.log.Warn("pipeline decode failed, passthrough", "error", err)
		if s.promMetrics != nil {
			s.promMetrics.PipelineDecodeErrorsTotal.Inc()
		}
		s.programRelay.BroadcastVideo(frame)
		return
	}

	// Chain YUV processors
	if keyActive {
		pf.YUV = keyBridge.ProcessYUV(pf.YUV, pf.Width, pf.Height)
	}
	if vidActive {
		pf.YUV = compositor.ProcessYUV(pf.YUV, pf.Width, pf.Height)
	}

	// Encode once
	out, err := pipeCodecs.encode(pf, frame.IsKeyframe)
	if err != nil {
		s.log.Warn("pipeline encode failed, dropping frame", "error", err)
		if s.promMetrics != nil {
			s.promMetrics.PipelineEncodeErrorsTotal.Inc()
		}
		return
	}

	if s.promMetrics != nil {
		s.promMetrics.PipelineFramesProcessed.Inc()
	}
	s.programRelay.BroadcastVideo(out)
}

// broadcastProcessed handles frames that are already decoded to YUV
// (e.g., from the transition engine). Runs YUV processors, then encodes once.
func (s *Switcher) broadcastProcessed(yuv []byte, width, height int, pts int64, isKeyframe bool) {
	s.mu.RLock()
	keyBridge := s.keyBridge
	compositor := s.compositorRef
	pipeCodecs := s.pipeCodecs
	var groupID uint32
	if ss, ok := s.sources[s.programSource]; ok {
		groupID = ss.lastGroupID
	}
	s.mu.RUnlock()

	if pipeCodecs == nil {
		return
	}

	// Run YUV processors
	if keyBridge != nil && keyBridge.HasEnabledKeysWithFills() {
		yuv = keyBridge.ProcessYUV(yuv, width, height)
	}
	if compositor != nil && compositor.IsActive() {
		yuv = compositor.ProcessYUV(yuv, width, height)
	}

	// Encode once
	pf := &ProcessingFrame{
		YUV: yuv, Width: width, Height: height,
		PTS: pts, DTS: pts, IsKeyframe: isKeyframe,
		Codec:   "h264", // only codec supported today
		GroupID: groupID,
	}
	frame, err := pipeCodecs.encode(pf, isKeyframe)
	if err != nil {
		s.log.Warn("pipeline encode failed, dropping frame", "error", err, "path", "transition")
		if s.promMetrics != nil {
			s.promMetrics.PipelineEncodeErrorsTotal.Inc()
		}
		return
	}

	if s.promMetrics != nil {
		s.promMetrics.PipelineFramesProcessed.Inc()
	}
	s.programRelay.BroadcastVideo(frame)
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

	// Capture codec factories before releasing lock.
	decoderFactory := s.transConfig.DecoderFactory

	// Mark transition as starting to prevent concurrent StartTransition/FTB calls.
	// handleVideoFrame checks transEngine != nil to route frames, so setting
	// StateTransitioning without transEngine is safe — frames won't route to
	// the engine and normal passthrough continues for the current program source.
	s.transitionState(StateTransitioning)
	s.mu.Unlock()

	// Phase 2: Create engine, start it, and warm decoders with NO lock held.
	// GOP cache has its own mutex, so GetGOP is safe without s.mu.
	// Decoder warmup can be slow (codec init + GOP feed), so releasing the
	// lock here allows frame routing to continue for all sources.

	// Check for cancellation before expensive codec initialization.
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

	// Warm up decoders BEFORE publishing the engine. This ensures live
	// frames cannot reach the engine (via handleVideoFrame) before the
	// decoders have been primed with the cached GOP. Warmup runs with
	// NO switcher lock held, so frame routing is unblocked.
	fromGOP := s.gopCache.GetGOP(fromSource)
	toGOP := s.gopCache.GetGOP(sourceKey)
	for _, cf := range fromGOP {
		engine.WarmupDecode(fromSource, cf.annexB)
	}
	for _, cf := range toGOP {
		engine.WarmupDecode(sourceKey, cf.annexB)
	}

	// Phase 3: Publish the warmed engine under write lock (fast).
	s.mu.Lock()
	s.transEngine = engine
	s.previewSource = sourceKey
	s.transitionsStarted.Add(1)
	audioHandler := s.audioTransition

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.log.Info("transition started",
		"type", string(tt), "from", fromSource, "to", sourceKey, "duration_ms", durationMs)

	if audioHandler != nil {
		audioMode := audio.AudioCrossfade
		if tt == transition.TransitionDip {
			audioMode = audio.AudioDipToSilence
		}
		audioHandler.OnTransitionStart(fromSource, sourceKey, audioMode, durationMs)
	}
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
		decoderFactory := s.transConfig.DecoderFactory

		// Mark transition as starting, then release lock for warmup.
		s.transitionState(StateFTBReversing)
		s.mu.Unlock()

		engine := transition.NewTransitionEngine(transition.EngineConfig{
			DecoderFactory: decoderFactory,
			Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
				s.broadcastProcessed(yuv, width, height, pts, isKeyframe)
			},
			OnComplete: func(aborted bool) {
				s.handleFTBReverseComplete(aborted)
			},
		})

		if err := engine.Start(fromSource, "", transition.TransitionFTBReverse, 1000); err != nil {
			s.mu.Lock()
			s.transitionState(StateFTB) // Roll back to StateFTB
			s.mu.Unlock()
			return fmt.Errorf("start FTB reverse: %w", err)
		}

		// Warm up decoder with NO lock held (see StartTransition).
		fromGOP := s.gopCache.GetGOP(fromSource)
		for _, cf := range fromGOP {
			engine.WarmupDecode(fromSource, cf.annexB)
		}

		// Publish the warmed engine under write lock.
		s.mu.Lock()
		s.transEngine = engine
		s.transitionsStarted.Add(1)
		audioHandler := s.audioTransition

		atomic.AddUint64(&s.seq, 1)
		snapshot := s.buildStateLocked()
		s.mu.Unlock()

		s.log.Info("transition started",
			"type", "ftb_reverse", "from", fromSource, "to", "", "duration_ms", 1000)

		if audioHandler != nil {
			// Unmute program audio so the fade-in is audible
			audioHandler.SetProgramMute(false)
			audioHandler.OnTransitionStart(fromSource, "", audio.AudioFadeIn, 1000)
		}
		s.notifyStateChange(snapshot)
		return nil
	}

	if s.programSource == "" {
		s.mu.Unlock()
		return fmt.Errorf("no program source set")
	}

	fromSource := s.programSource
	decoderFactory := s.transConfig.DecoderFactory

	// Mark transition as starting, then release lock for warmup.
	s.transitionState(StateFTBTransitioning)
	s.mu.Unlock()

	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: decoderFactory,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			s.broadcastProcessed(yuv, width, height, pts, isKeyframe)
		},
		OnComplete: func(aborted bool) {
			s.handleFTBComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, "", transition.TransitionFTB, 1000); err != nil {
		s.mu.Lock()
		s.transitionState(StateIdle)
		s.mu.Unlock()
		return fmt.Errorf("start FTB: %w", err)
	}

	// Warm up decoder with NO lock held (see StartTransition).
	fromGOP := s.gopCache.GetGOP(fromSource)
	for _, cf := range fromGOP {
		engine.WarmupDecode(fromSource, cf.annexB)
	}

	// Publish the warmed engine under write lock.
	s.mu.Lock()
	s.transEngine = engine
	s.transitionsStarted.Add(1)
	audioHandler := s.audioTransition

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.log.Info("transition started",
		"type", "ftb", "from", fromSource, "to", "", "duration_ms", 1000)

	if audioHandler != nil {
		audioHandler.OnTransitionStart(fromSource, "", audio.AudioFadeOut, 1000)
	}
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
// sources and replays the new source's cached GOP to avoid a keyframe gap.
func (s *Switcher) handleTransitionComplete(aborted bool) {
	s.mu.Lock()
	if !s.state.isInTransition() {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	var audioCut audioCutHandler
	var newProgram string

	if !aborted && s.transEngine != nil {
		newProgram = s.transEngine.ToSource()
		oldProgram := s.programSource
		if newProgram != "" {
			s.programSource = newProgram
			s.previewSource = oldProgram
			audioCut = s.audioCut
			// Gate passthrough frames. The transition encoder's SPS/PPS
			// differ from the source's, so delta frames would be
			// undecodable. The gate is cleared below after GOP replay,
			// or held until the next natural keyframe as fallback.
			if ss, ok := s.sources[newProgram]; ok {
				ss.pendingIDR = true
				s.idrGateStartNano.Store(time.Now().UnixNano())
			}
		}
	}

	// Get cached GOP for replay (uses its own mutex, no deadlock risk)
	var replayFrames []*media.VideoFrame
	if newProgram != "" {
		replayFrames = s.gopCache.GetOriginalGOP(newProgram)
	}

	transType := ""
	if s.transEngine != nil {
		transType = string(s.transEngine.TransitionType())
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

	if aborted {
		s.log.Warn("transition aborted", "type", transType, "reason", "engine aborted")
	} else {
		s.log.Info("transition completed", "type", transType)
	}

	// Replay the source's cached GOP to bridge the transition→passthrough
	// gap. pendingIDR=true prevents live passthrough from interleaving.
	if len(replayFrames) > 0 {
		for _, f := range replayFrames {
			s.broadcastVideo(f)
		}
		// Clear the IDR gate — the replayed GOP provided a keyframe
		s.mu.Lock()
		if ss, ok := s.sources[newProgram]; ok && ss.pendingIDR {
			ss.pendingIDR = false
			if startNano := s.idrGateStartNano.Load(); startNano > 0 {
				dur := time.Since(time.Unix(0, startNano))
				s.lastIDRGateDurationMs.Store(dur.Milliseconds())
				if s.promMetrics != nil {
					s.promMetrics.IDRGateDuration.Observe(dur.Seconds())
				}
			}
		}
		s.mu.Unlock()
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
// StateIdle (screen is now fully visible) and replays the GOP to avoid a
// keyframe gap. If aborted, it transitions to StateFTB (screen stays black).
func (s *Switcher) handleFTBReverseComplete(aborted bool) {
	s.mu.Lock()
	if !s.state.isInTransition() {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	programSource := s.programSource

	if aborted {
		s.transitionState(StateFTB) // Aborted — screen stays black
	} else {
		s.transitionState(StateIdle) // Completed — screen is visible
		// Gate passthrough until GOP replay provides a keyframe.
		// The transition encoder's SPS/PPS differ from the source's.
		if ss, ok := s.sources[programSource]; ok {
			ss.pendingIDR = true
			s.idrGateStartNano.Store(time.Now().UnixNano())
		}
	}
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb_reverse").Inc()
	}

	var replayFrames []*media.VideoFrame
	if !aborted && programSource != "" {
		replayFrames = s.gopCache.GetOriginalGOP(programSource)
	}

	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	// Replay the source's cached GOP to bridge the gap
	if len(replayFrames) > 0 {
		for _, f := range replayFrames {
			s.broadcastVideo(f)
		}
		s.mu.Lock()
		if ss, ok := s.sources[programSource]; ok && ss.pendingIDR {
			ss.pendingIDR = false
			if startNano := s.idrGateStartNano.Load(); startNano > 0 {
				dur := time.Since(time.Unix(0, startNano))
				s.lastIDRGateDurationMs.Store(dur.Milliseconds())
				if s.promMetrics != nil {
					s.promMetrics.IDRGateDuration.Observe(dur.Seconds())
				}
			}
		}
		s.mu.Unlock()
	}

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
	if s.frameSyncActive && s.frameSync != nil {
		viewer.frameSync.Store(s.frameSync)
		s.frameSync.AddSource(key)
	} else {
		viewer.delayBuffer.Store(s.delayBuffer)
	}
	relay.AddViewer(viewer)
	s.sources[key] = &sourceState{key: key, relay: relay, viewer: viewer}
	s.health.registerSource(key)
	s.mu.Unlock()

	s.log.Info("source registered", "source_key", key)
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
	}
	s.health.registerSource(key)
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()
	s.log.Info("virtual source registered", "source_key", key)
	s.notifyStateChange(snapshot)
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
	ss.relay.RemoveViewer(ss.viewer.ID())
	delete(s.sources, key)
	s.health.removeSource(key)
	s.gopCache.RemoveSource(key)
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
		s.sources[sourceKey].pendingIDR = true
		s.idrGateEvents.Add(1)
		s.idrGateStartNano.Store(time.Now().UnixNano())
		if s.promMetrics != nil {
			s.promMetrics.CutsTotal.Inc()
			s.promMetrics.IDRGateEventsTotal.Inc()
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

// SetSourceDelay sets the input delay for a source in milliseconds (0-500).
// A delay of 0 means no buffering (passthrough). Non-zero delays are used
// for lip-sync compensation. Returns ErrSourceNotFound if the source is not
// registered, or ErrInvalidDelay if the value is out of range.
func (s *Switcher) SetSourceDelay(sourceKey string, delayMs int) error {
	if delayMs < 0 || delayMs > 500 {
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
	for key, ss := range s.sources {
		sources[key] = map[string]any{
			"video_frames_in":   ss.viewer.videoSent.Load(),
			"audio_frames_in":   ss.viewer.audioSent.Load(),
			"health_status":     string(s.health.rawStatus(key)),
			"last_frame_ago_ms": s.health.lastFrameAgoMs(key),
			"pending_idr":       ss.pendingIDR,
		}
	}

	return map[string]any{
		"program_source":            s.programSource,
		"preview_source":            s.previewSource,
		"state":                     s.state.String(),
		"in_transition":             s.state.isInTransition(),
		"ftb_active":                s.state.isFTBActive(),
		"seq":                       atomic.LoadUint64(&s.seq),
		"sources":                   sources,
		"idr_gate_events":           s.idrGateEvents.Load(),
		"last_idr_gate_duration_ms": s.lastIDRGateDurationMs.Load(),
		"transitions_started":       s.transitionsStarted.Load(),
		"transitions_completed":     s.transitionsCompleted.Load(),
	}
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
	}

	// Populate audio state from mixer if available.
	if s.mixer != nil {
		state.AudioChannels = s.mixer.ChannelStates()
		state.MasterLevel = s.mixer.MasterLevel()
		state.ProgramPeak = s.mixer.ProgramPeak()
		state.GainReduction = s.mixer.GainReduction()
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
// source. Called on every video frame. Uses an exponential moving average
// with alpha=0.1 for stability. Caller must hold s.mu (write lock).
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
	if frame.GroupID > ss.lastGroupID {
		ss.lastGroupID = frame.GroupID
	}
}

// handleVideoFrame implements frameHandler. It is called by sourceViewers
// when a video frame arrives from a source. Only frames from the current
// program source are forwarded to the program Relay. After a cut, frames
// are gated until the first keyframe (IDR) to prevent decoder artifacts.
func (s *Switcher) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	s.health.recordFrame(sourceKey)

	// Update per-source frame statistics (needs write lock)
	s.mu.Lock()
	if ss, ok := s.sources[sourceKey]; ok {
		s.updateFrameStats(ss, frame)
		// Propagate program source stats to pipeline encoder
		if sourceKey == s.programSource && s.pipeCodecs != nil {
			s.pipeCodecs.updateSourceStats(ss.avgFrameSize, ss.avgFPS)
		}
	}
	s.mu.Unlock()

	// Record frame in GOP cache for all sources (uses its own mutex)
	s.gopCache.RecordFrame(sourceKey, frame)

	// Check if transition is active — route both sources to engine
	s.mu.RLock()
	engine := s.transEngine
	inTrans := s.state.isInTransition()
	s.mu.RUnlock()

	if inTrans && engine != nil {
		// WireData is AVC1 (length-prefixed); OpenH264 decoder expects Annex B.
		annexB := codec.AVC1ToAnnexB(frame.WireData)
		if frame.IsKeyframe && len(frame.SPS) > 0 {
			// Prepend SPS/PPS as Annex B NALUs so decoder can (re)configure.
			var buf []byte
			buf = append(buf, 0x00, 0x00, 0x00, 0x01)
			buf = append(buf, frame.SPS...)
			buf = append(buf, 0x00, 0x00, 0x00, 0x01)
			buf = append(buf, frame.PPS...)
			buf = append(buf, annexB...)
			annexB = buf
		}
		engine.IngestFrame(sourceKey, annexB, frame.PTS)

		// Sync audio crossfade position with video on every frame.
		// Without this, auto-timed transitions only update audio at
		// start/complete, causing the audio to jump 0→1 instead of
		// smoothly tracking the video dissolve.
		s.mu.RLock()
		audioHandler := s.audioTransition
		s.mu.RUnlock()
		if audioHandler != nil {
			audioHandler.OnTransitionPosition(engine.Position())
		}
		return
	}

	// Forward source frames to the key fill ingestor (if set).
	// This must happen before the program source check because keyed
	// sources may be non-program sources that need their fills cached.
	s.mu.RLock()
	fillIngestor := s.keyFillIngestor
	s.mu.RUnlock()
	if fillIngestor != nil {
		fillIngestor(sourceKey, frame)
	}

	// Normal passthrough: RLock for steady-state (pendingIDR is false most of the time).
	s.mu.RLock()
	ss, ok := s.sources[sourceKey]
	if !ok || s.programSource != sourceKey || s.state.isFTBActive() {
		s.mu.RUnlock()
		return
	}
	if !ss.pendingIDR {
		s.mu.RUnlock()
		s.broadcastVideo(frame)
		return
	}
	s.mu.RUnlock()

	// Slow path: pendingIDR is true. Need write lock to clear it.
	if !frame.IsKeyframe {
		return
	}
	s.mu.Lock()
	// Re-check under write lock (another goroutine may have cleared it).
	var gateDurationMs int64
	if ss.pendingIDR {
		ss.pendingIDR = false
		// Record how long the IDR gate was active.
		if startNano := s.idrGateStartNano.Load(); startNano > 0 {
			dur := time.Since(time.Unix(0, startNano))
			gateDurationMs = dur.Milliseconds()
			s.lastIDRGateDurationMs.Store(gateDurationMs)
			if s.promMetrics != nil {
				s.promMetrics.IDRGateDuration.Observe(dur.Seconds())
			}
		}
	}
	s.mu.Unlock()

	s.log.Debug("IDR gate cleared", "source", sourceKey, "gate_duration_ms", gateDurationMs)
	s.broadcastVideo(frame)
}

// handleAudioFrame implements frameHandler. It is called by sourceViewers
// when an audio frame arrives from a source. If an audio handler (mixer)
// is set, ALL source audio is forwarded to it — the mixer decides routing.
// Otherwise, only the current program source's audio is forwarded to the
// program Relay, gated along with video until the first keyframe after a cut.
func (s *Switcher) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	s.health.recordFrame(sourceKey)

	s.mu.RLock()
	handler := s.audioHandler
	ss, ok := s.sources[sourceKey]
	isProgram := ok && s.programSource == sourceKey && !ss.pendingIDR
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

// handleCaptionFrame implements frameHandler. Only the current program
// source's captions are forwarded to the program Relay, gated by the
// same pendingIDR flag as video/audio.
func (s *Switcher) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	s.mu.RLock()
	ss, ok := s.sources[sourceKey]
	isProgram := ok && s.programSource == sourceKey && !ss.pendingIDR
	s.mu.RUnlock()

	if !isProgram {
		return
	}
	s.programRelay.BroadcastCaptions(frame)
}
