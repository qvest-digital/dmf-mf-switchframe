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
	Key           string             `json:"key"`
	Label         string             `json:"label,omitempty"`
	Status        SourceHealthStatus `json:"status"`
	LastFrameTime int64              `json:"lastFrameTime"`
	Width         int                `json:"width,omitempty"`
	Height        int                `json:"height,omitempty"`
	Codec         string             `json:"codec,omitempty"`
}

// ControlRoomState is the full state of the switcher control room,
// broadcast to all connected browsers via the MoQ "control" track.
type ControlRoomState struct {
	ProgramSource        string                 `json:"programSource"`
	PreviewSource        string                 `json:"previewSource"`
	TransitionType       string                 `json:"transitionType"`
	TransitionDurationMs int                    `json:"transitionDurationMs"`
	TransitionPosition   float64                `json:"transitionPosition"`
	InTransition         bool                   `json:"inTransition"`
	AudioLevels          map[string]float64     `json:"audioLevels"`
	TallyState           map[string]TallyStatus `json:"tallyState"`
	Sources              map[string]SourceInfo  `json:"sources"`
	Seq                  uint64                 `json:"seq"`
	Timestamp            int64                  `json:"timestamp"`
}
