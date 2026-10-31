package switcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/transition"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

const (
	maxDelayMs = 500 // maximum source delay for lip-sync correction

	minFTBDurationMs     = 100
	maxFTBDurationMs     = 5000
	defaultFTBDurationMs = 1000

	minTransitionAbortThreshold     = 0.05
	maxTransitionAbortThreshold     = 0.5
	defaultTransitionAbortThreshold = 0.3
)

// Sentinel errors for the switcher package.
var (
	ErrSourceNotFound                  = errors.New("switcher: source not found")
	ErrAlreadyOnProgram                = errors.New("switcher: already on program")
	ErrInvalidDelay                    = errors.New("switcher: delay must be 0-500ms")
	ErrInvalidPosition                 = errors.New("switcher: position must be >= 1")
	ErrNoTransition                    = errors.New("switcher: no active transition")
	ErrFormatDuringTransition          = errors.New("switcher: cannot change pipeline format during active transition")
	ErrEncoderNotAvailable             = errors.New("switcher: encoder not available")
	ErrInvalidFTBDuration              = errors.New("switcher: ftb duration must be 100-5000ms")
	ErrInvalidTransitionAbortThreshold = errors.New("switcher: transition abort threshold must be 0.05-0.5")
	errTransitionNotConfigured         = errors.New("transition not configured")
	errNoProgramSource                 = errors.New("no program source set")
	errNoTargetSource                  = errors.New("no target source specified")
	errStingerDataRequired             = errors.New("stinger transition requires stinger data")
)

// ErrTransitionNeedsGPU refuses a DVE transition the CPU does not draw, on
// an engine with no GPU pipeline; see dve.CPURefusedTransitions. The
// returned error carries the reason.
var ErrTransitionNeedsGPU = errors.New("switcher: transition needs a GPU engine")

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
// Caller must hold s.configMu (write lock).
//
// NOTE: intentionally does NOT publish to hotState. The legacy s.state field
// doubles as a writer-serialization flag (e.g., StartTransition flips it to
// StateTransitioning early to block concurrent starts) — publishing that
// early would expose a torn snapshot where hotState.state==Transitioning but
// hotState.transEngine is still nil because the engine hasn't been built.
// Callers must publish state to hotState in the same updateHotState closure
// as the paired field change (transEngine, programSource, etc.) so readers
// never observe a half-applied transition.
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
	// LimiterSettings returns threshold, ceiling, GR, and last-block true-peak
	// (dBTP).  truePeakDBTP must be JSON-encodable — ±Inf/NaN sentinels must
	// be coerced by the implementation.
	LimiterSettings() (thresholdDB, ceilingDB, gainReduction, truePeakDBTP float64)
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
	// OnTransitionStart returns an ownership token for the transition state
	// it established in the mixer. The token must be passed back to
	// OnTransitionAbort, which ignores the abort when the token is stale —
	// i.e. a newer cut/program change/transition has since established its
	// own mixer state. This makes late aborts from a pre-empted transition
	// setup harmless.
	OnTransitionStart(oldSource, newSource string, mode audio.TransitionMode, durationMs int) uint64
	OnTransitionPosition(position float64)
	OnTransitionComplete()
	OnTransitionAbort(token uint64)
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
	wipeConfig  *wipemap.WipeConfig
}

// WithStingerData sets the stinger overlay data for a stinger transition.
func WithStingerData(sd *transition.StingerData) TransitionOption {
	return func(o *transitionOpts) { o.stingerData = sd }
}

// WithEasing sets the easing curve for the transition.
func WithEasing(ec *transition.EasingCurve) TransitionOption {
	return func(o *transitionOpts) { o.easing = ec }
}

// WithWipeConfig sets gradient-map wipe parameters for a wipe transition.
func WithWipeConfig(wc *wipemap.WipeConfig) TransitionOption {
	return func(o *transitionOpts) { o.wipeConfig = wc }
}

// sourceState tracks a registered source and its Relay/viewer pair.
type sourceState struct {
	key       string
	label     string
	relay     *distribution.Relay
	viewer    *sourceViewer
	isVirtual bool // true for virtual sources (replay, etc.)
	isMXL     bool // true for MXL raw YUV sources (no H.264 decode/IDR gating)
	// mediaTimed: the producer stamps frames with engine-timeline PTS (MXL
	// grain index, or an anchored ST 2110 media clock). Other sources,
	// SRT among them, are stamped at arrival.
	mediaTimed     bool
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

	// Atomic mirrors of label and position. Phase 4 mirror writes from
	// SetLabel and SetSourcePosition so that Phase 5 (sources map →
	// atomic.Pointer[map]) can expose these fields to lock-free readers
	// without tearing. Mutated only under Switcher.configMu.
	labelAtomic    atomic.Pointer[string]
	positionAtomic atomic.Int32
}

// initAtomicMirrors seeds labelAtomic and positionAtomic from the legacy
// label/position fields. Called once per sourceState right after struct
// construction while the caller holds Switcher.configMu (Lock). The atomic
// snapshots allow Phase 5 readers to consume these fields without re-taking
// configMu once the sources map itself moves to an atomic.Pointer[map].
func (ss *sourceState) initAtomicMirrors() {
	label := ss.label
	ss.labelAtomic.Store(&label)
	ss.positionAtomic.Store(int32(ss.position))
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
	log                 *slog.Logger
	configMu            sync.Mutex                      // writer-serialization for COLD/WARM fields; all hot-path readers use atomic slots
	builtinSources      map[string]*builtinSource       // always-registered virtual sources (source:black, source:colorbars)
	builtinProducerStop chan struct{}                   // closed by Close() to shut down the built-in producer goroutine
	builtinProducerDone chan struct{}                   // closed by the producer loop on exit
	closeOnce           sync.Once                       // gates Close() body so double-close (e.g. t.Cleanup + defer sw.Close) is safe
	builtinFrameTap     atomic.Pointer[BuiltinFrameTap] // app-installed observer routing built-in frames to per-source MoQ preview encoders
	programSource       string
	previewSource       string
	programRelay        *distribution.Relay
	seq                 atomic.Uint64 // read on lock-free paths, so written atomically even under s.configMu
	lastStateStamp      atomic.Int64  // Timestamp of the latest state snapshot; see stateStamp
	stateCallbacks      []func(internal.ControlRoomState)
	health              *healthMonitor
	audioHandler        func(sourceKey string, frame *media.AudioFrame)
	mixer               audioStateProvider
	audioCut            audioCutHandler
	transConfig         *TransitionConfig
	transEngine         *transition.Engine
	transTypeOrig       string // original transition type string (e.g., "dve-push-left") for GPU dispatch
	// transStartGen is bumped (under configMu) each time StartTransition or
	// FadeToBlack begins its unlocked engine-creation window. Publish/rollback
	// phases re-check it (together with state) so a concurrent Cut/abort or a
	// newer transition start that landed inside the window is detected and the
	// stale engine is never published over the live state.
	transStartGen uint64
	// audioTransToken is the mixer ownership token returned by the live
	// transition's OnTransitionStart (guarded by configMu, set when the
	// transition engine is published). It is passed to OnTransitionAbort so
	// the mixer can ignore aborts that no longer own its transition state.
	audioTransToken uint64
	state           State
	ftbDurationMs   int

	// transitionAbortThreshold is the T-bar position (in [0.05, 0.5]) below
	// which a mid-AUTO pull-back internally aborts the transition, provided
	// the operator had driven the T-bar forward past 2× the threshold first.
	// The 2× gate keeps trivial jitter at low positions from triggering
	// unwanted aborts.
	transitionAbortThreshold float64
	// transitionMaxForward tracks the maximum T-bar position reached during
	// the active transition. Reset to 0 at each StartTransition.
	transitionMaxForward float64
	audioTransition      audioTransitionHandler
	delayBuffer          *DelayBuffer
	frameSync            *FrameSynchronizer

	// DSK graphics compositor — applies overlay in YUV420 domain.
	compositorRef *graphics.Compositor

	// Upstream key bridge — applies chroma/luma keys in YUV420 domain.
	keyBridge *graphics.KeyProcessorBridge

	// DVE compositor — applies DVE-transformed PIP/split-screen/quad layouts.
	dveCompositor *dve.Compositor

	// ST map registry — per-source lens correction applied in sourceDecoder.
	stmapRegistry *stmap.Registry

	// CPU color grading: the program grade runs in the colorgrade pipeline
	// node, per-source correction at ingest. Both stand aside when a GPU
	// pipeline or GPU source manager grades instead.
	programGrader ProgramGrader
	sourceGrader  atomic.Pointer[SourceGrader]
	// sourceGradeErrLogged holds the sources whose correction has failed,
	// so the failure is logged once per source, not once per frame.
	sourceGradeErrLogged sync.Map

	// Thumbnail cache — pipeline routing view JPEG thumbnails.
	// Created in New(), passed to each sourceDecoder and tapped in
	// videoProcessingLoop for program output capture.
	thumbnailCache       *ThumbnailCache
	lastSourceLostLog    time.Time // sync-loop goroutine only
	lastProgramThumbTime time.Time

	// Wipe map store — resolves gradient maps for gradient-map-based wipe transitions.
	wipeMapStore *wipemap.WipeMapStore

	// GPU pipeline runner — when set, videoProcessingLoop routes frames through
	// the GPU pipeline (upload → key → dve → compositor → stmap → raw sinks → encode)
	// instead of the CPU pipeline. Falls back to CPU on GPU errors.
	// Stored via atomic pointer for lock-free reads on the hot path.
	gpuRunner atomic.Pointer[gpuRunnerHolder]

	// GPU source manager — when set, handleRawVideoFrame routes YUV through
	// GPU upload + ST map + cache instead of CPU fill paths.
	gpuSourceMgr atomic.Pointer[gpuSourceMgrHolder]

	// liveTaps keeps the latest picture of sources playout airs as live
	// items when no GPU source manager is set.
	liveTaps liveTaps

	// gpuSourceActive — when true, sourceDecoder skips CPU ST map correction
	// (the GPU source manager handles it on GPU instead).
	gpuSourceActive atomic.Bool

	// onFormatChange — called after SetPipelineFormat rebuilds the CPU pipeline.
	// Used by the app layer to tear down and recreate the GPU pipeline at the
	// new dimensions. Protected by s.configMu (set during init, read during format change).
	onFormatChange func(width, height, fpsNum, fpsDen int)

	// Per-source decoder factory — when set, RegisterSource creates a
	// sourceDecoder for each source that decodes H.264 to YUV at ingest time.
	// This eliminates keyframe wait on cuts/transitions (always-decode mode).
	sourceDecoderFactory transition.DecoderFactory

	// Global pipeline format (resolution + frame rate). Atomic pointer for
	// lock-free reads on the hot path (frame budget check, encoder FPS).
	pipelineFormat atomic.Pointer[PipelineFormat]

	// hotState bundles per-frame-hot fields for lock-free snapshot reads.
	// Publication pattern: load, copy, mutate copy, Store. See
	// docs/concepts/locking-and-concurrency.md.
	hotState atomic.Pointer[hotState]

	// Grouped handler bundles — each is a read-together/write-together
	// subset of legacy s.mu-guarded fields, published atomically via CoW.
	// Initialized non-nil in New() so Load() never returns nil.
	graphicsHandlers atomic.Pointer[graphicsHandlers]
	audioHandlers    atomic.Pointer[audioHandlers]

	// sourcesAtomic is the primary sources map, published via atomic
	// CoW in withSourcesMut. All readers call *sourcesAtomic.Load() — no
	// lock. Writers hold configMu to serialize publication only.
	// Never nil after New().
	sourcesAtomic atomic.Pointer[map[string]*sourceState]

	// ftbDurationMsAtomic / transitionAbortThresholdAtomic — scalar mirrors
	// of the corresponding s.mu-guarded fields. Float64 packed as uint64
	// bit-pattern via math.Float64bits. Phase 1 mirror only.
	ftbDurationMsAtomic            atomic.Int64
	transitionAbortThresholdAtomic atomic.Uint64

	// pipeCodecsAtomic — atomic mirror of s.pipeCodecs pointer for lock-free
	// hot-path reads in broadcastProcessed* and the encode node.
	pipeCodecsAtomic atomic.Pointer[pipelineCodecs]

	// Pipeline codec pool — shared decoder/encoder for the video processing chain.
	// Used when any YUV processor (compositor, key bridge) is active or when
	// the transition engine outputs raw YUV.
	pipeCodecs *pipelineCodecs

	// Pre-allocated YUV buffer pool — replaces sync.Pool for deterministic
	// buffer lifecycle. Sized at pipeline format resolution. Atomic pointer:
	// SetPipelineFormat reassigns it under configMu while hot-path readers
	// (broadcastProcessed, normalizeResolution) Load() it without a lock,
	// matching the pipelineFormat/pipeCodecsAtomic pattern.
	framePool atomic.Pointer[FramePool]

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

	// Command queue for PTP-timed command execution (multi-region redundancy).
	cmdQueue          *cmdqueue.Queue
	cmdDispatcher     *cmdqueue.Dispatcher
	onCommandExecuted func()                 // called after each successful command dispatch (state broadcast)
	lastCommandSeq    atomic.Uint64          // highest enqueued seq (set at enqueue time via TrackCommandSeq)
	lastExecutedSeq   atomic.Uint64          // highest seq actually executed in the drain loop
	lastExecutedErr   atomic.Pointer[string] // error from the last executed command, nil on success
	recentCmdErrors   *recentErrors          // ring buffer of recent per-sequence command errors

	// lastCommandSeq persistence primitives. Writes-through to disk so a
	// process restart can re-seed lastCommandSeq and avoid the peer reading
	// the fresh engine as regressed-to-0 when the peer last saw a higher seq.
	// SetSeqPersistFn installs the callback + starts seqPersistLoop (which captures fn by parameter,
	// not by field read). The three fields here are atomic-only / channel-
	// only so TrackCommandSeq stays lock-free on the hot path; configMu
	// gates setup + teardown transitions.
	seqDirty       atomic.Bool   // set by TrackCommandSeq advances, cleared by the persist loop
	seqPersistStop chan struct{} // closed by Close() to stop the persist loop
	seqPersistDone chan struct{} // closed by the persist loop on exit, confirms final flush

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
	// PTS delta diagnostic: detect jitter at program broadcast stage.

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
	routeFiltered atomic.Int64 // frames filtered (non-program, FTB, etc.)

	// GPU pipeline counters (atomic, lock-free).
	gpuFramesProcessed  atomic.Int64 // frames processed via GPU pipeline
	gpuCacheMisses      atomic.Int64 // RunFromCache failed (fallback to RunWithUpload)
	gpuTransitionFrames atomic.Int64 // frames processed via GPU transition
	gpuFallbackFrames   atomic.Int64 // frames that fell back to CPU pipeline

	// frameInterval observes the gap between program frames; nil without
	// metrics. A pointer of its own because the broadcast path holds no lock.
	frameInterval atomic.Pointer[prometheus.Histogram]

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

	// Program epoch — monotonically increasing counter incremented on every
	// program source change (Cut, transition complete). Frames stamped with
	// a stale epoch are discarded in videoProcessingLoop to prevent wrong-source
	// frames from reaching the pipeline during concurrent Cut/transition races.

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

	// onCaptionBroadcast is called after captions are broadcast to the program
	// relay, allowing additional sinks (e.g., program-preview relay) to receive
	// the same caption data. Set by the app layer via SetOnCaptionBroadcast.
	onCaptionBroadcast atomic.Pointer[func(*ccx.CaptionFrame)]

	// onKeyframeBroadcast is called when an IDR keyframe is broadcast to the
	// program relay, allowing the app layer to track video start for A/V sync
	// gating. Set by the app layer via SetOnKeyframeBroadcast.
	onKeyframeBroadcast atomic.Pointer[func()]

	// Caption SEI injection buffers removed — broadcastWithCaptions is now
	// called from the async encodeLoop goroutine, so shared mutable buffers
	// would race during pipeline swap. Per-frame allocation is negligible.

	// pic_timing SEI configuration — derived from encoder SPS VUI/HRD parameters.
	// Set via SetPicTimingConfig(). The frame counter increments per encoded frame
	// and drives the cpb_removal_delay field in pic_timing SEI.
	picTimingConfig atomic.Pointer[caption.PicTimingConfig]
	picTimingFrame  atomic.Int64

	// Codec info — which encoder/decoder were selected at startup.
	// Set via SetCodecInfo(), read-only after initialization.
	codecEncoder              string
	codecDecoder              string
	codecHWAccel              bool
	availableEncoders         []codec.EncoderInfo
	availableEncodersInternal []internal.EncoderInfo // cached conversion for state broadcast

	// Encoder options — user-configurable output encoding parameters (bitrate,
	// GOP, preset, timecode). Stored for use by SetEncoder() when rebuilding
	// the encoder factory at runtime. Set via SetEncoderOpts().
	encoderOpts *codec.EncoderOptions

	// The reference clock drives every program frame and audio period
	// (sync_loop.go).
	refClock         *clock.ReferenceClock
	clockStopped     bool // set by stopClock under configMu; a replaced clock is not restarted
	aacBuffer        *clock.AACFrameBuffer
	syncMixer        syncMixerInterface
	syncLoopWg       sync.WaitGroup
	ticksBehind      int
	audioStamped     bool                            // sync-loop goroutine only
	nextAudioStart   uint64                          // sync-loop goroutine only
	syncLoopRunning  atomic.Bool                     // double-start guard (Issue 10)
	cachedBlackFrame atomic.Pointer[ProcessingFrame] // cached broadcast black (Issue 11)

	// stall feeds the pipeline while the program source delivers nothing.
	// programRepeats counts ticks that showed a frame again because the
	// program source had nothing new, or black after a loss.
	programRepeats atomic.Int64
	// lastEnqueueNano is when a frame other than a fill was last queued for
	// the pipeline: it arrived then, however long the pipeline takes.
	lastEnqueueNano atomic.Int64
	// transitionBlendBuf is the persistent dst for engine.BlendRawInto on
	// the sync-loop transition path. Grown on first tick and reused every
	// tick thereafter — kills the ~3 MB/frame result allocation that
	// BlendRaw's older copy-out contract forced.
	transitionBlendBuf  []byte
	overloadDrops       atomic.Int64
	syncLoopRepeats     atomic.Int64
	lastEncodedFrame    atomic.Pointer[ProcessingFrame]
	lastBroadcastVFrame atomic.Pointer[media.VideoFrame] // last broadcast H.264 frame (for overload repeat)
	lastOverloadLog     time.Time                        // rate-limit overload logging to 1/sec
	lastGPUErrorLog     time.Time                        // rate-limit GPU error logging to 1/sec

	// Sync loop audio output callbacks (used in ref clock mode).
	// outputAudioCallback delivers AAC frames to the program relay (browser).
	// outputAudioCallbackDirect delivers AAC frames to the muxer (SRT/recording).
	outputAudioCallback       atomic.Pointer[func(*media.AudioFrame)]
	outputAudioCallbackDirect atomic.Pointer[func(*media.AudioFrame)]
}

// Compile-time check that Switcher implements the frameHandler interface.
var _ frameHandler = (*Switcher)(nil)

// New creates a Switcher that forwards program frames to programRelay.
func New(programRelay *distribution.Relay) *Switcher {
	defaultFmt := DefaultFormat
	s := &Switcher{
		log:                      slog.With("component", "switcher"),
		programRelay:             programRelay,
		health:                   newHealthMonitor(),
		frameSync:                NewFrameSynchronizer(),
		thumbnailCache:           NewThumbnailCache(),
		ftbDurationMs:            defaultFTBDurationMs,
		transitionAbortThreshold: defaultTransitionAbortThreshold,
	}
	s.framePool.Store(NewFramePool(512, defaultFmt.Width, defaultFmt.Height, defaultFmt.Color.ColorFormat))
	s.frameBudgetNs.Store(defaultFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&defaultFmt)
	s.recentCmdErrors = newRecentErrors(8)
	s.delayBuffer = NewDelayBuffer(s)
	s.initAtomicSlots()
	s.registerBuiltinSourcesLocked()
	s.startBuiltinProducer()
	return s
}

// SetMixer attaches an audio mixer to the switcher for state broadcasts.
// When set, buildStateLocked will include audio channel states, master level,
// and program peak levels in the ControlRoomState. If the mixer also implements
// audioCutHandler, crossfade and AFV program changes are triggered automatically
// on Cut().
func (s *Switcher) SetMixer(m audioStateProvider) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.mixer = m
	if handler, ok := m.(audioCutHandler); ok {
		s.audioCut = handler
	}
	if handler, ok := m.(audioTransitionHandler); ok {
		s.audioTransition = handler
		s.updateAudioHandlers(func(ah *audioHandlers) { ah.audioTransition = handler })
	}
}

// ThumbnailCache returns the switcher's thumbnail cache for pipeline routing
// view JPEG captures. Never nil — created in New().
func (s *Switcher) ThumbnailCache() *ThumbnailCache {
	return s.thumbnailCache
}

// SetMetrics attaches Prometheus metrics to the switcher for production
// observability. When set, the switcher increments counters for cuts,
// transitions, and source health transitions alongside the existing atomic
// debug counters.
func (s *Switcher) SetMetrics(m *metrics.Metrics) {
	s.configMu.Lock()
	s.promMetrics = m
	s.configMu.Unlock()

	if m == nil {
		s.frameInterval.Store(nil)
		s.health.setHooks(nil, nil)
		return
	}
	s.frameInterval.Store(&m.ProgramFrameInterval)
	// A removed source's series go with it: SRT listener inputs come and go
	// with every stream id a publisher sends, and would otherwise pile up.
	s.health.setHooks(func(source string, from, to SourceHealthStatus) {
		m.SourceStatusChangesTotal.WithLabelValues(metrics.SourceLabel(source), string(from), string(to)).Inc()
	}, func(source string) {
		m.SourceStatusChangesTotal.DeletePartialMatch(prometheus.Labels{"source": metrics.SourceLabel(source)})
	})
}

// SetCodecInfo records which encoder/decoder were selected at startup and
// whether hardware acceleration is active. Called once during init after
// codec.ProbeEncoders(). Values are exposed in DebugSnapshot() under "codec".
func (s *Switcher) SetCodecInfo(encoder, decoder string, hwAccel bool) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.codecEncoder = encoder
	s.codecDecoder = decoder
	s.codecHWAccel = hwAccel
}

// SetAvailableEncoders stores the list of encoders that are available on this
// system. Called once at startup from the codec probe results. Also
// pre-computes the internal.EncoderInfo conversion to avoid per-broadcast
// allocations in state enrichment.
func (s *Switcher) SetAvailableEncoders(encoders []codec.EncoderInfo) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
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
	s.configMu.Lock()
	defer s.configMu.Unlock()
	out := make([]codec.EncoderInfo, len(s.availableEncoders))
	copy(out, s.availableEncoders)
	return out
}

// EncoderName returns the name of the currently active encoder.
func (s *Switcher) EncoderName() string {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.codecEncoder
}

// AvailableEncodersInternal returns the pre-computed internal.EncoderInfo
// slice for state broadcast enrichment. The returned slice must not be modified.
func (s *Switcher) AvailableEncodersInternal() []internal.EncoderInfo {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.availableEncodersInternal
}

// SetEncoder switches the video encoder at runtime. The name must match one
// of the entries in AvailableEncoders. Returns ErrFormatDuringTransition if
// a transition is in progress. The new encoder takes effect on the next
// encoded frame (the current encoder is invalidated).
func (s *Switcher) SetEncoder(name string) error {
	s.configMu.Lock()

	if s.state.isInTransition() {
		s.configMu.Unlock()
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
		s.configMu.Unlock()
		return fmt.Errorf("switcher: encoder %q: %w", name, ErrEncoderNotAvailable)
	}

	// Build factory for the requested encoder.
	// Capture encoder opts so user preferences (bitrate/GOP/preset) are
	// preserved across runtime encoder switches.
	encOpts := s.encoderOpts
	var factory transition.EncoderFactory
	if name == "openh264" {
		factory = func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return codec.NewOpenH264Encoder(w, h, bitrate, fpsNum, fpsDen)
		}
	} else {
		codecName := name
		factory = func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			gopSecs := transition.DefaultGOPSecs
			if encOpts != nil && encOpts.GOPSecs > 0 {
				gopSecs = encOpts.GOPSecs
			}
			return codec.NewFFmpegEncoder(codecName, w, h, bitrate, fpsNum, fpsDen,
				gopSecs, codec.HWDeviceCtx(), encOpts)
		}
	}

	// Update codec info.
	oldEncoder := s.codecEncoder
	s.codecEncoder = name
	// HW accel is true for any non-software encoder.
	s.codecHWAccel = name != "libx264" && name != "openh264"

	// Capture pipeCodecs ref before releasing lock.
	pc := s.pipeCodecs
	s.configMu.Unlock()

	// Swap factory outside s.configMu to avoid holding both s.configMu and pipeCodecs.mu.
	if pc != nil {
		pc.SetEncoderFactory(factory)
	}

	// Force the new encoder's first frame to be an IDR keyframe so
	// downstream decoders can sync immediately after the switch.
	s.forceNextIDR.Store(true)

	s.log.Info("encoder changed", "from", oldEncoder, "to", name)

	// Broadcast updated state to all connected clients.
	s.seq.Add(1)
	s.configMu.Lock()
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()
	s.notifyStateChange(snapshot)

	return nil
}

// SetEncoderOpts stores the user-configurable encoder options. These are used
// by SetEncoder() when rebuilding the encoder factory at runtime, so the
// user's bitrate/GOP/preset preferences are preserved across encoder switches.
func (s *Switcher) SetEncoderOpts(opts *codec.EncoderOptions) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.encoderOpts = opts
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

// Close stops the health monitor, delay buffer, frame sync, and unregisters
// all sources. Idempotent — gated by sync.Once so a second call (e.g. from
// `t.Cleanup` plus an existing `defer sw.Close()`) does not panic on
// double-close. Concurrent callers see serialized behavior:
// one runs the body, the rest wait and observe a fully-closed switcher.
func (s *Switcher) Close() {
	s.closeOnce.Do(func() {
		s.stopBuiltinProducer()
		s.health.stop()
		s.delayBuffer.Close()

		// Stopping the reference clock closes its Ticks channel, which ends
		// the sync loop, the only goroutine running the pipeline.
		s.stopClock()

		// Release pre-allocated frame pool buffers. Safe once the sync loop
		// has exited: no more pipeline work (no concurrent Acquire calls).
		if fp := s.framePool.Load(); fp != nil {
			fp.Close()
		}
		// Swap pipeline to nil and synchronously drain + close.
		if p := s.pipeline.Swap(nil); p != nil {
			p.Wait()
			_ = p.Close()
		}
		// Wait for any background drain goroutines from previous swaps
		// before closing pipeCodecs (prevents use-after-close on encoder).
		s.drainWg.Wait()
		s.configMu.Lock()
		pipeCodecs := s.pipeCodecs
		s.configMu.Unlock()
		if pipeCodecs != nil {
			pipeCodecs.close()
		}
		sources := *s.sourcesAtomic.Load()
		keys := make([]string, 0, len(sources))
		for k := range sources {
			keys = append(keys, k)
		}
		for _, k := range keys {
			s.UnregisterSource(k)
		}

		// Stop the seq-persist loop after source teardown so a final flush
		// captures any TrackCommandSeq advance that fired from an in-flight
		// drain during the rest of Close().
		s.configMu.Lock()
		stop := s.seqPersistStop
		done := s.seqPersistDone
		s.seqPersistStop = nil
		s.seqPersistDone = nil
		s.configMu.Unlock()
		if stop != nil {
			close(stop)
			<-done
		}
	})
}

// OnStateChange registers a callback invoked whenever the switcher state
// changes. Multiple callbacks may be registered; they are called in order.
// Callbacks are called outside the lock so they may safely perform slow
// operations (JSON marshal, network I/O).
func (s *Switcher) OnStateChange(cb func(internal.ControlRoomState)) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.stateCallbacks = append(s.stateCallbacks, cb)
}

// SetAudioHandler registers a handler that receives audio frames from ALL
// sources. When set, the handler (typically an audio mixer) is responsible
// for deciding which audio reaches the program output. When no handler is
// set, the original behavior (only program source audio forwarded) is used.
func (s *Switcher) SetAudioHandler(handler func(sourceKey string, frame *media.AudioFrame)) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.audioHandler = handler
	s.updateAudioHandlers(func(ah *audioHandlers) { ah.audioHandler = handler })
}

// SetTransitionConfig stores the transition codec configuration under lock.
func (s *Switcher) SetTransitionConfig(config TransitionConfig) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.transConfig = &config
}

// SetAudioTransition attaches an audio transition handler for dissolve sync.
func (s *Switcher) SetAudioTransition(handler audioTransitionHandler) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.audioTransition = handler
	s.updateAudioHandlers(func(ah *audioHandlers) { ah.audioTransition = handler })
}

// RequestKeyframe forces the next encoded frame to be an IDR keyframe.
// Called when a new output viewer joins (e.g., SRT output starts) so the
// TSMuxer can initialize immediately without waiting for the next GOP boundary.
func (s *Switcher) RequestKeyframe() {
	s.forceNextIDR.Store(true)
}

// SetOnKeyframeBroadcast registers a callback that fires when an IDR keyframe
// is broadcast to the program relay. Used by the app layer to gate relay audio
// until the first video keyframe is available for browser A/V sync.
func (s *Switcher) SetOnKeyframeBroadcast(fn func()) {
	if fn != nil {
		s.onKeyframeBroadcast.Store(&fn)
	} else {
		s.onKeyframeBroadcast.Store(nil)
	}
}

// SetCompositor attaches the DSK graphics compositor. The compositor's
// ProcessYUV method is called in the video processing pipeline when active.
func (s *Switcher) SetCompositor(c *graphics.Compositor) {
	s.configMu.Lock()
	s.compositorRef = c
	s.configMu.Unlock()
	s.rebuildPipeline()
}

// SetKeyBridge attaches the upstream key bridge for chroma/luma keying.
// The bridge's ProcessYUV method is called in the video processing pipeline.
func (s *Switcher) SetKeyBridge(kb *graphics.KeyProcessorBridge) {
	s.configMu.Lock()
	s.keyBridge = kb
	s.updateGraphicsHandlers(func(gh *graphicsHandlers) { gh.keyBridge = kb })
	s.configMu.Unlock()
	s.rebuildPipeline()
}

// SetDVECompositor sets the DVE compositor for DVE-transformed PIP layouts.
func (s *Switcher) SetDVECompositor(dc *dve.Compositor) {
	s.configMu.Lock()
	s.dveCompositor = dc
	s.updateGraphicsHandlers(func(gh *graphicsHandlers) { gh.dveCompositor = dc })
	s.configMu.Unlock()
	if dc != nil {
		dc.OnActiveChange = func() { s.rebuildPipeline() }
	}
	s.rebuildPipeline()
}

// SetSTMapRegistry sets the ST map registry for per-source correction.
// When set, each source decoder applies the assigned ST map warp after
// decode and resolution normalization, before fan-out to all consumers.
func (s *Switcher) SetSTMapRegistry(r *stmap.Registry) {
	s.configMu.Lock()
	s.stmapRegistry = r
	s.configMu.Unlock()
}

// SetColorGraders attaches the CPU color grade sources: the program grade
// (look and corrector) and per-source correction. Either may be nil.
func (s *Switcher) SetColorGraders(program ProgramGrader, sources SourceGrader) {
	s.configMu.Lock()
	s.programGrader = program
	s.configMu.Unlock()
	if sources != nil {
		s.sourceGrader.Store(&sources)
	} else {
		s.sourceGrader.Store(nil)
	}
	s.rebuildPipeline()
}

// SetWipeMapStore sets the wipe map store used to resolve gradient maps for
// gradient-map-based wipe transitions.
func (s *Switcher) SetWipeMapStore(store *wipemap.WipeMapStore) {
	s.configMu.Lock()
	s.wipeMapStore = store
	s.configMu.Unlock()
}

// unavailableTransitions lists the transition types this engine refuses,
// with the reasons: the ones the CPU does not draw while no GPU pipeline
// is active, none otherwise.
func (s *Switcher) unavailableTransitions() map[string]string {
	if s.gpuRunner.Load() != nil {
		return nil
	}
	return dve.CPURefusedTransitions()
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
// (keyBridge.IngestFillYUV, dveCompositor.IngestSourceFrame). Also sets
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

// SetOnFormatChange registers a callback that fires after SetPipelineFormat
// rebuilds the CPU pipeline. Used by the app layer to tear down and recreate
// the GPU pipeline at the new dimensions.
func (s *Switcher) SetOnFormatChange(fn func(width, height, fpsNum, fpsDen int)) {
	s.configMu.Lock()
	s.onFormatChange = fn
	s.configMu.Unlock()
}

// SetSourceDecoderFactory enables always-decode mode. When set, RegisterSource
// creates a per-source decoder that decodes H.264 to raw YUV at ingest time,
// eliminating keyframe waits on cuts and transitions. Must be called before
// any sources are registered.
func (s *Switcher) SetSourceDecoderFactory(factory transition.DecoderFactory) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.sourceDecoderFactory = factory
	s.log.Info("always-decode mode enabled")
}

// SetPipelineCodecs creates the shared pipeline encoder for the video
// processing chain. Called from app.go during initialization.
// The pipelineCodec parameter determines wire format (AVC vs HEVC) and
// parameter set extraction (SPS/PPS vs VPS/SPS/PPS).
func (s *Switcher) SetPipelineCodecs(encoderFactory transition.EncoderFactory, pipelineCodec ...PipelineCodec) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	pc := CodecAVC
	if len(pipelineCodec) > 0 {
		pc = pipelineCodec[0]
	}
	s.pipeCodecs = &pipelineCodecs{
		encoderFactory: encoderFactory,
		pipelineCodec:  pc,
		formatRef:      &s.pipelineFormat,
		encoderOpts:    s.encoderOpts,
	}
	s.pipeCodecsAtomic.Store(s.pipeCodecs)
}

// SetPipelineVideoInfoCallback sets the callback invoked when the pipeline
// encoder produces a keyframe with new VPS/SPS/PPS parameters.
// For AVC pipelines, vps is always nil.
func (s *Switcher) SetPipelineVideoInfoCallback(cb func(vps, sps, pps []byte, width, height int)) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.pipeCodecs != nil {
		s.pipeCodecs.onVideoInfoChange = cb
	}
}

// buildNodeList constructs the ordered list of CPU pipeline nodes.
// Must be called with s.configMu held since it reads s.keyBridge,
// s.compositorRef, s.pipeCodecs, and s.promMetrics.
//
// When a GPU pipeline is active (via SetGPUPipeline), the CPU pipeline is
// only used as fallback. The GPU pipeline runs independently via RunWithUpload.
//
// CPU pipeline order:
//
//	[upstream-key -> dve-compositor -> compositor -> colorgrade -> stmap-program -> raw-sink-mxl -> raw-sink-preview -> h264-encode]
func (s *Switcher) buildNodeList() []PipelineNode {
	return []PipelineNode{
		&upstreamKeyNode{bridge: s.keyBridge},
		&dveCompositorNode{compositor: s.dveCompositor},
		&compositorNode{compositor: s.compositorRef},
		&colorGradeNode{grader: s.programGrader},
		&stmapProgramNode{registry: s.stmapRegistry},
		&rawSinkNode{sink: &s.rawVideoSink, name: "raw-sink-mxl"},
		&rawSinkNode{sink: &s.rawPreviewSink, name: "raw-sink-preview"},
		newAsyncEncodeNode(s),
	}
}

// newAsyncEncodeNode creates an encodeNode with its async goroutine started.
// Must be called with s.configMu held (reads pipeCodecs, promMetrics, etc.).
func newAsyncEncodeNode(s *Switcher) *encodeNode {
	// Determine if the encoder is 10-bit capable (professional pipeline with
	// HEVC codec and YUV422P10LE color format). When true, 10-bit frames
	// bypass downconversion and are passed directly to the encoder.
	is10bit := false
	if pf := s.pipelineFormat.Load(); pf != nil {
		is10bit = pf.Color.ColorFormat == ColorYUV422_10bit && pf.Color.Codec == CodecHEVC
	}
	enc := &encodeNode{
		codecs:          s.pipeCodecs,
		forceIDR:        &s.forceNextIDR,
		promMetrics:     s.promMetrics,
		encodeNilCount:  &s.pipeEncodeNil,
		encodeDropCount: &s.pipeEncodeDrop,
		onEncoded:       s.broadcastWithCaptions,
		encoder10bit:    is10bit,
	}
	enc.start()
	return enc
}

// BuildPipeline constructs and stores the video processing pipeline.
// Must be called after SetCompositor, SetKeyBridge, and SetPipelineCodecs.
// Safe to call multiple times — each call rebuilds from scratch.
func (s *Switcher) BuildPipeline() error {
	s.configMu.Lock()
	hasPipeCodecs := s.pipeCodecs != nil
	prom := s.promMetrics
	var nodes []PipelineNode
	if hasPipeCodecs {
		nodes = s.buildNodeList()
	}
	s.configMu.Unlock()

	if !hasPipeCodecs {
		return nil // no encoder configured yet
	}

	format := s.PipelineFormat()
	p := &Pipeline{}
	// Preserve bypass flags from the old pipeline across rebuild.
	if old := s.pipeline.Load(); old != nil {
		p.RestoreBypass(old.BypassState())
	}
	if err := p.Build(format, s.framePool.Load(), nodes); err != nil {
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

// runPipelineGuarded runs a frame through the current pipeline, closing the
// Load→inflight.Add window that swapPipeline's drain goroutine could slip
// through: with a bare `s.pipeline.Load()` followed by `p.Run(frame)`, a swap
// landing between the two lets `old.Wait()` observe a zero in-flight counter
// and Close all nodes (including cgo encoder nodes) while Run is about to
// process a frame through them. Here the in-flight registration happens
// FIRST, and the pipeline pointer is re-checked afterwards: if a swap won
// the race, the registration is dropped and the new pipeline is used.
// Returns (frame, false) when no pipeline is configured.
func (s *Switcher) runPipelineGuarded(frame *ProcessingFrame) (*ProcessingFrame, bool) {
	for {
		p := s.pipeline.Load()
		if p == nil {
			return frame, false
		}
		p.inflight.Add(1)
		if s.pipeline.Load() != p {
			// Lost the race with swapPipeline — its drain goroutine may
			// already be waiting to close this pipeline's nodes. Drop the
			// registration and retry on the new pipeline.
			p.inflight.Done()
			continue
		}
		out := p.Run(frame)
		p.inflight.Done()
		return out, true
	}
}

// rebuildPipeline builds a fresh Pipeline from current state and atomically
// swaps it in. Logs a warning and keeps the old pipeline if Build() fails.
// This is the runtime reconfiguration path — SetCompositor, SetKeyBridge,
// SetRawVideoSink, and external callbacks all use this.
func (s *Switcher) rebuildPipeline() {
	s.configMu.Lock()
	hasPipeCodecs := s.pipeCodecs != nil
	prom := s.promMetrics
	var nodes []PipelineNode
	if hasPipeCodecs {
		nodes = s.buildNodeList()
	}
	s.configMu.Unlock()

	if !hasPipeCodecs {
		return
	}

	format := s.PipelineFormat()
	p := &Pipeline{}
	// Preserve bypass flags from the old pipeline across rebuild.
	if old := s.pipeline.Load(); old != nil {
		p.RestoreBypass(old.BypassState())
	}
	if err := p.Build(format, s.framePool.Load(), nodes); err != nil {
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
	return s.framePool.Load()
}

// SetPipelineFormat changes the global pipeline format at runtime.
// Returns error if a transition is currently active.
// Propagates change to: frame budget, encoder, and the reference clock rate.
func (s *Switcher) SetPipelineFormat(f PipelineFormat) error {
	if err := s.setPipelineFormat(f); err != nil {
		return err
	}
	s.restartClockAt(f.FPSNum, f.FPSDen)
	return nil
}

func (s *Switcher) setPipelineFormat(f PipelineFormat) error {
	s.configMu.Lock()

	if s.state.isInTransition() {
		s.configMu.Unlock()
		return ErrFormatDuringTransition
	}

	s.pipelineFormat.Store(&f)
	s.frameBudgetNs.Store(f.FrameBudgetNs())
	s.invalidateBuiltinSources()

	// Recreate frame pool at new dimensions. Old pool drains naturally —
	// Release() discards wrong-sized buffers via cap check. Store under
	// configMu; hot-path readers Load() it without a lock.
	newPool := NewFramePool(512, f.Width, f.Height, f.Color.ColorFormat)
	s.framePool.Store(newPool)

	// Force encoder recreation on next frame.
	if s.pipeCodecs != nil {
		s.pipeCodecs.invalidateEncoder()
	}

	// Build new pipeline with new pool + new format, swap atomically.
	// Capture node list and metrics under lock to avoid race on s.compositorRef etc.
	hasPipeCodecs := s.pipeCodecs != nil
	prom := s.promMetrics
	formatChangeFn := s.onFormatChange
	var nodes []PipelineNode
	if hasPipeCodecs {
		nodes = s.buildNodeList()
	}
	s.configMu.Unlock()

	if hasPipeCodecs {
		p := &Pipeline{}
		if err := p.Build(f, newPool, nodes); err != nil {
			s.log.Warn("pipeline rebuild failed on format change", "error", err)
		} else {
			p.SetMetrics(prom)
			p.epoch = s.pipelineEpoch.Add(1)
			s.swapPipeline(p)
		}
	}

	// Notify GPU pipeline of format change (tear down + rebuild at new dimensions).
	if formatChangeFn != nil {
		formatChangeFn(f.Width, f.Height, f.FPSNum, f.FPSDen)
	}

	s.log.Info("pipeline format changed",
		"name", f.Name,
		"width", f.Width,
		"height", f.Height,
		"fps", fmt.Sprintf("%d/%d", f.FPSNum, f.FPSDen))

	s.seq.Add(1)
	s.configMu.Lock()
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

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
	s.configMu.Lock()
	defer s.configMu.Unlock()
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
	if h := s.frameInterval.Load(); h != nil {
		(*h).Observe(float64(gap) / 1e9)
	}
	atomicutil.UpdateMax(&s.maxBroadcastIntervalNano, gap)
	// Log when gap exceeds 100ms (>2 frame times at 24fps) to pinpoint stalls
	if gap > 100_000_000 { // 100ms in nanoseconds
		s.configMu.Lock()
		programSrc := s.programSource
		hasEngine := s.transEngine != nil
		state := s.state
		s.configMu.Unlock()
		s.log.Warn("program broadcast gap",
			"gap_ms", float64(gap)/1e6,
			"state", state.String(),
			"program_source", programSrc,
			"has_engine", hasEngine,
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

// broadcastToProgram sends a video frame to the program relay with a
// monotonically increasing GroupID, keeping its PTS: the tick's.
func (s *Switcher) broadcastToProgram(frame *media.VideoFrame) {
	f := *frame // shallow struct copy — avoids mutating shared frame
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

	// Store last broadcast frame for overload repeat (Issue 1).
	// This ensures repeatLastVideoFrame replays encoded H.264 data,
	// not raw YUV bytes.
	stored := f // copy for atomic store
	s.lastBroadcastVFrame.Store(&stored)
}

// broadcastOwnedToProgram sends an owned frame (safe to mutate) to the
// program relay with a monotonically increasing GroupID, keeping its PTS.
func (s *Switcher) broadcastOwnedToProgram(frame *media.VideoFrame) {
	frame.DTS = frame.PTS
	if frame.IsKeyframe {
		frame.GroupID = s.programGroupID.Add(1)
		// Notify app layer that a keyframe has been broadcast (used to
		// gate relay audio until video is decodable for A/V sync).
		if cb := s.onKeyframeBroadcast.Load(); cb != nil {
			(*cb)()
		}
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
	// Kept for repeats on a tick with no new frame or under overload.
	stored := *frame
	s.lastBroadcastVFrame.Store(&stored)
}

// ProgramRelay returns the program relay for external broadcast (e.g. authored captions).
func (s *Switcher) ProgramRelay() *distribution.Relay {
	return s.programRelay
}

// SetCaptionManager attaches a caption manager for CEA-608/708 SEI injection.
func (s *Switcher) SetCaptionManager(cm captionManager) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.captionMgr = cm
}

// SetPicTimingConfig configures pic_timing SEI injection parameters.
// When set (non-nil), BuildPicTimingAndCaptionSEI is used instead of BuildSEINALU,
// producing a combined SEI NALU with pic_timing before caption data for broadcast
// decoder compatibility (Evertz, Harmonic, etc.).
func (s *Switcher) SetPicTimingConfig(cfg *caption.PicTimingConfig) {
	s.picTimingConfig.Store(cfg)
}

// PicTimingConfig returns the current pic_timing SEI configuration, or nil if
// pic_timing injection is not enabled. Used by the GPU pipeline encode callback
// which maintains its own frame counter.
func (s *Switcher) PicTimingConfig() *caption.PicTimingConfig {
	return s.picTimingConfig.Load()
}

// SetOnCaptionBroadcast registers a callback that fires whenever passthrough
// captions are broadcast to the program relay. Used by the app layer to
// mirror captions to the program-preview relay.
func (s *Switcher) SetOnCaptionBroadcast(fn func(*ccx.CaptionFrame)) {
	s.onCaptionBroadcast.Store(&fn)
}

// SetCommandQueue wires a PTP-timed command queue and its dispatcher into the
// switcher. Once set, the video processing loop drains ready commands on every
// frame tick, giving frame-level execution accuracy (~33ms at 30fps).
func (s *Switcher) SetCommandQueue(q *cmdqueue.Queue, d *cmdqueue.Dispatcher) {
	s.cmdQueue = q
	s.cmdDispatcher = d
}

// SetOnCommandExecuted registers a callback that fires after each command
// dispatch attempt in drainCommandQueue (including failures, since error state
// changes are also relevant to browsers). Used by the app layer to trigger
// state broadcasts so browsers see state changes from timed commands.
func (s *Switcher) SetOnCommandExecuted(fn func()) {
	s.onCommandExecuted = fn
}

// drainCommandQueue pops all ready commands from the PTP-timed queue and
// dispatches them. Tracks lastExecutedSeq, lastExecutedErr, and
// recentCmdErrors for sync health reporting. Fires onCommandExecuted after
// each command dispatch so the app layer can broadcast updated state.
func (s *Switcher) drainCommandQueue() {
	if s.cmdQueue == nil {
		return
	}
	for _, cmd := range s.cmdQueue.Drain() {
		var dispatchErr error
		if s.cmdDispatcher != nil {
			dispatchErr = s.cmdDispatcher.Dispatch(cmd)
			if dispatchErr != nil {
				s.log.Warn("queued command dispatch failed",
					"action", cmd.Action,
					"seq", cmd.Seq,
					"err", dispatchErr)
			}
		}
		if cmd.Seq > 0 {
			s.lastExecutedSeq.Store(cmd.Seq)
			if dispatchErr != nil {
				errStr := dispatchErr.Error()
				s.lastExecutedErr.Store(&errStr)
				s.recentCmdErrors.record(cmd.Seq, errStr)
			} else {
				s.lastExecutedErr.Store(nil)
			}
		}
		if s.onCommandExecuted != nil {
			s.onCommandExecuted()
		}
	}
}

// TrackCommandSeq updates lastCommandSeq to the given value if it's higher
// than the current value. Uses CAS loop for lock-free thread safety. Marks
// seqDirty so the debounced persist loop flushes the new high-water mark
// to disk on its next tick — see seqPersistLoop.
func (s *Switcher) TrackCommandSeq(seq uint64) {
	for {
		old := s.lastCommandSeq.Load()
		if seq <= old {
			return
		}
		if s.lastCommandSeq.CompareAndSwap(old, seq) {
			s.seqDirty.Store(true)
			return
		}
	}
}

// SetInitialCommandSeq seeds lastCommandSeq from the persisted on-disk
// value before the HTTP listener and peer poller start. Only advances the
// tracker — never regresses — so a later wire-up that reads a stale file
// cannot undo a higher in-memory value. Called from app init.
func (s *Switcher) SetInitialCommandSeq(seq uint64) {
	s.TrackCommandSeq(seq)
	// Clear the dirty flag that TrackCommandSeq sets: the value we just
	// seeded came FROM disk, so there's nothing to write back yet.
	s.seqDirty.Store(false)
}

// SetSeqPersistFn installs a callback invoked by the debounced persist
// loop whenever lastCommandSeq advances. Must be called before the first
// TrackCommandSeq advance that should survive a restart; setting fn starts
// the persist goroutine. Passing a nil fn disables persistence (used for
// tests + the single-engine fast path).
//
// Contract: do NOT call after Close. Close drops the persist loop; a
// post-Close call would start a new loop with no teardown path and leak a
// goroutine. In practice this is called exactly once at app init.
func (s *Switcher) SetSeqPersistFn(fn func(uint64) error) {
	s.configMu.Lock()
	// Stop any prior persist loop cleanly so the contract "only one loop
	// at a time" holds if a test swaps fns.
	prevStop, prevDone := s.seqPersistStop, s.seqPersistDone
	if fn == nil {
		s.seqPersistStop = nil
		s.seqPersistDone = nil
		s.configMu.Unlock()
		if prevStop != nil {
			close(prevStop)
			<-prevDone
		}
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.seqPersistStop = stop
	s.seqPersistDone = done
	s.configMu.Unlock()
	if prevStop != nil {
		close(prevStop)
		<-prevDone
	}
	go s.seqPersistLoop(fn, stop, done)
}

// seqPersistLoop debounces disk writes of lastCommandSeq. The loop flushes
// the current atomic value every 100 ms if seqDirty is set — bursty CUT
// storms coalesce to ≤10 writes/sec regardless of API command rate. A
// final flush runs on stop so a clean shutdown does not lose the last
// advance.
func (s *Switcher) seqPersistLoop(fn func(uint64) error, stop, done chan struct{}) {
	defer close(done)
	const interval = 100 * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastWritten uint64
	flush := func() {
		if !s.seqDirty.CompareAndSwap(true, false) {
			return
		}
		cur := s.lastCommandSeq.Load()
		if cur <= lastWritten {
			return
		}
		if err := fn(cur); err != nil {
			s.log.Warn("persist lastCommandSeq failed; will retry", "err", err, "seq", cur)
			s.seqDirty.Store(true)
			return
		}
		lastWritten = cur
	}
	for {
		select {
		case <-stop:
			flush()
			return
		case <-ticker.C:
			flush()
		}
	}
}

// ForceNextIDRPtr returns a pointer to the forceNextIDR atomic for GPU encode.
func (s *Switcher) ForceNextIDRPtr() *atomic.Bool { return &s.forceNextIDR }

// BroadcastWithCaptionsFunc returns the caption-injecting broadcast callback
// for use by the CPU pipeline's encode node.
func (s *Switcher) BroadcastWithCaptionsFunc() func(*media.VideoFrame) {
	return s.broadcastWithCaptions
}

// BroadcastToProgramFunc returns the direct program broadcast callback
// (no caption injection). Used when the caller handles caption SEI
// injection externally (e.g., GPU pipeline with shared caption cache).
func (s *Switcher) BroadcastToProgramFunc() func(*media.VideoFrame) {
	return s.broadcastOwnedToProgram
}

// broadcastWithCaptions injects caption SEI NALUs into the encoded video frame
// before broadcasting to the program relay. This is the post-encode callback
// used by the pipeline's encodeNode when captions are enabled.
func (s *Switcher) broadcastWithCaptions(frame *media.VideoFrame) {
	s.configMu.Lock()
	cm := s.captionMgr
	s.configMu.Unlock()

	if cm == nil {
		s.broadcastOwnedToProgram(frame)
		return
	}

	pairs := cm.ConsumeForFrameWithVANC()

	// Determine codec string for SEI framing (H.264 vs HEVC).
	codecStr := "h264"
	if s.pipeCodecs != nil && s.pipeCodecs.pipelineCodec == CodecHEVC {
		codecStr = "h265"
	}

	// Build SEI NALU. When pic_timing is configured, produce a combined NALU
	// with pic_timing (payload_type=1) before caption (payload_type=4) for
	// broadcast decoder compliance (Evertz, Harmonic, etc.).
	var seiNALU []byte
	if ptCfg := s.picTimingConfig.Load(); ptCfg != nil {
		frameNum := s.picTimingFrame.Add(1) - 1
		var tc *caption.Timecode
		if ptCfg.TimecodeEnabled {
			t := caption.UTCToTimecode(time.Now().UTC(), ptCfg.FPSNum, ptCfg.FPSDen)
			tc = &t
		}
		seiNALU = caption.BuildPicTimingAndCaptionSEI(pairs, *ptCfg, frameNum, tc, codecStr)
	} else {
		seiNALU = caption.BuildSEINALU(pairs, codecStr)
	}
	if len(seiNALU) == 0 {
		s.broadcastOwnedToProgram(frame)
		return
	}

	// Convert wire format → Annex B → insert SEI → convert back to wire format.
	// Uses codec-agnostic WireToAnnexBInto/AnnexBToWireInto (identical for AVC/HEVC).
	// Buffers are local (not reused across frames) because broadcastWithCaptions
	// is called from the async encodeLoop goroutine and would race during
	// pipeline swap if stored on the Switcher struct.
	annexB := codec.WireToAnnexBInto(frame.WireData, nil)
	inserted := caption.InsertSEIBeforeVCLInto(seiNALU, annexB, nil, codecStr)
	frame.WireData = codec.AnnexBToWireInto(inserted, nil)
	s.broadcastOwnedToProgram(frame)
}

// normalizeResolution scales a ProcessingFrame to the pipeline format
// resolution if it differs. Called in the video processing goroutine
// (not on the delivery hot path) to avoid blocking source frame delivery.
// Returns the same frame if no scaling needed, or a new scaled frame.
// Skips scaling when the frame pool can't accommodate the target resolution
// (e.g., test environments with small pools).
func (s *Switcher) normalizeResolution(pf *ProcessingFrame) *ProcessingFrame {
	if len(pf.Data) == 0 {
		return pf
	}
	fmt := s.pipelineFormat.Load()
	if fmt == nil || (pf.Width == fmt.Width && pf.Height == fmt.Height) {
		return pf
	}
	dstW, dstH := fmt.Width, fmt.Height
	colorFmt := fmt.Color.ColorFormat
	dstSize := colorFmt.FrameSize(dstW, dstH)
	// Load the pool once: the BufSize guard, Acquire, and the stored pool
	// pointer must all refer to the same FramePool even if SetPipelineFormat
	// swaps s.framePool concurrently.
	pool := s.framePool.Load()
	// Skip if the pool can't hold the target resolution (test safety).
	if pool.BufSize() < dstSize {
		return pf
	}
	buf := pool.Acquire()
	buf = buf[:dstSize]
	if colorFmt == ColorYUV422_10bit {
		if err := transition.ScaleYUV422P10(pf.Data, pf.Width, pf.Height, buf, dstW, dstH); err != nil {
			slog.Error("scale YUV422P10 failed", "err", err)
		}
	} else {
		transition.ScaleYUV420(pf.Data, pf.Width, pf.Height, buf, dstW, dstH)
	}
	pf.ReleaseYUV()
	scaled := &ProcessingFrame{
		Data:       buf,
		Width:      dstW,
		Height:     dstH,
		PTS:        pf.PTS,
		DTS:        pf.DTS,
		IsKeyframe: pf.IsKeyframe,
		Format:     pf.Format,
		Codec:      pf.Codec,
		GroupID:    pf.GroupID,
		pool:       pool,
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
	s.configMu.Lock()

	if s.transConfig == nil {
		s.configMu.Unlock()
		return errTransitionNotConfigured
	}
	if s.state.isInTransition() {
		s.configMu.Unlock()
		return fmt.Errorf("transition: %w", transition.ErrActive)
	}
	if s.state.isFTBActive() {
		s.configMu.Unlock()
		return fmt.Errorf("cannot start transition: %w", transition.ErrFTBActive)
	}
	if s.programSource == "" {
		s.configMu.Unlock()
		return errNoProgramSource
	}
	if sourceKey == "" {
		s.configMu.Unlock()
		return errNoTargetSource
	}
	if _, ok := (*s.sourcesAtomic.Load())[sourceKey]; !ok {
		s.configMu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}

	if s.programSource == sourceKey {
		s.configMu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrAlreadyOnProgram)
	}

	// Apply options
	var topts transitionOpts
	for _, opt := range opts {
		opt(&topts)
	}

	tt := transition.Type(transType)
	isDVE := dve.IsDVETransition(transType)
	if !isDVE && tt != transition.Mix && tt != transition.Dip && tt != transition.Wipe && tt != transition.Stinger {
		s.configMu.Unlock()
		return fmt.Errorf("unsupported transition type: %q", transType)
	}
	if reason, refused := dve.CPURefusal(transType); refused && s.gpuRunner.Load() == nil {
		s.configMu.Unlock()
		return fmt.Errorf("%w: %s", ErrTransitionNeedsGPU, reason)
	}
	// DVE transitions use mix as the underlying engine type — the DVE compositor
	// handles the visual effect based on position from the engine.
	engineType := tt
	if isDVE {
		engineType = transition.Mix
	}

	if tt == transition.Stinger && topts.stingerData == nil {
		s.configMu.Unlock()
		return errStingerDataRequired
	}

	// Validate wipe direction when type is wipe.
	// When a WipeConfig is provided, the gradient-map pattern replaces the
	// legacy direction — no WipeDirection validation needed.
	var wipeDir transition.WipeDirection
	if tt == transition.Wipe && topts.wipeConfig == nil {
		wipeDir = transition.WipeDirection(wipeDirection)
		if !transition.ValidWipeDirections[wipeDir] {
			s.configMu.Unlock()
			return fmt.Errorf("invalid wipe direction: %q", wipeDirection)
		}
	}

	fromSource := s.programSource

	// Capture pipeline dimensions for the transition engine. Use PipelineFormat
	// directly — pipeCodecs.dimensions() returns 0,0 when the GPU pipeline is
	// active because the CPU encoder is never created.
	pf := s.pipelineFormat.Load()
	hintW, hintH := pf.Width, pf.Height

	// Mark transition as starting to prevent concurrent StartTransition/FTB calls.
	// handleVideoFrame checks transEngine != nil to route frames, so setting
	// StateTransitioning without transEngine is safe — frames won't route to
	// the engine and normal frame processing continues for the current program source.
	s.transitionState(StateTransitioning)
	s.transStartGen++
	startGen := s.transStartGen
	s.configMu.Unlock()

	// Capture decoder factory — still needed for virtual/legacy sources
	// that may send H.264 via IngestFrame during transitions.
	decoderFactory := s.transConfig.DecoderFactory

	// Phase 2: Create engine and start it with NO lock held.
	// In always-decode mode, most sources provide raw YUV via IngestRawFrame.
	// The decoder factory is passed for backward compatibility with virtual
	// sources that may still send H.264 via handleVideoFrame → IngestFrame.

	// Check for cancellation before engine creation.
	if err := ctx.Err(); err != nil {
		s.rollbackTransitionState(StateTransitioning, startGen, StateIdle)
		return fmt.Errorf("start transition: %w", err)
	}

	// When GPU pipeline is active, set SkipBlend so the CPU engine only
	// tracks state (position, auto-complete) without performing the pixel
	// blend — the GPU handles blending directly in VRAM. Output is nil
	// because the GPU pipeline produces the encoded output.
	gpuActive := false
	if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
		gpuActive = true
	}

	var outputCb func(yuv []byte, width, height int, pts int64, isKeyframe bool)

	// A DVE transition runs on a mix engine. The GPU pipeline draws it
	// when there is one; otherwise the engine draws it on the CPU.
	var render transition.RenderFunc
	var framePeriod time.Duration
	if isDVE && !gpuActive {
		render = dve.CPUTransitionForType(transType)
		if pf.FPSNum > 0 && pf.FPSDen > 0 {
			framePeriod = time.Second * time.Duration(pf.FPSDen) / time.Duration(pf.FPSNum)
		}
	}

	engine := transition.NewEngine(transition.EngineConfig{
		DecoderFactory: decoderFactory,
		WipeDirection:  wipeDir,
		WipeConfig:     topts.wipeConfig,
		WipeMapStore:   s.wipeMapStore,
		Stinger:        topts.stingerData,
		Easing:         topts.easing,
		HintWidth:      hintW,
		HintHeight:     hintH,
		SkipBlend:      gpuActive,
		Render:         render,
		FramePeriod:    framePeriod,
		Output:         outputCb,
		OnComplete: func(aborted bool) {
			s.handleTransitionComplete(aborted)
		},
	})

	// Set 10-bit color format on the transition engine so it uses the correct
	// blend functions and frame sizing for the pipeline's color format.
	if pf.Color.ColorFormat == ColorYUV422_10bit {
		engine.SetColorFormat(video.YUV422_10bit)
	}

	// The sync loop advances the position one tick at a time.
	engine.StartSync(fromSource, sourceKey, engineType, durationMs)

	// Upload gradient map to GPU runner for gradient-map wipe transitions.
	// This must happen after engine.Start (which resolves the gradient map)
	// and before the first RunTransition call. The gradient map persists on
	// GPU until ClearWipeMap is called at transition completion.
	if gpuActive {
		if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
			wipeConfig := engine.WipeMapConfig()
			gradient := engine.WipeGradient()
			if wipeConfig != nil && gradient != nil {
				if err := h.runner.SetWipeMap(gradient, wipeConfig, hintW, hintH); err != nil {
					s.log.Warn("GPU wipe map upload failed, will use legacy directional wipe",
						"pattern", wipeConfig.PatternID, "err", err)
				}
			} else {
				h.runner.ClearWipeMap()
			}
		}
	}

	// Phase 3: Start audio crossfade BEFORE publishing the engine.
	// This eliminates a race where video frames see the engine and call
	// OnTransitionPosition before the mixer receives OnTransitionStart,
	// causing a gain discontinuity (audible pop).
	s.configMu.Lock()
	audioHandler := s.audioTransition
	s.configMu.Unlock()

	var audioToken uint64
	if audioHandler != nil {
		audioMode := audio.Crossfade
		if tt == transition.Dip {
			audioMode = audio.DipToSilence
		}
		audioToken = audioHandler.OnTransitionStart(fromSource, sourceKey, audioMode, durationMs)

		// Pass stinger audio to mixer for additive overlay
		if tt == transition.Stinger && topts.stingerData != nil && topts.stingerData.Audio != nil {
			audioHandler.SetStingerAudio(topts.stingerData.Audio, topts.stingerData.AudioSampleRate, topts.stingerData.AudioChannels)
		}
	}

	// Now publish the engine — audio crossfade is already active, so the
	// first OnTransitionPosition from a video frame will be handled.
	s.configMu.Lock()
	if s.state != StateTransitioning || s.transStartGen != startGen {
		// A concurrent Cut (or abort) tore this transition down during the
		// unlocked setup window — it observed StateTransitioning with a nil
		// engine and already returned the switcher to its post-cut state.
		// Publishing now would install a dead engine over the live hotState
		// (frames would route to it forever; its completion handler would
		// early-return on the state guard). Abandon the start instead.
		return s.abandonTransitionSetup(engine, audioHandler, audioToken, transType, "start transition")
	}
	s.transEngine = engine
	s.audioTransToken = audioToken
	s.transTypeOrig = transType // preserve original type string for GPU DVE dispatch
	s.transitionMaxForward = 0  // fresh transition: reset T-bar pull-back gate
	// Swap preview to old program at transition start (matches CUT's
	// program/preview swap semantic). This lets operators queue the next
	// cut during an in-flight AUTO; the completion handler below
	// deliberately does NOT reassert this value so their mid-transition
	// SetPreview is preserved.
	s.previewSource = fromSource
	s.updateHotState(func(hs *hotState) {
		hs.state = StateTransitioning
		hs.transEngine = engine
		hs.transTypeOrig = transType
		hs.transitionMaxForward = 0
		hs.previewSource = fromSource
	})
	s.transitionsStarted.Add(1)

	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	s.log.Info("transition started",
		"type", string(tt), "from", fromSource, "to", sourceKey, "duration_ms", durationMs)

	s.notifyStateChange(snapshot)
	return nil
}

// SetTransitionPosition sets the T-bar position during an active transition.
//
// If the operator has driven the T-bar forward past 2× transitionAbortThreshold
// and then pulls back below that threshold, the transition is internally
// aborted — the engine is torn down, program reverts to the from-source, and
// a fresh StartTransition is required to retry. The 2× max-forward gate
// prevents trivial jitter near zero from triggering unwanted aborts.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) SetTransitionPosition(ctx context.Context, position float64) error {
	s.configMu.Lock()
	engine := s.transEngine
	inTrans := s.state.isInTransition()
	audioHandler := s.audioTransition
	threshold := s.transitionAbortThreshold
	if position > s.transitionMaxForward {
		s.transitionMaxForward = position
		s.updateHotState(func(hs *hotState) { hs.transitionMaxForward = position })
	}
	maxForward := s.transitionMaxForward
	s.configMu.Unlock()

	if !inTrans || engine == nil {
		return ErrNoTransition
	}

	if position < threshold && maxForward >= 2*threshold {
		s.AbortTransition()
		return nil
	}

	engine.SetPosition(position)

	if audioHandler != nil {
		audioHandler.OnTransitionPosition(position)
	}
	return nil
}

// TransitionAbortThreshold returns the configured T-bar pull-back abort
// threshold in (0.05, 0.5].
func (s *Switcher) TransitionAbortThreshold() float64 {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.transitionAbortThreshold
}

// SetTransitionAbortThreshold updates the T-bar pull-back abort threshold.
// Range [0.05, 0.5]. The new value takes effect on the next
// SetTransitionPosition call; an in-flight threshold evaluation is not
// retroactively re-checked.
func (s *Switcher) SetTransitionAbortThreshold(threshold float64) error {
	if threshold < minTransitionAbortThreshold || threshold > maxTransitionAbortThreshold {
		return ErrInvalidTransitionAbortThreshold
	}
	s.configMu.Lock()
	s.transitionAbortThreshold = threshold
	s.storeTransitionAbortThresholdAtomic(threshold)
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()
	s.notifyStateChange(snapshot)
	return nil
}

// FadeToBlack starts or toggles a Fade to Black transition. If FTB is already
// active and no transition is running, it toggles off (restores normal output).
// If a non-FTB transition is active, FTB is rejected.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) FadeToBlack(ctx context.Context) error {
	s.configMu.Lock()

	if s.transConfig == nil {
		s.configMu.Unlock()
		return errTransitionNotConfigured
	}

	// Reject if a non-FTB transition is active (mix/dip/wipe)
	if s.state == StateTransitioning {
		s.configMu.Unlock()
		return fmt.Errorf("cannot FTB while mix/dip transition is active: %w", transition.ErrActive)
	}

	// Reject a double-press while an FTB leg is still in flight (forward or
	// reverse). Falling through would spawn a second concurrent engine over
	// the live one, orphaning the first engine mid-fade.
	if s.state == StateFTBTransitioning || s.state == StateFTBReversing {
		s.configMu.Unlock()
		return fmt.Errorf("FTB transition already in progress: %w", transition.ErrActive)
	}

	// Toggle off: FTB is active but transition is complete (fully black).
	// Start a reverse FTB transition to fade back from black.
	if s.state == StateFTB {
		if s.programSource == "" {
			s.configMu.Unlock()
			return errNoProgramSource
		}

		fromSource := s.programSource
		ftbPF := s.pipelineFormat.Load()
		ftbHintW, ftbHintH := ftbPF.Width, ftbPF.Height
		ftbRevDecoderFactory := s.transConfig.DecoderFactory
		ftbRevDurMs := s.ftbDurationMs

		// Mark transition as starting, then release lock.
		s.transitionState(StateFTBReversing)
		s.transStartGen++
		ftbRevStartGen := s.transStartGen
		s.configMu.Unlock()

		// No decoder warmup needed — sources provide raw YUV.
		ftbRevGPUActive := false
		if gh := s.gpuRunner.Load(); gh != nil && gh.runner != nil {
			ftbRevGPUActive = true
		}
		var ftbRevOutputCb func(yuv []byte, width, height int, pts int64, isKeyframe bool)
		engine := transition.NewEngine(transition.EngineConfig{
			DecoderFactory: ftbRevDecoderFactory,
			HintWidth:      ftbHintW,
			HintHeight:     ftbHintH,
			SkipBlend:      ftbRevGPUActive,
			Output:         ftbRevOutputCb,
			OnComplete: func(aborted bool) {
				s.handleFTBReverseComplete(aborted)
			},
		})

		// Set 10-bit color format for FTB reverse transition engine.
		if ftbPF.Color.ColorFormat == ColorYUV422_10bit {
			engine.SetColorFormat(video.YUV422_10bit)
		}

		engine.StartSync(fromSource, "", transition.FTBReverse, ftbRevDurMs)

		// Start audio BEFORE publishing engine (prevents position race).
		audioHandler := s.audioHandlers.Load().audioTransition
		var ftbRevAudioToken uint64
		if audioHandler != nil {
			audioHandler.SetProgramMute(false)
			ftbRevAudioToken = audioHandler.OnTransitionStart(fromSource, "", audio.FadeIn, ftbRevDurMs)
		}

		// Now publish the engine.
		s.configMu.Lock()
		if s.state != StateFTBReversing || s.transStartGen != ftbRevStartGen {
			// A concurrent Cut/abort pre-empted this FTB reverse during the
			// unlocked setup window — do not publish the dead engine.
			return s.abandonTransitionSetup(engine, audioHandler, ftbRevAudioToken, "ftb_reverse", "start FTB reverse")
		}
		s.transEngine = engine
		s.audioTransToken = ftbRevAudioToken
		s.updateHotState(func(hs *hotState) {
			hs.state = StateFTBReversing
			hs.transEngine = engine
		})
		s.transitionsStarted.Add(1)

		s.seq.Add(1)
		snapshot := s.buildStateLocked()
		s.configMu.Unlock()

		s.log.Info("transition started",
			"type", "ftb_reverse", "from", fromSource, "to", "", "duration_ms", ftbRevDurMs)

		s.notifyStateChange(snapshot)
		return nil
	}

	if s.programSource == "" {
		s.configMu.Unlock()
		return errNoProgramSource
	}

	fromSource := s.programSource
	ftbFwdPF := s.pipelineFormat.Load()
	ftbFwdHintW, ftbFwdHintH := ftbFwdPF.Width, ftbFwdPF.Height
	ftbFwdDecoderFactory := s.transConfig.DecoderFactory
	ftbFwdDurMs := s.ftbDurationMs

	// Mark transition as starting, then release lock.
	s.transitionState(StateFTBTransitioning)
	s.transStartGen++
	ftbFwdStartGen := s.transStartGen
	s.configMu.Unlock()

	// No decoder warmup needed — sources provide raw YUV.
	ftbFwdGPUActive := false
	if gh := s.gpuRunner.Load(); gh != nil && gh.runner != nil {
		ftbFwdGPUActive = true
	}
	var ftbFwdOutputCb func(yuv []byte, width, height int, pts int64, isKeyframe bool)
	engine := transition.NewEngine(transition.EngineConfig{
		DecoderFactory: ftbFwdDecoderFactory,
		HintWidth:      ftbFwdHintW,
		HintHeight:     ftbFwdHintH,
		SkipBlend:      ftbFwdGPUActive,
		Output:         ftbFwdOutputCb,
		OnComplete: func(aborted bool) {
			s.handleFTBComplete(aborted)
		},
	})

	// Set 10-bit color format for FTB forward transition engine.
	if ftbFwdPF.Color.ColorFormat == ColorYUV422_10bit {
		engine.SetColorFormat(video.YUV422_10bit)
	}

	engine.StartSync(fromSource, "", transition.FTB, ftbFwdDurMs)

	// Start audio BEFORE publishing engine (prevents position race).
	audioHandler := s.audioHandlers.Load().audioTransition
	var ftbFwdAudioToken uint64
	if audioHandler != nil {
		ftbFwdAudioToken = audioHandler.OnTransitionStart(fromSource, "", audio.FadeOut, ftbFwdDurMs)
	}

	// Now publish the engine.
	s.configMu.Lock()
	if s.state != StateFTBTransitioning || s.transStartGen != ftbFwdStartGen {
		// A concurrent Cut/abort pre-empted this FTB during the unlocked
		// setup window — do not publish the dead engine.
		return s.abandonTransitionSetup(engine, audioHandler, ftbFwdAudioToken, "ftb", "start FTB")
	}
	s.transEngine = engine
	s.audioTransToken = ftbFwdAudioToken
	s.updateHotState(func(hs *hotState) {
		hs.state = StateFTBTransitioning
		hs.transEngine = engine
	})
	s.transitionsStarted.Add(1)

	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	s.log.Info("transition started",
		"type", "ftb", "from", fromSource, "to", "", "duration_ms", ftbFwdDurMs)

	s.notifyStateChange(snapshot)
	return nil
}

func (s *Switcher) FTBDurationMs() int {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	return s.ftbDurationMs
}

// SetFTBDuration updates the fade-to-black duration in ms. Range [100, 5000].
// The change applies to the next FadeToBlack trigger; an in-flight leg keeps
// its original rate (duration is captured under lock at FadeToBlack entry).
func (s *Switcher) SetFTBDuration(durationMs int) error {
	if durationMs < minFTBDurationMs || durationMs > maxFTBDurationMs {
		return ErrInvalidFTBDuration
	}
	s.configMu.Lock()
	s.ftbDurationMs = durationMs
	s.ftbDurationMsAtomic.Store(int64(durationMs))
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()
	s.notifyStateChange(snapshot)
	return nil
}

// rollbackTransitionState reverts the transitional state set at the start of
// StartTransition/FadeToBlack's unlocked setup window, but only if no
// concurrent Cut/abort/transition-start already moved the state on (detected
// via the expected state + start generation). Used on setup-failure paths so
// a blind rollback cannot clobber state a concurrent operation just set.
func (s *Switcher) rollbackTransitionState(expect State, expectGen uint64, to State) {
	s.configMu.Lock()
	if s.state == expect && s.transStartGen == expectGen {
		s.transitionState(to)
	}
	s.configMu.Unlock()
}

// abandonTransitionSetup tears down a transition that was pre-empted during
// its unlocked setup window: a concurrent Cut/abort (or a newer transition
// start) moved the switcher state on before the engine could be published, so
// publishing now would install a dead engine over the live state. The caller
// must hold configMu; it is released here. audioToken is the mixer ownership
// token returned by this transition's OnTransitionStart — the mixer treats
// the abort as a no-op if a newer owner (a cut's crossfade or a newer
// transition) has since established its own state, so this late cleanup can
// never wipe state it doesn't own. transType labels the log line and op
// prefixes the returned error.
func (s *Switcher) abandonTransitionSetup(engine *transition.Engine, audioHandler audioTransitionHandler, audioToken uint64, transType, op string) error {
	s.configMu.Unlock()
	engine.Stop()
	if audioHandler != nil {
		audioHandler.OnTransitionAbort(audioToken)
	}
	s.log.Warn("transition aborted", "type", transType, "reason", "pre-empted during setup")
	return fmt.Errorf("%s: pre-empted by concurrent cut/abort", op)
}

// AbortTransition stops any active transition and restores normal frame routing.
func (s *Switcher) AbortTransition() {
	s.configMu.Lock()
	engine := s.transEngine
	wasActive := s.state.isInTransition()
	audioHandler := s.audioTransition
	audioToken := s.audioTransToken
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
		newState := s.state
		s.updateHotState(func(hs *hotState) {
			hs.state = newState
			hs.transEngine = nil
		})
		s.seq.Add(1)
	}
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	if engine != nil {
		engine.Stop()
	}
	if wasActive {

		s.log.Warn("transition aborted", "type", transType, "reason", "manual abort")

		if audioHandler != nil {
			audioHandler.OnTransitionAbort(audioToken)
		}
		s.notifyStateChange(snapshot)
	}
}

// handleTransitionComplete is called by the transition engine when a mix/dip
// transition finishes. If completed (not aborted), it swaps program/preview
// sources. All sources use the raw pipeline (always-decode), so no GOP
// replay or IDR gating is needed — frames flow immediately.
// transitionWipeDirToGPU maps transition.WipeDirection (string) to gpu.WipeDirection (int).
// Returns 0 (WipeHLeft) for unknown directions.
func (s *Switcher) transitionWipeDirToGPU(dir transition.WipeDirection) int {
	switch dir {
	case transition.WipeHLeft:
		return 0
	case transition.WipeHRight:
		return 1
	case transition.WipeVTop:
		return 2
	case transition.WipeVBottom:
		return 3
	case transition.WipeBoxCenterOut:
		return 4
	case transition.WipeBoxEdgesIn:
		return 5
	default:
		return 0
	}
}

func (s *Switcher) handleTransitionComplete(aborted bool) {
	completeStart := time.Now()

	s.configMu.Lock()
	if !s.state.isInTransition() {
		s.configMu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	audioToken := s.audioTransToken
	var audioCut audioCutHandler
	var newProgram string

	if !aborted && s.transEngine != nil {
		newProgram = s.transEngine.ToSource()
		if newProgram != "" {
			audioCut = s.audioCut
		}
	}

	transType := s.transTypeOrig
	if transType == "" && s.transEngine != nil {
		transType = string(s.transEngine.TransitionType())
	}
	s.transTypeOrig = "" // clear for next transition

	if newProgram != "" {
		s.programSource = newProgram
		// previewSource was swapped to oldProgram at transition start (see
		// StartTransition). Don't reassert it here — an operator who called
		// SetPreview during the transition to queue the next cut would see
		// their choice clobbered.
	}

	// Record transition seam start — the gap from now until the first
	// post-transition frame reaches broadcastToProgram.
	if !aborted {
		s.transSeamStartNano.Store(time.Now().UnixNano())
	}

	s.transitionState(StateIdle)
	s.transEngine = nil
	s.updateHotState(func(hs *hotState) {
		hs.state = StateIdle
		hs.transTypeOrig = ""
		if newProgram != "" {
			hs.programSource = newProgram
		}
		hs.transEngine = nil
	})
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues(transType).Inc()
	}
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	// Release GPU gradient map buffer now that the transition is done.
	if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
		h.runner.ClearWipeMap()
	}

	completeDur := time.Since(completeStart)
	if aborted {
		s.log.Warn("transition aborted", "type", transType, "reason", "engine aborted",
			"complete_ms", completeDur.Milliseconds())
	} else {
		s.log.Info("transition completed", "type", transType,
			"complete_ms", completeDur.Milliseconds())
	}

	if audioHandler != nil {
		if aborted {
			audioHandler.OnTransitionAbort(audioToken)
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
	s.configMu.Lock()
	if !s.state.isInTransition() {
		s.configMu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	audioToken := s.audioTransToken
	if aborted {
		s.transitionState(StateIdle) // Aborted — return to idle
	} else {
		s.transitionState(StateFTB) // Completed — hold at black
	}
	s.transEngine = nil
	newState := s.state
	s.updateHotState(func(hs *hotState) {
		hs.state = newState
		hs.transEngine = nil
	})
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb").Inc()
	}
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	if aborted {
		s.log.Warn("transition aborted", "type", "ftb", "reason", "engine aborted")
	} else {
		s.log.Info("FTB activated")
	}

	if audioHandler != nil {
		if aborted {
			audioHandler.OnTransitionAbort(audioToken)
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
	s.configMu.Lock()
	if !s.state.isInTransition() {
		s.configMu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	audioToken := s.audioTransToken

	if aborted {
		s.transitionState(StateFTB) // Aborted — screen stays black
	} else {
		// Record transition seam start for FTB reverse.
		s.transSeamStartNano.Store(time.Now().UnixNano())
		s.transitionState(StateIdle) // Completed — screen is visible
	}
	s.transEngine = nil
	newState := s.state
	s.updateHotState(func(hs *hotState) {
		hs.state = newState
		hs.transEngine = nil
	})
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb_reverse").Inc()
	}

	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	if aborted {
		s.log.Warn("transition aborted", "type", "ftb_reverse", "reason", "engine aborted")
	} else {
		s.log.Info("FTB deactivated")
	}

	if audioHandler != nil {
		if aborted {
			// FTB reverse aborted — screen stays black, re-mute audio
			audioHandler.OnTransitionAbort(audioToken)
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
	s.configMu.Lock()
	viewer := newSourceViewer(key, s)
	useRaw := false

	// Always-decode mode: create per-source decoder that converts H.264 → YUV
	// at ingest time. Decoded frames enter through IngestRawVideo.
	if s.sourceDecoderFactory != nil {
		cb := s.IngestRawVideo
		sd := newSourceDecoder(key, s.sourceDecoderFactory, cb, s.framePool.Load(), &s.pipelineFormat, s.stmapRegistry, &s.gpuSourceActive)
		if sd != nil {
			sd.thumbnailCache = s.thumbnailCache
			viewer.srcDecoder.Store(sd)
			useRaw = true
		}
	}

	// Decoded video goes to the frame synchronizer, audio and captions
	// through the delay buffer.
	viewer.frameSync.Store(s.frameSync)
	viewer.delayBuffer.Store(s.delayBuffer)
	s.frameSync.AddSource(key)
	relay.AddViewer(viewer)
	var decoderCount int
	s.withSourcesMut(func(m map[string]*sourceState) {
		ss := &sourceState{key: key, relay: relay, viewer: viewer, useRawPipeline: useRaw, position: len(m) + 1}
		ss.initAtomicMirrors()
		m[key] = ss
		for _, other := range m {
			if other.viewer != nil && other.viewer.srcDecoder.Load() != nil {
				decoderCount++
			}
		}
	})
	s.health.registerSource(key)
	s.configMu.Unlock()

	s.log.Info("source registered", "source_key", key, "raw_pipeline", useRaw)

	// Warn when estimated YUV memory exceeds 256MB (~85 1080p sources).
	// Each 1080p decoder holds ~3MB of YUV buffer.
	if estimatedMB := decoderCount * 3; estimatedMB > 256 {
		s.log.Warn("high source decoder memory usage",
			"active_decoders", decoderCount,
			"estimated_yuv_mb", estimatedMB)
	}
}

// RegisterVirtualSource registers a transient internal source (e.g. replay).
// Virtual sources skip delay buffer, frame sync, and replay buffering.
// Safe to call if the key already exists — cleans up the old viewer first.
func (s *Switcher) RegisterVirtualSource(key string, relay *distribution.Relay) {
	s.configMu.Lock()
	viewer := newSourceViewer(key, s)
	relay.AddViewer(viewer)
	s.withSourcesMut(func(m map[string]*sourceState) {
		// Clean up existing registration to prevent viewer leak on rapid re-register.
		if old, exists := m[key]; exists {
			old.relay.RemoveViewer(old.viewer.ID())
			delete(m, key)
			s.health.removeSource(key)
		}
		ss := &sourceState{
			key:       key,
			label:     strings.ToUpper(key),
			relay:     relay,
			viewer:    viewer,
			isVirtual: true,
			position:  len(m) + 1,
		}
		ss.initAtomicMirrors()
		m[key] = ss
	})
	s.health.registerSource(key)
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()
	s.log.Info("virtual source registered", "source_key", key)
	s.notifyStateChange(snapshot)
}

// registerRawSource is the shared implementation for RegisterMXLSource and
// RegisterSRTSource. It creates a sourceState with no relay/viewer (raw YUV
// frames arrive via IngestRawVideo) and notifies state listeners.
// When frame sync is active, the source is added to the synchronizer so that
// IngestRawVideo routes through the ring buffer for steady-rate release.
func (s *Switcher) registerRawSource(key, label string, mxl bool) {
	s.configMu.Lock()
	s.withSourcesMut(func(m map[string]*sourceState) {
		ss := &sourceState{
			key:        key,
			label:      label,
			position:   len(m) + 1,
			isMXL:      mxl,
			mediaTimed: mxl,
		}
		ss.initAtomicMirrors()
		m[key] = ss
	})
	s.health.registerSource(key)
	s.frameSync.AddSource(key)
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()
	s.log.Info("raw source registered", "source_key", key, "mxl", mxl)
	s.notifyStateChange(snapshot)
}

// RegisterMXLSource registers a source that provides raw YUV420p frames
// directly (no Prism relay/viewer). Used for MXL shared-memory sources.
func (s *Switcher) RegisterMXLSource(key string) {
	s.registerRawSource(key, strings.ToUpper(key), true)
}

// RegisterMXLSourceLabeled is RegisterMXLSource with the label the source is
// shown under until one is set.
func (s *Switcher) RegisterMXLSourceLabeled(key, label string) {
	s.registerRawSource(key, label, true)
}

// RegisterSRTSource registers a source that provides raw YUV420p frames
// via IngestRawVideo (same path as MXL). Used for SRT input sources that
// are decoded by the srt.Source orchestrator before being fed to the switcher.
func (s *Switcher) RegisterSRTSource(key string) {
	s.registerRawSource(key, strings.TrimPrefix(key, "srt:"), false)
}

// RegisterReplaySource registers a transient replay source that receives
// raw YUV frames via IngestReplayVideo. Virtual sources skip delay buffer
// and replay buffering, but ARE registered with the FrameSynchronizer so
// the ref clock sync loop can read their frames.
// Safe to call if the key already exists — cleans up the old registration.
func (s *Switcher) RegisterReplaySource(key string) {
	s.configMu.Lock()
	s.withSourcesMut(func(m map[string]*sourceState) {
		if old, exists := m[key]; exists {
			if old.relay != nil && old.viewer != nil {
				old.relay.RemoveViewer(old.viewer.ID())
			}
			delete(m, key)
			s.health.removeSource(key)
		}
		ss := &sourceState{
			key:       key,
			label:     strings.ToUpper(key),
			position:  len(m) + 1,
			isVirtual: true,
		}
		ss.initAtomicMirrors()
		m[key] = ss
	})
	s.health.registerSource(key)
	s.frameSync.AddSource(key)
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()
	s.log.Info("replay source registered", "source_key", key)
	s.notifyStateChange(snapshot)
}

// IngestReplayVideo accepts a raw YUV420p frame from the replay player,
// clip player, or playout channel. Routes through IngestRawVideo so
// frames enter the FrameSynchronizer (needed for ref clock sync loop).
func (s *Switcher) IngestReplayVideo(sourceKey string, pf *ProcessingFrame) {
	s.IngestRawVideo(sourceKey, pf)
}

// IngestRawVideo is the one entry point for decoded source video. It
// consumes pf. An MXL or ST 2110 source's producer stamps its frames on the
// engine timeline; any other source's frame is stamped with the TAI of its
// arrival, so every frame in the synchronizer is on one timeline.
func (s *Switcher) IngestRawVideo(sourceKey string, pf *ProcessingFrame) {
	s.health.recordFrame(sourceKey, time.Now())
	ss := (*s.sourcesAtomic.Load())[sourceKey]
	if ss == nil {
		pf.ReleaseYUV()
		s.routeFiltered.Add(1)
		return
	}
	if !ss.mediaTimed {
		pf.PTS = clock.NowPTS()
	}
	pf.DTS = pf.PTS

	// The GPU source manager is fed here, once per frame, and nowhere else.
	if h := s.gpuSourceMgr.Load(); h != nil && h.mgr != nil && len(pf.Data) > 0 {
		h.mgr.IngestYUV(sourceKey, pf.Data, pf.Width, pf.Height, pf.PTS)
	} else {
		pf = s.gradeSourceFrame(sourceKey, pf)
		s.recordLiveTap(sourceKey, pf)
	}
	s.noteRawVideo(ss, sourceKey, pf)
	s.frameSync.IngestRawVideo(sourceKey, pf)
}

// noteRawVideo does the per-frame work that belongs to arrival rather than
// to a tick: source counters, encoder parameters from the program source's
// decoder, and the CPU fill feeds of keyers and the DVE compositor, which
// read every source, not only program. With a GPU source manager those are
// fed from its cache instead.
func (s *Switcher) noteRawVideo(ss *sourceState, sourceKey string, pf *ProcessingFrame) {
	ss.rawFrameCount.Add(1)
	if ss.viewer != nil {
		if dec := ss.viewer.srcDecoder.Load(); dec != nil && sourceKey == s.hotState.Load().programSource {
			avgSize, avgFPS := dec.Stats()
			if pipeCodecs := s.pipeCodecsAtomic.Load(); pipeCodecs != nil {
				pipeCodecs.updateSourceStats(avgSize, avgFPS)
			}
		}
	}
	ss.lastGroupID.Store(pf.GroupID)
	if h := s.gpuSourceMgr.Load(); h != nil && h.mgr != nil {
		return
	}
	gh := s.graphicsHandlers.Load()
	if pf.Format == ColorYUV422_10bit {
		if gh.keyBridge != nil {
			gh.keyBridge.IngestFillYUV10(sourceKey, pf.Data, pf.Width, pf.Height)
		}
		return
	}
	if gh.keyBridge != nil {
		gh.keyBridge.IngestFillYUV(sourceKey, pf.Data, pf.Width, pf.Height)
	}
	if gh.dveCompositor != nil && gh.dveCompositor.NeedsSource(sourceKey) {
		gh.dveCompositor.IngestSourceFrame(sourceKey, pf.Data, pf.Width, pf.Height, pf.PTS)
	}
}

// UnregisterSource removes a source from the switcher and detaches its
// viewer from the source Relay. If the removed source was on program or
// preview, those fields are cleared.
func (s *Switcher) UnregisterSource(key string) {
	s.configMu.Lock()
	current := *s.sourcesAtomic.Load()
	ss, ok := current[key]
	if !ok {
		s.configMu.Unlock()
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
	s.withSourcesMut(func(m map[string]*sourceState) {
		delete(m, key)
	})
	s.health.removeSource(key)
	s.delayBuffer.RemoveSource(key)
	if s.frameSync != nil {
		s.frameSync.RemoveSource(key)
	}
	// Clean up GPU source cache so stale frames aren't used after re-registration.
	if h := s.gpuSourceMgr.Load(); h != nil && h.mgr != nil {
		h.mgr.RemoveSource(key)
	}
	s.liveTaps.entries.Delete(key)
	if s.programSource == key {
		s.programSource = ""
	}
	if s.previewSource == key {
		s.previewSource = ""
	}
	newProgram := s.programSource
	newPreview := s.previewSource
	s.updateHotState(func(hs *hotState) {
		hs.programSource = newProgram
		hs.previewSource = newPreview
	})
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

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
	var abortAudioToken uint64
	changed := false

	s.configMu.Lock()
	if _, ok := (*s.sourcesAtomic.Load())[sourceKey]; !ok {
		s.configMu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	if s.programSource != sourceKey {
		// If a transition is active, abort it first so its OnComplete
		// callback won't overwrite our new programSource.
		if s.state.isInTransition() {
			abortEngine = s.transEngine
			abortAudioHandler = s.audioTransition
			abortAudioToken = s.audioTransToken
			s.transEngine = nil
			s.transitionState(StateIdle)
		}
		oldProgram = s.programSource
		s.programSource = sourceKey
		// Bump program epoch so in-flight frames from the old program
		// source are discarded in videoProcessingLoop.
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
		newPreview := s.previewSource
		newState := s.state
		s.updateHotState(func(hs *hotState) {
			hs.state = newState
			hs.transEngine = nil
			hs.programSource = sourceKey
			hs.previewSource = newPreview
		})
		audioCut = s.audioCut
		s.seq.Add(1)
		snapshot = s.buildStateLocked()
		changed = true
	}
	s.configMu.Unlock()

	// Stop the aborted transition engine outside the lock.
	if abortEngine != nil {
		abortEngine.Stop()
		s.log.Warn("transition aborted", "reason", "cut override")
		if abortAudioHandler != nil {
			abortAudioHandler.OnTransitionAbort(abortAudioToken)
		}
	}

	if changed {
		s.log.Info("cut executed", "source", sourceKey, "previous_source", oldProgram)

		// Record transition seam for cut timing diagnostics.
		s.transSeamStartNano.Store(time.Now().UnixNano())

		// Auto-dissolve any PIP slot showing the new program source.
		s.configMu.Lock()
		dveComp := s.dveCompositor
		s.configMu.Unlock()
		if dveComp != nil {
			dveComp.AutoDissolveSource(sourceKey)
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
	s.configMu.Lock()
	if _, ok := (*s.sourcesAtomic.Load())[sourceKey]; !ok {
		s.configMu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	s.previewSource = sourceKey
	s.updateHotState(func(hs *hotState) { hs.previewSource = sourceKey })
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// SetLabel sets a human-readable label for the given source.
//
// The ctx parameter is accepted for API compatibility and future use (e.g.
// tracing) but is not currently checked; the operation is sub-millisecond.
func (s *Switcher) SetLabel(ctx context.Context, sourceKey, label string) error {
	s.configMu.Lock()
	ss, ok := (*s.sourcesAtomic.Load())[sourceKey]
	if !ok {
		s.configMu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	ss.label = label
	labelCopy := label
	ss.labelAtomic.Store(&labelCopy)
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

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
	s.configMu.Lock()
	sources := *s.sourcesAtomic.Load()
	ss, ok := sources[sourceKey]
	if !ok {
		s.configMu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	// Swap with any source already at the target position
	for _, other := range sources {
		if other.key != sourceKey && other.position == position {
			other.position = ss.position
			other.positionAtomic.Store(int32(ss.position))
			break
		}
	}
	ss.position = position
	ss.positionAtomic.Store(int32(position))
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

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
	if _, ok := (*s.sourcesAtomic.Load())[sourceKey]; !ok {
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}

	// Video and audio are each delayed once, as a read offset in media
	// time: the frame sync reads the source's frames, the mixer its audio,
	// that much earlier. The delay buffer holds back captions.
	s.delayBuffer.SetDelay(sourceKey, time.Duration(delayMs)*time.Millisecond)
	s.frameSync.SetSourceDelay(sourceKey, delayMs, s.pipelineFormat.Load().FrameDuration())
	s.configMu.Lock()
	m, _ := s.syncMixer.(sourceDelayMixer)
	s.configMu.Unlock()
	if m != nil {
		m.SetSourceDelay(sourceKey, delayMs)
	}

	s.log.Info("source delay set", "source_key", sourceKey, "delay_ms", delayMs)

	s.configMu.Lock()
	s.seq.Add(1)
	snapshot := s.buildStateLocked()
	s.configMu.Unlock()

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
	s.configMu.Lock()
	state := s.buildStateLocked()
	s.configMu.Unlock()
	// Publish clock-sync metrics outside the lock so prom's internal
	// GaugeVec mutex does not serialize with the broadcast hot path.
	s.publishClockSyncMetrics(state.ClockSync)
	return state
}

// stateStamp returns the time in Unix milliseconds, but later than any
// stamp it returned before. A browser keeps the state snapshot with the
// latest Timestamp: snapshots are built on several goroutines, and each
// reaches the browser on a stream of its own, so they can arrive out of
// order. Two snapshots built within one millisecond would tie, and the
// browser could keep the one built first.
func (s *Switcher) stateStamp() int64 {
	now := time.Now().UnixMilli()
	for {
		last := s.lastStateStamp.Load()
		stamp := max(now, last+1)
		if s.lastStateStamp.CompareAndSwap(last, stamp) {
			return stamp
		}
	}
}

// DebugSnapshot returns a map of debug instrumentation data for diagnostics.
func (s *Switcher) DebugSnapshot() map[string]any {
	s.configMu.Lock()
	defer s.configMu.Unlock()

	srcMap := *s.sourcesAtomic.Load()
	sources := make(map[string]any, len(srcMap))
	activeDecoders := 0
	for key, ss := range srcMap {
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
		"seq":            s.seq.Load(),
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
			"frames_processed":      s.videoProcCount.Load(),
			"frames_broadcast":      s.videoBroadcastCount.Load(),
			"stall_fill_frames":     s.programRepeats.Load(),
			"frames_dropped":        s.overloadDrops.Load(),
			"encode_nil":            s.pipeEncodeNil.Load(),
			"encode_drop":           s.pipeEncodeDrop.Load(),
			"trans_output":          s.transOutputCount.Load(),
			"last_proc_time_ms":     float64(s.videoProcLastNano.Load()) / 1e6,
			"max_proc_time_ms":      float64(s.videoProcMaxNano.Load()) / 1e6,
			"max_broadcast_gap_ms":  float64(s.maxBroadcastIntervalNano.Load()) / 1e6,
			"route_filtered":        s.routeFiltered.Load(),
			"output_fps":            s.outputFPSLastSecond.Load(),
			"trans_seam_last_ms":    float64(s.transSeamLastNano.Load()) / 1e6,
			"trans_seam_max_ms":     float64(s.transSeamMaxNano.Load()) / 1e6,
			"trans_seam_count":      s.transSeamCount.Load(),
			"gpu_frames_processed":  s.gpuFramesProcessed.Load(),
			"gpu_cache_misses":      s.gpuCacheMisses.Load(),
			"gpu_transition_frames": s.gpuTransitionFrames.Load(),
			"gpu_fallback_frames":   s.gpuFallbackFrames.Load(),
		},
		"codec": map[string]any{
			"encoder":  s.codecEncoder,
			"decoder":  s.codecDecoder,
			"hw_accel": s.codecHWAccel,
		},
	}

	if fp := s.framePool.Load(); fp != nil {
		hits, misses := fp.Stats()
		result["frame_pool"] = map[string]any{
			"hits":     hits,
			"misses":   misses,
			"capacity": fp.cap,
			"buf_size": fp.bufSize,
		}
	}

	if p := s.pipeline.Load(); p != nil {
		result["pipeline"] = p.Snapshot()
	}

	// GPU pipeline stats (when active, provides GPU node timings and source manager state).
	if h := s.gpuRunner.Load(); h != nil {
		result["gpu_pipeline"] = h.runner.Snapshot()
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
	for key, ss := range *s.sourcesAtomic.Load() {
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

// SourceKeys returns the keys of all registered sources. Lock-free via
// sourcesAtomic — no configMu acquired.
func (s *Switcher) SourceKeys() []string {
	sources := *s.sourcesAtomic.Load()
	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	return keys
}

// PipelineSnapshot returns a combined snapshot of all pipeline stages:
// CPU pipeline nodes, GPU pipeline nodes, and registered sources with health.
// The response is structured for the routing overlay UI.
func (s *Switcher) PipelineSnapshot() map[string]any {
	result := make(map[string]any)

	// CPU pipeline nodes.
	if p := s.pipeline.Load(); p != nil {
		result["cpu_pipeline"] = p.Snapshot()
	}

	// GPU pipeline nodes (when active, provides GPU node timings and source manager state).
	if h := s.gpuRunner.Load(); h != nil {
		result["gpu_pipeline"] = h.runner.Snapshot()
	}

	// Source info with health status.
	srcMap := *s.sourcesAtomic.Load()
	sources := make(map[string]map[string]any, len(srcMap))
	for key := range srcMap {
		sources[key] = map[string]any{
			"health": string(s.health.rawStatus(key)),
		}
	}
	result["sources"] = sources

	return result
}

// SetPipelineBypass toggles the bypass flag for the named pipeline node.
// When the GPU pipeline is active, bypass is set there (GPU nodes have names
// like gpu_key, gpu_layout, etc). Falls back to the CPU pipeline for CPU node
// names. Returns an error if the node is protected or unknown in both pipelines.
func (s *Switcher) SetPipelineBypass(name string, bypass bool) error {
	// Try GPU pipeline first (active when gpu runner is set).
	if h := s.gpuRunner.Load(); h != nil {
		if gp, ok := h.runner.(interface {
			SetBypass(string, bool) error
		}); ok {
			err := gp.SetBypass(name, bypass)
			if err == nil {
				return nil
			}
			// If node not found in GPU pipeline, fall through to CPU pipeline.
			// Protected node errors should propagate immediately.
			if !isNodeNotFoundError(err) {
				return err
			}
		}
	}

	// CPU pipeline fallback.
	p := s.pipeline.Load()
	if p == nil {
		return fmt.Errorf("no pipeline loaded")
	}
	return p.SetBypass(name, bypass)
}

// isNodeNotFoundError checks if the error indicates a node was not found
// (vs. a protected node error which should be returned immediately).
func isNodeNotFoundError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// buildStateLocked constructs a ControlRoomState snapshot. Caller must hold
// at least s.configMu.Lock().
// clockSyncAsInfo translates clock.SyncState into the ControlRoomState-
// facing type. The two structs are field-wise identical by design (see
// internal.ClockSyncInfo doc) so a straight copy keeps the JSON shape
// stable across refactors.
func clockSyncAsInfo(s clock.SyncState) internal.ClockSyncInfo {
	return internal.ClockSyncInfo{
		Synchronized:  s.Synchronized,
		WindowDriftUs: s.WindowDriftUs,
		Source:        s.Source,
	}
}

// clockSyncSourceLabels enumerates every valid Source string so
// publishClockSyncMetrics can zero the inactive-source gauges each tick.
// Keeping the enum here (not exported) avoids churn in the clock
// package's public API for what is strictly a metric-labels concern.
var clockSyncSourceLabels = []string{
	"systemclock-initializing",
	"systemclock-monitored",
	"unknown",
	"ptp",
	"ntp",
}

// publishClockSyncMetrics updates the clock-sync Prometheus gauges from
// the just-built ControlRoomState.ClockSync. Runs OUTSIDE configMu (call
// site in State() fires post-Unlock) so the broadcast hot path isn't
// serialized on the prom GaugeVec internal mutex.
func (s *Switcher) publishClockSyncMetrics(info internal.ClockSyncInfo) {
	if s.promMetrics == nil {
		return
	}
	if info.Synchronized {
		s.promMetrics.ClockSyncSynchronized.Set(1)
	} else {
		s.promMetrics.ClockSyncSynchronized.Set(0)
	}
	s.promMetrics.ClockSyncWindowDriftNs.Set(float64(info.WindowDriftUs) * 1000)
	for _, label := range clockSyncSourceLabels {
		if label == info.Source {
			s.promMetrics.ClockSyncSource.WithLabelValues(label).Set(1)
		} else {
			s.promMetrics.ClockSyncSource.WithLabelValues(label).Set(0)
		}
	}
}

func (s *Switcher) buildStateLocked() internal.ControlRoomState {
	// Stamped before anything is read, so a later stamp never carries
	// older contents.
	stamp := s.stateStamp()
	srcMap := *s.sourcesAtomic.Load()
	tally := make(map[string]string, len(srcMap))
	sources := make(map[string]internal.SourceInfo, len(srcMap))
	for key, ss := range srcMap {
		tally[key] = string(TallyIdle)
		// Built-in reference sources are backed by an internal generator,
		// not an ingest feed that can go stale — their health is reported
		// as healthy regardless of whether the producer is currently
		// feeding frames (only happens when selected on PGM/PVW).
		status := s.health.status(key)
		if isBuiltinSource(key) {
			status = SourceHealthy
		}
		sources[key] = internal.SourceInfo{
			Key:       key,
			Label:     ss.label,
			Status:    string(status),
			Position:  ss.position,
			DelayMs:   int(s.delayBuffer.GetDelay(key) / time.Millisecond),
			IsVirtual: ss.isVirtual,
		}
	}
	// Broadcast-safe tally precedence:
	//   program > key_fill > preview > key_alpha > idle
	// Key fills must outrank preview because an enabled USK puts the fill on
	// the air once composited — the operator must see the fill as live.
	if s.keyBridge != nil {
		kp := s.keyBridge.KeyProcessor()
		if kp != nil {
			for _, src := range kp.KeyAlphas() {
				if _, ok := srcMap[src]; ok {
					tally[src] = string(TallyKeyAlpha)
				}
			}
			for _, src := range kp.KeyFills() {
				if _, ok := srcMap[src]; ok {
					tally[src] = string(TallyKeyFill)
				}
			}
		}
	}
	if s.previewSource != "" && s.previewSource != s.programSource {
		if tally[s.previewSource] != string(TallyKeyFill) {
			tally[s.previewSource] = string(TallyPreview)
		}
	}
	if s.programSource != "" {
		tally[s.programSource] = string(TallyProgram)
	}
	// During a transition, the destination is going-to-air via the blend —
	// SMPTE broadcast-safe tally marks both source and destination as
	// program ("live") so the operator can see at a glance which cameras
	// are on air. Without this, the destination has no tally (preview is
	// suppressed above because previewSource == programSource post-swap).
	if s.state.isInTransition() && s.transEngine != nil {
		if to := s.transEngine.ToSource(); to != "" {
			if _, ok := srcMap[to]; ok {
				tally[to] = string(TallyProgram)
			}
		}
	}
	transType := "cut"
	if s.state.isInTransition() && s.transEngine != nil {
		transType = string(s.transEngine.TransitionType())
	}
	state := internal.ControlRoomState{
		ProgramSource:            s.programSource,
		PreviewSource:            s.previewSource,
		TransitionType:           transType,
		InTransition:             s.state.isInTransition(),
		FTBActive:                s.state.isFTBActive(),
		FTBDurationMs:            s.ftbDurationMs,
		TransitionAbortThreshold: s.transitionAbortThreshold,
		TallyState:               tally,
		UnavailableTransitions:   s.unavailableTransitions(),
		Sources:                  sources,
		Seq:                      s.seq.Load(),
		Timestamp:                stamp,
		LastCommandSeq:           s.lastCommandSeq.Load(),
		LastExecutedSeq:          s.lastExecutedSeq.Load(),
		ClockSync:                clockSyncAsInfo(clock.GetSyncState()),
	}
	if errPtr := s.lastExecutedErr.Load(); errPtr != nil {
		state.LastExecutedErr = *errPtr
	}
	if s.cmdQueue != nil {
		state.LateCommands = s.cmdQueue.LateCommands()
	}
	if s.recentCmdErrors != nil {
		if errs := s.recentCmdErrors.snapshot(); len(errs) > 0 {
			state.RecentCommandErrors = make([]internal.CommandError, len(errs))
			for i, e := range errs {
				state.RecentCommandErrors[i] = internal.CommandError{Seq: e.Seq, Error: e.Error}
			}
		}
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
		limThreshold, limCeiling, limGR, limTP := s.mixer.LimiterSettings()
		state.Limiter = &internal.LimiterSettings{
			Threshold:     limThreshold,
			Ceiling:       limCeiling,
			GainReduction: limGR,
			TruePeak:      limTP,
		}
		state.MomentaryLUFS = clampLUFS(s.mixer.MomentaryLUFS())
		state.ShortTermLUFS = clampLUFS(s.mixer.ShortTermLUFS())
		state.IntegratedLUFS = clampLUFS(s.mixer.IntegratedLUFS())
	}

	// Include pipeline format
	if f := s.pipelineFormat.Load(); f != nil {
		state.PipelineFormat = &internal.PipelineFormatInfo{
			Width:       f.Width,
			Height:      f.Height,
			FPSNum:      f.FPSNum,
			FPSDen:      f.FPSDen,
			Name:        f.Name,
			ColorFormat: f.Color.ColorFormat.String(),
			Codec:       f.Color.Codec.String(),
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
// Must be called WITHOUT holding s.configMu to avoid blocking frame handlers.
func (s *Switcher) notifyStateChange(snapshot internal.ControlRoomState) {
	s.configMu.Lock()
	cbs := s.stateCallbacks
	s.configMu.Unlock()
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

// handleVideoFrame takes a compressed frame from a source with no decoder.
// It only keeps the source's frame statistics: program video is made from
// decoded frames by the sync loop alone.
func (s *Switcher) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	s.health.recordFrame(sourceKey, time.Now())
	if ss := (*s.sourcesAtomic.Load())[sourceKey]; ss != nil {
		s.updateFrameStats(ss, frame)
	}
}

// handleAudioFrame implements frameHandler. It is called by sourceViewers
// when an audio frame arrives from a source. If an audio handler (mixer)
// is set, ALL source audio is forwarded to it — the mixer decides routing.
// Otherwise, only the current program source's audio is forwarded to the
// program Relay.
func (s *Switcher) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	s.health.recordFrame(sourceKey, time.Now())

	// Lock-free hot-path snapshot.
	hs := s.hotState.Load()
	sources := *s.sourcesAtomic.Load()
	ah := s.audioHandlers.Load()

	handler := ah.audioHandler
	_, ok := sources[sourceKey]
	isProgram := ok && hs.programSource == sourceKey

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
// source's captions are forwarded to the program Relay.
// When a caption manager is attached, the caption text is re-encoded
// as CEA-608 pairs for SEI injection into the encoded video output.
func (s *Switcher) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	_, ok := (*s.sourcesAtomic.Load())[sourceKey]
	hs := s.hotState.Load()
	isProgram := ok && hs.programSource == sourceKey
	s.configMu.Lock()
	cm := s.captionMgr
	s.configMu.Unlock()

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

	// Notify additional caption sinks (e.g., program-preview relay).
	if cb := s.onCaptionBroadcast.Load(); cb != nil {
		(*cb)(frame)
	}
}
