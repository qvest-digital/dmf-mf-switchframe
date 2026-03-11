package macro

import "fmt"

// ValidationError represents a validation failure for a specific macro step.
type ValidationError struct {
	Step    int    `json:"step"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("step %d: %s", e.Step, e.Message)
}

// ValidationWarning represents a non-blocking warning for a macro.
type ValidationWarning struct {
	Step    int    `json:"step"`
	Message string `json:"message"`
}

// ValidationResult contains errors (block save) and warnings (informational).
type ValidationResult struct {
	Errors   []ValidationError   `json:"errors,omitempty"`
	Warnings []ValidationWarning `json:"warnings,omitempty"`
}

// HasErrors returns true if the result contains any validation errors.
func (r *ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// validWipeDirections is the set of accepted wipe direction values.
var validWipeDirections = map[string]bool{
	"h-left":         true,
	"h-right":        true,
	"v-top":          true,
	"v-bottom":       true,
	"box-center-out": true,
	"box-edges-in":   true,
}

// actionsRequiringSource is the set of actions that require a "source" string param.
var actionsRequiringSource = map[Action]bool{
	ActionCut:             true,
	ActionPreview:         true,
	ActionSetAudio:        true,
	ActionAudioMute:       true,
	ActionAudioAFV:        true,
	ActionAudioTrim:       true,
	ActionAudioEQ:         true,
	ActionAudioCompressor: true,
	ActionAudioDelay:      true,
	ActionKeySet:          true,
	ActionKeyDelete:       true,
	ActionSourceLabel:     true,
	ActionSourceDelay:     true,
	ActionSourcePosition:  true,
	ActionReplayMarkIn:    true,
	ActionReplayMarkOut:   true,
	ActionReplayPlay:      true,
	ActionReplayPlayClip:  true,
	ActionReplayQuickClip: true,
}

// ValidateSteps validates the steps of a macro and returns errors and warnings.
// Errors block save; warnings are informational only.
func ValidateSteps(steps []Step) *ValidationResult {
	result := &ValidationResult{}

	for i, step := range steps {
		// Check that the action is known.
		if !AllActions[step.Action] {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("unknown action %q", step.Action),
			})
			continue
		}

		// Validate per-action requirements.
		validateStep(i, step, result)

		// Warn on consecutive transitions without a wait.
		if step.Action == ActionTransition && i > 0 && steps[i-1].Action == ActionTransition {
			result.Warnings = append(result.Warnings, ValidationWarning{
				Step:    i,
				Message: "consecutive transition without a wait step between them",
			})
		}
	}

	return result
}

// validateStep checks action-specific parameter requirements.
func validateStep(i int, step Step, result *ValidationResult) {
	// Check source requirement for applicable actions.
	if actionsRequiringSource[step.Action] {
		if !hasStringParam(step.Params, "source") {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("%s requires 'source' param", step.Action),
			})
			return
		}
	}

	switch step.Action {
	case ActionTransition:
		validateTransition(i, step, result)
	case ActionWait:
		validateWait(i, step, result)
	case ActionSCTE35Cue:
		validateSCTE35Cue(i, step, result)
	case ActionLayoutPreset:
		if !hasStringParam(step.Params, "preset") {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "layout_preset requires 'preset' param",
			})
		}
	case ActionCaptionMode:
		mode, _ := step.Params["mode"].(string)
		if mode != "off" && mode != "passthrough" && mode != "author" {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("caption_mode requires 'mode' param (off|passthrough|author), got %q", mode),
			})
		}
	case ActionLayoutSlotOn, ActionLayoutSlotOff, ActionLayoutSlotSource:
		if _, ok := step.Params["slot"].(float64); !ok {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("%s requires 'slot' param (number)", step.Action),
			})
		}
		if step.Action == ActionLayoutSlotSource && !hasStringParam(step.Params, "source") {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "layout_slot_source requires 'source' param",
			})
		}

	// Graphics layer actions that require layerId.
	case ActionGraphicsOn, ActionGraphicsOff, ActionGraphicsAutoOn, ActionGraphicsAutoOff,
		ActionGraphicsRemoveLayer, ActionGraphicsSetRect, ActionGraphicsSetZOrder,
		ActionGraphicsFlyIn, ActionGraphicsFlyOut, ActionGraphicsSlide,
		ActionGraphicsAnimate, ActionGraphicsAnimateStop, ActionGraphicsUploadFrame:
		// layerId defaults to 0 for backward compat, so it's a warning not an error.
		if _, ok := step.Params["layerId"].(float64); !ok {
			result.Warnings = append(result.Warnings, ValidationWarning{
				Step:    i,
				Message: fmt.Sprintf("%s missing 'layerId' param, will default to 0", step.Action),
			})
		}
		validateGraphicsStep(i, step, result)
	}
}

// validateTransition checks transition-specific params (source is already checked
// by the actionsRequiringSource map above — transition requires source via that path too).
func validateTransition(i int, step Step, result *ValidationResult) {
	// Source is required (already checked if in actionsRequiringSource, but transition
	// needs it too — add to the map or check here). We rely on the map lookup above
	// since ActionTransition is not in actionsRequiringSource. Check it here instead.
	if !hasStringParam(step.Params, "source") {
		result.Errors = append(result.Errors, ValidationError{
			Step:    i,
			Message: "transition requires 'source' param",
		})
		return
	}

	transType, _ := step.Params["type"].(string)

	// Wipe-specific checks.
	if transType == "wipe" {
		dir, ok := step.Params["wipeDirection"].(string)
		if !ok || dir == "" {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "wipe transition requires 'wipeDirection' param",
			})
		} else if !validWipeDirections[dir] {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("invalid wipeDirection %q", dir),
			})
		}
	}

	// Stinger-specific checks.
	if transType == "stinger" {
		if !hasStringParam(step.Params, "stingerName") {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "stinger transition requires non-empty 'stingerName' param",
			})
		}
	}

	// Duration bounds check (only if present).
	if d, ok := step.Params["durationMs"].(float64); ok {
		if d < 100 || d > 5000 {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("durationMs must be between 100 and 5000, got %.0f", d),
			})
		}
	}
}

// validateWait checks that ms > 0.
func validateWait(i int, step Step, result *ValidationResult) {
	ms, ok := step.Params["ms"].(float64)
	if !ok || ms <= 0 {
		result.Errors = append(result.Errors, ValidationError{
			Step:    i,
			Message: "wait requires 'ms' param > 0",
		})
	}
}

// validateSCTE35Cue checks that preRollMs is non-negative if present and
// that time_signal commands include at least one descriptor.
func validateSCTE35Cue(i int, step Step, result *ValidationResult) {
	if v, ok := step.Params["preRollMs"].(float64); ok && v < 0 {
		result.Errors = append(result.Errors, ValidationError{
			Step:    i,
			Message: fmt.Sprintf("preRollMs must be non-negative, got %.0f", v),
		})
	}
	if ct, ok := step.Params["commandType"].(string); ok && ct == "time_signal" {
		descs, _ := step.Params["descriptors"].([]interface{})
		if len(descs) == 0 {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "time_signal requires at least one descriptor",
			})
		}
	}
}

// validFlyDirectionsSet is the set of accepted fly-in/fly-out direction values.
var validFlyDirectionsSet = map[string]bool{
	"left":   true,
	"right":  true,
	"top":    true,
	"bottom": true,
}

// validateGraphicsStep checks graphics-specific parameter requirements.
func validateGraphicsStep(i int, step Step, result *ValidationResult) {
	switch step.Action {
	case ActionGraphicsFlyIn, ActionGraphicsFlyOut:
		dir, ok := step.Params["direction"].(string)
		if !ok || dir == "" {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("%s requires 'direction' param", step.Action),
			})
		} else if !validFlyDirectionsSet[dir] {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: fmt.Sprintf("invalid direction %q; must be left, right, top, or bottom", dir),
			})
		}
	case ActionGraphicsAnimate:
		mode, _ := step.Params["mode"].(string)
		if mode != "pulse" && mode != "transition" {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "graphics_animate requires 'mode' param (\"pulse\" or \"transition\")",
			})
		}
	case ActionGraphicsSetRect:
		for _, key := range []string{"x", "y", "width", "height"} {
			if _, ok := step.Params[key].(float64); !ok {
				result.Errors = append(result.Errors, ValidationError{
					Step:    i,
					Message: fmt.Sprintf("graphics_set_rect requires '%s' param (number)", key),
				})
				break
			}
		}
	case ActionGraphicsSlide:
		for _, key := range []string{"x", "y", "width", "height"} {
			if _, ok := step.Params[key].(float64); !ok {
				result.Errors = append(result.Errors, ValidationError{
					Step:    i,
					Message: fmt.Sprintf("graphics_slide requires '%s' param (number)", key),
				})
				break
			}
		}
	case ActionGraphicsUploadFrame:
		if !hasStringParam(step.Params, "template") {
			result.Errors = append(result.Errors, ValidationError{
				Step:    i,
				Message: "graphics_upload_frame requires 'template' param",
			})
		}
	}
}

// hasStringParam returns true if params[key] is a non-empty string.
func hasStringParam(params map[string]interface{}, key string) bool {
	if params == nil {
		return false
	}
	v, ok := params[key].(string)
	return ok && v != ""
}
