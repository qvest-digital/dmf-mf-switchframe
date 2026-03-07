//go:build cgo && !noffmpeg

package codec

import (
	"fmt"

	"github.com/zsiec/switchframe/server/transition"
)

// NewVideoEncoder creates a video encoder using the best available backend.
// The first call triggers codec probing (via ProbeEncoders) which tests
// available hardware and software encoders in priority order.
//
// Parameters match transition.EncoderFactory signature.
func NewVideoEncoder(width, height, bitrate int, fps float32) (transition.VideoEncoder, error) {
	enc, _ := ProbeEncoders()
	gopSecs := transition.DefaultGOPSecs
	switch enc {
	case "openh264":
		return NewOpenH264Encoder(width, height, bitrate, fps)
	case "none":
		return nil, fmt.Errorf("no H.264 encoder available")
	default:
		return NewFFmpegEncoder(enc, width, height, bitrate, fps, gopSecs, HWDeviceCtx())
	}
}

// NewVideoDecoder creates a video decoder using the best available backend.
// The first call triggers codec probing (via ProbeEncoders) which tests
// available encoders and selects a decoder strategy.
//
// When FFmpeg is available, the FFmpeg software H.264 decoder is used
// (universally available, supports all profiles). Falls back to OpenH264
// if FFmpeg probing indicates it's unavailable.
func NewVideoDecoder() (transition.VideoDecoder, error) {
	_, dec := ProbeEncoders()
	switch dec {
	case "openh264":
		return NewOpenH264Decoder()
	case "none":
		return nil, fmt.Errorf("no H.264 decoder available")
	default:
		// "h264" -> FFmpeg software decoder
		return NewFFmpegDecoder(HWDeviceCtx())
	}
}
