package output

import (
	"context"
	"time"
)

// AdapterState represents the lifecycle state of an output adapter.
type AdapterState string

const (
	StateStarting     AdapterState = "starting"
	StateActive       AdapterState = "active"
	StateReconnecting AdapterState = "reconnecting"
	StateStopped      AdapterState = "stopped"
	StateError        AdapterState = "error"
)

// AdapterStatus provides status information for a running output adapter.
type AdapterStatus struct {
	State        AdapterState
	BytesWritten int64
	StartedAt    time.Time
	Error        string
}

// OutputAdapter is the interface that all output destinations implement.
// Adapters receive muxed MPEG-TS bytes from the OutputManager and write
// them to their destination (file, SRT stream, etc.).
type OutputAdapter interface {
	// ID returns a unique identifier for this adapter instance.
	ID() string
	// Start initializes the adapter and begins accepting writes.
	Start(ctx context.Context) error
	// Write sends muxed MPEG-TS data to the output destination.
	Write(tsData []byte) (int, error)
	// Close shuts down the adapter and releases resources.
	Close() error
	// Status returns the current status of the adapter.
	Status() AdapterStatus
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
	Active      bool   `json:"active"`
	Mode        string `json:"mode,omitempty"`
	Address     string `json:"address,omitempty"`
	Port        int    `json:"port,omitempty"`
	State       string `json:"state,omitempty"`
	Connections int    `json:"connections,omitempty"`
	BytesWritten int64 `json:"bytesWritten,omitempty"`
	Error       string `json:"error,omitempty"`
}

// SRTOutputConfig holds configuration for creating an SRT output adapter.
type SRTOutputConfig struct {
	Mode     string `json:"mode"`
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port"`
	Latency  int    `json:"latency,omitempty"`
	StreamID string `json:"streamID,omitempty"`
}
