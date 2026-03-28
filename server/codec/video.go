//go:build cgo && !noffmpeg

package codec

import (
	"errors"
	"fmt"

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
		// Pass nil hwDeviceCtx so the encoder creates its own CUDA context.
		// See NewPreviewEncoder comment for thread-safety rationale.
		return NewFFmpegEncoder(enc, width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
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
	gopSecs := transition.PreviewGOPSecs
	// Use hardware encoder for previews when available (NVENC/VA-API/VT).
	// GPU encode is near-zero CPU, freeing cores for decode and compositing.
	// Pass nil hwDeviceCtx so each encoder creates its own CUDA context.
	// Sharing a single CUDA context across concurrent encoder goroutines
	// causes frame corruption because cuCtxPushCurrent/cuCtxPopCurrent
	// (used by FFmpeg around nvEncEncodePicture) is not thread-safe when
	// multiple threads share the same context.
	switch enc {
	case "h264_nvenc", "h264_vaapi", "h264_videotoolbox":
		return NewFFmpegEncoder(enc, width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
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

// NewHEVCVideoDecoder creates an HEVC video decoder using the FFmpeg software decoder.
// Returns an error if FFmpeg HEVC decoding is not available.
func NewHEVCVideoDecoder() (transition.VideoDecoder, error) {
	return NewFFmpegHEVCDecoder(HWDeviceCtx())
}

// NewHEVCVideoDecoderSingleThread creates an HEVC video decoder with single-threaded decoding.
// This eliminates frame-level multithreading buffering delay, ensuring each Decode()
// call produces output immediately. Use for clip/replay decoders.
func NewHEVCVideoDecoderSingleThread() (transition.VideoDecoder, error) {
	return NewFFmpegHEVCDecoderWithThreads(HWDeviceCtx(), 1)
}

// NewVideoDecoderForCodec creates a video decoder for the specified codec.
// Accepts "h264" or "h265"/"hevc". Returns an error for unknown codecs.
func NewVideoDecoderForCodec(codec string) (transition.VideoDecoder, error) {
	switch codec {
	case "h264":
		return NewVideoDecoder()
	case "h265", "hevc":
		return NewHEVCVideoDecoder()
	default:
		return nil, fmt.Errorf("unsupported video codec: %q (supported: h264, h265)", codec)
	}
}

// NewVideoDecoderForCodecSingleThread creates a single-threaded video decoder for the
// specified codec. Accepts "h264" or "h265"/"hevc".
func NewVideoDecoderForCodecSingleThread(codec string) (transition.VideoDecoder, error) {
	switch codec {
	case "h264":
		return NewVideoDecoderSingleThread()
	case "h265", "hevc":
		return NewHEVCVideoDecoderSingleThread()
	default:
		return nil, fmt.Errorf("unsupported video codec: %q (supported: h264, h265)", codec)
	}
}

// NewVideoDecoderNative10bit creates a video decoder that preserves 10-bit 422
// output when the source provides 10-bit content. For 10-bit HEVC sources, the
// decoder outputs YUV422P10LE (w*h*4 bytes) instead of downconverting to 8-bit
// 420, eliminating the lossy round-trip when the pipeline is in professional mode.
// For 8-bit sources, falls back to standard YUV420P 8-bit output.
func NewVideoDecoderNative10bit() (transition.VideoDecoder, error) {
	return NewFFmpegHEVCDecoderNative10bit(HWDeviceCtx())
}

// NewVideoDecoderNative10bitSingleThread creates a native 10-bit decoder with
// single-threaded decoding. Eliminates frame-level multithreading buffering delay.
// Use for clip/replay decoders where immediate per-frame output is needed.
func NewVideoDecoderNative10bitSingleThread() (transition.VideoDecoder, error) {
	return NewFFmpegHEVCDecoderNative10bitWithThreads(HWDeviceCtx(), 1)
}

// NewVideoDecoderForCodecNative10bit creates a native 10-bit decoder for the
// specified codec. For HEVC sources, preserves 10-bit 422 output.
// For H.264 sources (which don't support 10-bit 422), falls back to standard decoding.
func NewVideoDecoderForCodecNative10bit(codec string) (transition.VideoDecoder, error) {
	switch codec {
	case "h265", "hevc":
		return NewVideoDecoderNative10bit()
	case "h264":
		// H.264 doesn't support 10-bit 422, use standard decoder.
		return NewVideoDecoder()
	default:
		return nil, fmt.Errorf("unsupported video codec: %q (supported: h264, h265)", codec)
	}
}

// NewVideoDecoderForCodecNative10bitSingleThread creates a single-threaded
// native 10-bit decoder for the specified codec.
func NewVideoDecoderForCodecNative10bitSingleThread(codec string) (transition.VideoDecoder, error) {
	switch codec {
	case "h265", "hevc":
		return NewVideoDecoderNative10bitSingleThread()
	case "h264":
		return NewVideoDecoderSingleThread()
	default:
		return nil, fmt.Errorf("unsupported video codec: %q (supported: h264, h265)", codec)
	}
}

// NewHEVCVideoEncoder creates an HEVC video encoder using the best available backend.
// The first call triggers HEVC codec probing (via ProbeHEVCEncoders) which tests
// available hardware and software encoders in priority order.
func NewHEVCVideoEncoder(width, height, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
	enc, _ := ProbeHEVCEncoders()
	if enc == "none" {
		return nil, errors.New("no HEVC encoder available")
	}
	gopSecs := transition.DefaultGOPSecs
	return NewFFmpegEncoder(enc, width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
}

// NewHEVCVideoEncoder10bit creates an HEVC encoder configured for 10-bit YUV422P10LE
// input. Probes for available HEVC encoders and selects the best one.
// For libx265: uses main422-10 profile. For NVENC: uses main10 profile.
func NewHEVCVideoEncoder10bit(width, height, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
	enc, _ := ProbeHEVCEncoders()
	if enc == "none" {
		return nil, errors.New("no HEVC encoder available for 10-bit")
	}
	gopSecs := transition.DefaultGOPSecs
	return NewFFmpegEncoder10bit(enc, width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
}
