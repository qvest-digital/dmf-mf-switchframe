// Package switcher implements the core video switching state machine.
// It receives tagged frames from sourceViewer proxies and forwards only
// the current program source's frames to the program Relay.
package switcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/transition"
)

// audioStateProvider is the interface the Switcher needs from the AudioMixer
// to populate audio fields in state broadcasts.
type audioStateProvider interface {
	ProgramPeak() [2]float64
	ChannelStates() map[string]internal.AudioChannel
	MasterLevel() float64
}

// audioCutHandler is called during a cut to trigger audio crossfade.
type audioCutHandler interface {
	OnCut(oldSource, newSource string)
	OnProgramChange(newProgramSource string)
}

// audioTransitionHandler is called during transitions to sync audio crossfade
// with video dissolve progress.
type audioTransitionHandler interface {
	OnTransitionStart(oldSource, newSource string, durationMs int)
	OnTransitionPosition(position float64)
	OnTransitionComplete()
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
}

// Compile-time check that Switcher implements the frameHandler interface.
var _ frameHandler = (*Switcher)(nil)

// New creates a Switcher that forwards program frames to programRelay.
func New(programRelay *distribution.Relay) *Switcher {
	return &Switcher{
		sources:      make(map[string]*sourceState),
		programRelay: programRelay,
		health:       newHealthMonitor(nil),
	}
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

// Close stops the health monitor and unregisters all sources.
func (s *Switcher) Close() {
	s.health.stop()
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

// StartTransition begins a mix/dip transition from the current program source
// to the given target source. Frames from both sources are routed to the
// TransitionEngine which produces blended output on the program relay.
func (s *Switcher) StartTransition(ctx context.Context, sourceKey string, transType string, durationMs int) error {
	s.mu.Lock()

	if s.transConfig == nil {
		s.mu.Unlock()
		return fmt.Errorf("transition not configured")
	}
	if s.inTransition {
		s.mu.Unlock()
		return fmt.Errorf("transition already active")
	}
	if s.ftbActive {
		s.mu.Unlock()
		return fmt.Errorf("cannot start transition while FTB is active")
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
		return fmt.Errorf("source %q not found", sourceKey)
	}

	tt := transition.TransitionType(transType)
	if tt != transition.TransitionMix && tt != transition.TransitionDip {
		s.mu.Unlock()
		return fmt.Errorf("unsupported transition type: %q", transType)
	}

	fromSource := s.programSource
	programRelay := s.programRelay

	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: s.transConfig.DecoderFactory,
		EncoderFactory: s.transConfig.EncoderFactory,
		Output: func(data []byte, isKeyframe bool) {
			programRelay.BroadcastVideo(&media.VideoFrame{
				PTS:        time.Now().UnixMilli(),
				IsKeyframe: isKeyframe,
				WireData:   data,
			})
		},
		OnComplete: func(aborted bool) {
			s.handleTransitionComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, sourceKey, tt, durationMs); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("start transition: %w", err)
	}

	s.transEngine = engine
	s.inTransition = true
	s.previewSource = sourceKey
	audioHandler := s.audioTransition
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if audioHandler != nil {
		audioHandler.OnTransitionStart(fromSource, sourceKey, durationMs)
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
		return fmt.Errorf("cannot FTB while mix/dip transition is active")
	}

	// Toggle off: FTB is active but transition is complete (fully black)
	if s.ftbActive && !s.inTransition {
		s.ftbActive = false
		s.seq++
		snapshot := s.buildStateLocked()
		s.mu.Unlock()
		s.notifyStateChange(snapshot)
		return nil
	}

	if s.programSource == "" {
		s.mu.Unlock()
		return fmt.Errorf("no program source set")
	}

	fromSource := s.programSource
	programRelay := s.programRelay

	engine := transition.NewTransitionEngine(transition.EngineConfig{
		DecoderFactory: s.transConfig.DecoderFactory,
		EncoderFactory: s.transConfig.EncoderFactory,
		Output: func(data []byte, isKeyframe bool) {
			programRelay.BroadcastVideo(&media.VideoFrame{
				PTS:        time.Now().UnixMilli(),
				IsKeyframe: isKeyframe,
				WireData:   data,
			})
		},
		OnComplete: func(aborted bool) {
			s.handleFTBComplete(aborted)
		},
	})

	if err := engine.Start(fromSource, "", transition.TransitionFTB, 1000); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("start FTB: %w", err)
	}

	s.transEngine = engine
	s.inTransition = true
	s.ftbActive = true
	audioHandler := s.audioTransition
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if audioHandler != nil {
		audioHandler.OnTransitionStart(fromSource, "", 1000)
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

	if wasActive {
		s.inTransition = false
		s.ftbActive = false
		s.transEngine = nil
		s.seq++
	}
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if engine != nil {
		engine.Stop()
	}
	if wasActive {
		if audioHandler != nil {
			audioHandler.OnTransitionComplete()
		}
		s.notifyStateChange(snapshot)
	}
}

// handleTransitionComplete is called by the TransitionEngine when a mix/dip
// transition finishes. If completed (not aborted), it swaps program/preview
// sources.
func (s *Switcher) handleTransitionComplete(aborted bool) {
	s.mu.Lock()
	if !s.inTransition {
		s.mu.Unlock()
		return
	}

	audioHandler := s.audioTransition
	var audioCut audioCutHandler

	if !aborted && s.transEngine != nil {
		newProgram := s.transEngine.ToSource()
		oldProgram := s.programSource
		if newProgram != "" {
			s.programSource = newProgram
			s.previewSource = oldProgram
			audioCut = s.audioCut
		}
	}

	s.inTransition = false
	s.transEngine = nil
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

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
	if aborted {
		s.ftbActive = false
	}
	// ftbActive stays true when completed (screen is black)
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	if audioHandler != nil {
		audioHandler.OnTransitionComplete()
	}
	s.notifyStateChange(snapshot)
}

// RegisterSource adds a source to the switcher. A sourceViewer proxy is
// created and attached to the source's Relay so that frames flow into the
// Switcher's handleVideoFrame/handleAudioFrame methods tagged with the
// source key.
func (s *Switcher) RegisterSource(key string, relay *distribution.Relay) {
	s.mu.Lock()
	defer s.mu.Unlock()
	viewer := newSourceViewer(key, s)
	relay.AddViewer(viewer)
	s.sources[key] = &sourceState{key: key, relay: relay, viewer: viewer}
	s.health.registerSource(key)
}

// UnregisterSource removes a source from the switcher and detaches its
// viewer from the source Relay. If the removed source was on program or
// preview, those fields are cleared.
func (s *Switcher) UnregisterSource(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sources[key]
	if !ok {
		return
	}
	ss.relay.RemoveViewer(ss.viewer.ID())
	delete(s.sources, key)
	s.health.removeSource(key)
	if s.programSource == key {
		s.programSource = ""
	}
	if s.previewSource == key {
		s.previewSource = ""
	}
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
		return fmt.Errorf("source %q not found", sourceKey)
	}
	if s.programSource != sourceKey {
		oldProgram = s.programSource
		s.programSource = sourceKey
		s.sources[sourceKey].pendingIDR = true
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
		return fmt.Errorf("source %q not found", sourceKey)
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
		return fmt.Errorf("source %q not found", sourceKey)
	}
	ss.label = label
	s.seq++
	snapshot := s.buildStateLocked()
	s.mu.Unlock()

	s.notifyStateChange(snapshot)
	return nil
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
		sources[key] = internal.SourceInfo{Key: key, Label: s.sources[key].label, Status: s.health.status(key)}
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

// handleVideoFrame implements frameHandler. It is called by sourceViewers
// when a video frame arrives from a source. Only frames from the current
// program source are forwarded to the program Relay. After a cut, frames
// are gated until the first keyframe (IDR) to prevent decoder artifacts.
func (s *Switcher) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	s.health.recordFrame(sourceKey)

	// Check if transition is active — route both sources to engine
	s.mu.RLock()
	engine := s.transEngine
	inTrans := s.inTransition
	s.mu.RUnlock()

	if inTrans && engine != nil {
		engine.IngestFrame(sourceKey, frame.WireData)
		return
	}

	// Normal passthrough: RLock for steady-state (pendingIDR is false most of the time).
	s.mu.RLock()
	ss, ok := s.sources[sourceKey]
	if !ok || s.programSource != sourceKey {
		s.mu.RUnlock()
		return
	}
	if !ss.pendingIDR {
		s.mu.RUnlock()
		s.programRelay.BroadcastVideo(frame)
		return
	}
	s.mu.RUnlock()

	// Slow path: pendingIDR is true. Need write lock to clear it.
	if !frame.IsKeyframe {
		return
	}
	s.mu.Lock()
	// Re-check under write lock (another goroutine may have cleared it).
	if ss.pendingIDR {
		ss.pendingIDR = false
	}
	s.mu.Unlock()
	s.programRelay.BroadcastVideo(frame)
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
