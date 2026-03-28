package output

import (
	"context"
	"errors"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
)

const (
	rtmpInitialBackoff = 1 * time.Second
	rtmpMaxBackoff     = 30 * time.Second
)

// Sentinel errors for the output package.
var (
	ErrRecorderActive    = errors.New("output: recorder already active")
	ErrRecorderNotActive = errors.New("output: recorder not active")
	ErrSRTActive         = errors.New("output: SRT already active")
	ErrSRTNotActive      = errors.New("output: SRT not active")
	ErrRTMPNotAvailable  = errors.New("output: RTMP not available (built without FFmpeg support)")
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

// Adapter is the interface that all output destinations implement.
// Adapters receive muxed MPEG-TS bytes from the Manager and write
// them to their destination (file, SRT stream, etc.).
type Adapter interface {
	// ID returns a unique identifier for this adapter instance.
	ID() string
	// Start initializes the adapter and begins accepting writes.
	Start(ctx context.Context) error
	// Write sends muxed MPEG-TS data to the output destination.
	// Unlike io.Writer, partial writes are not supported: callers must
	// treat any error as a complete failure. The returned byte count is
	// informational and may not reflect the exact number of bytes
	// delivered to the underlying transport (e.g., SRT, file).
	Write(tsData []byte) (int, error)
	// Close shuts down the adapter and releases resources.
	Close() error
	// Status returns the current status of the adapter.
	Status() AdapterStatus
}

// FrameAdapter receives raw codec frames instead of muxed MPEG-TS.
// Used by RTMP output which muxes directly to FLV, bypassing TS.
type FrameAdapter interface {
	ID() string
	WriteVideo(frame *media.VideoFrame) error
	WriteAudio(frame *media.AudioFrame) error
	Start(ctx context.Context) error
	Close() error
	Status() AdapterStatus
}

// RecordingStatus is an alias for the canonical type in internal.
type RecordingStatus = internal.RecordingStatus

// SRTStatus is an alias for the canonical type in internal.
type SRTStatus = internal.SRTOutputStatus

// SRTConfig holds configuration for creating an SRT output adapter.
type SRTConfig struct {
	Mode     string `json:"mode"`
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port"`
	Latency  int    `json:"latency,omitempty"`
	StreamID string `json:"streamID,omitempty"`
}

// RTMPCallerConfig holds configuration for RTMP caller (push mode).
type RTMPCallerConfig struct {
	URL        string // full RTMP URL, e.g. rtmp://a.rtmp.youtube.com/live2/stream-key
	SampleRate int    // audio sample rate (default 48000)
	Channels   int    // audio channels (default 2 = stereo)
}

// RTMPStatus holds runtime RTMP connection info for ControlRoomState.
type RTMPStatus struct {
	Active       bool   `json:"active"`
	URL          string `json:"url,omitempty"`
	State        string `json:"state,omitempty"`
	BytesWritten int64  `json:"bytesWritten,omitempty"`
	Error        string `json:"error,omitempty"`
}

// rtmpMaskURL masks the stream key portion of an RTMP URL for safe logging.
// "rtmp://host/app/secret-key" -> "rtmp://host/app/****"
func rtmpMaskURL(rawURL string) string {
	// Find the fourth slash (after scheme://host/app/).
	slashCount := 0
	lastSlash := -1
	for i, ch := range rawURL {
		if ch == '/' {
			slashCount++
			if slashCount >= 4 {
				lastSlash = i
				break
			}
		}
	}
	if lastSlash > 0 && lastSlash < len(rawURL)-1 {
		return rawURL[:lastSlash+1] + "****"
	}
	return rawURL
}
