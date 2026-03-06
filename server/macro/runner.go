package macro

import (
	"context"
	"fmt"
	"time"
)

// MacroTarget is the interface that a macro runner uses to execute steps.
// It abstracts the switcher/mixer so macro execution is testable without
// real hardware or relays.
type MacroTarget interface {
	// Cut switches the program source.
	Cut(source string) error

	// SetPreview sets the preview source.
	SetPreview(source string) error

	// StartTransition begins a mix/dip/wipe transition.
	StartTransition(transType string, durationMs int) error

	// SetLevel sets the audio level for a source channel.
	SetLevel(source string, level float64) error
}

// Run executes a macro sequentially against the given target.
// It returns an error if any step fails or the context is cancelled.
// The "wait" action blocks for the specified duration; context cancellation
// aborts mid-wait.
func Run(ctx context.Context, m Macro, target MacroTarget) error {
	for i, step := range m.Steps {
		// Check context before each step.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := executeStep(ctx, step, target); err != nil {
			return fmt.Errorf("step %d (%s): %w", i, step.Action, err)
		}
	}
	return nil
}

func executeStep(ctx context.Context, step MacroStep, target MacroTarget) error {
	switch step.Action {
	case ActionCut:
		source, _ := step.Params["source"].(string)
		if source == "" {
			return fmt.Errorf("cut requires 'source' param")
		}
		return target.Cut(source)

	case ActionPreview:
		source, _ := step.Params["source"].(string)
		if source == "" {
			return fmt.Errorf("preview requires 'source' param")
		}
		return target.SetPreview(source)

	case ActionTransition:
		transType, _ := step.Params["type"].(string)
		if transType == "" {
			transType = "mix"
		}
		durationMs := 1000
		if d, ok := step.Params["durationMs"].(float64); ok {
			durationMs = int(d)
		}
		return target.StartTransition(transType, durationMs)

	case ActionWait:
		ms := 0.0
		if d, ok := step.Params["ms"].(float64); ok {
			ms = d
		}
		if ms <= 0 {
			return nil
		}
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}

	case ActionSetAudio:
		source, _ := step.Params["source"].(string)
		if source == "" {
			return fmt.Errorf("set_audio requires 'source' param")
		}
		level := 0.0
		if l, ok := step.Params["level"].(float64); ok {
			level = l
		}
		return target.SetLevel(source, level)

	default:
		return fmt.Errorf("unknown action %q", step.Action)
	}
}
