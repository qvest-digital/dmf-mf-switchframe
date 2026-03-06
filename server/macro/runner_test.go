package macro

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockTarget records calls made during macro execution.
type mockTarget struct {
	mu      sync.Mutex
	calls   []string
	failOn  string // action name to fail on
}

func (m *mockTarget) Cut(source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "cut:"+source)
	if m.failOn == "cut" {
		return errors.New("cut failed")
	}
	return nil
}

func (m *mockTarget) SetPreview(source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "preview:"+source)
	if m.failOn == "preview" {
		return errors.New("preview failed")
	}
	return nil
}

func (m *mockTarget) StartTransition(transType string, durationMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "transition:"+transType)
	if m.failOn == "transition" {
		return errors.New("transition failed")
	}
	return nil
}

func (m *mockTarget) SetLevel(source string, level float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "set_audio:"+source)
	if m.failOn == "set_audio" {
		return errors.New("set_audio failed")
	}
	return nil
}

func (m *mockTarget) getCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.calls))
	copy(result, m.calls)
	return result
}

func TestRunner_ExecutesStepsInOrder(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "test",
		Steps: []MacroStep{
			{Action: ActionPreview, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionSetAudio, Params: map[string]interface{}{"source": "cam1", "level": float64(-6)}},
		},
	}

	err := Run(context.Background(), macro, target)
	if err != nil {
		t.Fatal(err)
	}

	calls := target.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(calls), calls)
	}
	if calls[0] != "preview:cam1" {
		t.Fatalf("expected preview:cam1, got %s", calls[0])
	}
	if calls[1] != "cut:cam1" {
		t.Fatalf("expected cut:cam1, got %s", calls[1])
	}
	if calls[2] != "set_audio:cam1" {
		t.Fatalf("expected set_audio:cam1, got %s", calls[2])
	}
}

func TestRunner_WaitAction(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "wait-test",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionWait, Params: map[string]interface{}{"ms": float64(50)}},
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam2"}},
		},
	}

	start := time.Now()
	err := Run(context.Background(), macro, target)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("wait should have delayed at least 40ms, got %v", elapsed)
	}

	calls := target.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (cuts), got %d: %v", len(calls), calls)
	}
}

func TestRunner_ContextCancellation(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "cancel-test",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionWait, Params: map[string]interface{}{"ms": float64(5000)}},
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam2"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Run(ctx, macro, target)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	calls := target.getCalls()
	// Should have executed the first cut but not the second (cancelled during wait)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (first cut), got %d: %v", len(calls), calls)
	}
}

func TestRunner_UnknownAction(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "unknown-test",
		Steps: []MacroStep{
			{Action: MacroAction("bogus"), Params: map[string]interface{}{}},
		},
	}

	err := Run(context.Background(), macro, target)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestRunner_TransitionAction(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "transition-test",
		Steps: []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{"type": "mix", "durationMs": float64(500)}},
		},
	}

	err := Run(context.Background(), macro, target)
	if err != nil {
		t.Fatal(err)
	}

	calls := target.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0] != "transition:mix" {
		t.Fatalf("expected transition:mix, got %s", calls[0])
	}
}

func TestRunner_ActionError(t *testing.T) {
	target := &mockTarget{failOn: "cut"}
	macro := Macro{
		Name: "fail-test",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}},
		},
	}

	err := Run(context.Background(), macro, target)
	if err == nil {
		t.Fatal("expected error from failed cut")
	}

	// Second step should not have been called
	calls := target.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (failed cut only), got %d: %v", len(calls), calls)
	}
}
