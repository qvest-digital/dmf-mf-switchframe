package transition

import "errors"

// Sentinel errors for the transition package.
var (
	ErrTransitionActive = errors.New("transition already active")
	ErrFTBActive        = errors.New("FTB is active")
)

// TransitionType identifies the visual transition effect.
type TransitionType string

const (
	TransitionMix        TransitionType = "mix"
	TransitionDip        TransitionType = "dip"
	TransitionFTB        TransitionType = "ftb"
	TransitionFTBReverse TransitionType = "ftb_reverse"
)

// TransitionState tracks whether a transition is currently running.
type TransitionState int

const (
	StateIdle   TransitionState = 0
	StateActive TransitionState = 1
)
