package macro

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- corrector_set validation ---

func TestValidateSteps_CorrectorSet_Valid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"temperature": 50.0}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_MultipleParams(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"temperature": -30.0,
		"tint":        20.0,
		"saturation":  1.5,
		"contrast":    0.5,
		"sharpness":   0.3,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_EmptyParams(t *testing.T) {
	t.Parallel()
	// Empty params is valid — no fields to set means a no-op.
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_InvalidRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"temperature": 200.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "temperature")
}

func TestValidateSteps_CorrectorSet_TemperatureNegativeOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"temperature": -101.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "temperature")
}

func TestValidateSteps_CorrectorSet_SaturationOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"saturation": 3.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "saturation")
}

func TestValidateSteps_CorrectorSet_SaturationNegative(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"saturation": -0.1}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "saturation")
}

func TestValidateSteps_CorrectorSet_ContrastOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"contrast": 1.5}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "contrast")
}

func TestValidateSteps_CorrectorSet_SharpnessOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"sharpness": 2.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "sharpness")
}

func TestValidateSteps_CorrectorSet_LiftValid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"lift0": -0.5,
		"lift1": 0.0,
		"lift2": 1.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_LiftOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"lift0": 1.5}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "lift0")
}

func TestValidateSteps_CorrectorSet_GammaValid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"gamma0": 0.1,
		"gamma1": 1.0,
		"gamma2": 4.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_GammaOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"gamma0": 0.05}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "gamma0")
}

func TestValidateSteps_CorrectorSet_GainValid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"gain0": 0.0,
		"gain1": 2.0,
		"gain2": 4.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_GainOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"gain1": 5.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "gain1")
}

func TestValidateSteps_CorrectorSet_GainNegative(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"gain2": -0.1}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "gain2")
}

func TestValidateSteps_CorrectorSet_ShadowsValid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"shadows0": -1.0,
		"shadows1": 0.0,
		"shadows2": 1.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_ShadowsOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"shadows0": 1.5}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "shadows0")
}

func TestValidateSteps_CorrectorSet_MidtonesValid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"midtones0": -0.5,
		"midtones1": 0.5,
		"midtones2": 0.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_MidtonesOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"midtones1": -1.5}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "midtones1")
}

func TestValidateSteps_CorrectorSet_HighlightsValid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{
		"highlights0": -1.0,
		"highlights1": 0.0,
		"highlights2": 1.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorSet_HighlightsOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"highlights2": 2.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "highlights2")
}

func TestValidateSteps_CorrectorSet_TintOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorSet, Params: map[string]any{"tint": 101.0}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "tint")
}

// --- corrector_reset validation ---

func TestValidateSteps_CorrectorReset(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorReset, Params: map[string]any{}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_CorrectorReset_NilParams(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionCorrectorReset, Params: nil}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

// --- colorgrade_save validation ---

func TestValidateSteps_ColorGradeSave_Valid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionColorGradeSave, Params: map[string]any{"name": "my-look"}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_ColorGradeSave_MissingName(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionColorGradeSave, Params: map[string]any{}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "name")
}

func TestValidateSteps_ColorGradeSave_EmptyName(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionColorGradeSave, Params: map[string]any{"name": ""}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "name")
}

// --- source_corrector_set validation ---

func TestValidateSteps_SourceCorrectorSet_Valid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorSet, Params: map[string]any{
		"source": "cam1", "temperature": 50.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_SourceCorrectorSet_MissingSource(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorSet, Params: map[string]any{
		"temperature": 50.0,
	}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "source")
}

func TestValidateSteps_SourceCorrectorSet_InvalidRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorSet, Params: map[string]any{
		"source": "cam1", "temperature": 200.0,
	}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "temperature")
}

func TestValidateSteps_SourceCorrectorSet_MultipleFields(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorSet, Params: map[string]any{
		"source":      "cam1",
		"temperature": 50.0,
		"tint":        -30.0,
		"saturation":  1.5,
		"contrast":    0.5,
		"sharpness":   0.8,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_SourceCorrectorSet_TripletFields(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorSet, Params: map[string]any{
		"source": "cam1",
		"lift0":  0.5,
		"lift1":  -0.5,
		"lift2":  0.0,
		"gamma0": 1.0,
		"gain0":  2.0,
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_SourceCorrectorSet_TripletOutOfRange(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorSet, Params: map[string]any{
		"source": "cam1",
		"lift0":  5.0, // out of range: [-1, 1]
	}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "lift0")
}

// --- source_corrector_reset validation ---

func TestValidateSteps_SourceCorrectorReset_Valid(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorReset, Params: map[string]any{
		"source": "cam1",
	}}}
	result := ValidateSteps(steps)
	require.Empty(t, result.Errors)
}

func TestValidateSteps_SourceCorrectorReset_MissingSource(t *testing.T) {
	t.Parallel()
	steps := []Step{{Action: ActionSourceCorrectorReset, Params: map[string]any{}}}
	result := ValidateSteps(steps)
	require.NotEmpty(t, result.Errors)
	require.Contains(t, result.Errors[0].Message, "source")
}

// --- Action registration ---

func TestValidateSteps_CorrectorActionsAreValid(t *testing.T) {
	t.Parallel()
	require.True(t, IsValidAction(ActionCorrectorSet))
	require.True(t, IsValidAction(ActionCorrectorReset))
	require.True(t, IsValidAction(ActionColorGradeSave))
	require.True(t, IsValidAction(ActionSourceCorrectorSet))
	require.True(t, IsValidAction(ActionSourceCorrectorReset))
}

// --- Runner dispatch ---

func TestRunner_CorrectorActionsDispatchViaExecute(t *testing.T) {
	t.Parallel()
	actions := []Action{ActionCorrectorSet, ActionCorrectorReset, ActionColorGradeSave,
		ActionSourceCorrectorSet, ActionSourceCorrectorReset}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			target := &mockTarget{}
			m := Macro{
				Name:  "corrector-" + string(action),
				Steps: []Step{{Action: action, Params: map[string]any{"name": "test-look", "temperature": 50.0, "source": "cam1"}}},
			}
			err := Run(t.Context(), m, target, nil)
			require.NoError(t, err)

			calls := target.getCalls()
			require.Len(t, calls, 1)
			require.Equal(t, "execute:"+string(action), calls[0])
		})
	}
}

// --- Summary functions ---

func TestStepSummary_CorrectorSet(t *testing.T) {
	t.Parallel()
	s := StepSummary(Step{
		Action: ActionCorrectorSet,
		Params: map[string]any{"temperature": 50.0, "saturation": 1.2},
	})
	require.Equal(t, "Corrector: set parameters", s)
}

func TestStepSummary_CorrectorReset(t *testing.T) {
	t.Parallel()
	s := StepSummary(Step{Action: ActionCorrectorReset, Params: map[string]any{}})
	require.Equal(t, "Corrector: reset", s)
}

func TestStepSummary_ColorGradeSave(t *testing.T) {
	t.Parallel()
	s := StepSummary(Step{
		Action: ActionColorGradeSave,
		Params: map[string]any{"name": "golden-hour"},
	})
	require.Equal(t, "Save Look: golden-hour", s)
}

func TestStepSummary_SourceCorrectorSet(t *testing.T) {
	t.Parallel()
	s := StepSummary(Step{
		Action: ActionSourceCorrectorSet,
		Params: map[string]any{"source": "cam2", "temperature": 25.0},
	})
	require.Equal(t, "Source Corrector: cam2", s)
}

func TestStepSummary_SourceCorrectorReset(t *testing.T) {
	t.Parallel()
	s := StepSummary(Step{
		Action: ActionSourceCorrectorReset,
		Params: map[string]any{"source": "cam3"},
	})
	require.Equal(t, "Source Corrector Reset: cam3", s)
}
