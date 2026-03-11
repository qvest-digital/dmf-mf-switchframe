package audio

// TransitionMode describes how audio should behave during a video transition.
type TransitionMode int

const (
	Crossfade    TransitionMode = iota // Mix: equal-power A→B
	DipToSilence                       // Dip: A→silence→B
	FadeOut                            // FTB: A→silence
	FadeIn                             // FTB Reverse: silence→A
)
