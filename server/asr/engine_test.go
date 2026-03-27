package asr

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// mockBackend implements Backend for testing Engine dispatch without real backends.
type mockBackend struct {
	name        string
	started     bool
	stopped     bool
	closedFlag  bool
	resetCalled bool
	ingestCount atomic.Int64
	modelName   string
	models      []string
	configJSON  json.RawMessage

	onTranscript func([]TranscriptWord, bool)
	mu           sync.Mutex
}

func newMockBackend(name string) *mockBackend {
	return &mockBackend{
		name:      name,
		modelName: name + "-default-model",
		models:    []string{name + "-small", name + "-large"},
	}
}

func (m *mockBackend) Name() string { return m.name }

func (m *mockBackend) Start(onTranscript func([]TranscriptWord, bool)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.stopped = false
	m.onTranscript = onTranscript
	return nil
}

func (m *mockBackend) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	m.ingestCount.Add(1)
}

func (m *mockBackend) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resetCalled = true
}

func (m *mockBackend) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	m.onTranscript = nil
}

func (m *mockBackend) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closedFlag = true
}

func (m *mockBackend) Models() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.models
}

func (m *mockBackend) SetModel(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelName = name
	return nil
}

func (m *mockBackend) ModelName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modelName
}

func (m *mockBackend) Config() json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.configJSON != nil {
		return m.configJSON
	}
	return json.RawMessage(`{"mock":true}`)
}

func (m *mockBackend) SetConfig(raw json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configJSON = raw
	return nil
}

// --- Tests ---

func TestEngine_ActiveBackend(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")

	if got := e.ActiveBackend(); got != "alpha" {
		t.Fatalf("expected active backend 'alpha', got %q", got)
	}
}

func TestEngine_SwitchBackend(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")
	defer e.Close()

	// alpha should be started.
	a.mu.Lock()
	if !a.started {
		t.Fatal("expected alpha to be started")
	}
	a.mu.Unlock()

	// Switch to beta.
	if err := e.SwitchBackend("beta"); err != nil {
		t.Fatalf("SwitchBackend failed: %v", err)
	}

	// alpha should be stopped, beta started.
	a.mu.Lock()
	if !a.stopped {
		t.Fatal("expected alpha to be stopped after switch")
	}
	a.mu.Unlock()

	b.mu.Lock()
	if !b.started {
		t.Fatal("expected beta to be started after switch")
	}
	b.mu.Unlock()

	if got := e.ActiveBackend(); got != "beta" {
		t.Fatalf("expected active backend 'beta', got %q", got)
	}
}

func TestEngine_SwitchBackendUnknownProxy(t *testing.T) {
	a := newMockBackend("alpha")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	// Unknown backend names are proxied through the active backend's SetConfig.
	// The mock backend accepts any config, so this should succeed.
	err := e.SwitchBackend("nonexistent")
	if err != nil {
		t.Fatalf("expected proxy to succeed, got: %v", err)
	}

	// Verify SetConfig was called with the backend switch payload.
	a.mu.Lock()
	cfg := a.configJSON
	a.mu.Unlock()
	if cfg == nil {
		t.Fatal("expected SetConfig to be called on active backend")
	}
	var parsed map[string]string
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if parsed["backend"] != "nonexistent" {
		t.Errorf("expected backend='nonexistent' in config, got %q", parsed["backend"])
	}
}


func TestEngine_IngestAudioDispatches(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")
	defer e.Close()

	pcm := make([]float32, 1024)
	e.IngestAudio(pcm, 0, 48000, 2)
	e.IngestAudio(pcm, 90000, 48000, 2)

	if got := a.ingestCount.Load(); got != 2 {
		t.Fatalf("expected 2 ingest calls to alpha, got %d", got)
	}
	if got := b.ingestCount.Load(); got != 0 {
		t.Fatalf("expected 0 ingest calls to beta, got %d", got)
	}

	// Switch and ingest again.
	if err := e.SwitchBackend("beta"); err != nil {
		t.Fatalf("SwitchBackend failed: %v", err)
	}

	e.IngestAudio(pcm, 180000, 48000, 2)

	if got := a.ingestCount.Load(); got != 2 {
		t.Fatalf("expected alpha still at 2, got %d", got)
	}
	if got := b.ingestCount.Load(); got != 1 {
		t.Fatalf("expected 1 ingest call to beta, got %d", got)
	}
}

func TestEngine_ResetDispatches(t *testing.T) {
	a := newMockBackend("alpha")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	e.Reset()

	a.mu.Lock()
	if !a.resetCalled {
		t.Fatal("expected Reset to be forwarded to active backend")
	}
	a.mu.Unlock()
}

func TestEngine_CloseAll(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")

	e.Close()

	a.mu.Lock()
	if !a.closedFlag {
		t.Fatal("expected alpha to be closed")
	}
	a.mu.Unlock()

	b.mu.Lock()
	if !b.closedFlag {
		t.Fatal("expected beta to be closed")
	}
	b.mu.Unlock()

	// Idempotent.
	e.Close()
}

func TestEngine_AvailableBackends(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")
	defer e.Close()

	got := e.AvailableBackends()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("expected [alpha beta], got %v", got)
	}
}

func TestEngine_ModelsDelegate(t *testing.T) {
	a := newMockBackend("alpha")
	a.models = []string{"base", "medium", "large"}

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	models := e.Models()
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}
	if models[0] != "base" || models[1] != "medium" || models[2] != "large" {
		t.Fatalf("unexpected models: %v", models)
	}
}

func TestEngine_SetModelDelegate(t *testing.T) {
	a := newMockBackend("alpha")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	if err := e.SetModel("medium"); err != nil {
		t.Fatalf("SetModel failed: %v", err)
	}

	if got := e.ModelName(); got != "medium" {
		t.Fatalf("expected model name 'medium', got %q", got)
	}
}

func TestEngine_BackendConfigDelegate(t *testing.T) {
	a := newMockBackend("alpha")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	raw := e.BackendConfig()
	if string(raw) != `{"mock":true}` {
		t.Fatalf("expected default config, got %s", raw)
	}

	newCfg := json.RawMessage(`{"language":"fr"}`)
	if err := e.SetBackendConfig(newCfg); err != nil {
		t.Fatalf("SetBackendConfig failed: %v", err)
	}

	got := e.BackendConfig()
	if string(got) != `{"language":"fr"}` {
		t.Fatalf("expected updated config, got %s", got)
	}
}

func TestEngine_OnTranscriptWired(t *testing.T) {
	a := newMockBackend("alpha")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	var mu sync.Mutex
	var received []TranscriptWord
	var gotFinal bool

	e.OnTranscript(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		received = append(received, words...)
		gotFinal = isFinal
		mu.Unlock()
	})

	// Simulate the backend calling the transcript callback.
	a.mu.Lock()
	cb := a.onTranscript
	a.mu.Unlock()

	if cb == nil {
		t.Fatal("expected backend to have onTranscript set after Engine.OnTranscript")
	}

	cb([]TranscriptWord{{Text: "hello", Confidence: 0.9}}, false)
	cb([]TranscriptWord{{Text: "world", Confidence: 0.95}}, true)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Fatalf("expected 2 words, got %d", len(received))
	}
	if received[0].Text != "hello" {
		t.Fatalf("expected first word 'hello', got %q", received[0].Text)
	}
	if received[1].Text != "world" {
		t.Fatalf("expected second word 'world', got %q", received[1].Text)
	}
	if !gotFinal {
		t.Fatal("expected isFinal=true for last callback")
	}
}

func TestEngine_OnTranscriptSurvivesSwitchBackend(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")
	defer e.Close()

	var mu sync.Mutex
	var received []string

	e.OnTranscript(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		for _, w := range words {
			received = append(received, w.Text)
		}
		mu.Unlock()
	})

	// Switch to beta — callback should survive.
	if err := e.SwitchBackend("beta"); err != nil {
		t.Fatalf("SwitchBackend failed: %v", err)
	}

	b.mu.Lock()
	cb := b.onTranscript
	b.mu.Unlock()

	if cb == nil {
		t.Fatal("expected beta to have onTranscript after switch")
	}

	cb([]TranscriptWord{{Text: "bonjour"}}, true)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 || received[0] != "bonjour" {
		t.Fatalf("expected [bonjour], got %v", received)
	}
}

func TestEngine_IngestAfterClose(t *testing.T) {
	a := newMockBackend("alpha")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")

	e.Close()

	// Ingest after close should not panic.
	pcm := make([]float32, 1024)
	e.IngestAudio(pcm, 0, 48000, 2)

	if got := a.ingestCount.Load(); got != 0 {
		t.Fatalf("expected 0 ingests after close, got %d", got)
	}
}

func TestEngine_NewEngineWithBackendsDefault(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	// Default "beta" should be the active backend.
	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "beta")
	defer e.Close()

	if got := e.ActiveBackend(); got != "beta" {
		t.Fatalf("expected active backend 'beta', got %q", got)
	}
}

func TestEngine_NewEngineWithBackendsFallsBackIfDefaultMissing(t *testing.T) {
	a := newMockBackend("alpha")

	// Request "beta" as default, but only alpha is available.
	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "beta")
	defer e.Close()

	// Should fall back to the only available backend.
	if got := e.ActiveBackend(); got != "alpha" {
		t.Fatalf("expected fallback to 'alpha', got %q", got)
	}
}

func TestEngine_NewEngineNoBackendsPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when no backends provided")
		}
	}()

	newEngineWithBackends(map[string]Backend{}, "alpha")
}

func TestEngine_SwitchBackendAfterClose(t *testing.T) {
	a := newMockBackend("alpha")
	b := newMockBackend("beta")

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
		"beta":  b,
	}, "alpha")

	e.Close()

	err := e.SwitchBackend("beta")
	if err == nil {
		t.Fatal("expected error switching backend after close")
	}
}

func TestEngine_ModelNameDelegate(t *testing.T) {
	a := newMockBackend("alpha")
	a.mu.Lock()
	a.modelName = "parakeet-tdt-1.1b"
	a.mu.Unlock()

	e := newEngineWithBackends(map[string]Backend{
		"alpha": a,
	}, "alpha")
	defer e.Close()

	if got := e.ModelName(); got != "parakeet-tdt-1.1b" {
		t.Fatalf("expected 'parakeet-tdt-1.1b', got %q", got)
	}
}

// errMockBackend is a mock that fails Start().
type errMockBackend struct {
	mockBackend
}

func (m *errMockBackend) Start(onTranscript func([]TranscriptWord, bool)) error {
	return fmt.Errorf("start failed")
}

func TestEngine_SwitchBackendStartError(t *testing.T) {
	a := newMockBackend("alpha")
	eb := &errMockBackend{mockBackend: mockBackend{name: "failing"}}

	e := newEngineWithBackends(map[string]Backend{
		"alpha":   a,
		"failing": eb,
	}, "alpha")
	defer e.Close()

	err := e.SwitchBackend("failing")
	if err == nil {
		t.Fatal("expected error from SwitchBackend when Start fails")
	}

	// Active backend should still be alpha after failed switch.
	if got := e.ActiveBackend(); got != "alpha" {
		t.Fatalf("expected active backend still 'alpha' after failed switch, got %q", got)
	}
}

func TestEngine_SwitchToSidecar(t *testing.T) {
	a := newMockBackend("alpha")
	sb := newMockBackend("sidecar")

	e := newEngineWithBackends(map[string]Backend{
		"alpha":   a,
		"sidecar": sb,
	}, "alpha")
	defer e.Close()

	if err := e.SwitchBackend("sidecar"); err != nil {
		t.Fatalf("SwitchBackend: %v", err)
	}
	if e.ActiveBackend() != "sidecar" {
		t.Fatalf("active = %q, want sidecar", e.ActiveBackend())
	}

	// Verify alpha was stopped and sidecar was started.
	a.mu.Lock()
	if !a.stopped {
		t.Fatal("expected alpha to be stopped after switch")
	}
	a.mu.Unlock()

	sb.mu.Lock()
	if !sb.started {
		t.Fatal("expected sidecar to be started after switch")
	}
	sb.mu.Unlock()
}

func TestEngine_SidecarInAvailableBackends(t *testing.T) {
	a := newMockBackend("alpha")
	sb := newMockBackend("sidecar")

	e := newEngineWithBackends(map[string]Backend{
		"alpha":   a,
		"sidecar": sb,
	}, "alpha")
	defer e.Close()

	got := e.AvailableBackends()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "sidecar" {
		t.Fatalf("expected [alpha sidecar], got %v", got)
	}
}

func TestEngine_SidecarDefaultBackend(t *testing.T) {
	sb := newMockBackend("sidecar")

	e := newEngineWithBackends(map[string]Backend{
		"sidecar": sb,
	}, "sidecar")
	defer e.Close()

	if got := e.ActiveBackend(); got != "sidecar" {
		t.Fatalf("expected active backend 'sidecar', got %q", got)
	}
}
