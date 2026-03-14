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
	ActionGraphicsOn              Action = "graphics_on"
	ActionGraphicsOff             Action = "graphics_off"
	ActionGraphicsAutoOn          Action = "graphics_auto_on"
	ActionGraphicsAutoOff         Action = "graphics_auto_off"
	ActionGraphicsAddLayer        Action = "graphics_add_layer"
	ActionGraphicsRemoveLayer     Action = "graphics_remove_layer"
	ActionGraphicsSetRect         Action = "graphics_set_rect"
	ActionGraphicsSetZOrder       Action = "graphics_set_zorder"
	ActionGraphicsFlyIn           Action = "graphics_fly_in"
	ActionGraphicsFlyOut          Action = "graphics_fly_out"
	ActionGraphicsFlyOn           Action = "graphics_fly_on"
	ActionGraphicsSlide           Action = "graphics_slide"
	ActionGraphicsAnimate         Action = "graphics_animate"
	ActionGraphicsAnimateStop     Action = "graphics_animate_stop"
	ActionGraphicsUploadFrame     Action = "graphics_upload_frame"
	ActionGraphicsTextAnimate     Action = "graphics_text_animate"
	ActionGraphicsTextAnimateStop Action = "graphics_text_animate_stop"
	ActionGraphicsTickerStart     Action = "graphics_ticker_start"
	ActionGraphicsTickerStop      Action = "graphics_ticker_stop"

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
	ActionLayoutPreset     Action = "layout_preset"
	ActionLayoutSlotOn     Action = "layout_slot_on"
	ActionLayoutSlotOff    Action = "layout_slot_off"
	ActionLayoutSlotSource Action = "layout_slot_source"
	ActionLayoutClear      Action = "layout_clear"

	// Caption actions.
	ActionCaptionMode  Action = "caption_mode"
	ActionCaptionText  Action = "caption_text"
	ActionCaptionClear Action = "caption_clear"

	// Clip player actions.
	ActionClipLoad  Action = "clip_load"
	ActionClipPlay  Action = "clip_play"
	ActionClipPause Action = "clip_pause"
	ActionClipStop  Action = "clip_stop"
	ActionClipEject Action = "clip_eject"
	ActionClipSeek  Action = "clip_seek"
)

// allActions is the set of all valid macro actions.
var allActions = map[Action]bool{
	ActionCut:                     true,
	ActionPreview:                 true,
	ActionTransition:              true,
	ActionWait:                    true,
	ActionSetAudio:                true,
	ActionAudioMute:               true,
	ActionAudioAFV:                true,
	ActionAudioTrim:               true,
	ActionAudioMaster:             true,
	ActionAudioEQ:                 true,
	ActionAudioCompressor:         true,
	ActionAudioDelay:              true,
	ActionFTB:                     true,
	ActionGraphicsOn:              true,
	ActionGraphicsOff:             true,
	ActionGraphicsAutoOn:          true,
	ActionGraphicsAutoOff:         true,
	ActionGraphicsAddLayer:        true,
	ActionGraphicsRemoveLayer:     true,
	ActionGraphicsSetRect:         true,
	ActionGraphicsSetZOrder:       true,
	ActionGraphicsFlyIn:           true,
	ActionGraphicsFlyOut:          true,
	ActionGraphicsFlyOn:           true,
	ActionGraphicsSlide:           true,
	ActionGraphicsAnimate:         true,
	ActionGraphicsAnimateStop:     true,
	ActionGraphicsUploadFrame:     true,
	ActionGraphicsTextAnimate:     true,
	ActionGraphicsTextAnimateStop: true,
	ActionGraphicsTickerStart:     true,
	ActionGraphicsTickerStop:      true,
	ActionRecordingStart:          true,
	ActionRecordingStop:           true,
	ActionPresetRecall:            true,
	ActionKeySet:                  true,
	ActionKeyDelete:               true,
	ActionSourceLabel:             true,
	ActionSourceDelay:             true,
	ActionSourcePosition:          true,
	ActionReplayMarkIn:            true,
	ActionReplayMarkOut:           true,
	ActionReplayPlay:              true,
	ActionReplayStop:              true,
	ActionReplayQuickClip:         true,
	ActionReplayPlayLast:          true,
	ActionReplayPlayClip:          true,
	ActionSCTE35Cue:               true,
	ActionSCTE35Return:            true,
	ActionSCTE35Cancel:            true,
	ActionSCTE35Hold:              true,
	ActionSCTE35Extend:            true,
	ActionLayoutPreset:            true,
	ActionLayoutSlotOn:            true,
	ActionLayoutSlotOff:           true,
	ActionLayoutSlotSource:        true,
	ActionLayoutClear:             true,
	ActionCaptionMode:             true,
	ActionCaptionText:             true,
	ActionCaptionClear:            true,
	ActionClipLoad:                true,
	ActionClipPlay:                true,
	ActionClipPause:               true,
	ActionClipStop:                true,
	ActionClipEject:               true,
	ActionClipSeek:                true,
}

// IsValidAction reports whether a is a recognized macro action.
func IsValidAction(a Action) bool {
	return allActions[a]
}

// Step is a single operation within a macro sequence.
type Step struct {
	Action Action         `json:"action"`
	Params map[string]any `json:"params"`
}

// Macro is a named sequence of steps that can be saved and replayed.
type Macro struct {
	Name  string `json:"name"`
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
	Action      Action     `json:"action"`
	Summary     string     `json:"summary"`
	Status      StepStatus `json:"status"`
	Error       string     `json:"error,omitempty"`
	WaitMs      int        `json:"waitMs,omitempty"`
	WaitStartMs int64      `json:"waitStartMs,omitempty"`
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

// stepSummaryFunc is a function that generates a summary string for a step's parameters.
type stepSummaryFunc func(step Step) string

// stepSummaryMap maps actions to their summary generation functions.
// Actions with custom parameter formatting use dedicated functions;
// simple actions use sourceSummary or staticSummary helpers.
var stepSummaryMap = map[Action]stepSummaryFunc{
	ActionCut:     func(s Step) string { return fmt.Sprintf("Cut → %s", paramStr(s, "source")) },
	ActionPreview: func(s Step) string { return fmt.Sprintf("Preview → %s", paramStr(s, "source")) },
	ActionTransition: func(s Step) string {
		transType, _ := s.Params["type"].(string)
		durationMs := 0
		if d, ok := s.Params["durationMs"].(float64); ok {
			durationMs = int(d)
		}
		return fmt.Sprintf("Transition %s %dms → %s", transType, durationMs, paramStr(s, "source"))
	},
	ActionWait: func(s Step) string {
		ms := 0
		if d, ok := s.Params["ms"].(float64); ok {
			ms = int(d)
		}
		return fmt.Sprintf("Wait %dms", ms)
	},
	ActionSetAudio: func(s Step) string {
		level := 0.0
		if l, ok := s.Params["level"].(float64); ok {
			level = l
		}
		return fmt.Sprintf("Set Audio %s %.1fdB", paramStr(s, "source"), level)
	},
	ActionAudioMute:           sourceSummary("Audio Mute"),
	ActionAudioAFV:            sourceSummary("Audio AFV"),
	ActionAudioTrim:           sourceSummary("Audio Trim"),
	ActionAudioMaster:         staticSummary("Audio Master"),
	ActionAudioEQ:             sourceSummary("Audio EQ"),
	ActionAudioCompressor:     sourceSummary("Audio Compressor"),
	ActionAudioDelay:          sourceSummary("Audio Delay"),
	ActionGraphicsOn:          layerSummary("Graphics On"),
	ActionGraphicsOff:         layerSummary("Graphics Off"),
	ActionGraphicsAutoOn:      layerSummary("Graphics Auto On"),
	ActionGraphicsAutoOff:     layerSummary("Graphics Auto Off"),
	ActionGraphicsAddLayer:    staticSummary("Graphics Add Layer"),
	ActionGraphicsRemoveLayer: func(s Step) string { return fmt.Sprintf("Graphics Remove Layer %s", fmtLayerID(s.Params)) },
	ActionGraphicsSetRect:     layerSummary("Graphics Set Rect"),
	ActionGraphicsSetZOrder: func(s Step) string {
		z := 0
		if v, ok := s.Params["zOrder"].(float64); ok {
			z = int(v)
		}
		return fmt.Sprintf("Graphics Z-Order %d (layer %s)", z, fmtLayerID(s.Params))
	},
	ActionGraphicsFlyIn:  layerDirSummary("Graphics Fly In"),
	ActionGraphicsFlyOut: layerDirSummary("Graphics Fly Out"),
	ActionGraphicsFlyOn:  layerDirSummary("Graphics Fly On"),
	ActionGraphicsSlide:  layerSummary("Graphics Slide"),
	ActionGraphicsAnimate: func(s Step) string {
		mode, _ := s.Params["mode"].(string)
		return fmt.Sprintf("Graphics Animate %s (layer %s)", mode, fmtLayerID(s.Params))
	},
	ActionGraphicsAnimateStop: layerSummary("Graphics Animate Stop"),
	ActionGraphicsUploadFrame: func(s Step) string {
		tmpl, _ := s.Params["template"].(string)
		return fmt.Sprintf("Graphics Upload %s (layer %s)", tmpl, fmtLayerID(s.Params))
	},
	ActionGraphicsTextAnimate: func(s Step) string {
		mode, _ := s.Params["mode"].(string)
		return fmt.Sprintf("Graphics Text Animate %s (layer %s)", mode, fmtLayerID(s.Params))
	},
	ActionGraphicsTextAnimateStop: layerSummary("Graphics Text Animate Stop"),
	ActionGraphicsTickerStart:     layerSummary("Graphics Ticker Start"),
	ActionGraphicsTickerStop:      layerSummary("Graphics Ticker Stop"),
	ActionRecordingStart:          staticSummary("Recording Start"),
	ActionRecordingStop:           staticSummary("Recording Stop"),
	ActionFTB:                     staticSummary("Fade to Black"),
	ActionPresetRecall: func(s Step) string {
		id, _ := s.Params["id"].(string)
		return fmt.Sprintf("Preset Recall %s", id)
	},
	ActionKeySet:          sourceSummary("Key Set"),
	ActionKeyDelete:       sourceSummary("Key Delete"),
	ActionSourceLabel:     sourceSummary("Source Label"),
	ActionSourceDelay:     sourceSummary("Source Delay"),
	ActionSourcePosition:  sourceSummary("Source Position"),
	ActionReplayMarkIn:    sourceSummary("Replay Mark In"),
	ActionReplayMarkOut:   sourceSummary("Replay Mark Out"),
	ActionReplayPlay:      sourceSummary("Replay Play"),
	ActionReplayStop:      staticSummary("Replay Stop"),
	ActionReplayQuickClip: sourceSummary("Replay Quick Clip"),
	ActionReplayPlayLast:  staticSummary("Replay Play Last"),
	ActionReplayPlayClip:  staticSummary("Replay Play Clip"),
	ActionSCTE35Cue:       staticSummary("SCTE-35 Cue"),
	ActionSCTE35Return:    staticSummary("SCTE-35 Return"),
	ActionSCTE35Cancel:    staticSummary("SCTE-35 Cancel"),
	ActionSCTE35Hold:      staticSummary("SCTE-35 Hold"),
	ActionSCTE35Extend:    staticSummary("SCTE-35 Extend"),
	ActionLayoutPreset: func(s Step) string {
		preset, _ := s.Params["preset"].(string)
		return fmt.Sprintf("Layout Preset %s", preset)
	},
	ActionLayoutSlotOn:     sourceSummary("Layout Slot On"),
	ActionLayoutSlotOff:    sourceSummary("Layout Slot Off"),
	ActionLayoutSlotSource: sourceSummary("Layout Slot Source"),
	ActionLayoutClear:      staticSummary("Layout Clear"),
	ActionCaptionMode: func(s Step) string {
		mode, _ := s.Params["mode"].(string)
		return fmt.Sprintf("Caption Mode %s", mode)
	},
	ActionCaptionText: func(s Step) string {
		text, _ := s.Params["text"].(string)
		if len(text) > 30 {
			text = text[:30] + "..."
		}
		return fmt.Sprintf("Caption Text %q", text)
	},
	ActionCaptionClear: staticSummary("Caption Clear"),

	// Clip player actions.
	ActionClipLoad: func(s Step) string {
		return fmt.Sprintf("Clip Load %s → Player %s", paramStr(s, "clipId"), fmtPlayerID(s.Params))
	},
	ActionClipPlay: func(s Step) string {
		return fmt.Sprintf("Clip Play Player %s", fmtPlayerID(s.Params))
	},
	ActionClipPause: func(s Step) string {
		return fmt.Sprintf("Clip Pause Player %s", fmtPlayerID(s.Params))
	},
	ActionClipStop: func(s Step) string {
		return fmt.Sprintf("Clip Stop Player %s", fmtPlayerID(s.Params))
	},
	ActionClipEject: func(s Step) string {
		return fmt.Sprintf("Clip Eject Player %s", fmtPlayerID(s.Params))
	},
	ActionClipSeek: func(s Step) string {
		pos := 0.0
		if v, ok := s.Params["position"].(float64); ok {
			pos = v
		}
		return fmt.Sprintf("Clip Seek Player %s → %.0f%%", fmtPlayerID(s.Params), pos*100)
	},
}

// paramStr extracts a string parameter from a step.
func paramStr(s Step, key string) string {
	v, _ := s.Params[key].(string)
	return v
}

// sourceSummary returns a summary function that formats "Label source".
func sourceSummary(label string) stepSummaryFunc {
	return func(s Step) string {
		return fmt.Sprintf("%s %s", label, paramStr(s, "source"))
	}
}

// staticSummary returns a summary function that always returns a fixed string.
func staticSummary(label string) stepSummaryFunc {
	return func(_ Step) string { return label }
}

// layerSummary returns a summary function that formats "Label (layer N)".
func layerSummary(label string) stepSummaryFunc {
	return func(s Step) string {
		return fmt.Sprintf("%s (layer %s)", label, fmtLayerID(s.Params))
	}
}

// layerDirSummary returns a summary function that formats "Label dir (layer N)".
func layerDirSummary(label string) stepSummaryFunc {
	return func(s Step) string {
		dir, _ := s.Params["direction"].(string)
		return fmt.Sprintf("%s %s (layer %s)", label, dir, fmtLayerID(s.Params))
	}
}

// StepSummary generates a human-readable summary string for a macro step.
func StepSummary(step Step) string {
	if fn, ok := stepSummaryMap[step.Action]; ok {
		return fn(step)
	}
	return string(step.Action)
}

// fmtPlayerID extracts a player param as a display string.
func fmtPlayerID(params map[string]any) string {
	if v, ok := params["player"].(float64); ok {
		return fmt.Sprintf("%d", int(v))
	}
	return "?"
}

// fmtLayerID extracts a layerId param as a display string.
func fmtLayerID(params map[string]any) string {
	if v, ok := params["layerId"].(float64); ok {
		return fmt.Sprintf("%d", int(v))
	}
	return "0"
}
