package switcher

import (
	"fmt"
	"time"
)

// PipelineColorFormat defines the pixel format and bit depth for the pipeline.
type PipelineColorFormat int

const (
	// ColorYUV420_8bit is planar YUV 4:2:0 at 8 bits per sample (standard broadcast).
	ColorYUV420_8bit PipelineColorFormat = iota
	// ColorYUV422_10bit is planar YUV 4:2:2 at 10 bits per sample (professional broadcast).
	ColorYUV422_10bit
)

// String returns the FFmpeg-style pixel format name.
func (f PipelineColorFormat) String() string {
	switch f {
	case ColorYUV420_8bit:
		return "yuv420p"
	case ColorYUV422_10bit:
		return "yuv422p10le"
	default:
		return fmt.Sprintf("unknown(%d)", int(f))
	}
}

// BytesPerSample returns the number of bytes per luma/chroma sample.
func (f PipelineColorFormat) BytesPerSample() int {
	switch f {
	case ColorYUV422_10bit:
		return 2
	default:
		return 1
	}
}

// ChromaWidth returns the chroma plane width (half luma width for both 420 and 422).
func (f PipelineColorFormat) ChromaWidth(w int) int {
	return w / 2
}

// ChromaHeight returns the chroma plane height.
// For 4:2:0 this is half the luma height; for 4:2:2 it equals luma height.
func (f PipelineColorFormat) ChromaHeight(h int) int {
	switch f {
	case ColorYUV422_10bit:
		return h
	default:
		return h / 2
	}
}

// YPlaneSize returns the Y plane size in bytes.
func (f PipelineColorFormat) YPlaneSize(w, h int) int {
	return w * h * f.BytesPerSample()
}

// ChromaPlaneSize returns the size of one chroma plane (Cb or Cr) in bytes.
func (f PipelineColorFormat) ChromaPlaneSize(w, h int) int {
	return f.ChromaWidth(w) * f.ChromaHeight(h) * f.BytesPerSample()
}

// FrameSize returns the total frame size in bytes (Y + Cb + Cr).
func (f PipelineColorFormat) FrameSize(w, h int) int {
	return f.YPlaneSize(w, h) + 2*f.ChromaPlaneSize(w, h)
}

// BlackY returns the black level for the Y (luma) channel.
// BT.709 limited range: 16 for 8-bit, 64 for 10-bit.
func (f PipelineColorFormat) BlackY() int {
	switch f {
	case ColorYUV422_10bit:
		return 64
	default:
		return 16
	}
}

// WhiteY returns the white level for the Y (luma) channel.
// BT.709 limited range: 235 for 8-bit, 940 for 10-bit.
func (f PipelineColorFormat) WhiteY() int {
	switch f {
	case ColorYUV422_10bit:
		return 940
	default:
		return 235
	}
}

// NeutralChroma returns the neutral (zero-color) value for Cb/Cr channels.
// 128 for 8-bit, 512 for 10-bit.
func (f PipelineColorFormat) NeutralChroma() int {
	switch f {
	case ColorYUV422_10bit:
		return 512
	default:
		return 128
	}
}

// MaxSampleValue returns the maximum representable sample value.
// 255 for 8-bit, 1023 for 10-bit.
func (f PipelineColorFormat) MaxSampleValue() int {
	switch f {
	case ColorYUV422_10bit:
		return 1023
	default:
		return 255
	}
}

// PipelineCodec identifies the video codec used for encoding/decoding.
type PipelineCodec int

const (
	// CodecAVC is H.264/AVC. Supports 8-bit 4:2:0 only.
	CodecAVC PipelineCodec = iota
	// CodecHEVC is H.265/HEVC. Supports 8-bit 4:2:0 and 10-bit 4:2:2.
	CodecHEVC
)

// String returns the codec name.
func (c PipelineCodec) String() string {
	switch c {
	case CodecAVC:
		return "h264"
	case CodecHEVC:
		return "h265"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// PipelineColorConfig combines color format and codec selection.
type PipelineColorConfig struct {
	ColorFormat PipelineColorFormat `json:"colorFormat"`
	Codec       PipelineCodec      `json:"codec"`
}

// Validate checks that the color format and codec are compatible.
func (c PipelineColorConfig) Validate() error {
	if c.ColorFormat == ColorYUV422_10bit && c.Codec == CodecAVC {
		return fmt.Errorf("AVC (H.264) does not support 10-bit 4:2:2; use HEVC")
	}
	return nil
}

// ColorProfileStandard returns the standard broadcast profile: YUV420 8-bit + AVC.
func ColorProfileStandard() PipelineColorConfig {
	return PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC}
}

// ColorProfileStandardHEVC returns YUV420 8-bit + HEVC (better compression, same quality).
func ColorProfileStandardHEVC() PipelineColorConfig {
	return PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecHEVC}
}

// ColorProfileProfessional returns the professional profile: YUV422 10-bit + HEVC.
func ColorProfileProfessional() PipelineColorConfig {
	return PipelineColorConfig{ColorFormat: ColorYUV422_10bit, Codec: CodecHEVC}
}

// ParseColorProfile returns a PipelineColorConfig for the given profile name.
// Valid names: "standard", "standard-hevc", "professional".
func ParseColorProfile(name string) (PipelineColorConfig, error) {
	switch name {
	case "standard":
		return ColorProfileStandard(), nil
	case "standard-hevc":
		return ColorProfileStandardHEVC(), nil
	case "professional":
		return ColorProfileProfessional(), nil
	default:
		return PipelineColorConfig{}, fmt.Errorf("unknown color profile %q; valid: standard, standard-hevc, professional", name)
	}
}

// PipelineFormat defines the global video pipeline format.
// All frame timing, encoder parameters, and resolution scaling derive from this.
// Frame rate is expressed as a rational number (FPSNum/FPSDen) for broadcast
// correctness — e.g. 30000/1001 for 29.97fps NTSC.
type PipelineFormat struct {
	Width  int                 `json:"width"`  // Horizontal resolution (e.g. 1920)
	Height int                 `json:"height"` // Vertical resolution (e.g. 1080)
	FPSNum int                 `json:"fpsNum"` // Frame rate numerator (e.g. 30000)
	FPSDen int                 `json:"fpsDen"` // Frame rate denominator (e.g. 1001)
	Name   string              `json:"name"`   // Human-readable name (e.g. "1080p29.97")
	Color  PipelineColorConfig `json:"color"`  // Color format and codec (default: YUV420 8-bit AVC)
}

// FPS returns the frame rate as a float64 (FPSNum / FPSDen).
// Returns 0 if FPSDen is zero.
func (f PipelineFormat) FPS() float64 {
	if f.FPSDen == 0 {
		return 0
	}
	return float64(f.FPSNum) / float64(f.FPSDen)
}

// FPSFloat32 returns the frame rate as a float32.
func (f PipelineFormat) FPSFloat32() float32 {
	return float32(f.FPS())
}

// FrameDuration returns the duration of one frame.
// Computed as FPSDen * time.Second / FPSNum.
// Returns 33333µs as a safety fallback if FPSNum is zero.
func (f PipelineFormat) FrameDuration() time.Duration {
	if f.FPSNum == 0 {
		return 33333 * time.Microsecond
	}
	return time.Duration(f.FPSDen) * time.Second / time.Duration(f.FPSNum)
}

// FrameBudgetNs returns the frame duration in nanoseconds.
func (f PipelineFormat) FrameBudgetNs() int64 {
	return f.FrameDuration().Nanoseconds()
}

// String returns the human-readable name if set, otherwise "WxH@Num/Den".
func (f PipelineFormat) String() string {
	if f.Name != "" {
		return f.Name
	}
	return fmt.Sprintf("%dx%d@%d/%d", f.Width, f.Height, f.FPSNum, f.FPSDen)
}

// FormatPresets contains standard broadcast format presets (ATSC + EBU).
var FormatPresets = map[string]PipelineFormat{
	// 1080p
	"1080p60":     {Width: 1920, Height: 1080, FPSNum: 60, FPSDen: 1, Name: "1080p60", Color: ColorProfileStandard()},
	"1080p59.94":  {Width: 1920, Height: 1080, FPSNum: 60000, FPSDen: 1001, Name: "1080p59.94", Color: ColorProfileStandard()},
	"1080p50":     {Width: 1920, Height: 1080, FPSNum: 50, FPSDen: 1, Name: "1080p50", Color: ColorProfileStandard()},
	"1080p30":     {Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, Name: "1080p30", Color: ColorProfileStandard()},
	"1080p29.97":  {Width: 1920, Height: 1080, FPSNum: 30000, FPSDen: 1001, Name: "1080p29.97", Color: ColorProfileStandard()},
	"1080p25":     {Width: 1920, Height: 1080, FPSNum: 25, FPSDen: 1, Name: "1080p25", Color: ColorProfileStandard()},
	"1080p24":     {Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Name: "1080p24", Color: ColorProfileStandard()},
	"1080p23.976": {Width: 1920, Height: 1080, FPSNum: 24000, FPSDen: 1001, Name: "1080p23.976", Color: ColorProfileStandard()},
	// 720p
	"720p60":    {Width: 1280, Height: 720, FPSNum: 60, FPSDen: 1, Name: "720p60", Color: ColorProfileStandard()},
	"720p59.94": {Width: 1280, Height: 720, FPSNum: 60000, FPSDen: 1001, Name: "720p59.94", Color: ColorProfileStandard()},
	"720p50":    {Width: 1280, Height: 720, FPSNum: 50, FPSDen: 1, Name: "720p50", Color: ColorProfileStandard()},
	"720p30":    {Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Name: "720p30", Color: ColorProfileStandard()},
	"720p29.97": {Width: 1280, Height: 720, FPSNum: 30000, FPSDen: 1001, Name: "720p29.97", Color: ColorProfileStandard()},
	"720p25":    {Width: 1280, Height: 720, FPSNum: 25, FPSDen: 1, Name: "720p25", Color: ColorProfileStandard()},
	// 4K UHD
	"2160p60":    {Width: 3840, Height: 2160, FPSNum: 60, FPSDen: 1, Name: "2160p60", Color: ColorProfileStandard()},
	"2160p59.94": {Width: 3840, Height: 2160, FPSNum: 60000, FPSDen: 1001, Name: "2160p59.94", Color: ColorProfileStandard()},
	"2160p50":    {Width: 3840, Height: 2160, FPSNum: 50, FPSDen: 1, Name: "2160p50", Color: ColorProfileStandard()},
	"2160p30":    {Width: 3840, Height: 2160, FPSNum: 30, FPSDen: 1, Name: "2160p30", Color: ColorProfileStandard()},
	"2160p29.97": {Width: 3840, Height: 2160, FPSNum: 30000, FPSDen: 1001, Name: "2160p29.97", Color: ColorProfileStandard()},
	"2160p25":    {Width: 3840, Height: 2160, FPSNum: 25, FPSDen: 1, Name: "2160p25", Color: ColorProfileStandard()},
}

// DefaultFormat is the startup default when no --format flag is provided.
var DefaultFormat = FormatPresets["1080p29.97"]

// ValidFormatPreset returns true if the name is a recognized preset.
func ValidFormatPreset(name string) bool {
	_, ok := FormatPresets[name]
	return ok
}
