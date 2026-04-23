package macro

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Target is the interface that a macro runner uses to execute steps.
// It abstracts the switcher/mixer so macro execution is testable without
// real hardware or relays.
type Target interface {
	// Cut switches the program source.
	Cut(ctx context.Context, source string) error

	// SetPreview sets the preview source.
	SetPreview(ctx context.Context, source string) error

	// StartTransition begins a mix/dip/wipe/stinger transition.
	StartTransition(ctx context.Context, source, transType string, durationMs int, wipeDirection, stingerName string) error

	// SetLevel sets the audio level for a source channel.
	SetLevel(ctx context.Context, source string, level float64) error

	// Execute dispatches a generic action with arbitrary params.
	// Used for all new actions that don't need dedicated interface methods.
	Execute(ctx context.Context, action string, params map[string]any) error

	// SCTE35Cue injects a SCTE-35 splice cue (e.g., ad break start).
	SCTE35Cue(ctx context.Context, params map[string]any) (uint32, error)

	// SCTE35Return signals a return-to-program for a splice event.
	// eventID=0 means the most recent event.
	SCTE35Return(ctx context.Context, eventID uint32) error

	// SCTE35Cancel cancels a pending splice event.
	SCTE35Cancel(ctx context.Context, eventID uint32) error

	// SCTE35Hold holds a break indefinitely (prevents auto-return).
	SCTE35Hold(ctx context.Context, eventID uint32) error

	// SCTE35Extend extends a break by the given duration.
	SCTE35Extend(ctx context.Context, eventID uint32, durationMs int64) error
}

// Run executes a macro sequentially against the given target.
// It returns an error if any step fails or the context is cancelled.
// The "wait" action blocks for the specified duration; context cancellation
// aborts mid-wait.
//
// If onProgress is non-nil, it is called with the current ExecutionState
// at each state transition (initial, step running, step done/failed).
func Run(ctx context.Context, m Macro, target Target, onProgress OnProgress) error {
	// Build initial state with all steps pending.
	state := ExecutionState{
		Running:   true,
		MacroName: m.Name,
		Steps:     make([]StepState, len(m.Steps)),
	}
	for i, step := range m.Steps {
		state.Steps[i] = StepState{
			Action:  step.Action,
			Summary: StepSummary(step),
			Status:  StepPending,
		}
	}

	notify := func() {
		if onProgress != nil {
			onProgress(state)
		}
	}

	// Notify initial state.
	notify()

	for i, step := range m.Steps {
		// Check context before each step.
		select {
		case <-ctx.Done():
			state.Steps[i].Status = StepFailed
			state.Steps[i].Error = "cancelled"
			for j := i + 1; j < len(state.Steps); j++ {
				state.Steps[j].Status = StepSkipped
			}
			state.Error = ctx.Err().Error()
			notify()
			return ctx.Err()
		default:
		}

		// Mark step running.
		state.CurrentStep = i
		state.Steps[i].Status = StepRunning

		// For wait steps, populate WaitMs and WaitStartMs before notifying.
		// A mistyped `ms` leaves WaitMs at 0 here; executeStep below will
		// reject the step and publish the real error via StepState.Error.
		if step.Action == ActionWait {
			if ms, _ := ParamFloatDefault(step.Params, "ms", 0); ms > 0 {
				state.Steps[i].WaitMs = int(ms)
			}
			state.Steps[i].WaitStartMs = time.Now().UnixMilli()
		}

		notify()

		if err := executeStep(ctx, step, target); err != nil {
			state.Steps[i].Status = StepFailed
			state.Steps[i].Error = err.Error()
			for j := i + 1; j < len(state.Steps); j++ {
				state.Steps[j].Status = StepSkipped
			}
			state.Error = fmt.Sprintf("step %d (%s): %s", i, step.Action, err.Error())
			notify()
			return fmt.Errorf("step %d (%s): %w", i, step.Action, err)
		}

		state.Steps[i].Status = StepDone
		notify()
	}
	state.Running = false
	notify()
	return nil
}

func executeStep(ctx context.Context, step Step, target Target) error {
	switch step.Action {
	case ActionCut:
		source, err := ParamString(step.Params, "source")
		if err != nil {
			return err
		}
		if source == "" {
			return errors.New("cut requires 'source' param")
		}
		return target.Cut(ctx, source)

	case ActionPreview:
		source, err := ParamString(step.Params, "source")
		if err != nil {
			return err
		}
		if source == "" {
			return errors.New("preview requires 'source' param")
		}
		return target.SetPreview(ctx, source)

	case ActionTransition:
		source, err := ParamString(step.Params, "source")
		if err != nil {
			return err
		}
		if source == "" {
			return errors.New("transition requires 'source' param")
		}
		transType, err := ParamStringDefault(step.Params, "type", "mix")
		if err != nil {
			return err
		}
		if transType == "" {
			transType = "mix"
		}
		durationMs, err := ParamFloatDefault(step.Params, "durationMs", 1000)
		if err != nil {
			return err
		}
		wipeDirection, err := ParamStringDefault(step.Params, "wipeDirection", "")
		if err != nil {
			return err
		}
		stingerName, err := ParamStringDefault(step.Params, "stingerName", "")
		if err != nil {
			return err
		}
		return target.StartTransition(ctx, source, transType, int(durationMs), wipeDirection, stingerName)

	case ActionWait:
		ms, err := ParamFloatDefault(step.Params, "ms", 0)
		if err != nil {
			return err
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
		source, err := ParamString(step.Params, "source")
		if err != nil {
			return err
		}
		if source == "" {
			return errors.New("set_audio requires 'source' param")
		}
		level, err := ParamFloatDefault(step.Params, "level", 0)
		if err != nil {
			return err
		}
		return target.SetLevel(ctx, source, level)

	case ActionSCTE35Cue:
		_, err := target.SCTE35Cue(ctx, step.Params)
		return err

	case ActionSCTE35Return:
		eventID, err := ParamFloatDefault(step.Params, "eventId", 0)
		if err != nil {
			return err
		}
		return target.SCTE35Return(ctx, uint32(eventID))

	case ActionSCTE35Cancel:
		id, err := ParamFloat(step.Params, "eventId")
		if err != nil {
			return err
		}
		return target.SCTE35Cancel(ctx, uint32(id))

	case ActionSCTE35Hold:
		id, err := ParamFloat(step.Params, "eventId")
		if err != nil {
			return err
		}
		return target.SCTE35Hold(ctx, uint32(id))

	case ActionSCTE35Extend:
		id, err := ParamFloat(step.Params, "eventId")
		if err != nil {
			return err
		}
		dur, err := ParamFloat(step.Params, "durationMs")
		if err != nil {
			return err
		}
		return target.SCTE35Extend(ctx, uint32(id), int64(dur))

	default:
		if IsValidAction(step.Action) {
			return target.Execute(ctx, string(step.Action), step.Params)
		}
		return fmt.Errorf("unknown action %q", step.Action)
	}
}
