// server/clip/types.go
package clip

import (
	"errors"
	"time"
)

// yuvFrameSize returns the byte size of a YUV420 8-bit frame (w*h*3/2).
// Local helper to avoid importing the switcher package.
func yuvFrameSize(w, h int) int {
	return w * h * 3 / 2
}

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

// FlushableDecoder extends VideoDecoder with codec flush support.
// Flush resets the decoder state so a new stream (e.g., loop restart
// with different SPS/PPS) can be decoded without errors.
type FlushableDecoder interface {
	VideoDecoder
	Flush()
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

// BufferedFrame mirrors replay's internal frame format for code reuse.
// Fields are unexported for encapsulation; use accessor methods.
type BufferedFrame struct {
	wireData   []byte
	vps        []byte // VPS NAL unit (HEVC keyframes only)
	sps        []byte
	pps        []byte
	pts        int64
	isKeyframe bool
	codec      string // "h264" or "h265"; empty defaults to "h264"
}

// WireData returns the wire-format frame data (excluding parameter sets).
func (f BufferedFrame) WireData() []byte { return f.wireData }

// VPS returns the VPS NALU bytes (set on HEVC keyframes, nil for H.264).
func (f BufferedFrame) VPS() []byte { return f.vps }

// SPS returns the SPS NALU bytes (set on keyframes, nil otherwise).
func (f BufferedFrame) SPS() []byte { return f.sps }

// PPS returns the PPS NALU bytes (set on keyframes, nil otherwise).
func (f BufferedFrame) PPS() []byte { return f.pps }

// PTS returns the presentation timestamp in 90kHz clock units.
func (f BufferedFrame) PTS() int64 { return f.pts }

// IsKeyframe returns true if this frame is an IDR/keyframe.
func (f BufferedFrame) IsKeyframe() bool { return f.isKeyframe }

// Codec returns the video codec ("h264" or "h265"). Empty string defaults to "h264".
func (f BufferedFrame) Codec() string {
	if f.codec == "" {
		return "h264"
	}
	return f.codec
}

// IsHEVC returns true if this frame uses the HEVC codec.
func (f BufferedFrame) IsHEVC() bool { return f.codec == "h265" }

// BufferedAudioFrame mirrors replay's internal audio frame format.
// Fields are unexported for encapsulation; use accessor methods.
type BufferedAudioFrame struct {
	data       []byte // raw AAC payload (no ADTS header)
	pts        int64
	sampleRate int
	channels   int
}

// Data returns the raw AAC payload (no ADTS header).
func (f BufferedAudioFrame) Data() []byte { return f.data }

// PTS returns the presentation timestamp in 90kHz clock units.
func (f BufferedAudioFrame) PTS() int64 { return f.pts }

// SampleRate returns the audio sample rate in Hz.
func (f BufferedAudioFrame) SampleRate() int { return f.sampleRate }

// Channels returns the number of audio channels.
func (f BufferedAudioFrame) Channels() int { return f.channels }

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
