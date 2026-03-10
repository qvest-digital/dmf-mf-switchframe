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

	err := Run(context.Background(), macro, target, nil)
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
	err := Run(context.Background(), macro, target, nil)
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

	err := Run(ctx, macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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
	err := Run(ctx, macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
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
		ActionCaptionMode,
		ActionCaptionText,
		ActionCaptionClear,
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

			err := Run(context.Background(), macro, target, nil)
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

	err := Run(context.Background(), macro, target, nil)
	require.NoError(t, err)

	calls := target.getCalls()
	// SCTE35Cue doesn't record a call in our mock, so calls should be empty
	// This verifies it went through SCTE35Cue, not Execute (which would record "execute:scte35_cue")
	require.Len(t, calls, 0)
}

func TestStepSummary(t *testing.T) {
	tests := []struct {
		name     string
		step     MacroStep
		expected string
	}{
		{
			name:     "cut",
			step:     MacroStep{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			expected: "Cut → cam1",
		},
		{
			name:     "preview",
			step:     MacroStep{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}},
			expected: "Preview → cam2",
		},
		{
			name:     "transition",
			step:     MacroStep{Action: ActionTransition, Params: map[string]interface{}{"source": "cam1", "type": "mix", "durationMs": float64(500)}},
			expected: "Transition mix 500ms → cam1",
		},
		{
			name:     "wait",
			step:     MacroStep{Action: ActionWait, Params: map[string]interface{}{"ms": float64(1000)}},
			expected: "Wait 1000ms",
		},
		{
			name:     "set_audio",
			step:     MacroStep{Action: ActionSetAudio, Params: map[string]interface{}{"source": "cam1", "level": float64(-6)}},
			expected: "Set Audio cam1 -6.0dB",
		},
		{
			name:     "graphics_on",
			step:     MacroStep{Action: ActionGraphicsOn, Params: map[string]interface{}{}},
			expected: "Graphics On (layer 0)",
		},
		{
			name:     "graphics_off",
			step:     MacroStep{Action: ActionGraphicsOff, Params: map[string]interface{}{}},
			expected: "Graphics Off (layer 0)",
		},
		{
			name:     "recording_start",
			step:     MacroStep{Action: ActionRecordingStart, Params: map[string]interface{}{}},
			expected: "Recording Start",
		},
		{
			name:     "recording_stop",
			step:     MacroStep{Action: ActionRecordingStop, Params: map[string]interface{}{}},
			expected: "Recording Stop",
		},
		{
			name:     "ftb",
			step:     MacroStep{Action: ActionFTB, Params: map[string]interface{}{}},
			expected: "Fade to Black",
		},
		{
			name:     "audio_mute",
			step:     MacroStep{Action: ActionAudioMute, Params: map[string]interface{}{"source": "mic1"}},
			expected: "Audio Mute mic1",
		},
		{
			name:     "preset_recall",
			step:     MacroStep{Action: ActionPresetRecall, Params: map[string]interface{}{"id": "preset-1"}},
			expected: "Preset Recall preset-1",
		},
		{
			name:     "key_set",
			step:     MacroStep{Action: ActionKeySet, Params: map[string]interface{}{"source": "cam3"}},
			expected: "Key Set cam3",
		},
		{
			name:     "replay_play",
			step:     MacroStep{Action: ActionReplayPlay, Params: map[string]interface{}{"source": "cam1"}},
			expected: "Replay Play cam1",
		},
		{
			name:     "scte35_cue",
			step:     MacroStep{Action: ActionSCTE35Cue, Params: map[string]interface{}{}},
			expected: "SCTE-35 Cue",
		},
		{
			name:     "caption_mode",
			step:     MacroStep{Action: ActionCaptionMode, Params: map[string]interface{}{"mode": "author"}},
			expected: "Caption Mode author",
		},
		{
			name:     "caption_text",
			step:     MacroStep{Action: ActionCaptionText, Params: map[string]interface{}{"text": "Hello world"}},
			expected: `Caption Text "Hello world"`,
		},
		{
			name:     "caption_clear",
			step:     MacroStep{Action: ActionCaptionClear, Params: map[string]interface{}{}},
			expected: "Caption Clear",
		},
		{
			name:     "unknown_action_fallback",
			step:     MacroStep{Action: MacroAction("custom_thing"), Params: map[string]interface{}{}},
			expected: "custom_thing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StepSummary(tt.step)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestRunner_OnProgressCallbacks(t *testing.T) {
	target := &mockTarget{}
	m := Macro{
		Name: "progress-test",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}},
		},
	}

	var states []ExecutionState
	onProgress := func(state ExecutionState) {
		// Deep copy steps to capture snapshot
		stepsCopy := make([]StepState, len(state.Steps))
		copy(stepsCopy, state.Steps)
		states = append(states, ExecutionState{
			Running:     state.Running,
			MacroName:   state.MacroName,
			Steps:       stepsCopy,
			CurrentStep: state.CurrentStep,
			Error:       state.Error,
		})
	}

	err := Run(context.Background(), m, target, onProgress)
	require.NoError(t, err)

	// Expected callbacks:
	// 1. initial (all pending)
	// 2. step 0 running
	// 3. step 0 done
	// 4. step 1 running
	// 5. step 1 done
	require.Len(t, states, 5, "expected 5 progress callbacks")

	// Initial state: all pending
	require.True(t, states[0].Running)
	require.Equal(t, "progress-test", states[0].MacroName)
	require.Equal(t, StepPending, states[0].Steps[0].Status)
	require.Equal(t, StepPending, states[0].Steps[1].Status)

	// Step 0 running
	require.Equal(t, StepRunning, states[1].Steps[0].Status)
	require.Equal(t, StepPending, states[1].Steps[1].Status)
	require.Equal(t, 0, states[1].CurrentStep)

	// Step 0 done
	require.Equal(t, StepDone, states[2].Steps[0].Status)
	require.Equal(t, StepPending, states[2].Steps[1].Status)

	// Step 1 running
	require.Equal(t, StepDone, states[3].Steps[0].Status)
	require.Equal(t, StepRunning, states[3].Steps[1].Status)
	require.Equal(t, 1, states[3].CurrentStep)

	// Step 1 done
	require.Equal(t, StepDone, states[4].Steps[0].Status)
	require.Equal(t, StepDone, states[4].Steps[1].Status)
}

func TestRunner_OnProgressFailure(t *testing.T) {
	target := &mockTarget{failOn: "cut"}
	m := Macro{
		Name: "fail-progress",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
			{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}},
		},
	}

	var states []ExecutionState
	onProgress := func(state ExecutionState) {
		stepsCopy := make([]StepState, len(state.Steps))
		copy(stepsCopy, state.Steps)
		states = append(states, ExecutionState{
			Running:     state.Running,
			MacroName:   state.MacroName,
			Steps:       stepsCopy,
			CurrentStep: state.CurrentStep,
			Error:       state.Error,
		})
	}

	err := Run(context.Background(), m, target, onProgress)
	require.Error(t, err)

	// Get last state
	require.NotEmpty(t, states)
	final := states[len(states)-1]
	require.Equal(t, StepFailed, final.Steps[0].Status)
	require.NotEmpty(t, final.Steps[0].Error)
	require.Equal(t, StepSkipped, final.Steps[1].Status)
	require.NotEmpty(t, final.Error)
}

func TestRunner_OnProgressWaitStep(t *testing.T) {
	target := &mockTarget{}
	m := Macro{
		Name: "wait-progress",
		Steps: []MacroStep{
			{Action: ActionWait, Params: map[string]interface{}{"ms": float64(50)}},
		},
	}

	var states []ExecutionState
	onProgress := func(state ExecutionState) {
		stepsCopy := make([]StepState, len(state.Steps))
		copy(stepsCopy, state.Steps)
		states = append(states, ExecutionState{
			Running:     state.Running,
			MacroName:   state.MacroName,
			Steps:       stepsCopy,
			CurrentStep: state.CurrentStep,
			Error:       state.Error,
		})
	}

	err := Run(context.Background(), m, target, onProgress)
	require.NoError(t, err)

	// Find the "running" state for step 0
	var runningState *ExecutionState
	for i := range states {
		if states[i].Steps[0].Status == StepRunning {
			runningState = &states[i]
			break
		}
	}
	require.NotNil(t, runningState, "expected a running state for the wait step")
	require.Equal(t, 50, runningState.Steps[0].WaitMs)
	require.NotZero(t, runningState.Steps[0].WaitStartMs)
}

func TestRunner_NilOnProgress(t *testing.T) {
	target := &mockTarget{}
	m := Macro{
		Name: "nil-progress",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
		},
	}

	// Should not panic with nil onProgress
	err := Run(context.Background(), m, target, nil)
	require.NoError(t, err)

	calls := target.getCalls()
	require.Len(t, calls, 1)
	require.Equal(t, "cut:cam1", calls[0])
}

func TestRunner_OnProgressContextCancel(t *testing.T) {
	target := &mockTarget{}
	m := Macro{
		Name: "cancel-progress",
		Steps: []MacroStep{
			{Action: ActionWait, Params: map[string]interface{}{"ms": float64(5000)}},
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var states []ExecutionState
	onProgress := func(state ExecutionState) {
		stepsCopy := make([]StepState, len(state.Steps))
		copy(stepsCopy, state.Steps)
		states = append(states, ExecutionState{
			Running:     state.Running,
			MacroName:   state.MacroName,
			Steps:       stepsCopy,
			CurrentStep: state.CurrentStep,
			Error:       state.Error,
		})
	}

	err := Run(ctx, m, target, onProgress)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	// Get last state
	require.NotEmpty(t, states)
	final := states[len(states)-1]
	require.Equal(t, StepFailed, final.Steps[0].Status)
	require.Contains(t, final.Steps[0].Error, "cancel")
	require.Equal(t, StepSkipped, final.Steps[1].Status)
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
		ActionGraphicsAddLayer, ActionGraphicsRemoveLayer, ActionGraphicsSetRect,
		ActionGraphicsSetZOrder, ActionGraphicsFlyIn, ActionGraphicsFlyOut,
		ActionGraphicsSlide, ActionGraphicsAnimate, ActionGraphicsAnimateStop,
		ActionGraphicsUploadFrame,
		ActionRecordingStart, ActionRecordingStop,
		ActionPresetRecall,
		ActionKeySet, ActionKeyDelete,
		ActionSourceLabel, ActionSourceDelay, ActionSourcePosition,
		ActionReplayMarkIn, ActionReplayMarkOut, ActionReplayPlay, ActionReplayStop,
		ActionReplayQuickClip, ActionReplayPlayLast, ActionReplayPlayClip,
		ActionLayoutPreset, ActionLayoutSlotOn, ActionLayoutSlotOff,
		ActionLayoutSlotSource, ActionLayoutClear,
		ActionCaptionMode, ActionCaptionText, ActionCaptionClear,
	}

	for _, action := range expectedActions {
		require.True(t, AllActions[action], "AllActions should contain %q", action)
	}

	// Verify map doesn't contain unexpected entries
	require.Equal(t, len(expectedActions), len(AllActions),
		"AllActions should have exactly %d entries, got %d", len(expectedActions), len(AllActions))
}
