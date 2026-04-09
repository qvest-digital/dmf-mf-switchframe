package codec

import "fmt"

// OutputPreset controls the encoder speed/quality tradeoff.
type OutputPreset string

const (
	OutputPresetLowLatency OutputPreset = "low-latency"
	OutputPresetBalanced   OutputPreset = "balanced"
	OutputPresetQuality    OutputPreset = "quality"
)

// Valid returns true for the three named presets or the empty string (meaning "use default").
func (p OutputPreset) Valid() bool {
	switch p {
	case OutputPresetLowLatency, OutputPresetBalanced, OutputPresetQuality, "":
		return true
	}
	return false
}

// NVENCPreset returns the NVENC preset string for this output preset.
func (p OutputPreset) NVENCPreset() string {
	switch p {
	case OutputPresetLowLatency:
		return "p3"
	case OutputPresetQuality:
		return "p5"
	default:
		return "p4"
	}
}

// X264Preset returns the x264 preset string for this output preset.
func (p OutputPreset) X264Preset() string {
	switch p {
	case OutputPresetQuality:
		return "faster"
	default:
		return "superfast"
	}
}

// X265Preset returns the x265 preset string for this output preset.
func (p OutputPreset) X265Preset() string {
	return p.X264Preset()
}

// Bitrate limits for encoder configuration.
const (
	MinBitrate = 1_000_000
	MaxBitrate = 50_000_000
)

// EncoderOptions holds user-configurable encoder parameters.
type EncoderOptions struct {
	Bitrate  int          // 0 means auto
	GOPSecs  int          // 0 means use default
	Preset   OutputPreset // "" means use default
	Timecode bool
}

// Validate checks that all fields are within acceptable ranges.
// Zero values are valid and mean "use default".
func (o EncoderOptions) Validate() error {
	if o.Bitrate != 0 && (o.Bitrate < MinBitrate || o.Bitrate > MaxBitrate) {
		return fmt.Errorf("bitrate %d out of range [%d, %d]", o.Bitrate, MinBitrate, MaxBitrate)
	}
	if o.GOPSecs != 0 {
		switch o.GOPSecs {
		case 1, 2, 4:
			// valid
		default:
			return fmt.Errorf("gop_secs must be 1, 2, or 4; got %d", o.GOPSecs)
		}
	}
	if !o.Preset.Valid() {
		return fmt.Errorf("invalid preset %q", o.Preset)
	}
	return nil
}

// WithDefaults returns a copy with zero-value fields replaced by sensible defaults.
// Bitrate 0 is left as-is (meaning auto).
func (o EncoderOptions) WithDefaults() EncoderOptions {
	out := o
	if out.GOPSecs == 0 {
		out.GOPSecs = 2
	}
	if out.Preset == "" {
		out.Preset = OutputPresetBalanced
	}
	return out
}

// BitrateWarning returns a non-empty warning string if the configured bitrate
// is below a format-appropriate floor. Bitrate 0 (auto) never warns.
func (o EncoderOptions) BitrateWarning(width, height, fpsNum, fpsDen int) string {
	if o.Bitrate == 0 {
		return ""
	}

	fps := float64(fpsNum) / float64(fpsDen)
	highFPS := fps > 45

	is1080 := height >= 1080
	is720 := height >= 720

	var floor int
	switch {
	case is1080 && highFPS:
		floor = 6_000_000
	case is1080:
		floor = 4_000_000
	case is720 && highFPS:
		floor = 3_000_000
	default:
		floor = 2_000_000
	}

	if o.Bitrate < floor {
		return fmt.Sprintf("bitrate %d is below recommended %d for %dx%d@%.2ffps",
			o.Bitrate, floor, width, height, fps)
	}
	return ""
}
