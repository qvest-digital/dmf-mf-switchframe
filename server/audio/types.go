package audio

// AudioTransitionMode describes how audio should behave during a video transition.
type AudioTransitionMode int

const (
	AudioCrossfade    AudioTransitionMode = iota // Mix: equal-power A→B
	AudioDipToSilence                            // Dip: A→silence→B
	AudioFadeOut                                 // FTB: A→silence
	AudioFadeIn                                  // FTB Reverse: silence→A
)
