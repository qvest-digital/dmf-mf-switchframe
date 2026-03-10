// Package macro provides a macro system for automating sequences of
// switcher operations (cut, preview, transition, audio adjustments).
// Macros are stored as JSON on disk and executed sequentially.
package macro

import "fmt"

// MacroAction identifies the type of operation a macro step performs.
type MacroAction string

const (
	ActionCut        MacroAction = "cut"
	ActionPreview    MacroAction = "preview"
	ActionTransition MacroAction = "transition"
	ActionWait       MacroAction = "wait"
	ActionSetAudio   MacroAction = "set_audio"

	// Audio actions.
	ActionAudioMute       MacroAction = "audio_mute"
	ActionAudioAFV        MacroAction = "audio_afv"
	ActionAudioTrim       MacroAction = "audio_trim"
	ActionAudioMaster     MacroAction = "audio_master"
	ActionAudioEQ         MacroAction = "audio_eq"
	ActionAudioCompressor MacroAction = "audio_compressor"
	ActionAudioDelay      MacroAction = "audio_delay"

	// Transition / program actions.
	ActionFTB MacroAction = "ftb"

	// Graphics actions.
	ActionGraphicsOn            MacroAction = "graphics_on"
	ActionGraphicsOff           MacroAction = "graphics_off"
	ActionGraphicsAutoOn        MacroAction = "graphics_auto_on"
	ActionGraphicsAutoOff       MacroAction = "graphics_auto_off"
	ActionGraphicsAddLayer      MacroAction = "graphics_add_layer"
	ActionGraphicsRemoveLayer   MacroAction = "graphics_remove_layer"
	ActionGraphicsSetRect       MacroAction = "graphics_set_rect"
	ActionGraphicsSetZOrder     MacroAction = "graphics_set_zorder"
	ActionGraphicsFlyIn         MacroAction = "graphics_fly_in"
	ActionGraphicsFlyOut        MacroAction = "graphics_fly_out"
	ActionGraphicsSlide         MacroAction = "graphics_slide"
	ActionGraphicsAnimate       MacroAction = "graphics_animate"
	ActionGraphicsAnimateStop   MacroAction = "graphics_animate_stop"
	ActionGraphicsUploadFrame   MacroAction = "graphics_upload_frame"

	// Recording actions.
	ActionRecordingStart MacroAction = "recording_start"
	ActionRecordingStop  MacroAction = "recording_stop"

	// Preset actions.
	ActionPresetRecall MacroAction = "preset_recall"

	// Source actions.
	ActionKeySet         MacroAction = "key_set"
	ActionKeyDelete      MacroAction = "key_delete"
	ActionSourceLabel    MacroAction = "source_label"
	ActionSourceDelay    MacroAction = "source_delay"
	ActionSourcePosition MacroAction = "source_position"

	// Replay actions.
	ActionReplayMarkIn    MacroAction = "replay_mark_in"
	ActionReplayMarkOut   MacroAction = "replay_mark_out"
	ActionReplayPlay      MacroAction = "replay_play"
	ActionReplayStop      MacroAction = "replay_stop"
	ActionReplayQuickClip MacroAction = "replay_quick_clip"
	ActionReplayPlayLast  MacroAction = "replay_play_last"
	ActionReplayPlayClip  MacroAction = "replay_play_clip"

	// SCTE-35 actions for ad break automation.
	ActionSCTE35Cue    MacroAction = "scte35_cue"
	ActionSCTE35Return MacroAction = "scte35_return"
	ActionSCTE35Cancel MacroAction = "scte35_cancel"
	ActionSCTE35Hold   MacroAction = "scte35_hold"
	ActionSCTE35Extend MacroAction = "scte35_extend"

	// Layout/PIP actions.
	ActionLayoutPreset    MacroAction = "layout_preset"
	ActionLayoutSlotOn    MacroAction = "layout_slot_on"
	ActionLayoutSlotOff   MacroAction = "layout_slot_off"
	ActionLayoutSlotSource MacroAction = "layout_slot_source"
	ActionLayoutClear     MacroAction = "layout_clear"
)

// AllActions is the set of all valid macro actions.
var AllActions = map[MacroAction]bool{
	ActionCut:             true,
	ActionPreview:         true,
	ActionTransition:      true,
	ActionWait:            true,
	ActionSetAudio:        true,
	ActionAudioMute:       true,
	ActionAudioAFV:        true,
	ActionAudioTrim:       true,
	ActionAudioMaster:     true,
	ActionAudioEQ:         true,
	ActionAudioCompressor: true,
	ActionAudioDelay:      true,
	ActionFTB:             true,
	ActionGraphicsOn:            true,
	ActionGraphicsOff:           true,
	ActionGraphicsAutoOn:        true,
	ActionGraphicsAutoOff:       true,
	ActionGraphicsAddLayer:      true,
	ActionGraphicsRemoveLayer:   true,
	ActionGraphicsSetRect:       true,
	ActionGraphicsSetZOrder:     true,
	ActionGraphicsFlyIn:         true,
	ActionGraphicsFlyOut:        true,
	ActionGraphicsSlide:         true,
	ActionGraphicsAnimate:       true,
	ActionGraphicsAnimateStop:   true,
	ActionGraphicsUploadFrame:   true,
	ActionRecordingStart:  true,
	ActionRecordingStop:   true,
	ActionPresetRecall:    true,
	ActionKeySet:          true,
	ActionKeyDelete:       true,
	ActionSourceLabel:     true,
	ActionSourceDelay:     true,
	ActionSourcePosition:  true,
	ActionReplayMarkIn:    true,
	ActionReplayMarkOut:   true,
	ActionReplayPlay:      true,
	ActionReplayStop:      true,
	ActionReplayQuickClip: true,
	ActionReplayPlayLast:  true,
	ActionReplayPlayClip:  true,
	ActionSCTE35Cue:       true,
	ActionSCTE35Return:    true,
	ActionSCTE35Cancel:    true,
	ActionSCTE35Hold:      true,
	ActionSCTE35Extend:    true,
	ActionLayoutPreset:    true,
	ActionLayoutSlotOn:    true,
	ActionLayoutSlotOff:   true,
	ActionLayoutSlotSource: true,
	ActionLayoutClear:     true,
}

// MacroStep is a single operation within a macro sequence.
type MacroStep struct {
	Action MacroAction            `json:"action"`
	Params map[string]interface{} `json:"params"`
}

// Macro is a named sequence of steps that can be saved and replayed.
type Macro struct {
	Name  string      `json:"name"`
	Steps []MacroStep `json:"steps"`
}

// StepStatus indicates the execution state of a single macro step.
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

// StepState tracks the runtime state of a single step during execution.
type StepState struct {
	Action      MacroAction `json:"action"`
	Summary     string      `json:"summary"`
	Status      StepStatus  `json:"status"`
	Error       string      `json:"error,omitempty"`
	WaitMs      int         `json:"waitMs,omitempty"`
	WaitStartMs int64       `json:"waitStartMs,omitempty"`
}

// ExecutionState represents the full progress of a macro execution,
// broadcast to browsers for real-time UI updates.
type ExecutionState struct {
	Running     bool        `json:"running"`
	MacroName   string      `json:"macroName"`
	Steps       []StepState `json:"steps"`
	CurrentStep int         `json:"currentStep"`
	Error       string      `json:"error,omitempty"`
}

// OnProgress is a callback invoked by the runner whenever execution state changes.
type OnProgress func(state ExecutionState)

// StepSummary generates a human-readable summary string for a macro step.
func StepSummary(step MacroStep) string {
	source, _ := step.Params["source"].(string)

	switch step.Action {
	case ActionCut:
		return fmt.Sprintf("Cut → %s", source)
	case ActionPreview:
		return fmt.Sprintf("Preview → %s", source)
	case ActionTransition:
		transType, _ := step.Params["type"].(string)
		durationMs := 0
		if d, ok := step.Params["durationMs"].(float64); ok {
			durationMs = int(d)
		}
		return fmt.Sprintf("Transition %s %dms → %s", transType, durationMs, source)
	case ActionWait:
		ms := 0
		if d, ok := step.Params["ms"].(float64); ok {
			ms = int(d)
		}
		return fmt.Sprintf("Wait %dms", ms)
	case ActionSetAudio:
		level := 0.0
		if l, ok := step.Params["level"].(float64); ok {
			level = l
		}
		return fmt.Sprintf("Set Audio %s %.1fdB", source, level)
	case ActionAudioMute:
		return fmt.Sprintf("Audio Mute %s", source)
	case ActionAudioAFV:
		return fmt.Sprintf("Audio AFV %s", source)
	case ActionAudioTrim:
		return fmt.Sprintf("Audio Trim %s", source)
	case ActionAudioMaster:
		return "Audio Master"
	case ActionAudioEQ:
		return fmt.Sprintf("Audio EQ %s", source)
	case ActionAudioCompressor:
		return fmt.Sprintf("Audio Compressor %s", source)
	case ActionAudioDelay:
		return fmt.Sprintf("Audio Delay %s", source)
	case ActionGraphicsOn:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics On (layer %s)", layerID)
	case ActionGraphicsOff:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Off (layer %s)", layerID)
	case ActionGraphicsAutoOn:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Auto On (layer %s)", layerID)
	case ActionGraphicsAutoOff:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Auto Off (layer %s)", layerID)
	case ActionGraphicsAddLayer:
		return "Graphics Add Layer"
	case ActionGraphicsRemoveLayer:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Remove Layer %s", layerID)
	case ActionGraphicsSetRect:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Set Rect (layer %s)", layerID)
	case ActionGraphicsSetZOrder:
		layerID := fmtLayerID(step.Params)
		z := 0
		if v, ok := step.Params["zOrder"].(float64); ok {
			z = int(v)
		}
		return fmt.Sprintf("Graphics Z-Order %d (layer %s)", z, layerID)
	case ActionGraphicsFlyIn:
		layerID := fmtLayerID(step.Params)
		dir, _ := step.Params["direction"].(string)
		return fmt.Sprintf("Graphics Fly In %s (layer %s)", dir, layerID)
	case ActionGraphicsFlyOut:
		layerID := fmtLayerID(step.Params)
		dir, _ := step.Params["direction"].(string)
		return fmt.Sprintf("Graphics Fly Out %s (layer %s)", dir, layerID)
	case ActionGraphicsSlide:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Slide (layer %s)", layerID)
	case ActionGraphicsAnimate:
		layerID := fmtLayerID(step.Params)
		mode, _ := step.Params["mode"].(string)
		return fmt.Sprintf("Graphics Animate %s (layer %s)", mode, layerID)
	case ActionGraphicsAnimateStop:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Animate Stop (layer %s)", layerID)
	case ActionGraphicsUploadFrame:
		layerID := fmtLayerID(step.Params)
		tmpl, _ := step.Params["template"].(string)
		return fmt.Sprintf("Graphics Upload %s (layer %s)", tmpl, layerID)
	case ActionRecordingStart:
		return "Recording Start"
	case ActionRecordingStop:
		return "Recording Stop"
	case ActionFTB:
		return "Fade to Black"
	case ActionPresetRecall:
		id, _ := step.Params["id"].(string)
		return fmt.Sprintf("Preset Recall %s", id)
	case ActionKeySet:
		return fmt.Sprintf("Key Set %s", source)
	case ActionKeyDelete:
		return fmt.Sprintf("Key Delete %s", source)
	case ActionSourceLabel:
		return fmt.Sprintf("Source Label %s", source)
	case ActionSourceDelay:
		return fmt.Sprintf("Source Delay %s", source)
	case ActionSourcePosition:
		return fmt.Sprintf("Source Position %s", source)
	case ActionReplayMarkIn:
		return fmt.Sprintf("Replay Mark In %s", source)
	case ActionReplayMarkOut:
		return fmt.Sprintf("Replay Mark Out %s", source)
	case ActionReplayPlay:
		return fmt.Sprintf("Replay Play %s", source)
	case ActionReplayStop:
		return "Replay Stop"
	case ActionReplayQuickClip:
		return fmt.Sprintf("Replay Quick Clip %s", source)
	case ActionReplayPlayLast:
		return "Replay Play Last"
	case ActionReplayPlayClip:
		return "Replay Play Clip"
	case ActionSCTE35Cue:
		return "SCTE-35 Cue"
	case ActionSCTE35Return:
		return "SCTE-35 Return"
	case ActionSCTE35Cancel:
		return "SCTE-35 Cancel"
	case ActionSCTE35Hold:
		return "SCTE-35 Hold"
	case ActionSCTE35Extend:
		return "SCTE-35 Extend"
	case ActionLayoutPreset:
		preset, _ := step.Params["preset"].(string)
		return fmt.Sprintf("Layout Preset %s", preset)
	case ActionLayoutSlotOn:
		return fmt.Sprintf("Layout Slot On %s", source)
	case ActionLayoutSlotOff:
		return fmt.Sprintf("Layout Slot Off %s", source)
	case ActionLayoutSlotSource:
		return fmt.Sprintf("Layout Slot Source %s", source)
	case ActionLayoutClear:
		return "Layout Clear"
	default:
		return string(step.Action)
	}
}

// fmtLayerID extracts a layerId param as a display string.
func fmtLayerID(params map[string]interface{}) string {
	if v, ok := params["layerId"].(float64); ok {
		return fmt.Sprintf("%d", int(v))
	}
	return "0"
}
