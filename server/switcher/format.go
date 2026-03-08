package switcher

import (
	"fmt"
	"time"
)

// PipelineFormat defines the global video pipeline format.
// All frame timing, encoder parameters, and resolution scaling derive from this.
// Frame rate is expressed as a rational number (FPSNum/FPSDen) for broadcast
// correctness — e.g. 30000/1001 for 29.97fps NTSC.
type PipelineFormat struct {
	Width  int    `json:"width"`  // Horizontal resolution (e.g. 1920)
	Height int    `json:"height"` // Vertical resolution (e.g. 1080)
	FPSNum int    `json:"fpsNum"` // Frame rate numerator (e.g. 30000)
	FPSDen int    `json:"fpsDen"` // Frame rate denominator (e.g. 1001)
	Name   string `json:"name"`   // Human-readable name (e.g. "1080p29.97")
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
	"1080p60":     {1920, 1080, 60, 1, "1080p60"},
	"1080p59.94":  {1920, 1080, 60000, 1001, "1080p59.94"},
	"1080p50":     {1920, 1080, 50, 1, "1080p50"},
	"1080p30":     {1920, 1080, 30, 1, "1080p30"},
	"1080p29.97":  {1920, 1080, 30000, 1001, "1080p29.97"},
	"1080p25":     {1920, 1080, 25, 1, "1080p25"},
	"1080p24":     {1920, 1080, 24, 1, "1080p24"},
	"1080p23.976": {1920, 1080, 24000, 1001, "1080p23.976"},
	// 720p
	"720p60":    {1280, 720, 60, 1, "720p60"},
	"720p59.94": {1280, 720, 60000, 1001, "720p59.94"},
	"720p50":    {1280, 720, 50, 1, "720p50"},
	"720p30":    {1280, 720, 30, 1, "720p30"},
	"720p29.97": {1280, 720, 30000, 1001, "720p29.97"},
	"720p25":    {1280, 720, 25, 1, "720p25"},
	// 4K UHD
	"2160p60":    {3840, 2160, 60, 1, "2160p60"},
	"2160p59.94": {3840, 2160, 60000, 1001, "2160p59.94"},
	"2160p50":    {3840, 2160, 50, 1, "2160p50"},
	"2160p30":    {3840, 2160, 30, 1, "2160p30"},
	"2160p29.97": {3840, 2160, 30000, 1001, "2160p29.97"},
	"2160p25":    {3840, 2160, 25, 1, "2160p25"},
}

// DefaultFormat is the startup default when no --format flag is provided.
var DefaultFormat = FormatPresets["1080p29.97"]

// ValidFormatPreset returns true if the name is a recognized preset.
func ValidFormatPreset(name string) bool {
	_, ok := FormatPresets[name]
	return ok
}
