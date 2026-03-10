package macro

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSteps_RejectsUnknownAction(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: "bogus_action", Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Len(t, result.Errors, 1)
	require.Equal(t, 0, result.Errors[0].Step)
	require.Contains(t, result.Errors[0].Message, "unknown action")
	require.Contains(t, result.Errors[0].Message, "bogus_action")
}

func TestValidateSteps_RejectsCutWithoutSource(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionCut, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "source")

	// Also test with nil params
	steps2 := []MacroStep{
		{Action: ActionCut, Params: nil},
	}
	result2 := ValidateSteps(steps2)
	require.True(t, result2.HasErrors())
}

func TestValidateSteps_RejectsPreviewWithoutSource(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionPreview, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "source")
}

func TestValidateSteps_RejectsTransitionWithoutSource(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{"type": "mix"}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "source")
}

func TestValidateSteps_RejectsWipeWithoutDirection(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source": "cam1",
			"type":   "wipe",
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "wipeDirection")
}

func TestValidateSteps_RejectsWipeWithInvalidDirection(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":        "cam1",
			"type":          "wipe",
			"wipeDirection": "diagonal",
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "wipeDirection")
	require.Contains(t, result.Errors[0].Message, "invalid")
}

func TestValidateSteps_AcceptsWipeWithValidDirections(t *testing.T) {
	t.Parallel()
	validDirs := []string{"h-left", "h-right", "v-top", "v-bottom", "box-center-out", "box-edges-in"}
	for _, dir := range validDirs {
		steps := []MacroStep{
			{Action: ActionTransition, Params: map[string]interface{}{
				"source":        "cam1",
				"type":          "wipe",
				"wipeDirection": dir,
			}},
		}
		result := ValidateSteps(steps)
		require.False(t, result.HasErrors(), "expected valid wipe direction %q to be accepted", dir)
	}
}

func TestValidateSteps_RejectsStingerWithoutName(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source": "cam1",
			"type":   "stinger",
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "stingerName")

	// Empty string stingerName should also fail.
	steps2 := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":      "cam1",
			"type":        "stinger",
			"stingerName": "",
		}},
	}
	result2 := ValidateSteps(steps2)
	require.True(t, result2.HasErrors())
	require.Contains(t, result2.Errors[0].Message, "stingerName")
}

func TestValidateSteps_RejectsWaitWithZeroMs(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionWait, Params: map[string]interface{}{"ms": float64(0)}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "ms")

	// Negative value
	steps2 := []MacroStep{
		{Action: ActionWait, Params: map[string]interface{}{"ms": float64(-100)}},
	}
	result2 := ValidateSteps(steps2)
	require.True(t, result2.HasErrors())

	// Missing ms param
	steps3 := []MacroStep{
		{Action: ActionWait, Params: map[string]interface{}{}},
	}
	result3 := ValidateSteps(steps3)
	require.True(t, result3.HasErrors())
}

func TestValidateSteps_RejectsSetAudioWithoutSource(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionSetAudio, Params: map[string]interface{}{"level": float64(0.5)}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "source")
}

func TestValidateSteps_AcceptsValidMacroWithNewActions(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionWait, Params: map[string]interface{}{"ms": float64(500)}},
		{Action: ActionAudioMute, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionAudioAFV, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionAudioTrim, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionAudioEQ, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionAudioCompressor, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionAudioDelay, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionKeySet, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionKeyDelete, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionSourceLabel, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionSourceDelay, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionSourcePosition, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionReplayMarkIn, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionReplayMarkOut, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionReplayPlay, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionReplayPlayClip, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionReplayQuickClip, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionFTB, Params: map[string]interface{}{}},
		{Action: ActionGraphicsOn, Params: map[string]interface{}{}},
		{Action: ActionGraphicsOff, Params: map[string]interface{}{}},
		{Action: ActionRecordingStart, Params: map[string]interface{}{}},
		{Action: ActionRecordingStop, Params: map[string]interface{}{}},
		{Action: ActionPresetRecall, Params: map[string]interface{}{}},
		{Action: ActionReplayPlayLast, Params: map[string]interface{}{}},
		{Action: ActionReplayStop, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors(), "expected valid macro to pass validation, got: %+v", result.Errors)
}

func TestValidateSteps_AcceptsOldMacrosBackwardCompat(t *testing.T) {
	t.Parallel()
	// Macros using only the original 5 actions should still pass.
	steps := []MacroStep{
		{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}},
		{Action: ActionTransition, Params: map[string]interface{}{"source": "cam1", "type": "mix"}},
		{Action: ActionWait, Params: map[string]interface{}{"ms": float64(1000)}},
		{Action: ActionSetAudio, Params: map[string]interface{}{"source": "cam1", "level": float64(0.8)}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_WarnsConsecutiveTransitions(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionTransition, Params: map[string]interface{}{"source": "cam2"}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors(), "consecutive transitions should warn, not error")
	require.Len(t, result.Warnings, 1)
	require.Contains(t, result.Warnings[0].Message, "consecutive")

	// If there's a wait between them, no warning.
	steps2 := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{"source": "cam1"}},
		{Action: ActionWait, Params: map[string]interface{}{"ms": float64(1000)}},
		{Action: ActionTransition, Params: map[string]interface{}{"source": "cam2"}},
	}
	result2 := ValidateSteps(steps2)
	require.Empty(t, result2.Warnings)
}

func TestValidateSteps_TransitionDurationBounds(t *testing.T) {
	t.Parallel()

	// Too short
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":     "cam1",
			"durationMs": float64(50),
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "durationMs")

	// Too long
	steps2 := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":     "cam1",
			"durationMs": float64(6000),
		}},
	}
	result2 := ValidateSteps(steps2)
	require.True(t, result2.HasErrors())
	require.Contains(t, result2.Errors[0].Message, "durationMs")

	// Valid bounds - exactly 100 and 5000
	steps3 := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":     "cam1",
			"durationMs": float64(100),
		}},
	}
	result3 := ValidateSteps(steps3)
	require.False(t, result3.HasErrors())

	steps4 := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":     "cam1",
			"durationMs": float64(5000),
		}},
	}
	result4 := ValidateSteps(steps4)
	require.False(t, result4.HasErrors())

	// No durationMs param is fine (uses default)
	steps5 := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source": "cam1",
		}},
	}
	result5 := ValidateSteps(steps5)
	require.False(t, result5.HasErrors())
}

func TestValidateSteps_RejectsReplayMarkInWithoutSource(t *testing.T) {
	t.Parallel()
	for _, action := range []MacroAction{
		ActionReplayMarkIn, ActionReplayMarkOut, ActionReplayPlay, ActionReplayPlayClip, ActionReplayQuickClip,
	} {
		steps := []MacroStep{
			{Action: action, Params: map[string]interface{}{}},
		}
		result := ValidateSteps(steps)
		require.True(t, result.HasErrors(), "expected %s without source to fail", action)
		require.Contains(t, result.Errors[0].Message, "source")
	}
}

func TestValidateSteps_AcceptsFTBNoParams(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionFTB, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())

	// Nil params should also work
	steps2 := []MacroStep{
		{Action: ActionFTB, Params: nil},
	}
	result2 := ValidateSteps(steps2)
	require.False(t, result2.HasErrors())
}

func TestValidateSteps_AcceptsGraphicsNoParams(t *testing.T) {
	t.Parallel()
	for _, action := range []MacroAction{
		ActionGraphicsOn, ActionGraphicsOff, ActionGraphicsAutoOn, ActionGraphicsAutoOff,
	} {
		steps := []MacroStep{
			{Action: action, Params: map[string]interface{}{}},
		}
		result := ValidateSteps(steps)
		require.False(t, result.HasErrors(), "expected %s with no params to pass", action)
	}
}

func TestValidateSteps_AcceptsRecordingNoParams(t *testing.T) {
	t.Parallel()
	for _, action := range []MacroAction{ActionRecordingStart, ActionRecordingStop} {
		steps := []MacroStep{
			{Action: action, Params: nil},
		}
		result := ValidateSteps(steps)
		require.False(t, result.HasErrors(), "expected %s with nil params to pass", action)
	}
}

func TestValidateSteps_AcceptsReplayStopNoParams(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionReplayStop, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_AcceptsReplayPlayLastNoParams(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionReplayPlayLast, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_MultipleErrors(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionCut, Params: map[string]interface{}{}},            // missing source
		{Action: ActionPreview, Params: map[string]interface{}{}},        // missing source
		{Action: ActionWait, Params: map[string]interface{}{"ms": float64(0)}}, // zero ms
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Len(t, result.Errors, 3)
	require.Equal(t, 0, result.Errors[0].Step)
	require.Equal(t, 1, result.Errors[1].Step)
	require.Equal(t, 2, result.Errors[2].Step)
}

func TestValidateSteps_SCTE35ActionsNoSourceRequired(t *testing.T) {
	t.Parallel()
	// SCTE-35 actions don't require source param at validation time.
	for _, action := range []MacroAction{
		ActionSCTE35Cue, ActionSCTE35Return, ActionSCTE35Cancel, ActionSCTE35Hold, ActionSCTE35Extend,
	} {
		steps := []MacroStep{
			{Action: action, Params: map[string]interface{}{}},
		}
		result := ValidateSteps(steps)
		require.False(t, result.HasErrors(), "expected %s without params to pass validation", action)
	}
}

func TestValidateSteps_AudioMasterNoSourceRequired(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionAudioMaster, Params: map[string]interface{}{}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_SourceRequiredActions(t *testing.T) {
	t.Parallel()
	// All these actions require a "source" param.
	sourceRequiredActions := []MacroAction{
		ActionCut, ActionPreview, ActionSetAudio,
		ActionAudioMute, ActionAudioAFV, ActionAudioTrim,
		ActionAudioEQ, ActionAudioCompressor, ActionAudioDelay,
		ActionKeySet, ActionKeyDelete, ActionSourceLabel,
		ActionSourceDelay, ActionSourcePosition,
		ActionReplayMarkIn, ActionReplayMarkOut,
		ActionReplayPlay, ActionReplayPlayClip, ActionReplayQuickClip,
	}
	for _, action := range sourceRequiredActions {
		steps := []MacroStep{
			{Action: action, Params: map[string]interface{}{}},
		}
		result := ValidateSteps(steps)
		require.True(t, result.HasErrors(), "expected %s without source to fail", action)
		require.Contains(t, result.Errors[0].Message, "source",
			"expected %s error to mention 'source'", action)
	}
}

func TestValidationError_ImplementsError(t *testing.T) {
	t.Parallel()
	var err error = &ValidationError{Step: 2, Message: "test error"}
	require.Equal(t, "step 2: test error", err.Error())
}

func TestStore_SaveRejectsInvalidMacro(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "macros.json")
	s, err := NewStore(path)
	require.NoError(t, err)

	// Try to save a macro with an unknown action.
	m := Macro{
		Name: "bad-macro",
		Steps: []MacroStep{
			{Action: "nonexistent", Params: map[string]interface{}{}},
		},
	}
	err = s.Save(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown action")

	// Try to save a macro with cut missing source.
	m2 := Macro{
		Name: "bad-cut",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{}},
		},
	}
	err = s.Save(m2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "source")

	// Verify the invalid macros were not persisted.
	_, err = s.Get("bad-macro")
	require.Error(t, err)
	_, err = s.Get("bad-cut")
	require.Error(t, err)

	// Verify valid macros still save fine.
	m3 := Macro{
		Name: "good-macro",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
		},
	}
	err = s.Save(m3)
	require.NoError(t, err)
	got, err := s.Get("good-macro")
	require.NoError(t, err)
	require.Equal(t, "good-macro", got.Name)
}

func TestValidateSteps_TransitionAcceptsMixNoExtras(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source": "cam1",
			"type":   "mix",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_TransitionAcceptsValidStinger(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionTransition, Params: map[string]interface{}{
			"source":      "cam1",
			"type":        "stinger",
			"stingerName": "intro",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_SCTE35Cue_RejectsNegativePreRollMs(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionSCTE35Cue, Params: map[string]interface{}{
			"commandType": "splice_insert",
			"preRollMs":   float64(-1000),
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "preRollMs")
	require.Contains(t, result.Errors[0].Message, "non-negative")
}

func TestValidateSteps_SCTE35Cue_AcceptsPositivePreRollMs(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionSCTE35Cue, Params: map[string]interface{}{
			"commandType": "splice_insert",
			"preRollMs":   float64(5000),
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_SCTE35Cue_AcceptsNoPreRollMs(t *testing.T) {
	t.Parallel()
	steps := []MacroStep{
		{Action: ActionSCTE35Cue, Params: map[string]interface{}{
			"commandType": "splice_insert",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_EmptySteps(t *testing.T) {
	t.Parallel()
	result := ValidateSteps([]MacroStep{})
	require.False(t, result.HasErrors())
	require.Empty(t, result.Warnings)
}

func TestValidateSteps_SCTE35Cue_TimeSignal_RequiresDescriptors(t *testing.T) {
	t.Parallel()
	// time_signal without descriptors should be an error.
	steps := []MacroStep{
		{Action: ActionSCTE35Cue, Params: map[string]interface{}{
			"commandType": "time_signal",
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "time_signal")
	require.Contains(t, result.Errors[0].Message, "descriptor")
}

func TestValidateSteps_SCTE35Cue_TimeSignal_WithDescriptors(t *testing.T) {
	t.Parallel()
	// time_signal with descriptors should pass validation.
	steps := []MacroStep{
		{Action: ActionSCTE35Cue, Params: map[string]interface{}{
			"commandType": "time_signal",
			"descriptors": []interface{}{
				map[string]interface{}{
					"segmentationType": float64(0x34),
					"upidType":         float64(0x09),
					"upid":             "TEST-SIGNAL",
				},
			},
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}

func TestValidateSteps_SCTE35Cue_SpliceInsert_NoDescriptorsOK(t *testing.T) {
	t.Parallel()
	// splice_insert without descriptors is valid.
	steps := []MacroStep{
		{Action: ActionSCTE35Cue, Params: map[string]interface{}{
			"commandType": "splice_insert",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors())
}
