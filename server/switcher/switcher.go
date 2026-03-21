package switcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/layout"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/transition"
)

const (
	maxDelayMs      = 500  // maximum source delay for lip-sync correction
	defaultFTBDurMs = 1000 // default FTB transition duration in milliseconds
)

// Sentinel errors for the switcher package.
var (
	ErrSourceNotFound          = errors.New("switcher: source not found")
	ErrAlreadyOnProgram        = errors.New("switcher: already on program")
	ErrInvalidDelay            = errors.New("switcher: delay must be 0-500ms")
	ErrInvalidPosition         = errors.New("switcher: position must be >= 1")
	ErrNoTransition            = errors.New("switcher: no active transition")
	ErrFormatDuringTransition  = errors.New("switcher: cannot change pipeline format during active transition")
	ErrEncoderNotAvailable     = errors.New("switcher: encoder not available")
	errTransitionNotConfigured = errors.New("transition not configured")
	errNoProgramSource         = errors.New("no program source set")
	errNoTargetSource          = errors.New("no target source specified")
	errStingerDataRequired     = errors.New("stinger transition requires stinger data")
)

// State represents the global state of the switching engine.
// It replaces the implicit (inTransition, ftbActive) boolean pair with an
// explicit enum that makes every valid state and transition auditable.
type State int

const (
	StateIdle             State = iota // No transition, normal frame routing
	StateTransitioning                 // Mix/dip/wipe in progress
	StateFTBTransitioning              // FTB forward in progress (transitioning to black)
	StateFTB                           // Faded to black (holding black)
	StateFTBReversing                  // Reversing FTB (fading back in)
)

// String returns the human-readable name of the switcher state.
func (s State) String() string {
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
func (s State) isInTransition() bool {
	return s == StateTransitioning || s == StateFTBTransitioning || s == StateFTBReversing
}

// isFTBActive returns true if the switcher is in any FTB-related state
// (transitioning to black, holding at black, or reversing from black).
// This maps to the ControlRoomState.FTBActive API field.
func (s State) isFTBActive() bool {
	return s == StateFTBTransitioning || s == StateFTB || s == StateFTBReversing
}

// validTransitions defines the allowed state transitions. Any transition not
// in this map is logged as a warning but still executed (no panics in production).
var validTransitions = map[State][]State{
	StateIdle:             {StateTransitioning, StateFTBTransitioning},
	StateTransitioning:    {StateIdle},
	StateFTBTransitioning: {StateFTB, StateIdle},
	StateFTB:              {StateFTBReversing},
	StateFTBReversing:     {StateFTB, StateIdle},
}

// transitionState changes the switcher state, logging a warning if the transition
// is not in the valid transitions map. Never panics in production.
// Caller must hold s.mu (write lock).
func (s *Switcher) transitionState(to State) {
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

// audioStateProvider is the interface the Switcher needs from the audio.Mixer
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
	OnTransitionStart(oldSource, newSource string, mode audio.TransitionMode, durationMs int)
	OnTransitionPosition(position float64)
	OnTransitionComplete()
	OnTransitionAbort()
	SetProgramMute(muted bool)
	SetStingerAudio(audio []float32, sampleRate, channels int)
}

// captionManager is the interface the Switcher needs from the caption system
// to embed CEA-608/708 captions in the H.264 output bitstream.
type captionManager interface {
	ConsumeForFrameWithVANC() []caption.CCPair
	SetPassThroughText(text string)
	NotifySourceCaptions(sourceKey string, has bool)
}

// RawVideoSink receives a deep copy of the processed YUV420p frame
// after all video processing (keying, compositor) but before H.264 encode.
// Used by MXL output to write raw video to shared memory.
type RawVideoSink func(pf *ProcessingFrame)

// TransitionConfig holds the codec factories needed to create transition engines.
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
	key            string
	label          string
	relay          *distribution.Relay
	viewer         *sourceViewer
	isVirtual      bool // true for virtual sources (replay, etc.)
	isMXL          bool // true for MXL raw YUV sources (no H.264 decode/IDR gating)
	useRawPipeline bool // true when source has a per-source decoder (always-decode mode)
	position       int  // display order in the UI (1-based)

	// Rolling frame statistics for dynamic encoder parameters.
	// Written by a single goroutine (source viewer) via updateFrameStats,
	// read concurrently by state broadcast / debug snapshot goroutines.
	// Uses atomic Uint64 + Float64bits/Float64frombits to avoid data race
	// (same pattern as sourceDecoder.updateStats).
	avgFrameSizeBits atomic.Uint64 // exponential moving average of len(WireData) in bytes
	avgFPSBits       atomic.Uint64 // exponential moving average of fps from PTS deltas
	lastPTS          atomic.Int64  // PTS of the most recent video frame (90kHz clock units)
	frameCount       atomic.Int32  // total video frames received (for EMA warmup)
	lastGroupID      atomic.Uint32 // most recent GroupID from this source's video frames

	// Raw video ingest counter — incremented on every handleRawVideoFrame call.
	// Used to compute per-source ingest FPS in PerfSample(). Atomic for lock-free
	// writes from frame delivery goroutines.
	rawFrameCount atomic.Int64
}

// getAvgFrameSize returns the rolling average frame size. Safe for concurrent access.
func (ss *sourceState) getAvgFrameSize() float64 {
	return math.Float64frombits(ss.avgFrameSizeBits.Load())
}

// getAvgFPS returns the rolling average FPS. Safe for concurrent access.
func (ss *sourceState) getAvgFPS() float64 {
	return math.Float64frombits(ss.avgFPSBits.Load())
}

// getLastPTS returns the most recent video PTS. Safe for concurrent access.
func (ss *sourceState) getLastPTS() int64 {
	return ss.lastPTS.Load()
}

// getFrameCount returns the total video frames received. Safe for concurrent access.
func (ss *sourceState) getFrameCount() int {
	return int(ss.frameCount.Load())
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
	transEngine     *transition.Engine
	state           State
	audioTransition audioTransitionHandler
	delayBuffer     *DelayBuffer
	frameSync       *FrameSynchronizer
	frameSyncActive bool

	// Callback to seed audio PTS epoch from first program video frame.
	onFirstVideoPTS     func(pts int64)
	firstVideoPTSSeeded bool

	// Wall-clock video PTS: rewrites program relay video PTS to match the
	// mixer's wall-clock audio PTS, keeping A/V aligned after source cuts.
	// Seeded from the first program video frame (same as audio seed).
	// Enabled via EnableWallClockVideoPTS() — off by default for backward compat.
	videoPTSStart         int64
	videoPTSEpoch         time.Time
	videoPTS              int64
	videoPTSInited        bool
	wallClockVideoEnabled bool

	// DSK graphics compositor — applies overlay in YUV420 domain.
	compositorRef *graphics.Compositor

	// Upstream key bridge — applies chroma/luma keys in YUV420 domain.
	keyBridge *graphics.KeyProcessorBridge

	// Layout compositor — applies PIP/split-screen/quad layouts in YUV420 domain.
	layoutCompositor *layout.Compositor

	// ST map registry — per-source lens correction applied in sourceDecoder.
	stmapRegistry *stmap.Registry

	// GPU pipeline runner — when set, videoProcessingLoop routes frames through
	// the GPU pipeline (upload → key → layout → compositor → stmap → raw sinks → encode)
	// instead of the CPU pipeline. Falls back to CPU on GPU errors.
	// Stored via atomic pointer for lock-free reads on the hot path.
	gpuRunner atomic.Pointer[gpuRunnerHolder]

	// GPU source manager — when set, handleRawVideoFrame routes YUV through
	// GPU upload + ST map + cache instead of CPU fill paths.
	gpuSourceMgr atomic.Pointer[gpuSourceMgrHolder]

	// gpuSourceActive — when true, sourceDecoder skips CPU ST map correction
	// (the GPU source manager handles it on GPU instead).
	gpuSourceActive atomic.Bool

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

	// Pre-allocated YUV buffer pool — replaces sync.Pool for deterministic
	// buffer lifecycle. Sized at pipeline format resolution.
	framePool *FramePool

	// Structured video processing pipeline. Built via BuildPipeline(),
	// called per-frame from videoProcessingLoop. Atomic pointer for
	// hot-swap reconfiguration via swapPipeline().
	pipeline atomic.Pointer[Pipeline]

	// Pipeline epoch — monotonically increasing counter for downstream
	// format change detection. Incremented on every pipeline rebuild/swap.
	pipelineEpoch atomic.Uint64

	// Tracks background drain goroutines launched by swapPipeline().
	// Close() waits on this before closing pipeCodecs to prevent
	// use-after-close on the encoder by still-draining old pipelines.
	drainWg sync.WaitGroup

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

	// Output FPS tracking (atomic, lock-free)
	outputFPSCount       atomic.Int64 // frames in current 1-second window
	outputFPSLastSecond  atomic.Int64 // FPS computed from previous second
	outputFPSWindowStart atomic.Int64 // UnixNano start of current window

	// Frame loss diagnostic counters (atomic, lock-free).
	pipeEncodeNil    atomic.Int64 // encoder returned nil (HW warmup)
	pipeEncodeDrop   atomic.Int64 // frames dropped due to async encoder backpressure
	transOutputCount atomic.Int64 // frames output by transition engine

	// Last broadcast PTS for replay PTS anchoring (atomic, lock-free).
	lastBroadcastPTS atomic.Int64

	// Broadcast interval diagnostics (atomic, lock-free).
	lastBroadcastNano         atomic.Int64 // UnixNano of last program broadcast
	maxBroadcastIntervalNano  atomic.Int64 // max gap between consecutive broadcasts (ns)
	lastBroadcastIntervalNano atomic.Int64 // most recent gap between consecutive broadcasts (ns)

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

	// E2E frame latency: source arrival → pipeline processing complete
	lastE2ENs atomic.Int64
	maxE2ENs  atomic.Int64

	// Sub-stage latency breakdown (all in nanoseconds, atomic for lock-free reads)
	lastDecodeQueueNs atomic.Int64 // T1 - T0: sourceDecoder channel wait
	lastDecodeNs      atomic.Int64 // T2 - T1: H.264 decode duration
	lastSyncWaitNs    atomic.Int64 // T3 - T2: frame_sync tick alignment wait
	lastProcQueueNs   atomic.Int64 // T5 - T4: videoProcCh queue wait

	// Program epoch — monotonically increasing counter incremented on every
	// program source change (Cut, transition complete). Frames stamped with
	// a stale epoch are discarded in videoProcessingLoop to prevent wrong-source
	// frames from reaching the pipeline during concurrent Cut/transition races.
	programEpoch      atomic.Uint64
	programEpochStale atomic.Int64 // count of frames discarded as stale

	// forceNextIDR is set when a new output viewer joins the program relay
	// (e.g., SRT output starts). The next encode call forces an IDR keyframe
	// so the TSMuxer can initialize immediately instead of waiting up to
	// one full GOP interval (~2 seconds).
	forceNextIDR atomic.Bool

	// Raw video output tap — receives deep copy of YUV after processing,
	// before encode. Used by MXL output to write raw video to shared memory.
	rawVideoSink atomic.Pointer[RawVideoSink]

	// Raw preview output — feeds program preview encoder (low-bitrate browser delivery).
	rawPreviewSink atomic.Pointer[RawVideoSink]

	// Direct output callback — bypasses the relay for zero-latency delivery
	// to the MPEG-TS muxer (SRT/recording output). The relay path adds 3
	// goroutine hops and 3 channel buffers; the direct path is a synchronous
	// function call from the encode goroutine.
	outputVideoCallback atomic.Pointer[func(*media.VideoFrame)]

	// Caption manager — handles CEA-608/708 caption encoding, pass-through,
	// and SEI injection into encoded video frames. Optional (nil = no captions).
	captionMgr captionManager

	// Caption SEI injection buffers removed — broadcastWithCaptions is now
	// called from the async encodeLoop goroutine, so shared mutable buffers
	// would race during pipeline swap. Per-frame allocation is negligible.

	// Codec info — which encoder/decoder were selected at startup.
	// Set via SetCodecInfo(), read-only after initialization.
	codecEncoder              string
	codecDecoder              string
	codecHWAccel              bool
	availableEncoders         []codec.EncoderInfo
	availableEncodersInternal []internal.EncoderInfo // cached conversion for state broadcast

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
	// epoch: program epoch at enqueue time. Frames with stale epoch are
	// discarded in videoProcessingLoop to prevent wrong-source flashes
	// during concurrent Cut/transition races. Zero means "always process"
	// (used by transition engine output which is always valid).
	epoch uint64
	// enqueueNano: UnixNano when enqueued to videoProcCh. Used to measure
	// how long the frame waited in the videoProcCh buffer before being
	// picked up by videoProcessingLoop.
	enqueueNano int64
	// sourceKey: the source key of the program source that produced this frame.
	// Used by the GPU pipeline to look up the source's cached GPU frame
	// (via RunFromCache) instead of re-uploading from CPU memory.
	// Empty for transition/FRC output which produces blended/synthesized frames.
	sourceKey string
}

// Compile-time check that Switcher implements the frameHandler interface.
var _ frameHandler = (*Switcher)(nil)

// New creates a Switcher that forwards program frames to programRelay.
func New(programRelay *distribution.Relay) *Switcher {
	defaultFmt := DefaultFormat
	s := &Switcher{
		log:           slog.With("component", "switcher"),
		sources:       make(map[string]*sourceState),
		programRelay:  programRelay,
		health:        newHealthMonitor(),
		videoProcCh:   make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
		framePool:     NewFramePool(512, defaultFmt.Width, defaultFmt.Height),
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

// SetCodecInfo records which encoder/decoder were selected at startup and
// whether hardware acceleration is active. Called once during init after
// codec.ProbeEncoders(). Values are exposed in DebugSnapshot() under "codec".
func (s *Switcher) SetCodecInfo(encoder, decoder string, hwAccel bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codecEncoder = encoder
	s.codecDecoder = decoder
	s.codecHWAccel = hwAccel
}

// SetAvailableEncoders stores the list of encoders that are available on this
// system. Called once at startup from the codec probe results. Also
// pre-computes the internal.EncoderInfo conversion to avoid per-broadcast
// allocations in state enrichment.
func (s *Switcher) SetAvailableEncoders(encoders []codec.EncoderInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.availableEncoders = encoders
	s.availableEncodersInternal = make([]internal.EncoderInfo, len(encoders))
	for i, e := range encoders {
		s.availableEncodersInternal[i] = internal.EncoderInfo{
			Name:        e.Name,
			DisplayName: e.DisplayName,
			IsDefault:   e.IsDefault,
		}
	}
}

// AvailableEncoders returns the list of encoders available on this system.
func (s *Switcher) AvailableEncoders() []codec.EncoderInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]codec.EncoderInfo, len(s.availableEncoders))
	copy(out, s.availableEncoders)
	return out
}

// EncoderName returns the name of the currently active encoder.
func (s *Switcher) EncoderName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.codecEncoder
}

// AvailableEncodersInternal returns the pre-computed internal.EncoderInfo
// slice for state broadcast enrichment. The returned slice must not be modified.
func (s *Switcher) AvailableEncodersInternal() []internal.EncoderInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.availableEncodersInternal
}

// SetEncoder switches the video encoder at runtime. The name must match one
// of the entries in AvailableEncoders. Returns ErrFormatDuringTransition if
// a transition is in progress. The new encoder takes effect on the next
// encoded frame (the current encoder is invalidated).
func (s *Switcher) SetEncoder(name string) error {
	s.mu.Lock()

	if s.state.isInTransition() {
		s.mu.Unlock()
		return ErrFormatDuringTransition
	}

	// Validate name against available encoders.
	found := false
	for _, enc := range s.availableEncoders {
		if enc.Name == name {
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("switcher: encoder %q: %w", name, ErrEncoderNotAvailable)
	}

	// Build factory for the requested encoder.
	var factory transition.EncoderFactory
	if name == "openh264" {
		factory = func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return codec.NewOpenH264Encoder(w, h, bitrate, fpsNum, fpsDen)
		}
	} else {
		codecName := name
		factory = func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return codec.NewFFmpegEncoder(codecName, w, h, bitrate, fpsNum, fpsDen,
				transition.DefaultGOPSecs, codec.HWDeviceCtx())
		}
	}

	// Update codec info.
	oldEncoder := s.codecEncoder
	s.codecEncoder = name
	// HW accel is true for any non-software encoder.
	s.codecHWAccel = name != "libx264" && name != "openh264"

	// Capture pipeCodecs ref before releasing lock.
	pc := s.pipeCodecs
	s.mu.Unlock()

	// Swap factory outside s.mu to avoid holding both s.mu and pipeCodecs.mu.
	if pc != nil {
		pc.SetEncoderFactory(factory)
	}

	// Force the new encoder's first frame to be an IDR keyframe so
	// downstream decoders can sync immediately after the switch.
	s.forceNextIDR.Store(true)

	s.log.Info("encoder changed", "from", oldEncoder, "to", name)

	// Broadcast updated state to all connected clients.
	atomic.AddUint64(&s.seq, 1)
	s.mu.RLock()
	snapshot := s.buildStateLocked()
	s.mu.RUnlock()
	s.notifyStateChange(snapshot)

	return nil
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
	s.rebuildPipeline()
}

// SetOutputVideoCallback registers a direct video output callback that
// bypasses the relay for zero-latency delivery to the MPEG-TS muxer.
// Called synchronously from the encode goroutine — no channels, no
// goroutine hops. Pass nil to clear.
func (s *Switcher) SetOutputVideoCallback(fn func(*media.VideoFrame)) {
	if fn != nil {
		s.outputVideoCallback.Store(&fn)
	} else {
		s.outputVideoCallback.Store(nil)
	}
}

// SetRawPreviewSink sets or clears the raw preview output tap.
// Same pattern as RawVideoSink and RawMonitorSink — receives a deep copy
// of each processed YUV420p frame after all processing but before H.264
// encode. Used by the program preview encoder for low-bitrate browser delivery.
func (s *Switcher) SetRawPreviewSink(sink RawVideoSink) {
	if sink != nil {
		s.rawPreviewSink.Store(&sink)
	} else {
		s.rawPreviewSink.Store(nil)
	}
	s.rebuildPipeline()
}

// GetRawVideoSink returns the currently set raw video sink, or nil if none.
func (s *Switcher) GetRawVideoSink() RawVideoSink {
	if p := s.rawVideoSink.Load(); p != nil {
		return *p
	}
	return nil
}

// GetRawPreviewSink returns the currently set raw preview sink, or nil if none.
func (s *Switcher) GetRawPreviewSink() RawVideoSink {
	if p := s.rawPreviewSink.Load(); p != nil {
		return *p
	}
	return nil
}

// Close stops the health monitor, delay buffer, frame sync, and unregisters all sources.
func (s *Switcher) Close() {
	s.health.stop()
	s.delayBuffer.Close()

	// Stop frame sync FIRST to prevent sends to videoProcCh after it is closed.
	// The frame sync ticker goroutine delivers frames via handleRawVideoFrame,
	// which calls enqueueVideoWork. If we close videoProcCh while the ticker is
	// still running, a send on the closed channel causes a panic.
	//
	// We grab the pointer under the lock but call Stop() outside the lock,
	// because Stop() waits for the tick loop goroutine to exit, and that
	// goroutine's delivery callbacks acquire s.mu.RLock() — holding s.mu.Lock()
	// here would deadlock.
	s.mu.Lock()
	fs := s.frameSync
	s.mu.Unlock()
	if fs != nil {
		fs.Stop()
	}

	// Shut down async video processing goroutine.
	close(s.videoProcCh)
	<-s.videoProcDone
	// Release pre-allocated frame pool buffers. Safe after videoProcDone
	// guarantees no more pipeline work (no concurrent Acquire calls).
	if s.framePool != nil {
		s.framePool.Close()
	}
	// Swap pipeline to nil and synchronously drain + close.
	if p := s.pipeline.Swap(nil); p != nil {
		p.Wait()
		_ = p.Close()
	}
	// Wait for any background drain goroutines from previous swaps
	// before closing pipeCodecs (prevents use-after-close on encoder).
	s.drainWg.Wait()
	s.mu.Lock()
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

// SetOnFirstVideoPTS registers a callback invoked once when the first
// program video frame enters the pipeline. Used to seed the audio mixer's
// PTS epoch so audio and video start from the same wall-clock moment.
func (s *Switcher) SetOnFirstVideoPTS(fn func(pts int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onFirstVideoPTS = fn
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
	s.compositorRef = c
	s.mu.Unlock()
	s.rebuildPipeline()
}

// SetKeyBridge attaches the upstream key bridge for chroma/luma keying.
// The bridge's ProcessYUV method is called in the video processing pipeline.
func (s *Switcher) SetKeyBridge(kb *graphics.KeyProcessorBridge) {
	s.mu.Lock()
	s.keyBridge = kb
	s.mu.Unlock()
	s.rebuildPipeline()
}

// SetLayoutCompositor sets the layout compositor for PIP/split-screen/quad.
func (s *Switcher) SetLayoutCompositor(lc *layout.Compositor) {
	s.mu.Lock()
	s.layoutCompositor = lc
	s.mu.Unlock()
	if lc != nil {
		lc.OnActiveChange = func() { s.rebuildPipeline() }
	}
	s.rebuildPipeline()
}

// SetSTMapRegistry sets the ST map registry for per-source correction.
// When set, each source decoder applies the assigned ST map warp after
// decode and resolution normalization, before fan-out to all consumers.
func (s *Switcher) SetSTMapRegistry(r *stmap.Registry) {
	s.mu.Lock()
	s.stmapRegistry = r
	s.mu.Unlock()
}

// gpuRunnerHolder wraps a GPUPipelineRunner for atomic pointer storage.
type gpuRunnerHolder struct {
	runner GPUPipelineRunner
}

// SetGPUPipeline registers a GPU pipeline that handles the full video
// processing chain (upload → key → layout → compositor → stmap → raw sinks → encode).
// When set, videoProcessingLoop routes frames through the GPU pipeline instead of
// the CPU pipeline, falling back to CPU on GPU errors. Pass nil to disable.
func (s *Switcher) SetGPUPipeline(gp GPUPipelineRunner) {
	if gp != nil {
		s.gpuRunner.Store(&gpuRunnerHolder{runner: gp})
	} else {
		s.gpuRunner.Store(nil)
	}
}

// gpuSourceMgrHolder wraps a GPUSourceManagerIface for atomic pointer storage.
type gpuSourceMgrHolder struct {
	mgr GPUSourceManagerIface
}

// SetGPUSourceManager registers a GPU source manager that handles per-source
// GPU upload, ST map correction, caching, and preview encoding. When set,
// handleRawVideoFrame routes YUV through IngestYUV instead of CPU fill paths
// (keyBridge.IngestFillYUV, layoutCompositor.IngestSourceFrame). Also sets
// gpuSourceActive so sourceDecoder skips redundant CPU ST map correction.
// Pass nil to disable.
func (s *Switcher) SetGPUSourceManager(mgr GPUSourceManagerIface) {
	if mgr != nil {
		s.gpuSourceMgr.Store(&gpuSourceMgrHolder{mgr: mgr})
		s.gpuSourceActive.Store(true)
	} else {
		s.gpuSourceMgr.Store(nil)
		s.gpuSourceActive.Store(false)
	}
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

// buildNodeList constructs the ordered list of CPU pipeline nodes.
// Must be called with s.mu held (RLock or Lock) since it reads
// s.keyBridge, s.compositorRef, s.pipeCodecs, and s.promMetrics.
//
// When a GPU pipeline is active (via SetGPUPipeline), the CPU pipeline is
// only used as fallback. The GPU pipeline runs independently via RunWithUpload.
//
// CPU pipeline order:
//   [upstream-key → layout-compositor → compositor → stmap-program → raw-sink-mxl → raw-sink-preview → h264-encode]
func (s *Switcher) buildNodeList() []PipelineNode {
	return []PipelineNode{
		&upstreamKeyNode{bridge: s.keyBridge},
		&layoutCompositorNode{compositor: s.layoutCompositor},
		&compositorNode{compositor: s.compositorRef},
		&stmapProgramNode{registry: s.stmapRegistry},
		&rawSinkNode{sink: &s.rawVideoSink, name: "raw-sink-mxl"},
		&rawSinkNode{sink: &s.rawPreviewSink, name: "raw-sink-preview"},
		newAsyncEncodeNode(s),
	}
}

// newAsyncEncodeNode creates an encodeNode with its async goroutine started.
// Must be called with s.mu held (reads pipeCodecs, promMetrics, etc.).
func newAsyncEncodeNode(s *Switcher) *encodeNode {
	enc := &encodeNode{
		codecs:          s.pipeCodecs,
		forceIDR:        &s.forceNextIDR,
		promMetrics:     s.promMetrics,
		encodeNilCount:  &s.pipeEncodeNil,
		encodeDropCount: &s.pipeEncodeDrop,
		onEncoded:       s.broadcastWithCaptions,
	}
	enc.start()
	return enc
}

// BuildPipeline constructs and stores the video processing pipeline.
// Must be called after SetCompositor, SetKeyBridge, and SetPipelineCodecs.
// Safe to call multiple times — each call rebuilds from scratch.
func (s *Switcher) BuildPipeline() error {
	s.mu.RLock()
	hasPipeCodecs := s.pipeCodecs != nil
	prom := s.promMetrics
	var nodes []PipelineNode
	if hasPipeCodecs {
		nodes = s.buildNodeList()
	}
	s.mu.RUnlock()

	if !hasPipeCodecs {
		return nil // no encoder configured yet
	}

	format := s.PipelineFormat()
	p := &Pipeline{}
	if err := p.Build(format, s.framePool, nodes); err != nil {
		return err
	}
	p.SetMetrics(prom)
	p.epoch = s.pipelineEpoch.Add(1)
	s.log.Info("pipeline built",
		"epoch", p.epoch,
		"active_nodes", len(p.activeNodes),
		"total_latency", p.TotalLatency(),
		"lip_sync_hint", p.TotalLatency()-aacFrameDuration,
	)
	s.pipeline.Store(p)
	return nil
}

// swapPipeline atomically replaces the current pipeline with newPipeline.
// The old pipeline drains in-flight frames via WaitGroup, then closes all
// nodes in a background goroutine. This is the primitive all rebuild triggers use.
func (s *Switcher) swapPipeline(newPipeline *Pipeline) {
	old := s.pipeline.Swap(newPipeline)
	if old == nil {
		return
	}
	s.drainWg.Add(1)
	go func() {
		defer s.drainWg.Done()
		old.Wait()
		_ = old.Close()
	}()
}

// rebuildPipeline builds a fresh Pipeline from current state and atomically
// swaps it in. Logs a warning and keeps the old pipeline if Build() fails.
// This is the runtime reconfiguration path — SetCompositor, SetKeyBridge,
// SetRawVideoSink, and external callbacks all use this.
func (s *Switcher) rebuildPipeline() {
	s.mu.RLock()
	hasPipeCodecs := s.pipeCodecs != nil
	prom := s.promMetrics
	var nodes []PipelineNode
	if hasPipeCodecs {
		nodes = s.buildNodeList()
	}
	s.mu.RUnlock()

	if !hasPipeCodecs {
		return
	}

	format := s.PipelineFormat()
	p := &Pipeline{}
	if err := p.Build(format, s.framePool, nodes); err != nil {
		s.log.Warn("pipeline rebuild failed", "error", err)
		return
	}
	p.SetMetrics(prom)
	p.epoch = s.pipelineEpoch.Add(1)
	s.log.Info("pipeline rebuilt",
		"epoch", p.epoch,
		"active_nodes", len(p.activeNodes),
		"total_latency", p.TotalLatency(),
		"lip_sync_hint", p.TotalLatency()-aacFrameDuration,
	)
	s.swapPipeline(p)
}

// RebuildPipeline rebuilds the video processing pipeline from current state.
// Called by external components (compositor, key processor) via callbacks
// when their Active() status may have changed.
func (s *Switcher) RebuildPipeline() {
	s.rebuildPipeline()
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
		fs.framePool = s.framePool
		s.frameSync = fs

		// Wire all existing source viewers to the frame sync.
		for key, ss := range s.sources {
			if ss.viewer != nil {
				ss.viewer.frameSync.Store(fs)
				ss.viewer.delayBuffer.Store(nil) // bypass delay buffer
			}
			fs.AddSource(key)
		}
		fs.SetProgramSource(s.programSource)
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

// SetClockDrivenSync enables clock-driven frame sync output. When enabled,
// the frame sync uses only timer-driven releases at a fixed rate, decoupling
// output timing from source jitter. Adds up to one frame of latency (~33ms)
// but produces rock-steady output timing like a hardware TBC/frame sync.
func (s *Switcher) SetClockDrivenSync(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frameSync != nil {
		s.frameSync.SetClockDriven(enabled)
	}
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

// GetFramePool returns the current FramePool for YUV buffer allocation.
// Used by external packages (e.g., SRT wiring) that create ProcessingFrames
// and want pool-managed buffer lifecycle instead of heap allocation.
func (s *Switcher) GetFramePool() *FramePool {
	return s.framePool
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

	// Recreate frame pool at new dimensions. Old pool drains naturally —
	// Release() discards wrong-sized buffers via cap check.
	s.framePool = NewFramePool(512, f.Width, f.Height)

	// Update frame sync tick rate and pool reference if active.
	if s.frameSyncActive && s.frameSync != nil {
		s.frameSync.SetFramePool(s.framePool)
		s.frameSync.SetTickRate(f.FrameDuration())
	}

	// Force encoder recreation on next frame.
	if s.pipeCodecs != nil {
		s.pipeCodecs.invalidateEncoder()
	}

	// Build new pipeline with new pool + new format, swap atomically.
	// Capture node list and metrics under lock to avoid race on s.compositorRef etc.
	hasPipeCodecs := s.pipeCodecs != nil
	prom := s.promMetrics
	var nodes []PipelineNode
	if hasPipeCodecs {
		nodes = s.buildNodeList()
	}
	s.mu.Unlock()

	if hasPipeCodecs {
		p := &Pipeline{}
		if err := p.Build(f, s.framePool, nodes); err != nil {
			s.log.Warn("pipeline rebuild failed on format change", "error", err)
		} else {
			p.SetMetrics(prom)
			p.epoch = s.pipelineEpoch.Add(1)
			s.swapPipeline(p)
		}
	}

	s.log.Info("pipeline format changed",
		"name", f.Name,
		"width", f.Width,
		"height", f.Height,
		"fps", fmt.Sprintf("%d/%d", f.FPSNum, f.FPSDen))

	atomic.AddUint64(&s.seq, 1)
	s.mu.RLock()
	snapshot := s.buildStateLocked()
	s.mu.RUnlock()

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
	s.lastBroadcastIntervalNano.Store(gap)
	atomicutil.UpdateMax(&s.maxBroadcastIntervalNano, gap)
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
	atomicutil.UpdateMax(&s.transSeamMaxNano, gap)
	s.transSeamCount.Add(1)
	s.log.Info("transition seam measured", "gap_ms", float64(gap)/1e6)
}

// EnableWallClockVideoPTS enables wall-clock PTS rewriting on the program
// relay. When enabled, video PTS is rewritten to match the mixer's
// wall-clock audio PTS, keeping A/V aligned after source cuts.
func (s *Switcher) EnableWallClockVideoPTS() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wallClockVideoEnabled = true
}

// wallClockVideoPTS returns a hybrid PTS: frame-counter for regular spacing
// with wall-clock resync to match the mixer's audio PTS timeline.
func (s *Switcher) wallClockVideoPTS(sourcePTS int64) int64 {
	if !s.wallClockVideoEnabled {
		return sourcePTS
	}
	now := time.Now()
	if !s.videoPTSInited {
		s.videoPTSStart = sourcePTS
		s.videoPTSEpoch = now
		s.videoPTS = sourcePTS
		s.videoPTSInited = true
		return sourcePTS
	}

	// Frame counter: advance by video frame duration for regular spacing.
	pf := s.pipelineFormat.Load()
	var frameDur int64 = 3000 // default 30fps
	if pf != nil {
		frameDur = int64(90000) * int64(pf.FPSDen) / int64(pf.FPSNum)
	}
	s.videoPTS += frameDur

	// Wall-clock resync: nudge toward wall clock to prevent drift.
	wallPTS := s.videoPTSStart + int64(now.Sub(s.videoPTSEpoch).Seconds()*90000)
	drift := wallPTS - s.videoPTS
	if drift > frameDur {
		s.videoPTS += frameDur / 2
	} else if drift < -frameDur {
		s.videoPTS -= frameDur / 2
	}

	// No 33-bit mask here — the muxer handles PTS rebasing and wrapping.
	// Masking would put video PTS in a different domain than audio PTS.
	return s.videoPTS
}

// broadcastToProgram sends a video frame to the program relay with a
// monotonically increasing GroupID. When wall-clock video PTS is enabled,
// rewrites PTS to stay aligned with the mixer's wall-clock audio PTS.
func (s *Switcher) broadcastToProgram(frame *media.VideoFrame) {
	f := *frame // shallow struct copy — avoids mutating shared frame
	// PTS is already in wall-clock domain — rewritten in videoProcessingLoop
	// before pipeline.Run(). No wallClockVideoPTS call here.
	f.DTS = f.PTS
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
	// Direct output path: zero-latency delivery to MPEG-TS muxer,
	// bypassing relay → viewer channel → drain goroutine.
	if cb := s.outputVideoCallback.Load(); cb != nil {
		(*cb)(&f)
	}
}

// broadcastOwnedToProgram sends an owned frame (safe to mutate) to the
// program relay with a monotonically increasing GroupID. Rewrites PTS
// to wall-clock time.
func (s *Switcher) broadcastOwnedToProgram(frame *media.VideoFrame) {
	// PTS is already in wall-clock domain — rewritten in videoProcessingLoop.
	frame.DTS = frame.PTS
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
	// Direct output path: zero-latency delivery to MPEG-TS muxer.
	if cb := s.outputVideoCallback.Load(); cb != nil {
		(*cb)(frame)
	}
}

// ProgramRelay returns the program relay for external broadcast (e.g. authored captions).
func (s *Switcher) ProgramRelay() *distribution.Relay {
	return s.programRelay
}

// SetCaptionManager attaches a caption manager for CEA-608/708 SEI injection.
func (s *Switcher) SetCaptionManager(cm captionManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captionMgr = cm
}

// ForceNextIDRPtr returns a pointer to the forceNextIDR atomic for GPU encode.
func (s *Switcher) ForceNextIDRPtr() *atomic.Bool { return &s.forceNextIDR }

// BroadcastWithCaptions returns the caption-injecting broadcast callback
// for use by the GPU encode node.
func (s *Switcher) BroadcastWithCaptionsFunc() func(*media.VideoFrame) {
	return s.broadcastWithCaptions
}

// broadcastWithCaptions injects caption SEI NALUs into the encoded video frame
// before broadcasting to the program relay. This is the post-encode callback
// used by the pipeline's encodeNode when captions are enabled.
func (s *Switcher) broadcastWithCaptions(frame *media.VideoFrame) {
	s.mu.RLock()
	cm := s.captionMgr
	s.mu.RUnlock()

	if cm == nil {
		s.broadcastOwnedToProgram(frame)
		return
	}

	pairs := cm.ConsumeForFrameWithVANC()
	if len(pairs) == 0 {
		s.broadcastOwnedToProgram(frame)
		return
	}

	// Build SEI NALU containing cc_data.
	seiNALU := caption.BuildSEINALU(pairs)
	if len(seiNALU) == 0 {
		s.broadcastOwnedToProgram(frame)
		return
	}

	// Convert AVC1 → Annex B → insert SEI → convert back to AVC1.
	// Buffers are local (not reused across frames) because broadcastWithCaptions
	// is called from the async encodeLoop goroutine and would race during
	// pipeline swap if stored on the Switcher struct.
	annexB := codec.AVC1ToAnnexBInto(frame.WireData, nil)
	inserted := caption.InsertSEIBeforeVCLInto(seiNALU, annexB, nil)
	frame.WireData = codec.AnnexBToAVC1Into(inserted, nil)
	s.broadcastOwnedToProgram(frame)
}

// enqueueVideoWork sends a work item to the async video processing goroutine
// with newest-wins drop policy when the channel is full.
func (s *Switcher) enqueueVideoWork(work videoProcWork) {
	select {
	case s.videoProcCh <- work:
	default:
		// Channel full — drop oldest, enqueue new (newest-wins).
		// Release pool buffer from dropped frame to prevent pool exhaustion.
		select {
		case dropped := <-s.videoProcCh:
			if dropped.yuvFrame != nil {
				dropped.yuvFrame.ReleaseYUV()
			}
		default:
		}
		select {
		case s.videoProcCh <- work:
		default:
			// Re-enqueue failed (race: another writer filled the slot).
			// Release the new frame's buffer to prevent pool exhaustion.
			if work.yuvFrame != nil {
				work.yuvFrame.ReleaseYUV()
			}
			s.videoProcDropped.Add(1)
		}
	}
}

// videoProcessingLoop runs in a dedicated goroutine, draining videoProcCh
// and running each frame through the encode pipeline. This prevents
// the source relay's delivery goroutine from blocking on video processing,
// which would starve audio delivery.
func (s *Switcher) videoProcessingLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.videoProcDone)

	for work := range s.videoProcCh {
		if work.yuvFrame == nil {
			continue
		}
		// Discard frames stamped with a stale program epoch. This prevents
		// wrong-source frames from reaching the pipeline when a Cut or
		// transition complete races with frame delivery.
		if work.epoch != 0 && work.epoch != s.programEpoch.Load() {
			work.yuvFrame.ReleaseYUV()
			s.programEpochStale.Add(1)
			continue
		}
		arrivalNano := work.yuvFrame.ArrivalNano // save before Run may reallocate

		// Compute sub-stage breakdown from frame timestamps
		if arrivalNano > 0 && work.yuvFrame.DecodeStartNano > 0 {
			s.lastDecodeQueueNs.Store(work.yuvFrame.DecodeStartNano - arrivalNano)
		}
		if work.yuvFrame.DecodeStartNano > 0 && work.yuvFrame.DecodeEndNano > 0 {
			s.lastDecodeNs.Store(work.yuvFrame.DecodeEndNano - work.yuvFrame.DecodeStartNano)
		}
		if work.yuvFrame.SyncReleaseNano > 0 {
			if work.yuvFrame.DecodeEndNano > 0 {
				s.lastSyncWaitNs.Store(work.yuvFrame.SyncReleaseNano - work.yuvFrame.DecodeEndNano)
			} else {
				// FRC-synthesized frame: no decode→sync wait by definition.
				// Store 0 to prevent stale values from previous fresh frames.
				s.lastSyncWaitNs.Store(0)
			}
		}
		if work.enqueueNano > 0 {
			s.lastProcQueueNs.Store(time.Now().UnixNano() - work.enqueueNano)
		}

		start := time.Now()

		// Normalize to pipeline format resolution before processing.
		// Sources may deliver at different resolutions (e.g., 720p camera
		// + 1080p file). Scaling here rather than on the delivery hot path
		// avoids blocking source frame delivery goroutines.
		work.yuvFrame = s.normalizeResolution(work.yuvFrame)

		// Rewrite PTS to wall-clock domain BEFORE pipeline.Run() so that
		// all pipeline nodes — including raw sinks (preview encoder, MXL
		// output) — see the same PTS as the program relay and audio mixer.
		// Previously, wallClockVideoPTS was called in broadcastToProgram
		// (after the encode node), so raw sinks received the raw source PTS
		// which was in a different domain than the mixer's audio PTS.
		work.yuvFrame.PTS = s.wallClockVideoPTS(work.yuvFrame.PTS)

		// Route through GPU pipeline if available, fall back to CPU.
		gpuUsed := false
		if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
			// Try zero-upload path first: use pre-cached GPU frame from source manager.
			if work.sourceKey != "" {
				if err := h.runner.RunFromCache(work.sourceKey, work.yuvFrame.PTS); err == nil {
					gpuUsed = true
				}
			}
			// Fall back to CPU→GPU upload if cache miss or no source key.
			if !gpuUsed {
				if err := h.runner.RunWithUpload(work.yuvFrame.YUV, work.yuvFrame.Width, work.yuvFrame.Height, work.yuvFrame.PTS); err == nil {
					gpuUsed = true
				}
			}
		}
		if !gpuUsed {
			if p := s.pipeline.Load(); p != nil {
				work.yuvFrame = p.Run(work.yuvFrame)
			}
		}
		work.yuvFrame.ReleaseYUV()

		dur := time.Since(start).Nanoseconds()
		s.videoProcLastNano.Store(dur)
		s.videoProcCount.Add(1)
		atomicutil.UpdateMax(&s.videoProcMaxNano, dur)
		if dur > s.frameBudgetNs.Load() {
			s.deadlineViolations.Add(1)
		}

		if arrivalNano > 0 {
			e2e := time.Now().UnixNano() - arrivalNano
			s.lastE2ENs.Store(e2e)
			atomicutil.UpdateMax(&s.maxE2ENs, e2e)
		}
	}
}

// broadcastProcessed handles frames that are already decoded to YUV
// (e.g., from the transition engine). Enqueues for pipeline processing.
func (s *Switcher) broadcastProcessed(yuv []byte, width, height int, pts int64, isKeyframe bool) {
	s.transOutputCount.Add(1)
	if s.pipeline.Load() == nil {
		return
	}

	s.mu.RLock()
	var groupID uint32
	if ss, ok := s.sources[s.programSource]; ok {
		groupID = ss.lastGroupID.Load()
	}
	s.mu.RUnlock()

	expectedSize := width * height * 3 / 2
	if len(yuv) < expectedSize {
		return
	}

	// Defensive: if the frame exceeds pool buffer capacity (e.g., 4K clip
	// in a 1080p pipeline), skip rather than panic on slice bounds.
	poolBufSize := s.framePool.BufSize()
	if expectedSize > poolBufSize {
		s.log.Warn("broadcastProcessed: frame exceeds pool buffer size, dropping",
			"frame_w", width, "frame_h", height,
			"frame_bytes", expectedSize, "pool_bytes", poolBufSize,
		)
		return
	}

	buf := s.framePool.Acquire()
	copy(buf, yuv[:expectedSize])

	pf := &ProcessingFrame{
		YUV: buf[:expectedSize], Width: width, Height: height,
		PTS: pts, DTS: pts, IsKeyframe: isKeyframe,
		Codec:   "h264",
		GroupID: groupID,
		pool:    s.framePool,
	}
	pf.SetRefs(1)
	s.enqueueVideoWork(videoProcWork{yuvFrame: pf, enqueueNano: time.Now().UnixNano()})
}

// broadcastProcessedFromPF handles a ProcessingFrame from the always-decode
// pipeline. For refcounted frames (from source_decoder via frame_sync), uses
// Ref + shallow copy for zero-copy delivery to the pipeline goroutine. The
// pipeline's MakeWritable call ensures exclusive buffer ownership before any
// in-place modification, so source frames retained by frame_sync (lastRawVideo)
// are never mutated. For unmanaged frames (FRC scratch buffers), falls back to
// DeepCopy since FRC reuses its internal scratch buffers between calls.
//
// sourceKey identifies the program source that produced this frame, enabling
// the GPU pipeline to use RunFromCache instead of re-uploading from CPU.
// Pass "" for transition/FRC output (blended/synthesized frames).
func (s *Switcher) broadcastProcessedFromPF(sourceKey string, pf *ProcessingFrame) {
	if s.pipeline.Load() == nil {
		return
	}
	epoch := s.programEpoch.Load()
	enqueueNano := time.Now().UnixNano()
	if pf.refs != nil {
		// Managed frame: Ref for pipeline, shallow copy shares YUV + refs.
		// Pipeline.Run calls MakeWritable before in-place processing.
		pf.Ref()
		cp := new(ProcessingFrame)
		*cp = *pf
		s.enqueueVideoWork(videoProcWork{yuvFrame: cp, epoch: epoch, enqueueNano: enqueueNano, sourceKey: sourceKey})
	} else {
		// Unmanaged frame (FRC scratch buffer): must deep-copy.
		cp := pf.DeepCopy()
		cp.SetRefs(1)
		s.enqueueVideoWork(videoProcWork{yuvFrame: cp, epoch: epoch, enqueueNano: enqueueNano, sourceKey: sourceKey})
	}
}

// normalizeResolution scales a ProcessingFrame to the pipeline format
// resolution if it differs. Called in the video processing goroutine
// (not on the delivery hot path) to avoid blocking source frame delivery.
// Returns the same frame if no scaling needed, or a new scaled frame.
// Skips scaling when the frame pool can't accommodate the target resolution
// (e.g., test environments with small pools).
func (s *Switcher) normalizeResolution(pf *ProcessingFrame) *ProcessingFrame {
	fmt := s.pipelineFormat.Load()
	if fmt == nil || (pf.Width == fmt.Width && pf.Height == fmt.Height) {
		return pf
	}
	dstW, dstH := fmt.Width, fmt.Height
	dstSize := dstW * dstH * 3 / 2
	// Skip if the pool can't hold the target resolution (test safety).
	if s.framePool.BufSize() < dstSize {
		return pf
	}
	buf := s.framePool.Acquire()
	buf = buf[:dstSize]
	transition.ScaleYUV420(pf.YUV, pf.Width, pf.Height, buf, dstW, dstH)
	pf.ReleaseYUV()
	scaled := &ProcessingFrame{
		YUV:        buf,
		Width:      dstW,
		Height:     dstH,
		PTS:        pf.PTS,
		DTS:        pf.DTS,
		IsKeyframe: pf.IsKeyframe,
		Codec:      pf.Codec,
		GroupID:    pf.GroupID,
		pool:       s.framePool,
	}
	scaled.SetRefs(1)
	return scaled
}

// StartTransition begins a mix/dip/wipe/stinger transition from the current
// program source to the given target source. Frames from both sources are
// routed to the transition engine which produces blended output on the program
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
		return errTransitionNotConfigured
	}
	if s.state.isInTransition() {
		s.mu.Unlock()
		return fmt.Errorf("transition: %w", transition.ErrActive)
	}
	if s.state.isFTBActive() {
		s.mu.Unlock()
		return fmt.Errorf("cannot start transition: %w", transition.ErrFTBActive)
	}
	if s.programSource == "" {
		s.mu.Unlock()
		return errNoProgramSource
	}
	if sourceKey == "" {
		s.mu.Unlock()
		return errNoTargetSource
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

	tt := transition.Type(transType)
	if tt != transition.Mix && tt != transition.Dip && tt != transition.Wipe && tt != transition.Stinger {
		s.mu.Unlock()
		return fmt.Errorf("unsupported transition type: %q", transType)
	}

	if tt == transition.Stinger && topts.stingerData == nil {
		s.mu.Unlock()
		return errStingerDataRequired
	}

	// Validate wipe direction when type is wipe
	var wipeDir transition.WipeDirection
	if tt == transition.Wipe {
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

	engine := transition.NewEngine(transition.EngineConfig{
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
		audioMode := audio.Crossfade
		if tt == transition.Dip {
			audioMode = audio.DipToSilence
		}
		audioHandler.OnTransitionStart(fromSource, sourceKey, audioMode, durationMs)

		// Pass stinger audio to mixer for additive overlay
		if tt == transition.Stinger && topts.stingerData != nil && topts.stingerData.Audio != nil {
			audioHandler.SetStingerAudio(topts.stingerData.Audio, topts.stingerData.AudioSampleRate, topts.stingerData.AudioChannels)
		}
	}

	// Now publish the engine — audio crossfade is already active, so the
	// first OnTransitionPosition from a video frame will be handled.
	s.mu.Lock()
	s.transEngine = engine
	s.previewSource = sourceKey
	s.transitionsStarted.Add(1)
	// During transition, the destination (incoming) source drives the release.
	if s.frameSync != nil {
		s.frameSync.SetProgramSource(sourceKey)
	}

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
		return errTransitionNotConfigured
	}

	// Reject if a non-FTB transition is active (mix/dip/wipe)
	if s.state == StateTransitioning {
		s.mu.Unlock()
		return fmt.Errorf("cannot FTB while mix/dip transition is active: %w", transition.ErrActive)
	}

	// Toggle off: FTB is active but transition is complete (fully black).
	// Start a reverse FTB transition to fade back from black.
	if s.state == StateFTB {
		if s.programSource == "" {
			s.mu.Unlock()
			return errNoProgramSource
		}

		fromSource := s.programSource
		ftbHintW, ftbHintH := s.pipeCodecs.dimensions()
		ftbRevDecoderFactory := s.transConfig.DecoderFactory

		// Mark transition as starting, then release lock.
		s.transitionState(StateFTBReversing)
		s.mu.Unlock()

		// No decoder warmup needed — sources provide raw YUV.
		engine := transition.NewEngine(transition.EngineConfig{
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

		if err := engine.Start(fromSource, "", transition.FTBReverse, defaultFTBDurMs); err != nil {
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
			audioHandler.OnTransitionStart(fromSource, "", audio.FadeIn, defaultFTBDurMs)
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
		return errNoProgramSource
	}

	fromSource := s.programSource
	ftbFwdHintW, ftbFwdHintH := s.pipeCodecs.dimensions()
	ftbFwdDecoderFactory := s.transConfig.DecoderFactory

	// Mark transition as starting, then release lock.
	s.transitionState(StateFTBTransitioning)
	s.mu.Unlock()

	// No decoder warmup needed — sources provide raw YUV.
	engine := transition.NewEngine(transition.EngineConfig{
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

	if err := engine.Start(fromSource, "", transition.FTB, defaultFTBDurMs); err != nil {
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
		audioHandler.OnTransitionStart(fromSource, "", audio.FadeOut, defaultFTBDurMs)
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
			audioHandler.OnTransitionAbort()
		}
		s.notifyStateChange(snapshot)
	}
}

// handleTransitionComplete is called by the transition engine when a mix/dip
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
		// Bump program epoch so in-flight frames from the transition
		// engine or old program source are discarded.
		s.programEpoch.Add(1)
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
	frameSync := s.frameSync
	programAfterTransition := s.programSource
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	// Update frame sync program source after unlock.
	// Completed: programSource was updated to destination.
	// Aborted: programSource is unchanged (reverts to old program).
	if frameSync != nil {
		frameSync.SetProgramSource(programAfterTransition)
	}

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
		if aborted {
			audioHandler.OnTransitionAbort()
		} else {
			audioHandler.OnTransitionComplete()
		}
	}
	if !aborted && audioCut != nil {
		audioCut.OnProgramChange(snapshot.ProgramSource)
	}
	s.notifyStateChange(snapshot)
}

// handleFTBComplete is called by the transition engine when an FTB transition
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
		if aborted {
			audioHandler.OnTransitionAbort()
		} else {
			audioHandler.OnTransitionComplete()
			// FTB completed — screen is black, mute program audio
			audioHandler.SetProgramMute(true)
		}
	}
	s.notifyStateChange(snapshot)
}

// handleFTBReverseComplete is called by the transition engine when a reverse
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
		if aborted {
			// FTB reverse aborted — screen stays black, re-mute audio
			audioHandler.OnTransitionAbort()
			audioHandler.SetProgramMute(true)
		} else {
			audioHandler.OnTransitionComplete()
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
		sd := newSourceDecoder(key, s.sourceDecoderFactory, cb, s.framePool, &s.pipelineFormat, s.stmapRegistry, &s.gpuSourceActive)
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
			pf.ReleaseYUV()
			return
		}
		if fs := ss.viewer.frameSync.Load(); fs != nil {
			fs.IngestRawVideo(sourceKey, pf)
			return // frame sync owns the buffer (FRC or ring buffer handles release)
		}
		if db := ss.viewer.delayBuffer.Load(); db != nil {
			db.handleRawVideoFrame(sourceKey, pf)
			return // delay buffer handles release internally
		}
		s.handleRawVideoFrame(sourceKey, pf)
		pf.ReleaseYUV() // direct path: frame consumed synchronously, safe to release
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

// registerRawSource is the shared implementation for RegisterMXLSource and
// RegisterSRTSource. It creates a sourceState with no relay/viewer (raw YUV
// frames arrive via IngestRawVideo) and notifies state listeners.
// When frame sync is active, the source is added to the synchronizer so that
// IngestRawVideo routes through the ring buffer for steady-rate release.
func (s *Switcher) registerRawSource(key, label string, mxl bool) {
	s.mu.Lock()
	s.sources[key] = &sourceState{
		key:      key,
		label:    label,
		position: len(s.sources) + 1,
		isMXL:    mxl,
	}
	s.health.registerSource(key)
	if s.frameSyncActive && s.frameSync != nil {
		s.frameSync.AddSource(key)
	}
	atomic.AddUint64(&s.seq, 1)
	snapshot := s.buildStateLocked()
	s.mu.Unlock()
	s.log.Info("raw source registered", "source_key", key, "mxl", mxl)
	s.notifyStateChange(snapshot)
}

// RegisterMXLSource registers a source that provides raw YUV420p frames
// directly (no Prism relay/viewer). Used for MXL shared-memory sources.
func (s *Switcher) RegisterMXLSource(key string) {
	s.registerRawSource(key, strings.ToUpper(key), true)
}

// RegisterSRTSource registers a source that provides raw YUV420p frames
// via IngestRawVideo (same path as MXL). Used for SRT input sources that
// are decoded by the srt.Source orchestrator before being fed to the switcher.
func (s *Switcher) RegisterSRTSource(key string) {
	s.registerRawSource(key, strings.TrimPrefix(key, "srt:"), false)
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

// IngestRawVideo accepts a raw YUV420p frame from an MXL or SRT source.
// When frame sync is active, frames are buffered in the synchronizer's
// per-source ring and released at steady tick rate. Otherwise, delegates
// directly to handleRawVideoFrame for immediate processing.
func (s *Switcher) IngestRawVideo(sourceKey string, pf *ProcessingFrame) {
	s.mu.RLock()
	fs := s.frameSync
	s.mu.RUnlock()
	if fs != nil {
		fs.IngestRawVideo(sourceKey, pf)
		return
	}
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
		s.programEpoch.Add(1)
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
	var abortEngine *transition.Engine
	var abortAudioHandler audioTransitionHandler
	changed := false

	s.mu.Lock()
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	if s.programSource != sourceKey {
		// If a transition is active, abort it first so its OnComplete
		// callback won't overwrite our new programSource.
		if s.state.isInTransition() {
			abortEngine = s.transEngine
			abortAudioHandler = s.audioTransition
			s.transEngine = nil
			s.transitionState(StateIdle)
		}
		oldProgram = s.programSource
		s.programSource = sourceKey
		if s.frameSync != nil {
			s.frameSync.SetProgramSource(sourceKey)
		}
		// Bump program epoch so in-flight frames from the old program
		// source are discarded in videoProcessingLoop.
		s.programEpoch.Add(1)
		// All sources use the raw pipeline (always-decode or MXL) —
		// no IDR gating needed. Frames flow immediately after cut.
		s.cutsTotal.Add(1)
		// Force an IDR on the next encoded frame so downstream decoders
		// (browser WebCodecs, SRT receivers) can sync immediately after
		// the source change. Source H.264 keyframes are NOT propagated
		// through the raw YUV pipeline (see source_decoder.go).
		s.forceNextIDR.Store(true)
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

	// Stop the aborted transition engine outside the lock.
	if abortEngine != nil {
		abortEngine.Stop()
		s.log.Warn("transition aborted", "reason", "cut override")
		if abortAudioHandler != nil {
			abortAudioHandler.OnTransitionAbort()
		}
	}

	if changed {
		s.log.Info("cut executed", "source", sourceKey, "previous_source", oldProgram)

		// Record transition seam for cut timing diagnostics.
		s.transSeamStartNano.Store(time.Now().UnixNano())

		// Auto-dissolve any PIP slot showing the new program source.
		s.mu.RLock()
		layoutComp := s.layoutCompositor
		s.mu.RUnlock()
		if layoutComp != nil {
			layoutComp.AutoDissolveSource(sourceKey)
		}

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
		srcInfo := map[string]any{
			"health_status":     string(s.health.rawStatus(key)),
			"last_frame_ago_ms": s.health.lastFrameAgoMs(key),
			"raw_pipeline":      ss.useRawPipeline,
		}
		if ss.viewer != nil {
			videoIn = ss.viewer.videoSent.Load()
			audioIn = ss.viewer.audioSent.Load()
			if sd := ss.viewer.srcDecoder.Load(); sd != nil {
				activeDecoders++
				avgSize, avgFPS := sd.Stats()
				srcInfo["decoder_avg_frame_bytes"] = int(avgSize)
				srcInfo["decoder_avg_fps"] = avgFPS
				srcInfo["decoder_active"] = true
			}
		}
		srcInfo["video_frames_in"] = videoIn
		srcInfo["audio_frames_in"] = audioIn
		sources[key] = srcInfo
	}

	// Estimate ~3MB per 1080p YUV420 decoder output buffer.
	estimatedYUVMB := activeDecoders * 3

	result := map[string]any{
		"program_source": s.programSource,
		"preview_source": s.previewSource,
		"state":          s.state.String(),
		"in_transition":  s.state.isInTransition(),
		"ftb_active":     s.state.isFTBActive(),
		"seq":            atomic.LoadUint64(&s.seq),
		"sources":        sources,
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
			"epoch_stale":          s.programEpochStale.Load(),
			"encode_nil":           s.pipeEncodeNil.Load(),
			"encode_drop":          s.pipeEncodeDrop.Load(),
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
			"trans_seam_last_ms":   float64(s.transSeamLastNano.Load()) / 1e6,
			"trans_seam_max_ms":    float64(s.transSeamMaxNano.Load()) / 1e6,
			"trans_seam_count":     s.transSeamCount.Load(),
		},
		"codec": map[string]any{
			"encoder":  s.codecEncoder,
			"decoder":  s.codecDecoder,
			"hw_accel": s.codecHWAccel,
		},
	}

	if s.framePool != nil {
		hits, misses := s.framePool.Stats()
		result["frame_pool"] = map[string]any{
			"hits":     hits,
			"misses":   misses,
			"capacity": s.framePool.cap,
			"buf_size": s.framePool.bufSize,
		}
	}

	if p := s.pipeline.Load(); p != nil {
		result["pipeline"] = p.Snapshot()
	}

	// Include transition engine timing when active
	if s.state.isInTransition() && s.transEngine != nil {
		result["transition_engine"] = s.transEngine.Timing()
	}

	// Include frame sync and FRC state when frame sync is active.
	if s.frameSync != nil {
		result["frame_sync"] = s.frameSync.DebugSnapshot()
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
		state.MomentaryLUFS = clampLUFS(s.mixer.MomentaryLUFS())
		state.ShortTermLUFS = clampLUFS(s.mixer.ShortTermLUFS())
		state.IntegratedLUFS = clampLUFS(s.mixer.IntegratedLUFS())
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

// clampLUFS clamps extreme LUFS values (e.g. -math.MaxFloat64 from an
// uninitialized meter) to -100, which is safely JSON-serializable and
// represents effective silence for UI display.
func clampLUFS(v float64) float64 {
	if v < -100 {
		return -100
	}
	return v
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
// (single-writer). Stats are stored atomically so concurrent readers
// (state broadcast, debug snapshot) can access them without a lock.
// Uses an exponential moving average with alpha=0.1.
func (s *Switcher) updateFrameStats(ss *sourceState, frame *media.VideoFrame) {
	const alpha = 0.1 // EMA smoothing factor

	frameSize := float64(len(frame.WireData))
	count := int(ss.frameCount.Add(1))

	if count == 1 {
		// First frame — seed the averages
		ss.avgFrameSizeBits.Store(math.Float64bits(frameSize))
		ss.lastPTS.Store(frame.PTS)
		return
	}

	// Update frame size EMA
	prev := math.Float64frombits(ss.avgFrameSizeBits.Load())
	ss.avgFrameSizeBits.Store(math.Float64bits(alpha*frameSize + (1-alpha)*prev))

	// Update FPS EMA from PTS delta
	prevPTS := ss.lastPTS.Load()
	if frame.PTS > prevPTS {
		deltaPTS := frame.PTS - prevPTS
		// PTS is in 90kHz clock units (standard MPEG-TS timebase).
		// Protect against unreasonable deltas (>1 second or negative)
		if deltaPTS > 0 && deltaPTS < 90000 {
			instantFPS := 90000.0 / float64(deltaPTS)
			prevFPS := math.Float64frombits(ss.avgFPSBits.Load())
			if prevFPS == 0 {
				ss.avgFPSBits.Store(math.Float64bits(instantFPS))
			} else {
				ss.avgFPSBits.Store(math.Float64bits(alpha*instantFPS + (1-alpha)*prevFPS))
			}
		}
	}
	ss.lastPTS.Store(frame.PTS)
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
	fs := s.frameSync
	s.mu.RUnlock()

	// Apply PTS correction from FrameSynchronizer. Audio bypasses the frame
	// sync entirely (to avoid bursty tick-quantized delivery), but video PTS
	// is rewritten by the frame sync (forward-clamped during freezes). This
	// creates A/V desync — audio PTS uses raw source PTS while video uses
	// adjusted PTS. The correction delta aligns audio PTS with video PTS.
	if fs != nil {
		if delta := fs.GetSourcePTSCorrection(sourceKey); delta > 0 {
			// Copy the frame before mutating PTS — the relay may fan-out
			// the same pointer to multiple viewers.
			adjusted := *frame
			adjusted.PTS = (adjusted.PTS + delta) & ptsMask33
			frame = &adjusted
		}
	}

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
	if ok && ss != nil {
		ss.rawFrameCount.Add(1)
	}
	programSource := s.programSource
	fTBActive := s.state.isFTBActive()
	inTrans := s.state.isInTransition()
	engine := s.transEngine
	audioHandler := s.audioTransition
	keyBridge := s.keyBridge
	layoutComp := s.layoutCompositor
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

	// GPU source manager: upload + ST map + cache + preview encode.
	// When active, fills are read from GPU cache by key/layout nodes (via GPUFill),
	// so CPU IngestFillYUV / IngestSourceFrame are skipped.
	if h := s.gpuSourceMgr.Load(); h != nil && h.mgr != nil {
		h.mgr.IngestYUV(sourceKey, pf.YUV, pf.Width, pf.Height, pf.PTS)
	} else {
		// CPU fill paths — feed key bridge and layout compositor with decoded YUV.
		if keyBridge != nil {
			keyBridge.IngestFillYUV(sourceKey, pf.YUV, pf.Width, pf.Height)
		}
		if layoutComp != nil && layoutComp.NeedsSource(sourceKey) {
			layoutComp.IngestSourceFrame(sourceKey, pf.YUV, pf.Width, pf.Height, pf.PTS)
		}
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

	// Seed BOTH audio and video PTS epochs from first program video frame.
	// Both must seed at the same moment with the same PTS to stay aligned.
	if !s.firstVideoPTSSeeded {
		s.firstVideoPTSSeeded = true
		now := time.Now()
		// Seed video epoch directly (same moment as audio).
		if s.wallClockVideoEnabled && !s.videoPTSInited {
			s.videoPTSStart = pf.PTS
			s.videoPTSEpoch = now
			s.videoPTS = pf.PTS
			s.videoPTSInited = true
		}
		// Seed audio epoch via callback.
		if s.onFirstVideoPTS != nil {
			s.onFirstVideoPTS(pf.PTS)
		}
	}

	// Enqueue as yuvFrame — the processing loop handles key→compositor→encode→broadcast.
	s.broadcastProcessedFromPF(sourceKey, pf)
}

// handleCaptionFrame implements frameHandler. Only the current program
// source's captions are forwarded to the program Relay.
// When a caption manager is attached, the caption text is re-encoded
// as CEA-608 pairs for SEI injection into the encoded video output.
func (s *Switcher) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	s.mu.RLock()
	_, ok := s.sources[sourceKey]
	isProgram := ok && s.programSource == sourceKey
	cm := s.captionMgr
	s.mu.RUnlock()

	// Notify caption manager that this source has captions.
	if cm != nil {
		cm.NotifySourceCaptions(sourceKey, true)
	}

	if !isProgram {
		return
	}

	// Re-encode caption text as CEA-608 pairs for SEI re-embedding.
	// Uses the manager's pass-through encoder for proper CEA-608 formatting
	// (parity, control codes, rate limiting).
	if cm != nil && frame != nil {
		text := frame.PlainText()
		cm.SetPassThroughText(text)
	}

	s.programRelay.BroadcastCaptions(frame)
}
