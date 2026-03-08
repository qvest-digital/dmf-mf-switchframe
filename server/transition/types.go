package transition

import "errors"

// Sentinel errors for the transition package.
var (
	ErrTransitionActive = errors.New("transition: already active")
	ErrFTBActive        = errors.New("transition: FTB is active")
)

// TransitionType identifies the visual transition effect.
type TransitionType string

const (
	TransitionMix        TransitionType = "mix"
	TransitionDip        TransitionType = "dip"
	TransitionFTB        TransitionType = "ftb"
	TransitionFTBReverse TransitionType = "ftb_reverse"
	TransitionWipe       TransitionType = "wipe"
	TransitionStinger    TransitionType = "stinger"
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

// TransitionState tracks whether a transition is currently running.
type TransitionState int

const (
	StateIdle   TransitionState = 0
	StateActive TransitionState = 1
)
