package macro

import "fmt"

// Param helpers read JSON-decoded values from a params map.
//
// Fail-loud on type mismatch: the earlier pattern `x, _ := m[k].(T)` silently
// returned the zero value, so a client posting `"durationMs":"5000"` instead
// of `"durationMs":5000` produced a 1-second cut when a 5-second dissolve was
// intended. These helpers return a named error instead, so `StepState.Error`
// surfaces the bad field to the operator.
//
// JSON numbers always decode as float64 (stdlib guarantee), so `ParamFloat`
// expects float64, not int.
//
// "Required" vs "Default":
//   - Required (ParamFloat / ParamString / ParamBool): error if key is missing
//     or present-but-wrong-type.
//   - Default (ParamFloatDefault / ParamStringDefault / ParamBoolDefault):
//     returns the default if key is missing; error if present-but-wrong-type.
//     A mistyped optional still fails loud — missing and mistyped are
//     different cases.

func ParamFloat(params map[string]any, key string) (float64, error) {
	raw, present := params[key]
	if !present {
		return 0, fmt.Errorf("param %q: required (expected number)", key)
	}
	v, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("param %q: expected number, got %T", key, raw)
	}
	return v, nil
}

func ParamFloatDefault(params map[string]any, key string, def float64) (float64, error) {
	raw, present := params[key]
	if !present {
		return def, nil
	}
	v, ok := raw.(float64)
	if !ok {
		return 0, fmt.Errorf("param %q: expected number, got %T", key, raw)
	}
	return v, nil
}

func ParamString(params map[string]any, key string) (string, error) {
	raw, present := params[key]
	if !present {
		return "", fmt.Errorf("param %q: required (expected string)", key)
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("param %q: expected string, got %T", key, raw)
	}
	return v, nil
}

func ParamStringDefault(params map[string]any, key string, def string) (string, error) {
	raw, present := params[key]
	if !present {
		return def, nil
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("param %q: expected string, got %T", key, raw)
	}
	return v, nil
}

func ParamBool(params map[string]any, key string) (bool, error) {
	raw, present := params[key]
	if !present {
		return false, fmt.Errorf("param %q: required (expected bool)", key)
	}
	v, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("param %q: expected bool, got %T", key, raw)
	}
	return v, nil
}

func ParamBoolDefault(params map[string]any, key string, def bool) (bool, error) {
	raw, present := params[key]
	if !present {
		return def, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("param %q: expected bool, got %T", key, raw)
	}
	return v, nil
}
