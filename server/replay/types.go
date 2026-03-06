// Package replay provides an instant replay system with variable-speed
// playback for live video switching. It maintains per-source circular
// buffers of encoded H.264 frames and can play back marked clips at
// configurable speeds (0.25x–1x) with frame duplication for slow motion.
package replay

import (
	"errors"
	"time"
)

// Sentinel errors for the replay subsystem.
var (
	ErrNoSource       = errors.New("replay: source not found")
	ErrNoMarkIn       = errors.New("replay: mark-in not set")
	ErrNoMarkOut      = errors.New("replay: mark-out not set")
	ErrInvalidMarks   = errors.New("replay: mark-out must be after mark-in")
	ErrPlayerActive   = errors.New("replay: player already active")
	ErrNoPlayer       = errors.New("replay: no active player")
	ErrEmptyClip      = errors.New("replay: clip contains no frames")
	ErrInvalidSpeed   = errors.New("replay: speed must be between 0.25 and 1.0")
	ErrBufferDisabled = errors.New("replay: buffer is disabled (0 duration)")
	ErrSourceMismatch = errors.New("replay: mark-out source must match mark-in source")
	ErrMaxSources     = errors.New("replay: maximum sources reached")
)

// PlayerState represents the current state of the replay player.
type PlayerState string

const (
	PlayerIdle    PlayerState = "idle"
	PlayerLoading PlayerState = "loading"
	PlayerPlaying PlayerState = "playing"
)

// Config holds configuration for the replay manager.
type Config struct {
	// BufferDurationSecs is the per-source buffer duration in seconds.
	// Default 60, max 300.
	BufferDurationSecs int

	// MaxSources is the maximum number of sources to buffer simultaneously.
	// Default 8.
	MaxSources int
}

// DefaultConfig returns the default replay configuration.
func DefaultConfig() Config {
	return Config{
		BufferDurationSecs: 60,
		MaxSources:         8,
	}
}

// bufferedFrame stores a single encoded video frame with wall-clock timestamp.
type bufferedFrame struct {
	wireData   []byte // AVC1 format (original from relay)
	sps        []byte // SPS NAL unit (keyframes only)
	pps        []byte // PPS NAL unit (keyframes only)
	pts        int64  // Original 90kHz PTS
	isKeyframe bool
	wallTime   time.Time // Wall-clock time for IN/OUT matching
}

// gopDescriptor tracks a group of pictures within the buffer.
type gopDescriptor struct {
	startIdx int       // Index of keyframe in frames slice
	endIdx   int       // Index of last frame in this GOP (inclusive)
	wallTime time.Time // Wall time of the keyframe
}

// clipFrame is a decoded frame ready for playback.
type clipFrame struct {
	yuv    []byte // YUV420 decoded pixel data
	width  int
	height int
	pts    int64 // Original PTS for ordering
}

// MarkPoints holds the in/out points for a replay clip.
type MarkPoints struct {
	Source  string    `json:"source"`
	InTime time.Time `json:"inTime"`
	OutTime time.Time `json:"outTime,omitempty"`
}

// ReplayStatus is the JSON-serializable status for the replay system,
// included in ControlRoomState for the browser.
type ReplayStatus struct {
	State       PlayerState        `json:"state"`
	Source      string             `json:"source,omitempty"`
	Speed       float64            `json:"speed,omitempty"`
	Loop        bool               `json:"loop,omitempty"`
	Position    float64            `json:"position,omitempty"`    // 0.0–1.0 playback progress
	MarkIn      *time.Time         `json:"markIn,omitempty"`
	MarkOut     *time.Time         `json:"markOut,omitempty"`
	MarkSource  string             `json:"markSource,omitempty"`
	Buffers     []SourceBufferInfo `json:"buffers,omitempty"`
}

// SourceBufferInfo describes the buffer state for a single source.
type SourceBufferInfo struct {
	Source       string  `json:"source"`
	FrameCount   int     `json:"frameCount"`
	GOPCount     int     `json:"gopCount"`
	DurationSecs float64 `json:"durationSecs"`
	BytesUsed    int64   `json:"bytesUsed"`
}

// MarkInRequest is the JSON body for the mark-in endpoint.
type MarkInRequest struct {
	Source string `json:"source"`
}

// MarkOutRequest is the JSON body for the mark-out endpoint.
type MarkOutRequest struct {
	Source string `json:"source"`
}

// PlayRequest is the JSON body for the play endpoint.
type PlayRequest struct {
	Source string  `json:"source"`
	Speed  float64 `json:"speed"`
	Loop   bool    `json:"loop"`
}
