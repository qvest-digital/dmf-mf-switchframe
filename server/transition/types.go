package transition

import "errors"

// Sentinel errors for the transition package.
var (
	ErrActive    = errors.New("transition: already active")
	ErrFTBActive = errors.New("transition: FTB is active")
)

// Type identifies the visual transition effect.
type Type string

const (
	Mix        Type = "mix"
	Dip        Type = "dip"
	FTB        Type = "ftb"
	FTBReverse Type = "ftb_reverse"
	Wipe       Type = "wipe"
	Stinger    Type = "stinger"

	// DVE transition types
	TypeDVEPushLeft  Type = "dve-push-left"
	TypeDVEPushRight Type = "dve-push-right"
	TypeDVEPushUp    Type = "dve-push-up"
	TypeDVEPushDown  Type = "dve-push-down"
	TypeDVESqueeze   Type = "dve-squeeze"
	TypeDVESpin      Type = "dve-spin"
	TypeDVEZoom      Type = "dve-zoom"
	TypeDVEFly       Type = "dve-fly"
	TypeDVECube      Type = "dve-cube"
	TypeDVEFlip      Type = "dve-flip"
	TypeDVEDoor      Type = "dve-door"
	// Extended 2D
	TypeDVESwingLeft   Type = "dve-swing-left"
	TypeDVESwingRight  Type = "dve-swing-right"
	TypeDVERevealLeft  Type = "dve-reveal-left"
	TypeDVERevealRight Type = "dve-reveal-right"
	TypeDVERevealUp    Type = "dve-reveal-up"
	TypeDVERevealDown  Type = "dve-reveal-down"
	TypeDVEScaleRotate Type = "dve-scale-rotate"
	TypeDVECrossZoom   Type = "dve-cross-zoom"
	TypeDVEShrinkGrow  Type = "dve-shrink-grow"
	TypeDVEBarnH       Type = "dve-barn-h"
	TypeDVEBarnV       Type = "dve-barn-v"
	TypeDVEMosaic      Type = "dve-mosaic"
	TypeDVECornerPeel  Type = "dve-corner-peel"
	// Extended 3D
	TypeDVECarousel  Type = "dve-carousel"
	TypeDVETumble    Type = "dve-tumble"
	TypeDVESwingDoor Type = "dve-swing-door"
	TypeDVEFold      Type = "dve-fold"
	TypeDVECubeRoll  Type = "dve-cube-roll"
	TypeDVEGlobe     Type = "dve-globe"
	// B2 extended transitions
	TypeDVEPageTurn     Type = "dve-page-turn"
	TypeDVEBounce       Type = "dve-bounce"
	TypeDVEBlurDissolve Type = "dve-blur-dissolve"
	// NAM is a blend mode, not a DVE transition
	TypeNAM Type = "nam"
)

// WipeDirection specifies the direction for a wipe transition.
type WipeDirection string

const (
	WipeHLeft        WipeDirection = "h-left"
	WipeHRight       WipeDirection = "h-right"
	WipeVTop         WipeDirection = "v-top"
	WipeVBottom      WipeDirection = "v-bottom"
	WipeBoxCenterOut WipeDirection = "box-center-out"
	WipeBoxEdgesIn   WipeDirection = "box-edges-in"
)

// ValidWipeDirections is the set of all valid wipe directions.
var ValidWipeDirections = map[WipeDirection]bool{
	WipeHLeft:        true,
	WipeHRight:       true,
	WipeVTop:         true,
	WipeVBottom:      true,
	WipeBoxCenterOut: true,
	WipeBoxEdgesIn:   true,
}

// EasingConfig is the JSON-serializable easing configuration for API requests.
type EasingConfig struct {
	Type string  `json:"type"`
	X1   float64 `json:"x1,omitempty"`
	Y1   float64 `json:"y1,omitempty"`
	X2   float64 `json:"x2,omitempty"`
	Y2   float64 `json:"y2,omitempty"`
}

// State tracks whether a transition is currently running.
type State int

const (
	StateIdle   State = 0
	StateActive State = 1
)
