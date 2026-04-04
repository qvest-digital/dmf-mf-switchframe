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
	Multiply int `json:"multiply"`
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
