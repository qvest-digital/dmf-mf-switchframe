//go:build cgo && !noffmpeg

package codec

import (
	"log/slog"
	"runtime"
	"sync"
	"unsafe"
)

var (
	probeOnce       sync.Once
	selectedEncoder string         // e.g. "h264_videotoolbox", "libx264"
	selectedDecoder string         // e.g. "h264"
	hwDeviceCtxPtr  unsafe.Pointer // *C.AVBufferRef, nil for software

	// probeResults caches which candidates passed during ProbeEncoders().
	// Used by ListAvailableEncoders() to avoid re-probing.
	probeResults map[string]bool

	listOnce          sync.Once
	availableEncoders []EncoderInfo

	// HEVC probing state — separate from AVC to allow independent selection.
	hevcProbeOnce       sync.Once
	selectedHEVCEncoder string         // e.g. "hevc_videotoolbox", "libx265"
	selectedHEVCDecoder string         // e.g. "hevc"
	hevcProbeResults    map[string]bool

	hevcListOnce          sync.Once
	availableHEVCEncoders []EncoderInfo
)

var displayNames = map[string]string{
	"h264_nvenc":        "NVENC (CUDA)",
	"h264_vaapi":        "VA-API (Linux)",
	"h264_videotoolbox": "VideoToolbox (macOS)",
	"libx264":           "x264 (Software)",
	"openh264":          "OpenH264 (Software)",
}

var hevcDisplayNames = map[string]string{
	"hevc_nvenc":        "NVENC HEVC (CUDA)",
	"hevc_vaapi":        "VA-API HEVC (Linux)",
	"hevc_videotoolbox": "VideoToolbox HEVC (macOS)",
	"libx265":           "x265 (Software)",
}

// encoderCandidate describes an encoder to try during probing.
type encoderCandidate struct {
	name   string
	hwType string // "cuda", "vaapi", "videotoolbox", or "" for software
}

// candidates lists encoder candidates in priority order.
// On macOS, libx264 is preferred over VideoToolbox because libx264 is 1.8-2.2x
// faster on Apple Silicon with working rate control. VT has a ~20ms fixed floor
// from the hardware round-trip and its FFmpeg rate control is broken (outputs
// ~20 Mbps when targeting 10 Mbps).
var candidates = []encoderCandidate{
	{name: "h264_nvenc", hwType: "cuda"},
	{name: "h264_vaapi", hwType: "vaapi"},
	{name: "h264_videotoolbox", hwType: "videotoolbox"},
	{name: "libx264", hwType: ""},
}

// hevcCandidates lists HEVC encoder candidates in priority order.
// Unlike AVC, hevc_videotoolbox on Apple Silicon has good rate control
// and quality, so it is NOT demoted on macOS.
var hevcCandidates = []encoderCandidate{
	{name: "hevc_nvenc", hwType: "cuda"},
	{name: "hevc_vaapi", hwType: "vaapi"},
	{name: "hevc_videotoolbox", hwType: "videotoolbox"},
	{name: "libx265", hwType: ""},
}

func init() {
	if runtime.GOOS == "darwin" {
		candidates = []encoderCandidate{
			{name: "h264_nvenc", hwType: "cuda"},
			{name: "h264_vaapi", hwType: "vaapi"},
			{name: "libx264", hwType: ""},
			{name: "h264_videotoolbox", hwType: "videotoolbox"},
		}
		// HEVC: keep hevc_videotoolbox in default priority (not demoted).
	}
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

		// Try to create a hardware device context matching the selected encoder.
		// This enables hardware-accelerated decoding when available.
		initHWDeviceCtx()

		slog.Info("codec: probe complete",
			"encoder", selectedEncoder,
			"decoder", selectedDecoder,
			"hw_accel", hwDeviceCtxPtr != nil,
		)

		if selectedEncoder == "libx264" && runtime.GOOS != "darwin" {
			slog.Warn("software-only encoder detected — transitions above 720p may drop frames; hardware encoder recommended")
		}
	})
	return selectedEncoder, selectedDecoder
}

// probeEncoder tries each candidate encoder in priority order.
// Returns the name of the first encoder that successfully opens and encodes a frame.
// Also populates probeResults so ListAvailableEncoders can reuse the results.
func probeEncoder() string {
	probeResults = make(map[string]bool)
	var best string

	for _, c := range candidates {
		ok := tryEncoder(c.name)
		probeResults[c.name] = ok
		if ok {
			slog.Debug("codec: probe candidate succeeded", "encoder", c.name)
			if best == "" {
				best = c.name
			}
		} else {
			slog.Debug("codec: probe candidate failed", "encoder", c.name)
		}
	}

	// Also probe OpenH264.
	ok := tryOpenH264Encoder()
	probeResults["openh264"] = ok

	if best != "" {
		return best
	}
	if ok {
		return "openh264"
	}

	// Nothing works. This should be extremely rare -- it means FFmpeg is linked
	// but has no H.264 encoder AND OpenH264 is also unavailable.
	slog.Warn("codec: no H.264 encoder found during probe")
	return "none"
}

// tryEncoder attempts to create a small FFmpeg encoder, encode a few frames,
// and close it. Returns true if the codec is functional.
// With sliced threading (tune zerolatency), libx264 produces output on frame 1.
// 30 frames provides headroom for hardware encoders with warmup latency.
// Probe uses 256×256 because NVENC has a minimum resolution requirement
// and crashes (SIGSEGV) on FFmpeg 7.x with smaller dimensions like 64×64.
func tryEncoder(codecName string) bool {
	enc, err := NewFFmpegEncoder(codecName, 256, 256, 100000, 30, 1, 2, nil)
	if err != nil {
		return false
	}
	defer enc.Close()

	yuvSize := yuvFrameSize(256, 256)
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	for i := range 30 {
		data, _, err := enc.Encode(yuv, int64(i*3000), i == 0)
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
	enc, err := NewOpenH264Encoder(64, 64, 100000, 30, 1)
	if err != nil {
		return false
	}
	defer enc.Close()

	yuvSize := yuvFrameSize(64, 64)
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	data, _, err := enc.Encode(yuv, 0, true)
	if err != nil {
		return false
	}
	return len(data) > 0
}

// initHWDeviceCtx attempts to create a hardware device context based on
// the selected encoder's hardware type. Falls back silently to software
// decode if hardware is unavailable.
func initHWDeviceCtx() {
	var hwType string
	for _, c := range candidates {
		if c.name == selectedEncoder {
			hwType = c.hwType
			break
		}
	}
	if hwType == "" {
		return // software encoder, no matching hw device
	}

	FFmpegOpenMu.Lock()
	ctx := CreateHWDeviceCtx(hwType)
	FFmpegOpenMu.Unlock()
	if ctx != nil {
		hwDeviceCtxPtr = ctx
		slog.Info("codec: hardware decode enabled", "type", hwType)
	} else {
		slog.Debug("codec: hardware device context unavailable, using software decode", "type", hwType)
	}
}

// ListAvailableEncoders returns all functional H.264 encoder backends on this
// system. Each entry includes the encoder name, a human-readable display name,
// and whether it is the current default (as selected by ProbeEncoders).
//
// The list is computed once and cached; safe to call from multiple goroutines.
// Reuses probe results from ProbeEncoders() to avoid re-creating encoder
// instances a second time.
func ListAvailableEncoders() []EncoderInfo {
	listOnce.Do(func() {
		defaultEnc, _ := ProbeEncoders()
		for _, c := range candidates {
			if probeResults[c.name] {
				display := displayNames[c.name]
				if display == "" {
					display = c.name
				}
				availableEncoders = append(availableEncoders, EncoderInfo{
					Name:        c.name,
					DisplayName: display,
					IsDefault:   c.name == defaultEnc,
				})
			}
		}
		if probeResults["openh264"] {
			display := displayNames["openh264"]
			if display == "" {
				display = "openh264"
			}
			availableEncoders = append(availableEncoders, EncoderInfo{
				Name:        "openh264",
				DisplayName: display,
				IsDefault:   defaultEnc == "openh264",
			})
		}
	})
	return availableEncoders
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

// ProbeHEVCEncoders tests available HEVC encoder backends and caches the result.
// It tries hardware-accelerated encoders first, falling back to libx265.
//
// Returns (encoderName, decoderName). decoderName is "hevc" when FFmpeg's HEVC
// decoder is available. encoderName may be "none" if no HEVC encoder works.
//
// Safe to call from multiple goroutines; probing runs exactly once.
func ProbeHEVCEncoders() (string, string) {
	hevcProbeOnce.Do(func() {
		selectedHEVCEncoder = probeHEVCEncoder()
		// FFmpeg software HEVC decoder is universally available when FFmpeg is linked
		// with libde265 or the built-in HEVC decoder.
		selectedHEVCDecoder = "hevc"

		slog.Info("codec: HEVC probe complete",
			"encoder", selectedHEVCEncoder,
			"decoder", selectedHEVCDecoder,
		)

		if selectedHEVCEncoder == "libx265" {
			slog.Warn("software-only HEVC encoder detected — may not sustain real-time at high resolutions; hardware encoder recommended")
		}
	})
	return selectedHEVCEncoder, selectedHEVCDecoder
}

// probeHEVCEncoder tries each HEVC candidate encoder in priority order.
// Returns the name of the first encoder that successfully opens and encodes a frame.
func probeHEVCEncoder() string {
	hevcProbeResults = make(map[string]bool)
	var best string

	for _, c := range hevcCandidates {
		ok := tryEncoder(c.name)
		hevcProbeResults[c.name] = ok
		if ok {
			slog.Debug("codec: HEVC probe candidate succeeded", "encoder", c.name)
			if best == "" {
				best = c.name
			}
		} else {
			slog.Debug("codec: HEVC probe candidate failed", "encoder", c.name)
		}
	}

	if best != "" {
		return best
	}

	slog.Info("codec: no HEVC encoder found during probe")
	return "none"
}

// ListAvailableHEVCEncoders returns all functional HEVC encoder backends on this
// system. Each entry includes the encoder name, a human-readable display name,
// and whether it is the current default (as selected by ProbeHEVCEncoders).
//
// The list is computed once and cached; safe to call from multiple goroutines.
func ListAvailableHEVCEncoders() []EncoderInfo {
	hevcListOnce.Do(func() {
		defaultEnc, _ := ProbeHEVCEncoders()
		for _, c := range hevcCandidates {
			if hevcProbeResults[c.name] {
				display := hevcDisplayNames[c.name]
				if display == "" {
					display = c.name
				}
				availableHEVCEncoders = append(availableHEVCEncoders, EncoderInfo{
					Name:        c.name,
					DisplayName: display,
					IsDefault:   c.name == defaultEnc,
				})
			}
		}
	})
	return availableHEVCEncoders
}
