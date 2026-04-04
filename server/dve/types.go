// Package dve defines types and validation for Digital Video Effects (DVE)
// transforms applied to video source slots in the compositor pipeline.
package dve

import (
	"fmt"
	"image"
)

// MaxSlots is the maximum number of DVE slots.
const MaxSlots = 8

// Scale mode constants.
const (
	ScaleModeStretch = "stretch"
	ScaleModeFill    = "fill"
)

// Transform holds all DVE parameters for a single slot.
type Transform struct {
	// Geometry
	PositionX    float64 `json:"positionX"`
	PositionY    float64 `json:"positionY"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	RotationDeg  float64 `json:"rotationDeg"`
	// AnchorX is the anchor point X for rotation/scale, normalized [0-1].
	// Zero-value pair treated as (0.5, 0.5) center. Use EffectiveAnchor() for resolved value.
	AnchorX float64 `json:"anchorX"`
	// AnchorY is the anchor point Y for rotation/scale, normalized [0-1].
	// Zero-value pair treated as (0.5, 0.5) center. Use EffectiveAnchor() for resolved value.
	AnchorY float64 `json:"anchorY"`
	PerspectiveX float64 `json:"perspectiveX"`
	PerspectiveY float64 `json:"perspectiveY"`

	// Crop
	CropTop      float64 `json:"cropTop"`
	CropBottom   float64 `json:"cropBottom"`
	CropLeft     float64 `json:"cropLeft"`
	CropRight    float64 `json:"cropRight"`
	CropSoftness float64 `json:"cropSoftness"`

	// Visual Effects
	// Opacity: 0.0-1.0. Zero value treated as 1.0 (fully opaque). Use EffectiveOpacity() for resolved value.
	Opacity       float64      `json:"opacity"`
	Border        BorderConfig `json:"border"`
	Shadow        ShadowConfig `json:"shadow"`
	DefocusRadius float64      `json:"defocusRadius"`

	// Mirror/flip
	MirrorH bool `json:"mirrorH,omitempty"`
	MirrorV bool `json:"mirrorV,omitempty"`

	// Image effects
	MosaicSize int     `json:"mosaicSize,omitempty"` // pixel block size, 0=disabled
	Saturation float64 `json:"saturation,omitempty"` // 0.0=monochrome, 1.0=normal, max 2.0 (super-saturated)
	Brightness float64 `json:"brightness,omitempty"` // -1.0 to 1.0, 0.0=normal
	Contrast   float64 `json:"contrast,omitempty"`   // 0.0=flat gray, 1.0=normal, max 3.0 (high contrast)

	// 3D DVE
	Mode3D     string         `json:"mode3d,omitempty"`     // "" = 2D (default), "3d" = 3D mode
	Transform3D *Transform3D  `json:"transform3d,omitempty"` // 3D parameters (only when Mode3D="3d")

	// Source Mapping
	ScaleMode  string     `json:"scaleMode,omitempty"`
	CropAnchor [2]float64 `json:"cropAnchor,omitempty"`
}

// Transform3D holds 3D DVE parameters for mapping video onto geometry.
type Transform3D struct {
	// 3D rotation (Euler angles in degrees)
	RotateX float64 `json:"rotateX"` // pitch: tilt forward/back, range [-180, 180]
	RotateY float64 `json:"rotateY"` // yaw: spin left/right, range [-180, 180]
	RotateZ float64 `json:"rotateZ"` // roll: tilt sideways, range [-180, 180]

	// Camera
	FOV      float64 `json:"fov"`      // field of view in degrees, default 20 (narrow=broadcast-natural)
	Distance float64 `json:"distance"` // camera distance, default 3.0

	// Geometry shape
	Shape string `json:"shape"` // "plane" (default), "cube"

	// Lighting
	LightEnabled bool    `json:"lightEnabled"`
	LightX       float64 `json:"lightX"`       // light direction X [-1, 1]
	LightY       float64 `json:"lightY"`       // light direction Y [-1, 1]
	Ambient      float64 `json:"ambient"`      // ambient light [0, 1], default 0.3
	Specular     float64 `json:"specular"`     // specular intensity [0, 1]

	// Reflection
	ReflectionEnabled bool    `json:"reflectionEnabled"`
	ReflectionOpacity float64 `json:"reflectionOpacity"` // 0-1
	ReflectionFade    float64 `json:"reflectionFade"`    // gradient height 0-1

	// Page turn (shape="page_turn")
	CurlProgress float64 `json:"curlProgress,omitempty"` // 0=flat, 1=fully turned
	CurlRadius   float64 `json:"curlRadius,omitempty"`   // cylinder radius for curl effect
}

// BorderConfig describes the visual border around a DVE slot.
type BorderConfig struct {
	Width        float64 `json:"width"`
	ColorY       byte    `json:"colorY"`
	ColorCb      byte    `json:"colorCb"`
	ColorCr      byte    `json:"colorCr"`
	CornerRadius float64 `json:"cornerRadius"`
	Softness     float64 `json:"softness"`
	InnerWidth   float64 `json:"innerWidth"`
}

// ShadowConfig describes the drop shadow for a DVE slot.
type ShadowConfig struct {
	Enabled bool    `json:"enabled"`
	OffsetX float64 `json:"offsetX"`
	OffsetY float64 `json:"offsetY"`
	Blur    float64 `json:"blur"`
	Opacity float64 `json:"opacity"`
	ColorY  byte    `json:"colorY"`
}

// Slot is a single source slot with a DVE transform.
type Slot struct {
	SourceKey    string    `json:"sourceKey"`
	Transform    Transform `json:"transform"`
	ZOrder       int       `json:"zOrder"`
	Enabled      bool      `json:"enabled"`
	Freeze       bool      `json:"freeze,omitempty"`
	StrobeFrames int       `json:"strobeFrames,omitempty"` // show every Nth frame, 0=disabled
}

// Layout is the complete DVE layout configuration.
type Layout struct {
	Name  string `json:"name"`
	Slots []Slot `json:"slots"`
}

// DefaultTransform returns a Transform with the given position/size,
// opacity=1.0, anchor at center (0.5, 0.5), and all effects off.
func DefaultTransform(x, y, w, h float64) Transform {
	return Transform{
		PositionX:  x,
		PositionY:  y,
		Width:      w,
		Height:     h,
		Opacity:    1.0,
		AnchorX:    0.5,
		AnchorY:    0.5,
		Saturation: 1.0,
		Contrast:   1.0,
	}
}

// IsIdentity returns true when the transform covers the full frame with no
// rotation, no perspective, no crop, no border, no shadow, no defocus,
// and opacity=1.0.
func (t Transform) IsIdentity(frameW, frameH int) bool {
	return t.PositionX == 0 &&
		t.PositionY == 0 &&
		t.Width == float64(frameW) &&
		t.Height == float64(frameH) &&
		t.RotationDeg == 0 &&
		t.PerspectiveX == 0 &&
		t.PerspectiveY == 0 &&
		t.CropTop == 0 &&
		t.CropBottom == 0 &&
		t.CropLeft == 0 &&
		t.CropRight == 0 &&
		t.CropSoftness == 0 &&
		t.Opacity == 1.0 &&
		t.Border.Width == 0 &&
		!t.Shadow.Enabled &&
		t.DefocusRadius == 0 &&
		!t.MirrorH &&
		!t.MirrorV &&
		t.MosaicSize == 0 &&
		t.Saturation == 1.0 &&
		t.Brightness == 0 &&
		t.Contrast == 1.0 &&
		t.Mode3D == ""
}

// Is3D returns true when the transform uses 3D mode.
func (t Transform) Is3D() bool {
	return t.Mode3D == "3d" && t.Transform3D != nil
}

// Default3DTransform returns a Transform3D with broadcast-standard defaults.
func Default3DTransform() *Transform3D {
	return &Transform3D{
		FOV:      20,
		Distance: 3.0,
		Shape:    "plane",
		Ambient:  0.3,
	}
}

// ToRect returns the integer axis-aligned bounding rect, even-aligned
// for YUV420 compatibility. Provides backwards compatibility with the
// legacy layout system.
func (t Transform) ToRect() image.Rectangle {
	x := EvenAlign(int(t.PositionX))
	y := EvenAlign(int(t.PositionY))
	w := EvenAlign(int(t.Width))
	h := EvenAlign(int(t.Height))
	return image.Rect(x, y, x+w, y+h)
}

// EffectiveOpacity returns opacity clamped to [0, 1], defaulting to 1.0
// if the zero-value (0) is stored.
func (t Transform) EffectiveOpacity() float64 {
	if t.Opacity == 0 {
		return 1.0
	}
	return clamp01(t.Opacity)
}

// EffectiveAnchor returns the anchor clamped to [0, 1], defaulting to
// (0.5, 0.5) if both values are zero.
func (t Transform) EffectiveAnchor() (float64, float64) {
	if t.AnchorX == 0 && t.AnchorY == 0 {
		return 0.5, 0.5
	}
	return clamp01(t.AnchorX), clamp01(t.AnchorY)
}

// ValidateTransform checks that all transform parameters are within valid ranges.
// Position is not bounds-checked because DVE transforms can intentionally fly
// off-screen during animations.
func ValidateTransform(t Transform) error {
	if t.Width <= 0 {
		return fmt.Errorf("width must be > 0, got %v", t.Width)
	}
	if t.Height <= 0 {
		return fmt.Errorf("height must be > 0, got %v", t.Height)
	}
	if t.RotationDeg < -360 || t.RotationDeg > 360 {
		return fmt.Errorf("rotationDeg must be in [-360, 360], got %v", t.RotationDeg)
	}
	if t.PerspectiveX < -60 || t.PerspectiveX > 60 {
		return fmt.Errorf("perspectiveX must be in [-60, 60], got %v", t.PerspectiveX)
	}
	if t.PerspectiveY < -60 || t.PerspectiveY > 60 {
		return fmt.Errorf("perspectiveY must be in [-60, 60], got %v", t.PerspectiveY)
	}
	if t.Opacity < 0 || t.Opacity > 1 {
		return fmt.Errorf("opacity must be in [0, 1], got %v", t.Opacity)
	}
	if t.CropTop < 0 {
		return fmt.Errorf("cropTop must be >= 0, got %v", t.CropTop)
	}
	if t.CropBottom < 0 {
		return fmt.Errorf("cropBottom must be >= 0, got %v", t.CropBottom)
	}
	if t.CropLeft < 0 {
		return fmt.Errorf("cropLeft must be >= 0, got %v", t.CropLeft)
	}
	if t.CropRight < 0 {
		return fmt.Errorf("cropRight must be >= 0, got %v", t.CropRight)
	}
	if t.CropSoftness < 0 {
		return fmt.Errorf("cropSoftness must be >= 0, got %v", t.CropSoftness)
	}
	if t.Border.Width < 0 {
		return fmt.Errorf("border width must be >= 0, got %v", t.Border.Width)
	}
	if t.Border.CornerRadius < 0 {
		return fmt.Errorf("border cornerRadius must be >= 0, got %v", t.Border.CornerRadius)
	}
	if t.Border.Softness < 0 {
		return fmt.Errorf("border softness must be >= 0, got %v", t.Border.Softness)
	}
	if t.Border.InnerWidth < 0 {
		return fmt.Errorf("border innerWidth must be >= 0, got %v", t.Border.InnerWidth)
	}
	if t.Shadow.Blur < 0 {
		return fmt.Errorf("shadow blur must be >= 0, got %v", t.Shadow.Blur)
	}
	if t.Shadow.Opacity < 0 || t.Shadow.Opacity > 1 {
		return fmt.Errorf("shadow opacity must be in [0, 1], got %v", t.Shadow.Opacity)
	}
	if t.DefocusRadius < 0 {
		return fmt.Errorf("defocusRadius must be >= 0, got %v", t.DefocusRadius)
	}
	if t.MosaicSize < 0 || t.MosaicSize > 256 {
		return fmt.Errorf("mosaicSize must be in [0, 256], got %v", t.MosaicSize)
	}
	if t.Saturation < 0 || t.Saturation > 2 {
		return fmt.Errorf("dve: saturation %f out of range [0.0, 2.0]", t.Saturation)
	}
	if t.Brightness < -1 || t.Brightness > 1 {
		return fmt.Errorf("dve: brightness %f out of range [-1.0, 1.0]", t.Brightness)
	}
	if t.Contrast < 0 || t.Contrast > 3 {
		return fmt.Errorf("dve: contrast %f out of range [0.0, 3.0]", t.Contrast)
	}
	if t.ScaleMode != "" && t.ScaleMode != ScaleModeStretch && t.ScaleMode != ScaleModeFill {
		return fmt.Errorf("unknown scaleMode %q (must be %q or %q)", t.ScaleMode, ScaleModeStretch, ScaleModeFill)
	}
	for i, v := range t.CropAnchor {
		if v < 0 || v > 1 {
			return fmt.Errorf("cropAnchor[%d] = %v out of range [0, 1]", i, v)
		}
	}
	return nil
}

// ValidateSlot validates a DVE slot, including its transform and ZOrder.
func ValidateSlot(s Slot) error {
	if s.ZOrder < 0 {
		return fmt.Errorf("zOrder must be >= 0, got %d", s.ZOrder)
	}
	if s.StrobeFrames < 0 {
		return fmt.Errorf("strobeFrames must be >= 0, got %d", s.StrobeFrames)
	}
	if err := ValidateTransform(s.Transform); err != nil {
		return err
	}
	return nil
}

// ValidateLayout validates a DVE layout, checking slot count and each slot.
func ValidateLayout(l *Layout) error {
	if len(l.Slots) > MaxSlots {
		return fmt.Errorf("layout has %d slots, max is %d", len(l.Slots), MaxSlots)
	}
	for i, slot := range l.Slots {
		if err := ValidateSlot(slot); err != nil {
			return fmt.Errorf("slot %d: %w", i, err)
		}
	}
	return nil
}

// EvenAlign rounds down to the nearest even number (YUV420 alignment).
func EvenAlign(v int) int { return v &^ 1 }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
