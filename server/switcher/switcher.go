// Package switcher implements the core video switching state machine.
// It receives tagged frames from sourceViewer proxies and forwards only
// the current program source's frames to the program Relay.
package switcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
)

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
	mu            sync.RWMutex
	sources       map[string]*sourceState
	programSource string
	previewSource string
	programRelay  *distribution.Relay
	seq           uint64
	stateCallbacks []func(internal.ControlRoomState)
	health        *healthMonitor
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
func (s *Switcher) Cut(ctx context.Context, sourceKey string) error {
	var snapshot internal.ControlRoomState
	changed := false

	s.mu.Lock()
	if _, ok := s.sources[sourceKey]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("source %q not found", sourceKey)
	}
	if s.programSource != sourceKey {
		oldProgram := s.programSource
		s.programSource = sourceKey
		s.sources[sourceKey].pendingIDR = true
		if oldProgram != "" {
			s.previewSource = oldProgram
		}
		s.seq++
		snapshot = s.buildStateLocked()
		changed = true
	}
	s.mu.Unlock()

	if changed {
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
	return internal.ControlRoomState{
		ProgramSource:  s.programSource,
		PreviewSource:  s.previewSource,
		TransitionType: "cut",
		TallyState:     tally,
		Sources:        sources,
		Seq:            s.seq,
		Timestamp:      time.Now().UnixMilli(),
	}
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

	s.mu.Lock()
	ss, ok := s.sources[sourceKey]
	if !ok || s.programSource != sourceKey {
		s.mu.Unlock()
		return
	}
	if ss.pendingIDR {
		if !frame.IsKeyframe {
			s.mu.Unlock()
			return
		}
		ss.pendingIDR = false
	}
	s.mu.Unlock()
	s.programRelay.BroadcastVideo(frame)
}

// handleAudioFrame implements frameHandler. It is called by sourceViewers
// when an audio frame arrives from a source. Only frames from the current
// program source are forwarded to the program Relay. Audio is gated along
// with video until the first keyframe after a cut.
func (s *Switcher) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	s.mu.RLock()
	ss, ok := s.sources[sourceKey]
	if !ok || s.programSource != sourceKey || ss.pendingIDR {
		s.mu.RUnlock()
		return
	}
	s.mu.RUnlock()
	s.programRelay.BroadcastAudio(frame)
}
