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

	// MaxBufferBytes is the per-source byte limit for the replay buffer.
	// When exceeded, oldest GOPs are trimmed. Default 200MB. 0 disables.
	MaxBufferBytes int64
}

// DefaultConfig returns the default replay configuration.
func DefaultConfig() Config {
	return Config{
		BufferDurationSecs: 60,
		MaxSources:         8,
		MaxBufferBytes:     200 * 1024 * 1024, // 200MB
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

// bufferedAudioFrame stores a single encoded audio frame with wall-clock timestamp.
type bufferedAudioFrame struct {
	data       []byte // AAC frame data (deep-copied)
	pts        int64  // 90kHz PTS
	sampleRate int
	channels   int
	wallTime   time.Time
}

// gopDescriptor tracks a group of pictures within the buffer.
type gopDescriptor struct {
	startIdx int       // Index of keyframe in frames slice
	endIdx   int       // Index of last frame in this GOP (inclusive)
	wallTime time.Time // Wall time of the keyframe
}

// Status is the JSON-serializable status for the replay system,
// included in ControlRoomState for the browser.
type Status struct {
	State      PlayerState        `json:"state"`
	Source     string             `json:"source,omitempty"`
	Speed      float64            `json:"speed,omitempty"`
	Loop       bool               `json:"loop,omitempty"`
	Position   float64            `json:"position,omitempty"` // 0.0–1.0 playback progress
	MarkIn     *time.Time         `json:"markIn,omitempty"`
	MarkOut    *time.Time         `json:"markOut,omitempty"`
	MarkSource string             `json:"markSource,omitempty"`
	Buffers    []SourceBufferInfo `json:"buffers,omitempty"`
}

// MarkInUnixMs returns the mark-in time as Unix milliseconds, or nil if not set.
func (rs Status) MarkInUnixMs() *int64 {
	if rs.MarkIn == nil {
		return nil
	}
	ms := rs.MarkIn.UnixMilli()
	return &ms
}

// MarkOutUnixMs returns the mark-out time as Unix milliseconds, or nil if not set.
func (rs Status) MarkOutUnixMs() *int64 {
	if rs.MarkOut == nil {
		return nil
	}
	ms := rs.MarkOut.UnixMilli()
	return &ms
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
