// Package internal provides shared types for the Switchframe server.
package internal

// TallyStatus represents the tally light state for a source.
type TallyStatus string

const (
	TallyProgram TallyStatus = "program"
	TallyPreview TallyStatus = "preview"
	TallyIdle    TallyStatus = "idle"
)

// SourceHealthStatus represents the health/connectivity state of a video source.
type SourceHealthStatus string

const (
	SourceHealthy  SourceHealthStatus = "healthy"
	SourceStale    SourceHealthStatus = "stale"
	SourceNoSignal SourceHealthStatus = "no_signal"
	SourceOffline  SourceHealthStatus = "offline"
)

// SourceInfo describes a connected video source and its current state.
type SourceInfo struct {
	Key     string             `json:"key"`
	Label   string             `json:"label,omitempty"`
	Status  SourceHealthStatus `json:"status"`
	DelayMs int                `json:"delayMs,omitempty"`
}

// AudioTransitionMode describes how audio should behave during a video transition.
type AudioTransitionMode int

const (
	AudioCrossfade    AudioTransitionMode = iota // Mix: equal-power A→B
	AudioDipToSilence                            // Dip: A→silence→B
	AudioFadeOut                                 // FTB: A→silence
	AudioFadeIn                                  // FTB Reverse: silence→A
)

// AudioChannel describes the audio mixer state for a single source.
type AudioChannel struct {
	Level float64 `json:"level"` // dB (-inf to +12)
	Muted bool    `json:"muted"`
	AFV   bool    `json:"afv"` // audio-follows-video
}

// RecordingStatus is the JSON-serializable status for recording output,
// included in ControlRoomState for the browser.
type RecordingStatus struct {
	Active       bool    `json:"active"`
	Filename     string  `json:"filename,omitempty"`
	BytesWritten int64   `json:"bytesWritten,omitempty"`
	DurationSecs float64 `json:"durationSecs,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// SRTOutputStatus is the JSON-serializable status for SRT output,
// included in ControlRoomState for the browser.
type SRTOutputStatus struct {
	Active       bool   `json:"active"`
	Mode         string `json:"mode,omitempty"`
	Address      string `json:"address,omitempty"`
	Port         int    `json:"port,omitempty"`
	State        string `json:"state,omitempty"`
	Connections  int    `json:"connections,omitempty"`
	BytesWritten int64  `json:"bytesWritten,omitempty"`
	Error        string `json:"error,omitempty"`
}

// PresetInfo is a summary of a saved preset, included in ControlRoomState
// so the browser knows which presets are available for recall.
type PresetInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GraphicsState is the JSON-serializable state for the downstream
// keyer (DSK) graphics overlay, included in ControlRoomState.
type GraphicsState struct {
	Active       bool    `json:"active"`
	Template     string  `json:"template,omitempty"`
	FadePosition float64 `json:"fadePosition,omitempty"`
}

// ControlRoomState is the full state of the switcher control room,
// broadcast to all connected browsers via the MoQ "control" track.
type ControlRoomState struct {
	ProgramSource        string                    `json:"programSource"`
	PreviewSource        string                    `json:"previewSource"`
	TransitionType       string                    `json:"transitionType"`
	TransitionDurationMs int                       `json:"transitionDurationMs,omitempty"`
	TransitionPosition   float64                   `json:"transitionPosition,omitempty"`
	InTransition         bool                      `json:"inTransition,omitempty"`
	FTBActive            bool                      `json:"ftbActive,omitempty"`
	AudioChannels        map[string]AudioChannel   `json:"audioChannels"`
	MasterLevel          float64                   `json:"masterLevel"`
	ProgramPeak          [2]float64                `json:"programPeak"`
	GainReduction        float64                   `json:"gainReduction,omitempty"`
	TallyState           map[string]TallyStatus    `json:"tallyState"`
	Recording            *RecordingStatus           `json:"recording,omitempty"`
	SRTOutput            *SRTOutputStatus           `json:"srtOutput,omitempty"`
	Sources              map[string]SourceInfo     `json:"sources"`
	Presets              []PresetInfo              `json:"presets,omitempty"`
	Graphics             *GraphicsState            `json:"graphics,omitempty"`
	Seq                  uint64                    `json:"seq"`
	Timestamp            int64                     `json:"timestamp"`
}
