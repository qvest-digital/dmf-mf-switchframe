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
