// server/clip/types.go
package clip

import (
	"errors"
	"time"
)

// ClipSource indicates how the clip was ingested.
type ClipSource string

const (
	SourceUpload    ClipSource = "upload"
	SourceReplay    ClipSource = "replay"
	SourceRecording ClipSource = "recording"
)

// PlayerState is the state of a clip player slot.
type PlayerState string

const (
	StateEmpty   PlayerState = "empty"
	StateLoaded  PlayerState = "loaded"
	StatePlaying PlayerState = "playing"
	StatePaused  PlayerState = "paused"
	StateHolding PlayerState = "holding"
)

// Clip is the metadata for a stored media clip.
type Clip struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Filename   string     `json:"filename"`
	Source     ClipSource `json:"source"`
	Codec      string     `json:"codec"`
	AudioCodec string     `json:"audioCodec,omitempty"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	FPSNum     int        `json:"fpsNum"`
	FPSDen     int        `json:"fpsDen"`
	DurationMs int64      `json:"durationMs"`
	SampleRate int        `json:"sampleRate,omitempty"`
	Channels   int        `json:"channels,omitempty"`
	ByteSize   int64      `json:"byteSize"`
	Loop       bool       `json:"loop"`
	CreatedAt  time.Time  `json:"createdAt"`
	Ephemeral  bool       `json:"ephemeral"`
}

// ClipPlayerState is broadcast in ControlRoomState.
type ClipPlayerState struct {
	ID       int         `json:"id"`
	ClipID   string      `json:"clipId,omitempty"`
	ClipName string      `json:"clipName,omitempty"`
	State    PlayerState `json:"state"`
	Speed    float64     `json:"speed,omitempty"`
	Position float64     `json:"position,omitempty"`
	Loop     bool        `json:"loop,omitempty"`
}

// Sentinel errors.
var (
	ErrNotFound        = errors.New("clip: not found")
	ErrInvalidName     = errors.New("clip: invalid name")
	ErrStorageFull     = errors.New("clip: storage limit exceeded")
	ErrInvalidFormat   = errors.New("clip: unsupported format")
	ErrTranscodeFailed = errors.New("clip: transcode failed")
	ErrCorruptFile     = errors.New("clip: file is corrupt or cannot be decoded")
	ErrOddDimensions   = errors.New("clip: video dimensions must be even")
	ErrNoVideo         = errors.New("clip: no video stream found")
	ErrPlayerFull      = errors.New("clip: all 4 player slots are in use")
	ErrPlayerEmpty     = errors.New("clip: player slot is empty")
	ErrPlayerBusy      = errors.New("clip: player is currently playing")
	ErrInvalidPlayer   = errors.New("clip: player ID must be 1-4")
	ErrInvalidSpeed    = errors.New("clip: speed must be between 0.25 and 2.0")
	ErrInvalidSeek     = errors.New("clip: seek position must be between 0.0 and 1.0")
	ErrAlreadyExists   = errors.New("clip: already exists")
)

// MaxPlayers is the number of clip player slots.
const MaxPlayers = 4

// VideoDecoder decodes H.264 frames to raw YUV420 planar data.
// Matches transition.VideoDecoder via structural typing.
type VideoDecoder interface {
	Decode(h264 []byte) (yuv []byte, width, height int, err error)
	Close()
}

// DrainableDecoder extends VideoDecoder with EOS drain support.
// When the decoder uses frame-level buffering (B-frame reordering),
// SendEOS + ReceiveFrame drains remaining buffered frames.
type DrainableDecoder interface {
	VideoDecoder
	SendEOS() error
	ReceiveFrame() (yuv []byte, width, height int, err error)
}

// VideoEncoder encodes raw YUV420 frames to H.264.
// Used to re-encode decoded clip frames to browser-compatible 8-bit H.264.
// Matches transition.VideoEncoder via structural typing.
type VideoEncoder interface {
	Encode(yuv []byte, pts int64, forceIDR bool) (data []byte, isKeyframe bool, err error)
	Close()
}

// bufferedFrame mirrors replay's internal frame format for code reuse.
type bufferedFrame struct {
	wireData   []byte
	sps        []byte
	pps        []byte
	pts        int64
	isKeyframe bool
}

// bufferedAudioFrame mirrors replay's internal audio frame format.
type bufferedAudioFrame struct {
	data       []byte // raw AAC payload (no ADTS header)
	pts        int64
	sampleRate int
	channels   int
}

// ProbeResult holds metadata extracted during validation.
type ProbeResult struct {
	Codec      string
	AudioCodec string
	Width      int
	Height     int
	FPSNum     int
	FPSDen     int
	DurationMs int64
	SampleRate int
	Channels   int
	FrameCount int
	Warnings   []string
}
