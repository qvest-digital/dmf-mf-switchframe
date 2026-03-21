package macro

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSteps_STMapAssignSource(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapAssignSource, Params: map[string]any{
			"source": "cam1",
			"map":    "background",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors(), "expected valid stmap_assign_source to pass; got: %+v", result.Errors)
}

func TestValidateSteps_STMapAssignSource_MissingSource(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapAssignSource, Params: map[string]any{
			"map": "background",
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "source")
}

func TestValidateSteps_STMapAssignSource_MissingMap(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapAssignSource, Params: map[string]any{
			"source": "cam1",
		}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "map")
}

func TestValidateSteps_STMapRemoveSource(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapRemoveSource, Params: map[string]any{
			"source": "cam1",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors(), "expected valid stmap_remove_source to pass; got: %+v", result.Errors)
}

func TestValidateSteps_STMapRemoveSource_MissingSource(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapRemoveSource, Params: map[string]any{}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "source")
}

func TestValidateSteps_STMapAssignProgram(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapAssignProgram, Params: map[string]any{
			"map": "background",
		}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors(), "expected valid stmap_assign_program to pass; got: %+v", result.Errors)
}

func TestValidateSteps_STMapAssignProgram_MissingMap(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapAssignProgram, Params: map[string]any{}},
	}
	result := ValidateSteps(steps)
	require.True(t, result.HasErrors())
	require.Contains(t, result.Errors[0].Message, "map")
}

func TestValidateSteps_STMapRemoveProgram(t *testing.T) {
	t.Parallel()
	steps := []Step{
		{Action: ActionSTMapRemoveProgram, Params: map[string]any{}},
	}
	result := ValidateSteps(steps)
	require.False(t, result.HasErrors(), "expected stmap_remove_program with no params to pass; got: %+v", result.Errors)

	// Nil params should also work.
	steps2 := []Step{
		{Action: ActionSTMapRemoveProgram, Params: nil},
	}
	result2 := ValidateSteps(steps2)
	require.False(t, result2.HasErrors(), "expected stmap_remove_program with nil params to pass; got: %+v", result2.Errors)
}

func TestValidateSteps_STMapActionsAreValid(t *testing.T) {
	t.Parallel()
	for _, action := range []Action{
		ActionSTMapAssignSource, ActionSTMapRemoveSource,
		ActionSTMapAssignProgram, ActionSTMapRemoveProgram,
	} {
		require.True(t, IsValidAction(action), "IsValidAction should return true for %q", action)
	}
}

func TestStepSummary_STMapActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		step     Step
		expected string
	}{
		{
			name:     "stmap_assign_source",
			step:     Step{Action: ActionSTMapAssignSource, Params: map[string]any{"source": "cam1", "map": "bg"}},
			expected: "ST Map Assign Source cam1",
		},
		{
			name:     "stmap_remove_source",
			step:     Step{Action: ActionSTMapRemoveSource, Params: map[string]any{"source": "cam1"}},
			expected: "ST Map Remove Source cam1",
		},
		{
			name:     "stmap_assign_program",
			step:     Step{Action: ActionSTMapAssignProgram, Params: map[string]any{"map": "bg"}},
			expected: "ST Map Assign Program",
		},
		{
			name:     "stmap_remove_program",
			step:     Step{Action: ActionSTMapRemoveProgram, Params: map[string]any{}},
			expected: "ST Map Remove Program",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StepSummary(tt.step)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestRunner_STMapActionsDispatchViaExecute(t *testing.T) {
	t.Parallel()
	stmapActions := []Action{
		ActionSTMapAssignSource, ActionSTMapRemoveSource,
		ActionSTMapAssignProgram, ActionSTMapRemoveProgram,
	}
	for _, action := range stmapActions {
		t.Run(string(action), func(t *testing.T) {
			target := &mockTarget{}
			m := Macro{
				Name: "stmap-" + string(action),
				Steps: []Step{
					{Action: action, Params: map[string]any{"source": "cam1", "map": "bg"}},
				},
			}
			err := Run(t.Context(), m, target, nil)
			require.NoError(t, err)

			calls := target.getCalls()
			require.Len(t, calls, 1)
			require.Equal(t, "execute:"+string(action), calls[0])
		})
	}
}
