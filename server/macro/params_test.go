package macro

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParamFloat_PresentCorrectType(t *testing.T) {
	p := map[string]any{"x": float64(42)}
	got, err := ParamFloat(p, "x")
	require.NoError(t, err)
	require.Equal(t, float64(42), got)
}

func TestParamFloat_Missing(t *testing.T) {
	p := map[string]any{}
	_, err := ParamFloat(p, "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "x")
	require.Contains(t, err.Error(), "required")
}

func TestParamFloat_WrongType(t *testing.T) {
	p := map[string]any{"ms": "1000"}
	_, err := ParamFloat(p, "ms")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ms")
	require.True(t,
		strings.Contains(err.Error(), "number") || strings.Contains(err.Error(), "float"),
		"error must indicate expected numeric type, got: %v", err,
	)
}

func TestParamFloatDefault_Missing(t *testing.T) {
	p := map[string]any{}
	got, err := ParamFloatDefault(p, "x", 99)
	require.NoError(t, err)
	require.Equal(t, float64(99), got)
}

func TestParamFloatDefault_PresentCorrectType(t *testing.T) {
	p := map[string]any{"x": float64(3)}
	got, err := ParamFloatDefault(p, "x", 99)
	require.NoError(t, err)
	require.Equal(t, float64(3), got)
}

func TestParamFloatDefault_WrongTypeErrors(t *testing.T) {
	p := map[string]any{"durationMs": "5000"}
	_, err := ParamFloatDefault(p, "durationMs", 1000)
	require.Error(t, err, "mistyped present value must fail loud, not silently default")
	require.Contains(t, err.Error(), "durationMs")
}

func TestParamString_PresentCorrectType(t *testing.T) {
	p := map[string]any{"name": "cam1"}
	got, err := ParamString(p, "name")
	require.NoError(t, err)
	require.Equal(t, "cam1", got)
}

func TestParamString_Missing(t *testing.T) {
	p := map[string]any{}
	_, err := ParamString(p, "name")
	require.Error(t, err)
	require.Contains(t, err.Error(), "name")
}

func TestParamString_WrongType(t *testing.T) {
	p := map[string]any{"source": float64(123)}
	_, err := ParamString(p, "source")
	require.Error(t, err)
	require.Contains(t, err.Error(), "source")
	require.Contains(t, err.Error(), "string")
}

func TestParamString_EmptyStringIsValid(t *testing.T) {
	p := map[string]any{"s": ""}
	got, err := ParamString(p, "s")
	require.NoError(t, err, "empty string is a valid present value")
	require.Equal(t, "", got)
}

func TestParamStringDefault_Missing(t *testing.T) {
	p := map[string]any{}
	got, err := ParamStringDefault(p, "x", "fallback")
	require.NoError(t, err)
	require.Equal(t, "fallback", got)
}

func TestParamStringDefault_WrongTypeErrors(t *testing.T) {
	p := map[string]any{"type": float64(7)}
	_, err := ParamStringDefault(p, "type", "mix")
	require.Error(t, err)
	require.Contains(t, err.Error(), "type")
}

func TestParamBool_PresentCorrectType(t *testing.T) {
	p := map[string]any{"enabled": true}
	got, err := ParamBool(p, "enabled")
	require.NoError(t, err)
	require.True(t, got)
}

func TestParamBool_Missing(t *testing.T) {
	p := map[string]any{}
	_, err := ParamBool(p, "enabled")
	require.Error(t, err)
	require.Contains(t, err.Error(), "enabled")
}

func TestParamBool_WrongType(t *testing.T) {
	p := map[string]any{"muted": "true"}
	_, err := ParamBool(p, "muted")
	require.Error(t, err)
	require.Contains(t, err.Error(), "muted")
	require.Contains(t, err.Error(), "bool")
}

func TestParamBoolDefault_Missing(t *testing.T) {
	p := map[string]any{}
	got, err := ParamBoolDefault(p, "x", true)
	require.NoError(t, err)
	require.True(t, got)
}

func TestParamBoolDefault_WrongTypeErrors(t *testing.T) {
	p := map[string]any{"afv": "yes"}
	_, err := ParamBoolDefault(p, "afv", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "afv")
}
