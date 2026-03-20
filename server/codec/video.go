//go:build cgo && !noffmpeg

package codec

import (
	"errors"

	"github.com/zsiec/switchframe/server/transition"
)

// NewVideoEncoder creates a video encoder using the best available backend.
// The first call triggers codec probing (via ProbeEncoders) which tests
// available hardware and software encoders in priority order.
//
// fpsNum/fpsDen express the frame rate as a rational number (e.g. 30000/1001 for 29.97fps).
// The encoder always uses constrained VBR (cVBR): ABR with a tight 1.2x VBV
// ceiling for predictable SRT output. Transport-level CBR padding is handled
// by the output layer's CBR pacer.
func NewVideoEncoder(width, height, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
	enc, _ := ProbeEncoders()
	gopSecs := transition.DefaultGOPSecs
	switch enc {
	case "openh264":
		return NewOpenH264Encoder(width, height, bitrate, fpsNum, fpsDen)
	case "none":
		return nil, errors.New("no H.264 encoder available")
	default:
		return NewFFmpegEncoder(enc, width, height, bitrate, fpsNum, fpsDen, gopSecs, HWDeviceCtx())
	}
}

// NewPreviewEncoder creates a preview encoder for browser multiview.
// When a hardware encoder (NVENC) is available, it uses that — GPU encode
// is essentially free and avoids consuming CPU cores. Falls back to libx264
// with baseline profile when no hardware encoder is detected.
// Optional preset parameter (default "ultrafast") is only used for the
// software fallback path.
//
// fpsNum/fpsDen express the frame rate as a rational number (e.g. 30/1 for 30fps).
func NewPreviewEncoder(width, height, bitrate, fpsNum, fpsDen int, preset ...string) (transition.VideoEncoder, error) {
	enc, _ := ProbeEncoders()
	gopSecs := transition.DefaultGOPSecs
	// Use hardware encoder for previews when available (NVENC/VA-API/VT).
	// GPU encode is near-zero CPU, freeing cores for decode and compositing.
	switch enc {
	case "h264_nvenc", "h264_vaapi", "h264_videotoolbox":
		return NewFFmpegEncoder(enc, width, height, bitrate, fpsNum, fpsDen, gopSecs, HWDeviceCtx())
	default:
		return NewFFmpegPreviewEncoder(width, height, bitrate, fpsNum, fpsDen, gopSecs, preset...)
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
		return nil, errors.New("no H.264 decoder available")
	default:
		// "h264" -> FFmpeg software decoder
		return NewFFmpegDecoder(HWDeviceCtx())
	}
}

// NewVideoDecoderSingleThread creates a video decoder with single-threaded decoding.
// This eliminates frame-level multithreading buffering delay, ensuring each Decode()
// call produces output immediately (only B-frame reordering delay remains).
// Use for clip/replay decoders where immediate per-frame output is needed.
func NewVideoDecoderSingleThread() (transition.VideoDecoder, error) {
	_, dec := ProbeEncoders()
	switch dec {
	case "openh264":
		return NewOpenH264Decoder()
	case "none":
		return nil, errors.New("no H.264 decoder available")
	default:
		return NewFFmpegDecoderWithThreads(HWDeviceCtx(), 1)
	}
}
