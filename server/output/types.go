package output

import (
	"context"
	"errors"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// Sentinel errors for the output package.
var (
	ErrRecorderActive    = errors.New("output: recorder already active")
	ErrRecorderNotActive = errors.New("output: recorder not active")
	ErrSRTActive         = errors.New("output: SRT already active")
	ErrSRTNotActive      = errors.New("output: SRT not active")
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

// RecordingStatus is an alias for the canonical type in internal.
type RecordingStatus = internal.RecordingStatus

// SRTOutputStatus is an alias for the canonical type in internal.
type SRTOutputStatus = internal.SRTOutputStatus

// SRTOutputConfig holds configuration for creating an SRT output adapter.
type SRTOutputConfig struct {
	Mode     string `json:"mode"`
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port"`
	Latency  int    `json:"latency,omitempty"`
	StreamID string `json:"streamID,omitempty"`
}
