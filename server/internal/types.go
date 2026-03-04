// Package internal provides shared types for the Switchframe server.
package internal

import "github.com/zsiec/switchframe/server/output"

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

// AudioChannel describes the audio mixer state for a single source.
type AudioChannel struct {
	Level float64 `json:"level"` // dB (-inf to +12)
	Muted bool    `json:"muted"`
	AFV   bool    `json:"afv"` // audio-follows-video
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
	AudioLevels          map[string]float64        `json:"audioLevels,omitempty"`
	AudioChannels        map[string]AudioChannel   `json:"audioChannels"`
	MasterLevel          float64                   `json:"masterLevel"`
	ProgramPeak          [2]float64                `json:"programPeak"`
	TallyState           map[string]TallyStatus    `json:"tallyState"`
	Recording            *output.RecordingStatus   `json:"recording,omitempty"`
	SRTOutput            *output.SRTOutputStatus   `json:"srtOutput,omitempty"`
	Sources              map[string]SourceInfo     `json:"sources"`
	Seq                  uint64                    `json:"seq"`
	Timestamp            int64                     `json:"timestamp"`
}
