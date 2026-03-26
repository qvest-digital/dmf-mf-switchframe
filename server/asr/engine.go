package asr

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
)

// EngineConfig configures the ASR engine with one or more backends.
type EngineConfig struct {
	// DefaultBackend is the name of the backend to activate on startup.
	// If not available, the first available backend is used.
	DefaultBackend string

	// Whisper configures the whisper.cpp backend. nil = don't register.
	Whisper *WhisperBackendConfig

	// Nemotron configures the Nemotron backend. nil = don't register.
	Nemotron *NemotronConfig
}

// Engine is the ASR orchestrator. It dispatches audio to the active Backend
// and delegates all model/config operations. Hot-swapping between backends
// is supported at runtime via SwitchBackend.
//
// The IngestAudio hot path uses atomic.Pointer for lock-free dispatch.
// All other methods take the mutex.
type Engine struct {
	mu          sync.Mutex
	active      atomic.Pointer[Backend]
	backends    map[string]Backend
	lazyConfigs map[string]interface{} // configs for backends not yet initialized

	onTranscript func(words []TranscriptWord, isFinal bool)
	closed       bool
	doneCh       chan struct{}
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
			panic("asr: no backend could be started")
		}
	}

	e.active.Store(&b)
	return e
}

// NewEngine creates a new ASR engine. It eagerly initializes only the default
// backend to conserve GPU memory. Non-default backends are lazy-loaded on
// first SwitchBackend call.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	backends := make(map[string]Backend)
	configs := make(map[string]interface{})

	// Determine default backend name.
	defaultName := cfg.DefaultBackend
	if defaultName == "" {
		if cfg.Whisper != nil {
			defaultName = "whisper"
		} else if cfg.Nemotron != nil {
			defaultName = "nemotron"
		}
	}

	// Eagerly initialize the default backend. Store configs for lazy init of others.
	if cfg.Whisper != nil {
		if defaultName == "whisper" {
			b, err := NewWhisperBackend(*cfg.Whisper)
			if err != nil {
				slog.Warn("ASR: whisper backend unavailable", "error", err)
			} else {
				backends["whisper"] = b
			}
		} else {
			configs["whisper"] = cfg.Whisper
		}
	}

	if cfg.Nemotron != nil {
		if defaultName == "nemotron" {
			b, err := NewNemotronBackend(*cfg.Nemotron)
			if err != nil {
				slog.Warn("ASR: nemotron backend unavailable", "error", err)
			} else {
				backends["nemotron"] = b
			}
		} else {
			configs["nemotron"] = cfg.Nemotron
		}
	}

	if len(backends) == 0 {
		return nil, fmt.Errorf("asr: no backends available")
	}

	// Track available backend names (including lazy ones).
	available := make(map[string]bool)
	for name := range backends {
		available[name] = true
	}
	for name := range configs {
		available[name] = true
	}

	if defaultName == "" {
		// Pick first available, preferring whisper.
		if _, ok := backends["whisper"]; ok {
			defaultName = "whisper"
		} else {
			names := make([]string, 0, len(backends))
			for name := range backends {
				names = append(names, name)
			}
			sort.Strings(names)
			defaultName = names[0]
		}
	}

	e := newEngineWithBackends(backends, defaultName)
	e.lazyConfigs = configs
	return e, nil
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
	if bp == nil {
		return
	}
	(*bp).IngestAudio(pcm, pts, sampleRate, channels)
}

// ActiveBackend returns the name of the currently active backend.
func (e *Engine) ActiveBackend() string {
	bp := e.active.Load()
	if bp == nil {
		return ""
	}
	return (*bp).Name()
}

// AvailableBackends returns a sorted list of registered backend names,
// including backends that haven't been initialized yet (lazy-loaded).
func (e *Engine) AvailableBackends() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := make(map[string]bool)
	for name := range e.backends {
		seen[name] = true
	}
	for name := range e.lazyConfigs {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SwitchBackend stops the current backend and starts the named one.
// The onTranscript callback is preserved across the switch.
func (e *Engine) SwitchBackend(name string) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return fmt.Errorf("asr: engine closed")
	}

	newBackend, ok := e.backends[name]
	if !ok {
		// Try lazy initialization from stored config.
		lazyCfg, hasLazy := e.lazyConfigs[name]
		e.mu.Unlock()
		if !hasLazy {
			return fmt.Errorf("asr: unknown backend %q", name)
		}
		var err error
		switch name {
		case "whisper":
			cfg := lazyCfg.(*WhisperBackendConfig)
			newBackend, err = NewWhisperBackend(*cfg)
		case "nemotron":
			cfg := lazyCfg.(*NemotronConfig)
			newBackend, err = NewNemotronBackend(*cfg)
		default:
			return fmt.Errorf("asr: unknown backend %q", name)
		}
		if err != nil {
			return fmt.Errorf("asr: initialize %s backend: %w", name, err)
		}
		slog.Info("ASR: lazy-initialized backend", "name", name)
		e.mu.Lock()
		e.backends[name] = newBackend
		delete(e.lazyConfigs, name)
	}

	// Build callback wrapper that reads onTranscript through the Engine mutex.
	cb := func(words []TranscriptWord, isFinal bool) {
		e.mu.Lock()
		fn := e.onTranscript
		e.mu.Unlock()
		if fn != nil {
			fn(words, isFinal)
		}
	}

	// Get current active backend.
	bp := e.active.Load()
	e.mu.Unlock()

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

	// Atomic swap.
	e.active.Store(&newBackend)
	return nil
}

// Models returns the available models for the active backend.
func (e *Engine) Models() []string {
	bp := e.active.Load()
	if bp == nil {
		return nil
	}
	return (*bp).Models()
}

// SetModel swaps the model within the active backend.
func (e *Engine) SetModel(name string) error {
	bp := e.active.Load()
	if bp == nil {
		return fmt.Errorf("asr: no active backend")
	}
	return (*bp).SetModel(name)
}

// ModelName returns the display name of the current model.
func (e *Engine) ModelName() string {
	bp := e.active.Load()
	if bp == nil {
		return ""
	}
	return (*bp).ModelName()
}

// BackendConfig returns the active backend's configuration as JSON.
func (e *Engine) BackendConfig() json.RawMessage {
	bp := e.active.Load()
	if bp == nil {
		return nil
	}
	return (*bp).Config()
}

// SetBackendConfig applies configuration to the active backend.
func (e *Engine) SetBackendConfig(raw json.RawMessage) error {
	bp := e.active.Load()
	if bp == nil {
		return fmt.Errorf("asr: no active backend")
	}
	return (*bp).SetConfig(raw)
}

// Reset clears the active backend's audio buffer and inference state.
func (e *Engine) Reset() {
	bp := e.active.Load()
	if bp == nil {
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

// ModelPath returns the path to a model file given a model name.
// Searches the standard whisper.cpp models directory.
func ModelPath(modelsDir, name string) string {
	return modelsDir + "/ggml-" + name + ".bin"
}
