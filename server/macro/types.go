// Package macro provides a macro system for automating sequences of
// switcher operations (cut, preview, transition, audio adjustments).
// Macros are stored as JSON on disk and executed sequentially.
package macro

import "fmt"

// Action identifies the type of operation a macro step performs.
type Action string

const (
	ActionCut        Action = "cut"
	ActionPreview    Action = "preview"
	ActionTransition Action = "transition"
	ActionWait       Action = "wait"
	ActionSetAudio   Action = "set_audio"

	// Audio actions.
	ActionAudioMute       Action = "audio_mute"
	ActionAudioAFV        Action = "audio_afv"
	ActionAudioTrim       Action = "audio_trim"
	ActionAudioMaster     Action = "audio_master"
	ActionAudioEQ         Action = "audio_eq"
	ActionAudioCompressor Action = "audio_compressor"
	ActionAudioDelay      Action = "audio_delay"

	// Transition / program actions.
	ActionFTB Action = "ftb"

	// Graphics actions.
	ActionGraphicsOn            Action = "graphics_on"
	ActionGraphicsOff           Action = "graphics_off"
	ActionGraphicsAutoOn        Action = "graphics_auto_on"
	ActionGraphicsAutoOff       Action = "graphics_auto_off"
	ActionGraphicsAddLayer      Action = "graphics_add_layer"
	ActionGraphicsRemoveLayer   Action = "graphics_remove_layer"
	ActionGraphicsSetRect       Action = "graphics_set_rect"
	ActionGraphicsSetZOrder     Action = "graphics_set_zorder"
	ActionGraphicsFlyIn         Action = "graphics_fly_in"
	ActionGraphicsFlyOut        Action = "graphics_fly_out"
	ActionGraphicsFlyOn         Action = "graphics_fly_on"
	ActionGraphicsSlide         Action = "graphics_slide"
	ActionGraphicsAnimate       Action = "graphics_animate"
	ActionGraphicsAnimateStop   Action = "graphics_animate_stop"
	ActionGraphicsUploadFrame       Action = "graphics_upload_frame"
	ActionGraphicsTextAnimate       Action = "graphics_text_animate"
	ActionGraphicsTextAnimateStop   Action = "graphics_text_animate_stop"
	ActionGraphicsTickerStart       Action = "graphics_ticker_start"
	ActionGraphicsTickerStop        Action = "graphics_ticker_stop"

	// Recording actions.
	ActionRecordingStart Action = "recording_start"
	ActionRecordingStop  Action = "recording_stop"

	// Preset actions.
	ActionPresetRecall Action = "preset_recall"

	// Source actions.
	ActionKeySet         Action = "key_set"
	ActionKeyDelete      Action = "key_delete"
	ActionSourceLabel    Action = "source_label"
	ActionSourceDelay    Action = "source_delay"
	ActionSourcePosition Action = "source_position"

	// Replay actions.
	ActionReplayMarkIn    Action = "replay_mark_in"
	ActionReplayMarkOut   Action = "replay_mark_out"
	ActionReplayPlay      Action = "replay_play"
	ActionReplayStop      Action = "replay_stop"
	ActionReplayQuickClip Action = "replay_quick_clip"
	ActionReplayPlayLast  Action = "replay_play_last"
	ActionReplayPlayClip  Action = "replay_play_clip"

	// SCTE-35 actions for ad break automation.
	ActionSCTE35Cue    Action = "scte35_cue"
	ActionSCTE35Return Action = "scte35_return"
	ActionSCTE35Cancel Action = "scte35_cancel"
	ActionSCTE35Hold   Action = "scte35_hold"
	ActionSCTE35Extend Action = "scte35_extend"

	// Layout/PIP actions.
	ActionLayoutPreset    Action = "layout_preset"
	ActionLayoutSlotOn    Action = "layout_slot_on"
	ActionLayoutSlotOff   Action = "layout_slot_off"
	ActionLayoutSlotSource Action = "layout_slot_source"
	ActionLayoutClear     Action = "layout_clear"

	// Caption actions.
	ActionCaptionMode  Action = "caption_mode"
	ActionCaptionText  Action = "caption_text"
	ActionCaptionClear Action = "caption_clear"
)

// AllActions is the set of all valid macro actions.
var AllActions = map[Action]bool{
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
	ActionGraphicsFlyOn:         true,
	ActionGraphicsSlide:         true,
	ActionGraphicsAnimate:       true,
	ActionGraphicsAnimateStop:   true,
	ActionGraphicsUploadFrame:       true,
	ActionGraphicsTextAnimate:      true,
	ActionGraphicsTextAnimateStop:  true,
	ActionGraphicsTickerStart:      true,
	ActionGraphicsTickerStop:       true,
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
	ActionCaptionMode:     true,
	ActionCaptionText:     true,
	ActionCaptionClear:    true,
}

// Step is a single operation within a macro sequence.
type Step struct {
	Action Action            `json:"action"`
	Params map[string]interface{} `json:"params"`
}

// Macro is a named sequence of steps that can be saved and replayed.
type Macro struct {
	Name  string      `json:"name"`
	Steps []Step `json:"steps"`
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
	Action      Action `json:"action"`
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
func StepSummary(step Step) string {
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
	case ActionGraphicsFlyOn:
		layerID := fmtLayerID(step.Params)
		dir, _ := step.Params["direction"].(string)
		return fmt.Sprintf("Graphics Fly On %s (layer %s)", dir, layerID)
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
	case ActionGraphicsTextAnimate:
		layerID := fmtLayerID(step.Params)
		mode, _ := step.Params["mode"].(string)
		return fmt.Sprintf("Graphics Text Animate %s (layer %s)", mode, layerID)
	case ActionGraphicsTextAnimateStop:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Text Animate Stop (layer %s)", layerID)
	case ActionGraphicsTickerStart:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Ticker Start (layer %s)", layerID)
	case ActionGraphicsTickerStop:
		layerID := fmtLayerID(step.Params)
		return fmt.Sprintf("Graphics Ticker Stop (layer %s)", layerID)
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
	case ActionCaptionMode:
		mode, _ := step.Params["mode"].(string)
		return fmt.Sprintf("Caption Mode %s", mode)
	case ActionCaptionText:
		text, _ := step.Params["text"].(string)
		if len(text) > 30 {
			text = text[:30] + "..."
		}
		return fmt.Sprintf("Caption Text %q", text)
	case ActionCaptionClear:
		return "Caption Clear"
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
