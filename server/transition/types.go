package transition

// TransitionType identifies the visual transition effect.
type TransitionType string

const (
	TransitionMix TransitionType = "mix"
	TransitionDip TransitionType = "dip"
	TransitionFTB TransitionType = "ftb"
)

// TransitionState tracks whether a transition is currently running.
type TransitionState int

const (
	StateIdle   TransitionState = 0
	StateActive TransitionState = 1
)
