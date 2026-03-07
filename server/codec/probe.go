//go:build cgo && !noffmpeg

package codec

import (
	"log/slog"
	"sync"
	"unsafe"
)

var (
	probeOnce       sync.Once
	selectedEncoder string         // e.g. "h264_videotoolbox", "libx264"
	selectedDecoder string         // e.g. "h264"
	hwDeviceCtxPtr  unsafe.Pointer // *C.AVBufferRef, nil for software
)

// encoderCandidate describes an encoder to try during probing.
type encoderCandidate struct {
	name   string
	hwType string // "cuda", "vaapi", "videotoolbox", or "" for software
}

// candidates lists encoder candidates in priority order (fastest first).
var candidates = []encoderCandidate{
	{name: "h264_nvenc", hwType: "cuda"},
	{name: "h264_vaapi", hwType: "vaapi"},
	{name: "h264_videotoolbox", hwType: "videotoolbox"},
	{name: "libx264", hwType: ""},
}

// ProbeEncoders tests available H.264 encoder backends and caches the result.
// It tries hardware-accelerated encoders first, falling back to libx264,
// then OpenH264 as a last resort.
//
// Returns (encoderName, decoderName). Both will be non-empty on any machine
// with at least FFmpeg or OpenH264 available.
//
// Safe to call from multiple goroutines; probing runs exactly once.
func ProbeEncoders() (string, string) {
	probeOnce.Do(func() {
		selectedEncoder = probeEncoder()
		// FFmpeg software decoder is universally available when FFmpeg is linked.
		selectedDecoder = "h264"

		slog.Info("codec: probe complete",
			"encoder", selectedEncoder,
			"decoder", selectedDecoder,
			"hw_accel", hwDeviceCtxPtr != nil,
		)

		if selectedEncoder == "libx264" {
			slog.Warn("software-only encoder detected — transitions above 720p may drop frames; hardware encoder recommended")
		}
	})
	return selectedEncoder, selectedDecoder
}

// probeEncoder tries each candidate encoder in priority order.
// Returns the name of the first encoder that successfully opens and encodes a frame.
func probeEncoder() string {
	for _, c := range candidates {
		if tryEncoder(c.name) {
			slog.Debug("codec: probe candidate succeeded", "encoder", c.name)
			return c.name
		}
		slog.Debug("codec: probe candidate failed", "encoder", c.name)
	}

	// All FFmpeg encoders failed. Try OpenH264 as the ultimate fallback.
	if tryOpenH264Encoder() {
		return "openh264"
	}

	// Nothing works. This should be extremely rare -- it means FFmpeg is linked
	// but has no H.264 encoder AND OpenH264 is also unavailable.
	slog.Warn("codec: no H.264 encoder found during probe")
	return "none"
}

// tryEncoder attempts to create a small FFmpeg encoder, encode a few frames,
// and close it. Returns true if the codec is functional.
// Some hardware encoders (e.g. VideoToolbox) return EAGAIN on the first
// frame(s) while warming up, so we send up to 4 frames.
func tryEncoder(codecName string) bool {
	enc, err := NewFFmpegEncoder(codecName, 64, 64, 100000, 30.0, 2, nil)
	if err != nil {
		return false
	}
	defer enc.Close()

	yuvSize := 64 * 64 * 3 / 2
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	for i := range 8 {
		data, _, err := enc.Encode(yuv, i == 0)
		if err != nil {
			continue // EAGAIN expected during warmup
		}
		if len(data) > 0 {
			return true
		}
	}
	return false
}

// tryOpenH264Encoder attempts to create a small OpenH264 encoder and encode one frame.
// Returns true if the codec is functional.
func tryOpenH264Encoder() bool {
	enc, err := NewOpenH264Encoder(64, 64, 100000, 30.0)
	if err != nil {
		return false
	}
	defer enc.Close()

	yuvSize := 64 * 64 * 3 / 2
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	data, _, err := enc.Encode(yuv, true)
	if err != nil {
		return false
	}
	return len(data) > 0
}

// HWDeviceCtx returns the cached hardware device context pointer.
// Returns nil for software codecs (libx264, openh264).
// The returned pointer is an *AVBufferRef suitable for passing to
// FFmpeg encoder/decoder creation functions.
//
// ProbeEncoders() must be called before this function returns a meaningful value.
func HWDeviceCtx() unsafe.Pointer {
	return hwDeviceCtxPtr
}
