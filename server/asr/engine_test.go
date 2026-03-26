package asr

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// mockBackend implements Backend for testing Engine dispatch without real whisper.
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
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")

	if got := e.ActiveBackend(); got != "whisper" {
		t.Fatalf("expected active backend 'whisper', got %q", got)
	}
}

func TestEngine_SwitchBackend(t *testing.T) {
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")
	defer e.Close()

	// whisper should be started.
	wb.mu.Lock()
	if !wb.started {
		t.Fatal("expected whisper to be started")
	}
	wb.mu.Unlock()

	// Switch to nemotron.
	if err := e.SwitchBackend("nemotron"); err != nil {
		t.Fatalf("SwitchBackend failed: %v", err)
	}

	// whisper should be stopped, nemotron started.
	wb.mu.Lock()
	if !wb.stopped {
		t.Fatal("expected whisper to be stopped after switch")
	}
	wb.mu.Unlock()

	nb.mu.Lock()
	if !nb.started {
		t.Fatal("expected nemotron to be started after switch")
	}
	nb.mu.Unlock()

	if got := e.ActiveBackend(); got != "nemotron" {
		t.Fatalf("expected active backend 'nemotron', got %q", got)
	}
}

func TestEngine_SwitchBackendUnknown(t *testing.T) {
	wb := newMockBackend("whisper")

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
	defer e.Close()

	err := e.SwitchBackend("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestEngine_IngestAudioDispatches(t *testing.T) {
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")
	defer e.Close()

	pcm := make([]float32, 1024)
	e.IngestAudio(pcm, 0, 48000, 2)
	e.IngestAudio(pcm, 90000, 48000, 2)

	if got := wb.ingestCount.Load(); got != 2 {
		t.Fatalf("expected 2 ingest calls to whisper, got %d", got)
	}
	if got := nb.ingestCount.Load(); got != 0 {
		t.Fatalf("expected 0 ingest calls to nemotron, got %d", got)
	}

	// Switch and ingest again.
	if err := e.SwitchBackend("nemotron"); err != nil {
		t.Fatalf("SwitchBackend failed: %v", err)
	}

	e.IngestAudio(pcm, 180000, 48000, 2)

	if got := wb.ingestCount.Load(); got != 2 {
		t.Fatalf("expected whisper still at 2, got %d", got)
	}
	if got := nb.ingestCount.Load(); got != 1 {
		t.Fatalf("expected 1 ingest call to nemotron, got %d", got)
	}
}

func TestEngine_ResetDispatches(t *testing.T) {
	wb := newMockBackend("whisper")

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
	defer e.Close()

	e.Reset()

	wb.mu.Lock()
	if !wb.resetCalled {
		t.Fatal("expected Reset to be forwarded to active backend")
	}
	wb.mu.Unlock()
}

func TestEngine_CloseAll(t *testing.T) {
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")

	e.Close()

	wb.mu.Lock()
	if !wb.closedFlag {
		t.Fatal("expected whisper to be closed")
	}
	wb.mu.Unlock()

	nb.mu.Lock()
	if !nb.closedFlag {
		t.Fatal("expected nemotron to be closed")
	}
	nb.mu.Unlock()

	// Idempotent.
	e.Close()
}

func TestEngine_AvailableBackends(t *testing.T) {
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")
	defer e.Close()

	got := e.AvailableBackends()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "nemotron" || got[1] != "whisper" {
		t.Fatalf("expected [nemotron whisper], got %v", got)
	}
}

func TestEngine_ModelsDelegate(t *testing.T) {
	wb := newMockBackend("whisper")
	wb.models = []string{"base", "medium", "large"}

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
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
	wb := newMockBackend("whisper")

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
	defer e.Close()

	if err := e.SetModel("medium"); err != nil {
		t.Fatalf("SetModel failed: %v", err)
	}

	if got := e.ModelName(); got != "medium" {
		t.Fatalf("expected model name 'medium', got %q", got)
	}
}

func TestEngine_BackendConfigDelegate(t *testing.T) {
	wb := newMockBackend("whisper")

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
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
	wb := newMockBackend("whisper")

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
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
	wb.mu.Lock()
	cb := wb.onTranscript
	wb.mu.Unlock()

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
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")
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

	// Switch to nemotron — callback should survive.
	if err := e.SwitchBackend("nemotron"); err != nil {
		t.Fatalf("SwitchBackend failed: %v", err)
	}

	nb.mu.Lock()
	cb := nb.onTranscript
	nb.mu.Unlock()

	if cb == nil {
		t.Fatal("expected nemotron to have onTranscript after switch")
	}

	cb([]TranscriptWord{{Text: "bonjour"}}, true)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 || received[0] != "bonjour" {
		t.Fatalf("expected [bonjour], got %v", received)
	}
}

func TestEngine_IngestAfterClose(t *testing.T) {
	wb := newMockBackend("whisper")

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")

	e.Close()

	// Ingest after close should not panic.
	pcm := make([]float32, 1024)
	e.IngestAudio(pcm, 0, 48000, 2)

	if got := wb.ingestCount.Load(); got != 0 {
		t.Fatalf("expected 0 ingests after close, got %d", got)
	}
}

func TestEngine_NewEngineWithBackendsDefault(t *testing.T) {
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	// Default "nemotron" should be the active backend.
	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "nemotron")
	defer e.Close()

	if got := e.ActiveBackend(); got != "nemotron" {
		t.Fatalf("expected active backend 'nemotron', got %q", got)
	}
}

func TestEngine_NewEngineWithBackendsFallsBackIfDefaultMissing(t *testing.T) {
	wb := newMockBackend("whisper")

	// Request "nemotron" as default, but only whisper is available.
	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "nemotron")
	defer e.Close()

	// Should fall back to the only available backend.
	if got := e.ActiveBackend(); got != "whisper" {
		t.Fatalf("expected fallback to 'whisper', got %q", got)
	}
}

func TestEngine_NewEngineNoBackendsPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when no backends provided")
		}
	}()

	newEngineWithBackends(map[string]Backend{}, "whisper")
}

func TestEngine_SwitchBackendAfterClose(t *testing.T) {
	wb := newMockBackend("whisper")
	nb := newMockBackend("nemotron")

	e := newEngineWithBackends(map[string]Backend{
		"whisper":  wb,
		"nemotron": nb,
	}, "whisper")

	e.Close()

	err := e.SwitchBackend("nemotron")
	if err == nil {
		t.Fatal("expected error switching backend after close")
	}
}

func TestEngine_ModelNameDelegate(t *testing.T) {
	wb := newMockBackend("whisper")
	wb.mu.Lock()
	wb.modelName = "ggml-base.bin"
	wb.mu.Unlock()

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
	}, "whisper")
	defer e.Close()

	if got := e.ModelName(); got != "ggml-base.bin" {
		t.Fatalf("expected 'ggml-base.bin', got %q", got)
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
	wb := newMockBackend("whisper")
	eb := &errMockBackend{mockBackend: mockBackend{name: "failing"}}

	e := newEngineWithBackends(map[string]Backend{
		"whisper": wb,
		"failing": eb,
	}, "whisper")
	defer e.Close()

	err := e.SwitchBackend("failing")
	if err == nil {
		t.Fatal("expected error from SwitchBackend when Start fails")
	}

	// Active backend should still be whisper after failed switch.
	if got := e.ActiveBackend(); got != "whisper" {
		t.Fatalf("expected active backend still 'whisper' after failed switch, got %q", got)
	}
}
