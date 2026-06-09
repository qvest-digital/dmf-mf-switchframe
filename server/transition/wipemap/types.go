package wipemap

// WipePattern describes a registered wipe pattern.
type WipePattern struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	Builtin     bool   `json:"builtin,omitempty"`
}

// WipeConfig holds runtime modifier parameters applied during wipe blending.
type WipeConfig struct {
	PatternID     string  `json:"pattern"`
	SoftEdge      float64 `json:"soft_edge"`
	BorderWidth   float64 `json:"border_width"`
	BorderColorY  byte    `json:"border_color_y"`
	BorderColorCb byte    `json:"border_color_cb"`
	BorderColorCr byte    `json:"border_color_cr"`
	Reverse       bool    `json:"reverse"`
	OffsetX       float64 `json:"offset_x"`
	OffsetY       float64 `json:"offset_y"`
	// Multiply repeats the gradient pattern N times, creating venetian-blind style effects.
	// Values > 1 create hard discontinuities at wrap points (intentional for periodic patterns).
	// Use with venetian blind, checkerboard, or zigzag patterns.
	// Always read this through Multiplier() in the blend path: it is decoded
	// straight from API/CEF JSON, and an unclamped value (e.g. a multiple of 256,
	// or one large enough to overflow int(g)*Multiply) corrupts the gradient.
	Multiply int `json:"multiply"`
}

// maxMultiply bounds the gradient repeat count. The largest gradient byte (255)
// times maxMultiply (64) is 16320, which fits comfortably in an int with no
// risk of the int(g)*Multiply product overflowing before the %256 wrap, and
// the cap keeps the value away from multiples of 256 that would collapse every
// gradient byte to 0 (a degenerate hard cut).
const maxMultiply = 64

// Multiplier returns the effective, sanitized gradient repeat count in [1, 64].
// A value <= 1 means "no repeat" (returns 1); values above maxMultiply are
// clamped. Blend code must use this instead of the raw Multiply field so that
// unvalidated JSON input cannot produce a corrupted or instant transition.
func (c WipeConfig) Multiplier() int {
	if c.Multiply <= 1 {
		return 1
	}
	if c.Multiply > maxMultiply {
		return maxMultiply
	}
	return c.Multiply
}

// DefaultWipeConfig returns a config with sensible defaults (4px soft edge at 1080p).
func DefaultWipeConfig(patternID string) WipeConfig {
	return WipeConfig{
		PatternID: patternID,
		SoftEdge:  0.005,
	}
}

// GeneratorFunc produces a gradient map (single-channel 0-255) at the given resolution.
type GeneratorFunc func(width, height int) []byte

const (
	CatBasic     = "basic"
	CatIris      = "iris"
	CatClock     = "clock"
	CatMatrix    = "matrix"
	CatShape     = "shape"
	CatSpecialty = "specialty"
	CatMask      = "mask"
	CatDissolve  = "dissolve"
	CatCustom    = "custom"
)
