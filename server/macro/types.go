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
)

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
