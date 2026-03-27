package asr

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
)

// EngineConfig configures the ASR engine.
type EngineConfig struct {
	// Sidecar configures the ASR sidecar subprocess backend.
	Sidecar *SidecarConfig
}

// Engine is the ASR orchestrator. It dispatches audio to the active Backend
// and delegates all model/config operations. Hot-swapping between backends
// is supported at runtime via SwitchBackend.
//
// The IngestAudio hot path uses atomic.Pointer for lock-free dispatch.
// All other methods take the mutex.
type Engine struct {
	mu       sync.Mutex
	active   atomic.Pointer[Backend]
	backends map[string]Backend

	onTranscript    func(words []TranscriptWord, isFinal bool)
	onBackendSwitch atomic.Pointer[func()] // called after async backend switch
	closed          bool
	doneCh          chan struct{}
}

// newEngineWithBackends creates an Engine from pre-built backends.
// Used by tests (with mocks) and by NewEngine (with real backends).
// Panics if backends is empty — caller must provide at least one.
func newEngineWithBackends(backends map[string]Backend, defaultName string) *Engine {
	if len(backends) == 0 {
		panic("asr: newEngineWithBackends called with no backends")
	}

	e := &Engine{
		backends: backends,
		doneCh:   make(chan struct{}),
	}

	// Pick the default backend. If the requested default isn't available,
	// fall back to any available backend (sorted for determinism).
	b, ok := backends[defaultName]
	if !ok {
		names := make([]string, 0, len(backends))
		for name := range backends {
			names = append(names, name)
		}
		sort.Strings(names)
		b = backends[names[0]]
	}

	// Start the default backend with a callback wrapper that reads
	// onTranscript through the Engine mutex.
	cb := func(words []TranscriptWord, isFinal bool) {
		e.mu.Lock()
		fn := e.onTranscript
		e.mu.Unlock()
		if fn != nil {
			fn(words, isFinal)
		}
	}
	if err := b.Start(cb); err != nil {
		slog.Warn("ASR: failed to start default backend, trying others",
			"backend", b.Name(), "error", err)
		// Try other backends.
		started := false
		names := make([]string, 0, len(backends))
		for name := range backends {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == b.Name() {
				continue
			}
			candidate := backends[name]
			if err := candidate.Start(cb); err == nil {
				b = candidate
				started = true
				break
			}
		}
		if !started {
			slog.Warn("ASR: no backend could be started, captions unavailable")
		}
	}

	// Store via map lookup for a stable heap pointer.
	stable := e.backends[b.Name()]
	e.active.Store(&stable)
	return e
}

// NewEngine creates a new ASR engine with the sidecar backend.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Sidecar == nil {
		return nil, fmt.Errorf("asr: sidecar config is required")
	}

	b, err := NewSidecarBackend(*cfg.Sidecar)
	if err != nil {
		return nil, fmt.Errorf("asr: sidecar backend: %w", err)
	}

	return newEngineWithBackends(map[string]Backend{
		"sidecar": b,
	}, "sidecar"), nil
}

// OnTranscript sets the callback for transcript updates. The backend's
// existing closure (wired in newEngineWithBackends or SwitchBackend)
// reads e.onTranscript under lock on each invocation, so updating the
// field is sufficient — no need to re-call Start().
func (e *Engine) OnTranscript(fn func([]TranscriptWord, bool)) {
	e.mu.Lock()
	e.onTranscript = fn
	e.mu.Unlock()
}

// IngestAudio forwards PCM audio to the active backend. Lock-free hot path
// via atomic pointer read.
func (e *Engine) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return
	}
	(*bp).IngestAudio(pcm, pts, sampleRate, channels)
}

// ActiveBackend returns the name of the currently active backend.
func (e *Engine) ActiveBackend() string {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return ""
	}
	return (*bp).Name()
}

// AvailableBackends returns the list of available backend names.
// If the active backend is a SidecarBackend, returns the sidecar's internal
// backend list (e.g., ["parakeet", "whisper"]) instead of engine-level names.
func (e *Engine) AvailableBackends() []string {
	bp := e.active.Load()
	if bp != nil {
		// Check if the active backend exposes available sub-backends.
		if sb, ok := (*bp).(*SidecarBackend); ok {
			if p := sb.cachedAvailableBackends.Load(); p != nil {
				return *p
			}
		}
	}
	// Fallback: engine-level backends.
	e.mu.Lock()
	defer e.mu.Unlock()
	names := make([]string, 0, len(e.backends))
	for name := range e.backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SwitchBackend stops the current backend and starts the named one.
// The onTranscript callback is preserved across the switch.
//
// If the name matches a direct engine-level backend, a full stop/start swap
// is performed. Otherwise, the switch is proxied through the active backend's
// SetConfig — this handles sidecar-internal backend switching (e.g., "whisper"
// to "parakeet") without restarting the sidecar process.
func (e *Engine) SwitchBackend(name string) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return fmt.Errorf("asr: engine closed")
	}

	// Already on requested backend — no-op.
	bp := e.active.Load()
	if bp != nil && (*bp).Name() == name {
		e.mu.Unlock()
		return nil
	}

	// Try direct engine backend first.
	newBackend, ok := e.backends[name]
	if ok {
		e.mu.Unlock()

		// Build callback wrapper that reads onTranscript through the Engine mutex.
		cb := func(words []TranscriptWord, isFinal bool) {
			e.mu.Lock()
			fn := e.onTranscript
			e.mu.Unlock()
			if fn != nil {
				fn(words, isFinal)
			}
		}

		// Stop old backend.
		if bp != nil {
			(*bp).Stop()
		}

		// Start new backend.
		if err := newBackend.Start(cb); err != nil {
			// Re-start old backend on failure.
			if bp != nil {
				_ = (*bp).Start(cb)
			}
			return fmt.Errorf("asr: failed to start backend %q: %w", name, err)
		}

		// Atomic swap — load from map for a stable heap pointer.
		e.mu.Lock()
		stable := e.backends[name]
		e.mu.Unlock()
		e.active.Store(&stable)

		if fn := e.onBackendSwitch.Load(); fn != nil {
			(*fn)()
		}
		return nil
	}
	e.mu.Unlock()

	// Not a direct backend — proxy through active backend's SetConfig.
	// This handles sidecar-internal backend switching (e.g., "whisper" <-> "parakeet").
	if bp != nil {
		cfg, _ := json.Marshal(map[string]string{"backend": name})
		if err := (*bp).SetConfig(json.RawMessage(cfg)); err != nil {
			return fmt.Errorf("asr: switch backend %q: %w", name, err)
		}
		if fn := e.onBackendSwitch.Load(); fn != nil {
			(*fn)()
		}
		return nil
	}
	return fmt.Errorf("asr: unknown backend %q", name)
}

// OnBackendSwitch registers a callback invoked after an async backend switch completes.
func (e *Engine) OnBackendSwitch(fn func()) {
	e.onBackendSwitch.Store(&fn)
}

// Models returns the available models for the active backend.
func (e *Engine) Models() []string {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return nil
	}
	return (*bp).Models()
}

// SetModel swaps the model within the active backend.
func (e *Engine) SetModel(name string) error {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return fmt.Errorf("asr: no active backend")
	}
	return (*bp).SetModel(name)
}

// ModelName returns the display name of the current model.
func (e *Engine) ModelName() string {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return ""
	}
	return (*bp).ModelName()
}

// BackendConfig returns the active backend's configuration as JSON.
func (e *Engine) BackendConfig() json.RawMessage {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return nil
	}
	return (*bp).Config()
}

// SetBackendConfig applies configuration to the active backend.
func (e *Engine) SetBackendConfig(raw json.RawMessage) error {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return fmt.Errorf("asr: no active backend")
	}
	return (*bp).SetConfig(raw)
}

// Reset clears the active backend's audio buffer and inference state.
func (e *Engine) Reset() {
	bp := e.active.Load()
	if bp == nil || *bp == nil {
		return
	}
	(*bp).Reset()
}

// Close shuts down ALL registered backends and releases resources. Idempotent.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.doneCh)

	// Nil out the active pointer so IngestAudio becomes a no-op.
	e.active.Store(nil)

	// Copy backend list under lock.
	backends := make([]Backend, 0, len(e.backends))
	for _, b := range e.backends {
		backends = append(backends, b)
	}
	e.mu.Unlock()

	// Close all backends outside the lock.
	for _, b := range backends {
		b.Close()
	}
}
