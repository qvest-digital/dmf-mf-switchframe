package macro

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSteps_ColorGradeSetValid(t *testing.T) {
	result := ValidateSteps([]Step{
		{Action: ActionColorGradeSet, Params: map[string]any{"lookName": "kodachrome", "intensity": 0.8}},
	})
	require.False(t, result.HasErrors(), "expected no errors: %v", result.Errors)
}

func TestValidateSteps_ColorGradeSetMissingLookName(t *testing.T) {
	result := ValidateSteps([]Step{
		{Action: ActionColorGradeSet, Params: map[string]any{"intensity": 0.5}},
	})
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "lookName")
}

func TestValidateSteps_ColorGradeSetIntensityTooHigh(t *testing.T) {
	result := ValidateSteps([]Step{
		{Action: ActionColorGradeSet, Params: map[string]any{"lookName": "kodachrome", "intensity": 1.5}},
	})
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "intensity")
}

func TestValidateSteps_ColorGradeSetIntensityNegative(t *testing.T) {
	result := ValidateSteps([]Step{
		{Action: ActionColorGradeSet, Params: map[string]any{"lookName": "kodachrome", "intensity": -0.1}},
	})
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "intensity")
}

func TestValidateSteps_ColorGradeSetIntensityOptional(t *testing.T) {
	// No intensity param is valid — defaults to 1.0 at runtime.
	result := ValidateSteps([]Step{
		{Action: ActionColorGradeSet, Params: map[string]any{"lookName": "golden-hour"}},
	})
	require.False(t, result.HasErrors())
}

func TestValidateSteps_ColorGradeOffNoParams(t *testing.T) {
	result := ValidateSteps([]Step{
		{Action: ActionColorGradeOff, Params: map[string]any{}},
	})
	require.False(t, result.HasErrors())
}

func TestValidateSteps_ColorGradeActionsAreValid(t *testing.T) {
	require.True(t, IsValidAction(ActionColorGradeSet))
	require.True(t, IsValidAction(ActionColorGradeOff))
}

func TestRunner_ColorGradeActionsDispatchViaExecute(t *testing.T) {
	t.Parallel()
	actions := []Action{ActionColorGradeSet, ActionColorGradeOff}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			target := &mockTarget{}
			m := Macro{
				Name:  "colorgrade-" + string(action),
				Steps: []Step{{Action: action, Params: map[string]any{"lookName": "kodachrome"}}},
			}
			err := Run(t.Context(), m, target, nil)
			require.NoError(t, err)

			calls := target.getCalls()
			require.Len(t, calls, 1)
			require.Equal(t, "execute:"+string(action), calls[0])
		})
	}
}

func TestStepSummary_ColorGradeSet(t *testing.T) {
	s := StepSummary(Step{
		Action: ActionColorGradeSet,
		Params: map[string]any{"lookName": "kodachrome", "intensity": 0.8},
	})
	require.Contains(t, s, "kodachrome")
	require.Contains(t, s, "80%")
}

func TestStepSummary_ColorGradeOff(t *testing.T) {
	s := StepSummary(Step{Action: ActionColorGradeOff, Params: map[string]any{}})
	require.Equal(t, "Color Grade Off", s)
}
