package macro

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockTarget records calls made during macro execution.
type mockTarget struct {
	mu     sync.Mutex
	calls  []string
	failOn string // action name to fail on
}

func (m *mockTarget) Cut(ctx context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "cut:"+source)
	if m.failOn == "cut" {
		return errors.New("cut failed")
	}
	return nil
}

func (m *mockTarget) SetPreview(ctx context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "preview:"+source)
	if m.failOn == "preview" {
		return errors.New("preview failed")
	}
	return nil
}

func (m *mockTarget) StartTransition(ctx context.Context, source string, transType string, durationMs int, wipeDirection, stingerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, fmt.Sprintf("transition:%s:%s:%s:%s", source, transType, wipeDirection, stingerName))
	if m.failOn == "transition" {
		return errors.New("transition failed")
	}
	return nil
}

func (m *mockTarget) SetLevel(ctx context.Context, source string, level float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "set_audio:"+source)
	if m.failOn == "set_audio" {
		return errors.New("set_audio failed")
	}
	return nil
}

func (m *mockTarget) Execute(ctx context.Context, action string, params map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "execute:"+action)
	if m.failOn == action {
		return fmt.Errorf("%s failed", action)
	}
	return nil
}

func (m *mockTarget) SCTE35Cue(ctx context.Context, params map[string]interface{}) (uint32, error) {
	return 0, nil
}

func (m *mockTarget) SCTE35Return(ctx context.Context, eventID uint32) error {
	return nil
}

func (m *mockTarget) SCTE35Cancel(ctx context.Context, eventID uint32) error {
	return nil
}

func (m *mockTarget) SCTE35Hold(ctx context.Context, eventID uint32) error {
	return nil
}

func (m *mockTarget) SCTE35Extend(ctx context.Context, eventID uint32, durationMs int64) error {
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
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 3)
	require.Equal(t, "preview:cam1", calls[0])
	require.Equal(t, "cut:cam1", calls[1])
	require.Equal(t, "set_audio:cam1", calls[2])
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

	require.NoError(t, err)
	require.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "wait should have delayed at least 40ms")

	calls := target.getCalls()
	require.Len(t, calls, 2)
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
	require.Error(t, err, "expected context cancellation error")
	require.ErrorIs(t, err, context.Canceled)

	calls := target.getCalls()
	// Should have executed the first cut but not the second (cancelled during wait)
	require.Len(t, calls, 1)
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
	require.Error(t, err, "expected error for unknown action")
}

func TestRunner_TransitionAction(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "transition-test",
		Steps: []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{"source": "cam1", "type": "mix", "durationMs": float64(500)}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 1)
	// mix transition with empty wipeDirection and stingerName
	require.Equal(t, "transition:cam1:mix::", calls[0])
}

func TestRunner_TransitionWithSource(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "transition-source-test",
		Steps: []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{"source": "cam2", "type": "mix", "durationMs": float64(1000)}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "transition:cam2:mix::", calls[0])
}

func TestRunner_TransitionMissingSource(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "transition-no-source",
		Steps: []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{"type": "mix"}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.Error(t, err, "expected error for transition without source")
	require.True(t, strings.Contains(err.Error(), "source"),
		"error should mention 'source', got: %s", err.Error())
}

func TestRunner_ContextCancellationDuringWait(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "ctx-cancel-test",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionWait, Params: map[string]interface{}{"ms": float64(5000)}},
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam2"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := Run(ctx, macro, target)
	elapsed := time.Since(start)

	require.Error(t, err, "expected context cancellation error")
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, elapsed, 1*time.Second, "should have cancelled quickly")

	calls := target.getCalls()
	require.Len(t, calls, 1)
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
	require.Error(t, err, "expected error from failed cut")

	// Second step should not have been called
	calls := target.getCalls()
	require.Len(t, calls, 1)
}

// --- New tests for macro system overhaul ---

func TestRunner_TransitionWipeDirection(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "wipe-test",
		Steps: []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{
				"source":        "cam1",
				"type":          "wipe",
				"durationMs":    float64(500),
				"wipeDirection": "h-left",
			}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "transition:cam1:wipe:h-left:", calls[0])
}

func TestRunner_TransitionStingerName(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "stinger-test",
		Steps: []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{
				"source":      "cam1",
				"type":        "stinger",
				"durationMs":  float64(1000),
				"stingerName": "intro",
			}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "transition:cam1:stinger::intro", calls[0])
}

func TestRunner_ExecuteDispatch(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "execute-test",
		Steps: []MacroStep{
			{Action: ActionFTB, Params: map[string]interface{}{}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "execute:ftb", calls[0])
}

func TestRunner_UnknownActionErrors(t *testing.T) {
	target := &mockTarget{}
	macro := Macro{
		Name: "unknown-action-test",
		Steps: []MacroStep{
			{Action: MacroAction("totally_bogus"), Params: map[string]interface{}{}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.Error(t, err, "expected error for truly unknown action")
	require.Contains(t, err.Error(), "unknown action")
	require.Contains(t, err.Error(), "totally_bogus")
}

func TestRunner_ExecuteDispatchAllNewActions(t *testing.T) {
	// Verify a selection of new actions all route through Execute
	newActions := []MacroAction{
		ActionFTB,
		ActionAudioMute,
		ActionAudioAFV,
		ActionAudioTrim,
		ActionAudioMaster,
		ActionAudioEQ,
		ActionAudioCompressor,
		ActionAudioDelay,
		ActionGraphicsOn,
		ActionGraphicsOff,
		ActionGraphicsAutoOn,
		ActionGraphicsAutoOff,
		ActionRecordingStart,
		ActionRecordingStop,
		ActionPresetRecall,
		ActionKeySet,
		ActionKeyDelete,
		ActionSourceLabel,
		ActionSourceDelay,
		ActionSourcePosition,
		ActionReplayMarkIn,
		ActionReplayMarkOut,
		ActionReplayPlay,
		ActionReplayStop,
		ActionReplayQuickClip,
		ActionReplayPlayLast,
		ActionReplayPlayClip,
	}

	for _, action := range newActions {
		t.Run(string(action), func(t *testing.T) {
			target := &mockTarget{}
			macro := Macro{
				Name: "dispatch-" + string(action),
				Steps: []MacroStep{
					{Action: action, Params: map[string]interface{}{"test": "value"}},
				},
			}

			err := Run(context.Background(), macro, target)
			require.NoError(t, err)

			calls := target.getCalls()
			require.Len(t, calls, 1)
			require.Equal(t, "execute:"+string(action), calls[0])
		})
	}
}

func TestRunner_SCTE35StillUsesSpecificMethods(t *testing.T) {
	// Verify existing SCTE-35 actions do NOT go through Execute
	target := &mockTarget{}
	macro := Macro{
		Name: "scte35-still-works",
		Steps: []MacroStep{
			{Action: ActionSCTE35Cue, Params: map[string]interface{}{"durationMs": float64(30000)}},
		},
	}

	err := Run(context.Background(), macro, target)
	require.NoError(t, err)

	calls := target.getCalls()
	// SCTE35Cue doesn't record a call in our mock, so calls should be empty
	// This verifies it went through SCTE35Cue, not Execute (which would record "execute:scte35_cue")
	require.Len(t, calls, 0)
}

func TestRunner_AllActionsMapComplete(t *testing.T) {
	// Verify AllActions contains all expected actions
	expectedActions := []MacroAction{
		ActionCut, ActionPreview, ActionTransition, ActionWait, ActionSetAudio,
		ActionSCTE35Cue, ActionSCTE35Return, ActionSCTE35Cancel, ActionSCTE35Hold, ActionSCTE35Extend,
		ActionFTB,
		ActionAudioMute, ActionAudioAFV, ActionAudioTrim, ActionAudioMaster,
		ActionAudioEQ, ActionAudioCompressor, ActionAudioDelay,
		ActionGraphicsOn, ActionGraphicsOff, ActionGraphicsAutoOn, ActionGraphicsAutoOff,
		ActionRecordingStart, ActionRecordingStop,
		ActionPresetRecall,
		ActionKeySet, ActionKeyDelete,
		ActionSourceLabel, ActionSourceDelay, ActionSourcePosition,
		ActionReplayMarkIn, ActionReplayMarkOut, ActionReplayPlay, ActionReplayStop,
		ActionReplayQuickClip, ActionReplayPlayLast, ActionReplayPlayClip,
	}

	for _, action := range expectedActions {
		require.True(t, AllActions[action], "AllActions should contain %q", action)
	}

	// Verify map doesn't contain unexpected entries
	require.Equal(t, len(expectedActions), len(AllActions),
		"AllActions should have exactly %d entries, got %d", len(expectedActions), len(AllActions))
}
