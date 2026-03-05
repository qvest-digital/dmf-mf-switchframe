// Package switcher implements the core video switching state machine.
// It receives tagged frames from sourceViewer proxies and forwards only
// the current program source's frames to the program Relay.
package switcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/transition"
)

// Sentinel errors for the switcher package.
var ErrSourceNotFound = errors.New("source not found")
var ErrAlreadyOnProgram = errors.New("already on program")
var ErrInvalidDelay = errors.New("delay must be 0-500ms")

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
	OnTransitionStart(oldSource, newSource string, mode internal.AudioTransitionMode, durationMs int)
	OnTransitionPosition(position float64)
	OnTransitionComplete()
	SetProgramMute(muted bool)
}

// TransitionConfig holds the codec factories needed to create TransitionEngines.
type TransitionConfig struct {
	DecoderFactory transition.DecoderFactory
	EncoderFactory transition.EncoderFactory
}

// sourceState tracks a registered source and its Relay/viewer pair.
type sourceState struct {
	key        string
	label      string
	relay      *distribution.Relay
	viewer     *sourceViewer
	pendingIDR bool // true after a cut until first keyframe from this source

	// Rolling frame statistics for dynamic encoder parameters.
	// Updated on every video frame. Used to estimate bitrate/fps for
	// the transition encoder so it matches the source stream quality.
	avgFrameSize float64 // exponential moving average of len(WireData) in bytes
	avgFPS       float64 // exponential moving average of fps from PTS deltas
	lastPTS      int64   // PTS of the most recent video frame (microseconds)
	frameCount   int     // total video frames received (for EMA warmup)
}

// Switcher is the central switching engine. It manages which source is
// on-program (live output) and which is on-preview, maintains tally state,
// and routes frames from the program source to the program Relay.
type Switcher struct {
	mu             sync.RWMutex
	sources        map[string]*sourceState
	programSource  string
	previewSource  string
	programRelay   *distribution.Relay
	seq            uint64
	stateCallbacks []func(internal.ControlRoomState)
	health         *healthMonitor
	audioHandler    func(sourceKey string, frame *media.AudioFrame)
	mixer           audioStateProvider
	audioCut        audioCutHandler
	transConfig     *TransitionConfig
	transEngine     *transition.TransitionEngine
	inTransition    bool
	ftbActive       bool
	audioTransition audioTransitionHandler
	gopCache        *gopCache
	delayBuffer     *DelayBuffer

	// Optional video processor hook — called before BroadcastVideo to allow
	// downstream keying (graphics overlay compositing) on every program frame.
	videoProcessor func(*media.VideoFrame) *media.VideoFrame

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

// Close stops the health monitor, delay buffer, and unregisters all sources.
func (s *Switcher) Close() {
	s.health.stop()
	s.delayBuffer.Close()
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

// SetVideoProcessor attaches a video frame processor that is called on every
// program frame before it is broadcast to viewers. Used by the graphics
// compositor to overlay lower-thirds / DSK onto the program output.
// The processor receives a frame and returns the (possibly modified) frame.
// Return nil to drop the frame. Passing nil disables processing.
func (s *Switcher) SetVideoProcessor(proc func(*media.VideoFrame) *media.VideoFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.videoProcessor = proc
}

// broadcastVideo sends a video frame to the program relay, optionally
// running it through the video processor first (for DSK compositing).
func (s *Switcher) broadcastVideo(frame *media.VideoFrame) {
	s.mu.RLock()
	proc := s.videoProcessor
	s.mu.RUnlock()
	if proc != nil {
		frame = proc(frame)
		if frame == nil {
			return
		}
	}
	s.programRelay.BroadcastVideo(frame)
}

// StartTransition begins a mix/dip/wipe transition from the current program source
// to the given target source. Frames from both sources are routed to the
// TransitionEngine which produces blended output on the program relay.
// wipeDirection is only used when transType is "wipe"; pass empty string otherwise.
func (s *Switcher) StartTransition(ctx context.Context, sourceKey string, transType string, durationMs int, wipeDirection string) error {
	s.mu.Lock()

	if s.transConfig == nil {
		s.mu.Unlock()
		return fmt.Errorf("transition not configured")
	}
	if s.inTransition {
		s.mu.Unlock()
		return fmt.Errorf("transition: %w", transition.ErrTransitionActive)
	}
	if s.ftbActive {
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

	tt := transition.TransitionType(transType)
	if tt != transition.TransitionMix && tt != transition.TransitionDip && tt != transition.TransitionWipe {
		s.mu.Unlock()
		return fmt.Errorf("unsupported transition type: %q", transType)
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

	// Estimate encoder parameters from the program source's recent frames.
	bitrate, fps := s.estimateEncoderParams(fromSource)

	var transGroupID uint32
	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: s.transConfig.DecoderFactory,
		EncoderFactory: s.transConfig.EncoderFactory,
		Bitrate:        bitrate,
		FPS:            fps,
		WipeDirection:  wipeDir,
		Output: func(data []byte, isKeyframe bool, pts int64) {
			if isKeyframe {
				transGroupID++
			}
			s.broadcastVideo(annexBToVideoFrame(data, isKeyframe, transGroupID, pts))
		},
		OnComplete: func(aborted bool) {
			s.handleTransitionComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, sourceKey, tt, durationMs); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("start transition: %w", err)
	}

	// Warm up decoders BEFORE publishing the engine. This ensures live
	// frames cannot reach the engine (via handleVideoFrame) before the
	// decoders have been primed with the cached GOP. The warmup acquires
	// engine.mu (not s.mu), so there is no deadlock risk. Holding s.mu
	// briefly during warmup (~1ms for a typical GOP) is acceptable.
	fromGOP := s.gopCache.GetGOP(fromSource)
	toGOP := s.gopCache.GetGOP(sourceKey)
	for _, cf := range fromGOP {
		engine.WarmupDecode(fromSource, cf.annexB)
	}
	for _, cf := range toGOP {
		engine.WarmupDecode(sourceKey, cf.annexB)
	}

	s.transEngine = engine
	s.inTransition = true
	s.previewSource = sourceKey
	s.transitionsStarted.Add(1)
	audioHandler := s.audioTransition

	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	slog.Info("switcher: transition started",
		"type", string(tt), "from", fromSource, "to", sourceKey, "duration_ms", durationMs)

	if audioHandler != nil {
		audioMode := internal.AudioCrossfade
		if tt == transition.TransitionDip {
			audioMode = internal.AudioDipToSilence
		}
		audioHandler.OnTransitionStart(fromSource, sourceKey, audioMode, durationMs)
	}
	s.notifyStateChange(snapshot)
	return nil
}

// SetTransitionPosition sets the T-bar position during an active transition.
func (s *Switcher) SetTransitionPosition(ctx context.Context, position float64) error {
	s.mu.RLock()
	engine := s.transEngine
	inTrans := s.inTransition
	audioHandler := s.audioTransition
	s.mu.RUnlock()

	if !inTrans || engine == nil {
		return fmt.Errorf("no active transition")
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
func (s *Switcher) FadeToBlack(ctx context.Context) error {
	s.mu.Lock()

	if s.transConfig == nil {
		s.mu.Unlock()
		return fmt.Errorf("transition not configured")
	}

	// Reject if a non-FTB transition is active
	if s.inTransition && !s.ftbActive {
		s.mu.Unlock()
		return fmt.Errorf("cannot FTB while mix/dip transition is active: %w", transition.ErrTransitionActive)
	}

	// Toggle off: FTB is active but transition is complete (fully black).
	// Start a reverse FTB transition to fade back from black.
	if s.ftbActive && !s.inTransition {
		if s.programSource == "" {
			s.mu.Unlock()
			return fmt.Errorf("no program source set")
		}

		fromSource := s.programSource
		bitrate, fps := s.estimateEncoderParams(fromSource)

		var revGroupID uint32
		engine := transition.NewTransitionEngine(transition.EngineConfig{
			DecoderFactory: s.transConfig.DecoderFactory,
			EncoderFactory: s.transConfig.EncoderFactory,
			Bitrate:        bitrate,
			FPS:            fps,
			Output: func(data []byte, isKeyframe bool, pts int64) {
				if isKeyframe {
					revGroupID++
				}
				s.broadcastVideo(annexBToVideoFrame(data, isKeyframe, revGroupID, pts))
			},
			OnComplete: func(aborted bool) {
				s.handleFTBReverseComplete(aborted)
			},
		})

		if err := engine.Start(fromSource, "", transition.TransitionFTBReverse, 1000); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("start FTB reverse: %w", err)
		}

		// Warm up decoder BEFORE publishing the engine (see StartTransition).
		fromGOP := s.gopCache.GetGOP(fromSource)
		for _, cf := range fromGOP {
			engine.WarmupDecode(fromSource, cf.annexB)
		}

		s.transEngine = engine
		s.inTransition = true
		// ftbActive stays true during the reverse transition
		s.transitionsStarted.Add(1)
		audioHandler := s.audioTransition

		s.seq++
		snapshot := s.buildStateLocked()
		s.mu.Unlock()

		slog.Info("switcher: transition started",
			"type", "ftb_reverse", "from", fromSource, "to", "", "duration_ms", 1000)

		if audioHandler != nil {
			// Unmute program audio so the fade-in is audible
			audioHandler.SetProgramMute(false)
			audioHandler.OnTransitionStart(fromSource, "", internal.AudioFadeIn, 1000)
		}
		s.notifyStateChange(snapshot)
		return nil
	}

	if s.programSource == "" {
		s.mu.Unlock()
		return fmt.Errorf("no program source set")
	}

	fromSource := s.programSource
	bitrate, fps := s.estimateEncoderParams(fromSource)

	var ftbGroupID uint32
	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: s.transConfig.DecoderFactory,
		EncoderFactory: s.transConfig.EncoderFactory,
		Bitrate:        bitrate,
		FPS:            fps,
		Output: func(data []byte, isKeyframe bool, pts int64) {
			if isKeyframe {
				ftbGroupID++
			}
			s.broadcastVideo(annexBToVideoFrame(data, isKeyframe, ftbGroupID, pts))
		},
		OnComplete: func(aborted bool) {
			s.handleFTBComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, "", transition.TransitionFTB, 1000); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("start FTB: %w", err)
	}

	// Warm up decoder BEFORE publishing the engine (see StartTransition).
	fromGOP := s.gopCache.GetGOP(fromSource)
	for _, cf := range fromGOP {
		engine.WarmupDecode(fromSource, cf.annexB)
	}

	s.transEngine = engine
	s.inTransition = true
	s.ftbActive = true
	s.transitionsStarted.Add(1)
	audioHandler := s.audioTransition

	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	slog.Info("switcher: transition started",
		"type", "ftb", "from", fromSource, "to", "", "duration_ms", 1000)

	if audioHandler != nil {
		audioHandler.OnTransitionStart(fromSource, "", internal.AudioFadeOut, 1000)
	}
	s.notifyStateChange(snapshot)
	return nil
}

// AbortTransition stops any active transition and restores normal frame routing.
func (s *Switcher) AbortTransition() {
	s.mu.Lock()
	engine := s.transEngine
	wasActive := s.inTransition
	audioHandler := s.audioTransition
	var transType string

	if wasActive {
		if engine != nil {
			transType = string(engine.TransitionType())
		}
		s.inTransition = false
		// When aborting a reverse FTB, keep ftbActive true (screen stays black).
		// For all other transitions (including forward FTB), clear ftbActive.
		if engine != nil && engine.TransitionType() == transition.TransitionFTBReverse {
			// ftbActive stays true — we're still in FTB state
		} else {
			s.ftbActive = false
		}
		s.transEngine = nil
		s.seq++
	}
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if engine != nil {
		engine.Stop()
	}
	if wasActive {
		slog.Warn("switcher: transition aborted", "type", transType, "reason", "manual abort")

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
	if !s.inTransition {
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

	s.inTransition = false
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues(transType).Inc()
	}
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if aborted {
		slog.Warn("switcher: transition aborted", "type", transType, "reason", "engine aborted")
	} else {
		slog.Info("switcher: transition completed", "type", transType)
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
	if !s.inTransition {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	s.inTransition = false
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb").Inc()
	}
	if aborted {
		s.ftbActive = false
	}
	// ftbActive stays true when completed (screen is black)
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if aborted {
		slog.Warn("switcher: transition aborted", "type", "ftb", "reason", "engine aborted")
	} else {
		slog.Info("switcher: FTB activated")
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
// FTB transition finishes. If completed (not aborted), it clears ftbActive
// (screen is now fully visible) and replays the GOP to avoid a keyframe gap.
// If aborted, ftbActive stays true (screen stays black).
func (s *Switcher) handleFTBReverseComplete(aborted bool) {
	s.mu.Lock()
	if !s.inTransition {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	programSource := s.programSource

	s.inTransition = false
	s.transEngine = nil
	s.transitionsCompleted.Add(1)
	if s.promMetrics != nil && !aborted {
		s.promMetrics.TransitionsTotal.WithLabelValues("ftb_reverse").Inc()
	}
	if !aborted {
		s.ftbActive = false
		// Gate passthrough until GOP replay provides a keyframe.
		// The transition encoder's SPS/PPS differ from the source's.
		if ss, ok := s.sources[programSource]; ok {
			ss.pendingIDR = true
			s.idrGateStartNano.Store(time.Now().UnixNano())
		}
	}
	// If aborted, ftbActive stays true (screen remains black)

	var replayFrames []*media.VideoFrame
	if !aborted && programSource != "" {
		replayFrames = s.gopCache.GetOriginalGOP(programSource)
	}

	s.seq++
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
		slog.Warn("switcher: transition aborted", "type", "ftb_reverse", "reason", "engine aborted")
	} else {
		slog.Info("switcher: FTB deactivated")
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
// source key. The delay buffer is attached so per-source lip-sync
// compensation is available.
func (s *Switcher) RegisterSource(key string, relay *distribution.Relay) {
	s.mu.Lock()
	viewer := newSourceViewer(key, s)
	viewer.delayBuffer = s.delayBuffer
	relay.AddViewer(viewer)
	s.sources[key] = &sourceState{key: key, relay: relay, viewer: viewer}
	s.health.registerSource(key)
	s.mu.Unlock()

	slog.Info("switcher: source registered", "source_key", key)
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
	if s.programSource == key {
		s.programSource = ""
	}
	if s.previewSource == key {
		s.previewSource = ""
	}
	s.mu.Unlock()

	slog.Info("switcher: source unregistered", "source_key", key)
}

// Cut performs a hard cut to the named source, making it the program output.
// The previous program source is automatically moved to preview. If the
// source is already on program, Cut is a no-op (Seq is not incremented).
// When an audioCutHandler (mixer) is attached, Cut triggers an audio crossfade
// and AFV program change automatically.
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
		s.seq++
		snapshot = s.buildStateLocked()
		changed = true
	}
	s.mu.Unlock()

	if changed {
		slog.Info("switcher: cut executed", "source", sourceKey, "previous_source", oldProgram)

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
func (s *Switcher) SetPreview(ctx context.Context, sourceKey string) error {
	s.mu.Lock()
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	s.previewSource = sourceKey
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
}

// SetLabel sets a human-readable label for the given source.
func (s *Switcher) SetLabel(ctx context.Context, sourceKey, label string) error {
	s.mu.Lock()
	ss, ok := s.sources[sourceKey]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q: %w", sourceKey, ErrSourceNotFound)
	}
	ss.label = label
	s.seq++
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

	slog.Info("switcher: source delay set", "source_key", sourceKey, "delay_ms", delayMs)

	s.mu.Lock()
	s.seq++
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
		"in_transition":             s.inTransition,
		"ftb_active":                s.ftbActive,
		"seq":                       s.seq,
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
	tally := make(map[string]internal.TallyStatus, len(s.sources))
	sources := make(map[string]internal.SourceInfo, len(s.sources))
	for key := range s.sources {
		tally[key] = internal.TallyIdle
		sources[key] = internal.SourceInfo{
			Key:     key,
			Label:   s.sources[key].label,
			Status:  s.health.status(key),
			DelayMs: int(s.delayBuffer.GetDelay(key) / time.Millisecond),
		}
	}
	if s.programSource != "" {
		tally[s.programSource] = internal.TallyProgram
	}
	if s.previewSource != "" && s.previewSource != s.programSource {
		tally[s.previewSource] = internal.TallyPreview
	}
	transType := "cut"
	if s.inTransition && s.transEngine != nil {
		transType = string(s.transEngine.TransitionType())
	}
	state := internal.ControlRoomState{
		ProgramSource:  s.programSource,
		PreviewSource:  s.previewSource,
		TransitionType: transType,
		InTransition:   s.inTransition,
		FTBActive:      s.ftbActive,
		TallyState:     tally,
		Sources:        sources,
		Seq:            s.seq,
		Timestamp:      time.Now().UnixMilli(),
	}
	if s.inTransition && s.transEngine != nil {
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

// annexBToVideoFrame converts Annex B encoder output to a media.VideoFrame
// with AVC1 WireData and extracted SPS/PPS for keyframes. PTS is passed
// through from the source frame to maintain timestamp continuity.
func annexBToVideoFrame(annexBData []byte, isKeyframe bool, groupID uint32, pts int64) *media.VideoFrame {
	avc1 := codec.AnnexBToAVC1(annexBData)
	frame := &media.VideoFrame{
		PTS:        pts,
		IsKeyframe: isKeyframe,
		WireData:   avc1,
		Codec:      "h264",
		GroupID:    groupID,
	}
	if isKeyframe {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7:
				frame.SPS = nalu
			case 8:
				frame.PPS = nalu
			}
		}
	}
	return frame
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
}

// estimateEncoderParams returns the estimated bitrate (bps) and FPS for the
// given source key, clamped to safe ranges. Returns defaults if no frames
// have been received or the source is not found. Caller must hold s.mu (write lock).
func (s *Switcher) estimateEncoderParams(sourceKey string) (bitrate int, fps float64) {
	ss, ok := s.sources[sourceKey]
	if !ok || ss.frameCount < 2 || ss.avgFPS == 0 {
		return transition.DefaultBitrate, transition.DefaultFPS
	}

	fps = ss.avgFPS
	// Clamp FPS to 15-60 range
	if fps < 15 {
		fps = 15
	} else if fps > 60 {
		fps = 60
	}

	// bitrate = avgFrameSize * fps * 8
	bitrateF := ss.avgFrameSize * fps * 8
	bitrate = int(bitrateF)

	// Clamp bitrate to 1-20 Mbps range
	if bitrate < 1_000_000 {
		bitrate = 1_000_000
	} else if bitrate > 20_000_000 {
		bitrate = 20_000_000
	}

	return bitrate, fps
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
	}
	s.mu.Unlock()

	// Record frame in GOP cache for all sources (uses its own mutex)
	s.gopCache.RecordFrame(sourceKey, frame)

	// Check if transition is active — route both sources to engine
	s.mu.RLock()
	engine := s.transEngine
	inTrans := s.inTransition
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

	// Normal passthrough: RLock for steady-state (pendingIDR is false most of the time).
	s.mu.RLock()
	ss, ok := s.sources[sourceKey]
	if !ok || s.programSource != sourceKey || s.ftbActive {
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

	slog.Debug("switcher: IDR gate cleared", "source", sourceKey, "gate_duration_ms", gateDurationMs)
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
