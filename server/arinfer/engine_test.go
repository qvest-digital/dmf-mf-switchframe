package arinfer

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNewEngine(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		cfg := Config{
			ModelsDir: "/tmp/models",
		}
		e := New(cfg)
		if e == nil {
			t.Fatal("expected non-nil engine")
		}
		defer e.Close()

		// MaxInferenceMs should default to 20 when 0
		if e.cfg.MaxInferenceMs != 20 {
			t.Errorf("expected MaxInferenceMs=20, got %d", e.cfg.MaxInferenceMs)
		}
	})

	t.Run("explicit config preserved", func(t *testing.T) {
		cfg := Config{
			ModelsDir:      "/tmp/models",
			DepthEnabled:   true,
			SAMEnabled:     true,
			MaxInferenceMs: 10,
		}
		e := New(cfg)
		defer e.Close()

		if e.cfg.MaxInferenceMs != 10 {
			t.Errorf("expected MaxInferenceMs=10, got %d", e.cfg.MaxInferenceMs)
		}
		if !e.cfg.DepthEnabled {
			t.Error("expected DepthEnabled=true")
		}
		if !e.cfg.SAMEnabled {
			t.Error("expected SAMEnabled=true")
		}
	})
}

func TestEngineClose(t *testing.T) {
	t.Run("close sets closed flag", func(t *testing.T) {
		e := New(Config{ModelsDir: "/tmp/models"})
		if e.closed.Load() {
			t.Fatal("expected closed=false initially")
		}
		e.Close()
		if !e.closed.Load() {
			t.Fatal("expected closed=true after Close()")
		}
	})

	t.Run("double close is safe", func(t *testing.T) {
		e := New(Config{ModelsDir: "/tmp/models"})
		e.Close()
		e.Close() // must not panic
	})
}

func TestEngineStatus(t *testing.T) {
	t.Run("initial status is all zeros", func(t *testing.T) {
		e := New(Config{ModelsDir: "/tmp/models"})
		defer e.Close()

		s := e.Status()
		if s.DepthReady {
			t.Error("expected DepthReady=false")
		}
		if s.SAMReady {
			t.Error("expected SAMReady=false")
		}
		if s.DepthLatencyMs != 0 {
			t.Errorf("expected DepthLatencyMs=0, got %f", s.DepthLatencyMs)
		}
		if s.SAMLatencyMs != 0 {
			t.Errorf("expected SAMLatencyMs=0, got %f", s.SAMLatencyMs)
		}
		if s.FramesProcessed != 0 {
			t.Errorf("expected FramesProcessed=0, got %d", s.FramesProcessed)
		}
		if s.FramesSkipped != 0 {
			t.Errorf("expected FramesSkipped=0, got %d", s.FramesSkipped)
		}
		if s.GPUMemUsedMB != 0 {
			t.Errorf("expected GPUMemUsedMB=0, got %f", s.GPUMemUsedMB)
		}
	})
}

func TestEngineLastOcclusion(t *testing.T) {
	t.Run("returns nil initially", func(t *testing.T) {
		e := New(Config{ModelsDir: "/tmp/models"})
		defer e.Close()

		om := e.LastOcclusion()
		if om != nil {
			t.Errorf("expected nil LastOcclusion, got %+v", om)
		}
	})
}

func TestStatusJSON(t *testing.T) {
	s := Status{
		DepthReady:      true,
		SAMReady:        false,
		DepthLatencyMs:  4.5,
		SAMLatencyMs:    8.2,
		FramesProcessed: 1000,
		FramesSkipped:   5,
		GPUMemUsedMB:    256.5,
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var s2 Status
	if err := json.Unmarshal(data, &s2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if s2.DepthReady != s.DepthReady {
		t.Errorf("DepthReady mismatch: got %v, want %v", s2.DepthReady, s.DepthReady)
	}
	if s2.SAMReady != s.SAMReady {
		t.Errorf("SAMReady mismatch: got %v, want %v", s2.SAMReady, s.SAMReady)
	}
	if s2.DepthLatencyMs != s.DepthLatencyMs {
		t.Errorf("DepthLatencyMs mismatch: got %f, want %f", s2.DepthLatencyMs, s.DepthLatencyMs)
	}
	if s2.SAMLatencyMs != s.SAMLatencyMs {
		t.Errorf("SAMLatencyMs mismatch: got %f, want %f", s2.SAMLatencyMs, s.SAMLatencyMs)
	}
	if s2.FramesProcessed != s.FramesProcessed {
		t.Errorf("FramesProcessed mismatch: got %d, want %d", s2.FramesProcessed, s.FramesProcessed)
	}
	if s2.FramesSkipped != s.FramesSkipped {
		t.Errorf("FramesSkipped mismatch: got %d, want %d", s2.FramesSkipped, s.FramesSkipped)
	}
	if s2.GPUMemUsedMB != s.GPUMemUsedMB {
		t.Errorf("GPUMemUsedMB mismatch: got %f, want %f", s2.GPUMemUsedMB, s.GPUMemUsedMB)
	}
}

func TestSAMPromptJSON(t *testing.T) {
	t.Run("point prompt round-trip", func(t *testing.T) {
		p := SAMPrompt{
			ID:     "prompt-1",
			Type:   "point",
			Points: [][2]int{{100, 200}, {300, 400}},
			Label:  "person",
		}

		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var p2 SAMPrompt
		if err := json.Unmarshal(data, &p2); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if p2.ID != p.ID {
			t.Errorf("ID mismatch: got %q, want %q", p2.ID, p.ID)
		}
		if p2.Type != p.Type {
			t.Errorf("Type mismatch: got %q, want %q", p2.Type, p.Type)
		}
		if len(p2.Points) != len(p.Points) {
			t.Fatalf("Points len mismatch: got %d, want %d", len(p2.Points), len(p.Points))
		}
		for i := range p.Points {
			if p2.Points[i] != p.Points[i] {
				t.Errorf("Points[%d] mismatch: got %v, want %v", i, p2.Points[i], p.Points[i])
			}
		}
		if p2.Label != p.Label {
			t.Errorf("Label mismatch: got %q, want %q", p2.Label, p.Label)
		}
		// Box should be zero-valued
		if p2.Box != [4]int{} {
			t.Errorf("Box should be zero for point prompt, got %v", p2.Box)
		}
	})

	t.Run("box prompt round-trip", func(t *testing.T) {
		p := SAMPrompt{
			ID:    "prompt-2",
			Type:  "box",
			Box:   [4]int{10, 20, 300, 400},
			Label: "table",
		}

		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var p2 SAMPrompt
		if err := json.Unmarshal(data, &p2); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if p2.ID != p.ID {
			t.Errorf("ID mismatch: got %q, want %q", p2.ID, p.ID)
		}
		if p2.Type != p.Type {
			t.Errorf("Type mismatch: got %q, want %q", p2.Type, p.Type)
		}
		if p2.Box != p.Box {
			t.Errorf("Box mismatch: got %v, want %v", p2.Box, p.Box)
		}
		if p2.Label != p.Label {
			t.Errorf("Label mismatch: got %q, want %q", p2.Label, p.Label)
		}
		// Points should be nil/empty for box prompt
		if len(p2.Points) != 0 {
			t.Errorf("Points should be empty for box prompt, got %v", p2.Points)
		}
	})

	t.Run("omitempty fields", func(t *testing.T) {
		p := SAMPrompt{
			ID:    "prompt-3",
			Type:  "point",
			Points: [][2]int{{50, 60}},
			Label: "test",
		}

		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		// Box should be omitted since it's zero-valued with omitempty
		s := string(data)
		// box is [4]int with omitempty — Go omits zero-value arrays only if
		// the tag uses omitempty and the value is the zero value.
		// Actually [4]int{0,0,0,0} is NOT considered empty by encoding/json.
		// Let's just verify the JSON parses correctly.
		var p2 SAMPrompt
		if err := json.Unmarshal([]byte(s), &p2); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p2.ID != "prompt-3" {
			t.Errorf("unexpected ID: %q", p2.ID)
		}
	})
}

func TestOcclusionMap(t *testing.T) {
	t.Run("fields are accessible", func(t *testing.T) {
		om := OcclusionMap{
			MaskPtr:  0x1000,
			DepthPtr: 0x2000,
			Width:    1920,
			Height:   1080,
			DepthW:   384,
			DepthH:   216,
			Stale:    false,
		}

		if om.Width != 1920 {
			t.Errorf("expected Width=1920, got %d", om.Width)
		}
		if om.Height != 1080 {
			t.Errorf("expected Height=1080, got %d", om.Height)
		}
		if om.DepthW != 384 {
			t.Errorf("expected DepthW=384, got %d", om.DepthW)
		}
		if om.DepthH != 216 {
			t.Errorf("expected DepthH=216, got %d", om.DepthH)
		}
		if om.Stale {
			t.Error("expected Stale=false")
		}
	})
}

func TestSentinelErrors(t *testing.T) {
	errors := []struct {
		err  error
		text string
	}{
		{ErrNotReady, "arinfer: engine not ready"},
		{ErrModelMissing, "arinfer: model file not found"},
		{ErrPromptFailed, "arinfer: SAM prompt failed"},
		{ErrDisabled, "arinfer: feature disabled"},
	}

	for _, tc := range errors {
		if tc.err.Error() != tc.text {
			t.Errorf("error %q: got %q, want %q", tc.text, tc.err.Error(), tc.text)
		}
	}
}

func TestEnginePrompts(t *testing.T) {
	t.Run("initial prompts are empty", func(t *testing.T) {
		e := New(Config{ModelsDir: "/tmp/models"})
		defer e.Close()

		prompts := e.Prompts()
		if len(prompts) != 0 {
			t.Errorf("expected 0 prompts initially, got %d", len(prompts))
		}
	})
}

// --- Mock models for engine-level testing ---

// mockDepthModel is a test double for depthModel that tracks calls.
type mockDepthModel struct {
	inferCount int
	inferErr   error
	closed     bool
	outputAddr uintptr // simulated output buffer address
}

func (m *mockDepthModel) Infer(inputPtr uintptr, w, h, pitch int, outputPtr uintptr) error {
	m.inferCount++
	return m.inferErr
}

func (m *mockDepthModel) OutputPtr() uintptr {
	return m.outputAddr
}

func (m *mockDepthModel) Close() {
	m.closed = true
}

// mockSAMModel is a test double for samModel that tracks calls.
type mockSAMModel struct {
	propagateCount int
	propagateErr   error
	prompts        []SAMPrompt
	closed         bool
	maskAddr       uintptr // simulated mask output buffer address
}

func (m *mockSAMModel) Encode(inputPtr uintptr, w, h, pitch int) error {
	return nil
}

func (m *mockSAMModel) Propagate(inputPtr uintptr, w, h, pitch int, outputPtr uintptr) error {
	m.propagateCount++
	return m.propagateErr
}

func (m *mockSAMModel) MaskOutputPtr() uintptr {
	return m.maskAddr
}

func (m *mockSAMModel) AddPrompt(p SAMPrompt) error {
	for _, existing := range m.prompts {
		if existing.ID == p.ID {
			return ErrPromptFailed
		}
	}
	m.prompts = append(m.prompts, p)
	return nil
}

func (m *mockSAMModel) RemovePrompt(id string) error {
	for i, p := range m.prompts {
		if p.ID == id {
			m.prompts = append(m.prompts[:i], m.prompts[i+1:]...)
			return nil
		}
	}
	return ErrPromptFailed
}

func (m *mockSAMModel) Prompts() []SAMPrompt {
	if len(m.prompts) == 0 {
		return nil
	}
	out := make([]SAMPrompt, len(m.prompts))
	copy(out, m.prompts)
	return out
}

func (m *mockSAMModel) Close() {
	m.closed = true
}

// --- ProcessFrame tests ---

func TestProcessFrame_NoModels(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	// With no models enabled, ProcessFrame should be a no-op.
	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if got := e.framesProcessed.Load(); got != 0 {
		t.Errorf("framesProcessed = %d, want 0", got)
	}
	if got := e.framesSkipped.Load(); got != 0 {
		t.Errorf("framesSkipped = %d, want 0", got)
	}
	if occ := e.LastOcclusion(); occ != nil {
		t.Errorf("LastOcclusion = %+v, want nil", occ)
	}
}

func TestProcessFrame_ClosedEngine(t *testing.T) {
	e := New(Config{})
	e.Close()

	// ProcessFrame on a closed engine should be a no-op.
	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if got := e.framesProcessed.Load(); got != 0 {
		t.Errorf("framesProcessed = %d, want 0", got)
	}
}

func TestProcessFrame_DepthOnly(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockDepthModel{outputAddr: 0xDEAD}
	e.mu.Lock()
	e.depth = mock
	e.depthW = 518
	e.depthH = 294
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if mock.inferCount != 1 {
		t.Errorf("depth Infer called %d times, want 1", mock.inferCount)
	}
	if got := e.framesProcessed.Load(); got != 1 {
		t.Errorf("framesProcessed = %d, want 1", got)
	}

	occ := e.LastOcclusion()
	if occ == nil {
		t.Fatal("LastOcclusion = nil, want non-nil")
	}
	if occ.Width != 1920 || occ.Height != 1080 {
		t.Errorf("OcclusionMap dims = %dx%d, want 1920x1080", occ.Width, occ.Height)
	}
	if occ.DepthW != 518 || occ.DepthH != 294 {
		t.Errorf("OcclusionMap depth dims = %dx%d, want 518x294", occ.DepthW, occ.DepthH)
	}
	if occ.DepthPtr != 0xDEAD {
		t.Errorf("OcclusionMap.DepthPtr = 0x%x, want 0xDEAD", occ.DepthPtr)
	}

	depthLat, _ := e.depthLatencyMs.Load().(float64)
	if depthLat < 0 {
		t.Errorf("depthLatencyMs = %f, want >= 0", depthLat)
	}
}

func TestProcessFrame_DepthError(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockDepthModel{inferErr: errors.New("GPU error")}
	e.mu.Lock()
	e.depth = mock
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if got := e.framesSkipped.Load(); got != 1 {
		t.Errorf("framesSkipped = %d, want 1", got)
	}
	if got := e.framesProcessed.Load(); got != 0 {
		t.Errorf("framesProcessed = %d, want 0 after depth error", got)
	}
}

func TestProcessFrame_SAMOnly(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{maskAddr: 0xBEEF}
	e.mu.Lock()
	e.sam = mock
	e.prompts = []SAMPrompt{{ID: "p1", Type: "point"}}
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if mock.propagateCount != 1 {
		t.Errorf("SAM Propagate called %d times, want 1", mock.propagateCount)
	}
	if got := e.framesProcessed.Load(); got != 1 {
		t.Errorf("framesProcessed = %d, want 1", got)
	}

	occ := e.LastOcclusion()
	if occ == nil {
		t.Fatal("LastOcclusion = nil, want non-nil")
	}
	if occ.MaskPtr != 0xBEEF {
		t.Errorf("OcclusionMap.MaskPtr = 0x%x, want 0xBEEF", occ.MaskPtr)
	}
}

func TestProcessFrame_SAMWithoutPrompts(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{}
	e.mu.Lock()
	e.sam = mock
	// No prompts — SAM should not run.
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if mock.propagateCount != 0 {
		t.Errorf("SAM Propagate called %d times, want 0 (no prompts)", mock.propagateCount)
	}
	if got := e.framesProcessed.Load(); got != 0 {
		t.Errorf("framesProcessed = %d, want 0 (nothing to do)", got)
	}
}

func TestProcessFrame_BothModels(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	depthMock := &mockDepthModel{}
	samMock := &mockSAMModel{}

	e.mu.Lock()
	e.depth = depthMock
	e.sam = samMock
	e.prompts = []SAMPrompt{{ID: "p1", Type: "point"}}
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	if depthMock.inferCount != 1 {
		t.Errorf("depth Infer called %d times, want 1", depthMock.inferCount)
	}
	if samMock.propagateCount != 1 {
		t.Errorf("SAM Propagate called %d times, want 1", samMock.propagateCount)
	}
	if got := e.framesProcessed.Load(); got != 1 {
		t.Errorf("framesProcessed = %d, want 1", got)
	}
}

func TestProcessFrame_SAMError_ContinuesWithDepth(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	depthMock := &mockDepthModel{}
	samMock := &mockSAMModel{propagateErr: errors.New("SAM failed")}

	e.mu.Lock()
	e.depth = depthMock
	e.sam = samMock
	e.prompts = []SAMPrompt{{ID: "p1", Type: "point"}}
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	// Depth succeeded, SAM failed — frame should still be counted.
	if got := e.framesProcessed.Load(); got != 1 {
		t.Errorf("framesProcessed = %d, want 1", got)
	}
	if got := e.framesSkipped.Load(); got != 0 {
		t.Errorf("framesSkipped = %d, want 0", got)
	}
}

func TestProcessFrame_MultipleFrames(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockDepthModel{}
	e.mu.Lock()
	e.depth = mock
	e.mu.Unlock()

	for i := 0; i < 10; i++ {
		e.ProcessFrame(0x1000, 1920, 1080, 1920)
	}

	if mock.inferCount != 10 {
		t.Errorf("depth Infer called %d times, want 10", mock.inferCount)
	}
	if got := e.framesProcessed.Load(); got != 10 {
		t.Errorf("framesProcessed = %d, want 10", got)
	}
}

// --- EnableDepth / DisableDepth tests ---

func TestEnableDepth_InvalidPathReturnsError(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	err := e.EnableDepth("/nonexistent/model.plan")
	if err == nil {
		t.Fatal("EnableDepth should return error for nonexistent model")
	}

	s := e.Status()
	if s.DepthReady {
		t.Error("Status.DepthReady = true after failed EnableDepth")
	}
}

func TestEnableDepth_DoubleEnable(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	// Both calls should fail with nonexistent model path.
	err1 := e.EnableDepth("/model.plan")
	err2 := e.EnableDepth("/model.plan")

	if err1 == nil {
		t.Error("first EnableDepth should return error for nonexistent path")
	}
	if err2 == nil {
		t.Error("second EnableDepth should return error for nonexistent path")
	}
}

func TestDisableDepth_NilSafe(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	// DisableDepth on nil depth model should be a safe no-op.
	e.DisableDepth()

	s := e.Status()
	if s.DepthReady {
		t.Error("Status.DepthReady = true after DisableDepth on nil")
	}
}

func TestDisableDepth_ClearsOcclusion(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	// Manually store an occlusion map, then disable depth.
	e.lastOcclusion.Store(&OcclusionMap{Width: 100, Height: 100})

	e.DisableDepth()

	if occ := e.LastOcclusion(); occ != nil {
		t.Errorf("LastOcclusion = %+v, want nil after DisableDepth", occ)
	}
}

func TestDisableDepth_WithModel(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockDepthModel{}
	e.mu.Lock()
	e.depth = mock
	e.mu.Unlock()

	e.DisableDepth()

	if !mock.closed {
		t.Error("depth model not closed by DisableDepth")
	}

	s := e.Status()
	if s.DepthReady {
		t.Error("Status.DepthReady = true after DisableDepth")
	}
}

// --- EnableSAM / DisableSAM tests ---

func TestEnableSAM_StubReturnsError(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	err := e.EnableSAM("/nonexistent/encoder.plan", "/nonexistent/decoder.plan")
	if err == nil {
		t.Fatal("EnableSAM should return error for nonexistent model")
	}

	s := e.Status()
	if s.SAMReady {
		t.Error("Status.SAMReady = true after failed EnableSAM")
	}
}

func TestEnableSAM_DoubleEnable(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	err1 := e.EnableSAM("/enc.plan", "/dec.plan")
	err2 := e.EnableSAM("/enc.plan", "/dec.plan")

	if err1 == nil {
		t.Error("first EnableSAM should return error for nonexistent path")
	}
	if err2 == nil {
		t.Error("second EnableSAM should return error for nonexistent path")
	}
}

func TestDisableSAM_NilSafe(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	// DisableSAM on nil SAM model should be a safe no-op.
	e.DisableSAM()

	s := e.Status()
	if s.SAMReady {
		t.Error("Status.SAMReady = true after DisableSAM on nil")
	}
}

func TestDisableSAM_ClearsPrompts(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	// Manually set prompts, then disable SAM.
	e.mu.Lock()
	e.prompts = []SAMPrompt{{ID: "test", Type: "point"}}
	e.mu.Unlock()

	e.DisableSAM()

	if prompts := e.Prompts(); prompts != nil {
		t.Errorf("Prompts = %v, want nil after DisableSAM", prompts)
	}
}

func TestDisableSAM_WithModel(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{
		prompts: []SAMPrompt{{ID: "p1", Type: "point"}},
	}
	e.mu.Lock()
	e.sam = mock
	e.prompts = mock.prompts
	e.mu.Unlock()

	e.DisableSAM()

	if !mock.closed {
		t.Error("SAM model not closed by DisableSAM")
	}

	s := e.Status()
	if s.SAMReady {
		t.Error("Status.SAMReady = true after DisableSAM")
	}

	if prompts := e.Prompts(); prompts != nil {
		t.Errorf("Prompts = %v, want nil after DisableSAM", prompts)
	}
}

// --- AddPrompt / RemovePrompt tests ---

func TestAddPrompt_SAMNotReady(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	err := e.AddPrompt(SAMPrompt{
		ID:     "test-1",
		Type:   "point",
		Points: [][2]int{{100, 200}},
		Label:  "person",
	})
	if !errors.Is(err, ErrNotReady) {
		t.Errorf("AddPrompt error = %v, want ErrNotReady", err)
	}
}

func TestRemovePrompt_SAMNotReady(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	err := e.RemovePrompt("nonexistent")
	if !errors.Is(err, ErrNotReady) {
		t.Errorf("RemovePrompt error = %v, want ErrNotReady", err)
	}
}

func TestAddPrompt_WithMockSAM(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{}
	e.mu.Lock()
	e.sam = mock
	e.mu.Unlock()

	err := e.AddPrompt(SAMPrompt{ID: "p1", Type: "point", Points: [][2]int{{100, 200}}})
	if err != nil {
		t.Fatalf("AddPrompt failed: %v", err)
	}

	prompts := e.Prompts()
	if len(prompts) != 1 {
		t.Fatalf("Prompts len = %d, want 1", len(prompts))
	}
	if prompts[0].ID != "p1" {
		t.Errorf("prompt ID = %q, want %q", prompts[0].ID, "p1")
	}
}

func TestRemovePrompt_WithMockSAM(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{
		prompts: []SAMPrompt{{ID: "p1", Type: "point"}},
	}
	e.mu.Lock()
	e.sam = mock
	e.prompts = mock.prompts
	e.mu.Unlock()

	err := e.RemovePrompt("p1")
	if err != nil {
		t.Fatalf("RemovePrompt failed: %v", err)
	}

	prompts := e.Prompts()
	if prompts != nil {
		t.Errorf("Prompts = %v, want nil after removal", prompts)
	}
}

func TestAddPrompt_DuplicateID(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{}
	e.mu.Lock()
	e.sam = mock
	e.mu.Unlock()

	if err := e.AddPrompt(SAMPrompt{ID: "p1", Type: "point"}); err != nil {
		t.Fatalf("first AddPrompt failed: %v", err)
	}
	err := e.AddPrompt(SAMPrompt{ID: "p1", Type: "point"})
	if !errors.Is(err, ErrPromptFailed) {
		t.Errorf("duplicate AddPrompt error = %v, want ErrPromptFailed", err)
	}
}

func TestRemovePrompt_NotFound(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	mock := &mockSAMModel{}
	e.mu.Lock()
	e.sam = mock
	e.mu.Unlock()

	err := e.RemovePrompt("nonexistent")
	if !errors.Is(err, ErrPromptFailed) {
		t.Errorf("RemovePrompt error = %v, want ErrPromptFailed", err)
	}
}

// --- Status with models ---

func TestStatus_WithModels(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	e.mu.Lock()
	e.depth = &mockDepthModel{}
	e.sam = &mockSAMModel{}
	e.mu.Unlock()

	s := e.Status()
	if !s.DepthReady {
		t.Error("Status.DepthReady = false, want true")
	}
	if !s.SAMReady {
		t.Error("Status.SAMReady = false, want true")
	}
}

func TestClose_ReleasesModels(t *testing.T) {
	e := New(Config{})

	depthMock := &mockDepthModel{}
	samMock := &mockSAMModel{}

	e.mu.Lock()
	e.depth = depthMock
	e.sam = samMock
	e.mu.Unlock()

	e.Close()

	if !depthMock.closed {
		t.Error("depth model not closed")
	}
	if !samMock.closed {
		t.Error("SAM model not closed")
	}

	s := e.Status()
	if s.DepthReady {
		t.Error("Status.DepthReady = true after Close")
	}
	if s.SAMReady {
		t.Error("Status.SAMReady = true after Close")
	}
}

func TestStatus_LatencyUpdatedByProcessFrame(t *testing.T) {
	e := New(Config{})
	defer e.Close()

	depthMock := &mockDepthModel{}
	samMock := &mockSAMModel{}

	e.mu.Lock()
	e.depth = depthMock
	e.sam = samMock
	e.prompts = []SAMPrompt{{ID: "p1", Type: "point"}}
	e.mu.Unlock()

	e.ProcessFrame(0x1000, 1920, 1080, 1920)

	s := e.Status()
	// Latency should be >= 0 after processing.
	if s.DepthLatencyMs < 0 {
		t.Errorf("DepthLatencyMs = %f, want >= 0", s.DepthLatencyMs)
	}
	if s.SAMLatencyMs < 0 {
		t.Errorf("SAMLatencyMs = %f, want >= 0", s.SAMLatencyMs)
	}
	if s.FramesProcessed != 1 {
		t.Errorf("FramesProcessed = %d, want 1", s.FramesProcessed)
	}
}
