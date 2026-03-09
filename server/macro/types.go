// Package macro provides a macro system for automating sequences of
// switcher operations (cut, preview, transition, audio adjustments).
// Macros are stored as JSON on disk and executed sequentially.
package macro

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
	ActionGraphicsOn      MacroAction = "graphics_on"
	ActionGraphicsOff     MacroAction = "graphics_off"
	ActionGraphicsAutoOn  MacroAction = "graphics_auto_on"
	ActionGraphicsAutoOff MacroAction = "graphics_auto_off"

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
	ActionGraphicsOn:      true,
	ActionGraphicsOff:     true,
	ActionGraphicsAutoOn:  true,
	ActionGraphicsAutoOff: true,
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
