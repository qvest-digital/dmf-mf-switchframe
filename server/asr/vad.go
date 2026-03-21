package asr

import "math"

// VADState represents the current state of the voice activity detector.
type VADState int

const (
	// VADIdle means no speech detected.
	VADIdle VADState = iota
	// VADSpeaking means speech is actively detected.
	VADSpeaking
	// VADTrailing means speech ended but we're in the hangover period,
	// waiting to confirm silence before returning to idle.
	VADTrailing
)

// String returns a human-readable name for the VAD state.
func (s VADState) String() string {
	switch s {
	case VADIdle:
		return "idle"
	case VADSpeaking:
		return "speaking"
	case VADTrailing:
		return "trailing"
	default:
		return "unknown"
	}
}

// VADConfig configures the voice activity detector.
type VADConfig struct {
	// ThresholdDB is the RMS threshold in dBFS (e.g., -35).
	// Signals with RMS above this level are considered speech.
	ThresholdDB float64
	// HangoverMs is the hold time in milliseconds after speech ends
	// before transitioning back to idle. This prevents mid-word cutoffs
	// during brief pauses.
	HangoverMs int
	// SampleRate is the audio sample rate in Hz (e.g., 16000 for Whisper).
	SampleRate int
}

// VAD is an energy-based voice activity detector. It uses RMS energy
// to classify audio chunks as speech or silence, with a hangover timer
// to avoid premature cutoff during natural speech pauses.
//
// State machine: idle -> speaking -> trailing -> idle
//
// The detector is not goroutine-safe; callers must synchronize access.
type VAD struct {
	cfg             VADConfig
	state           VADState
	thresholdLinear float64
	hangoverSamples int
	silentSamples   int
}

// NewVAD creates a new voice activity detector with the given configuration.
func NewVAD(cfg VADConfig) *VAD {
	return &VAD{
		cfg:             cfg,
		thresholdLinear: math.Pow(10, cfg.ThresholdDB/20.0),
		hangoverSamples: cfg.SampleRate * cfg.HangoverMs / 1000,
	}
}

// State returns the current VAD state.
func (v *VAD) State() VADState { return v.state }

// Process analyzes a chunk of PCM samples and updates the VAD state.
// Samples should be mono float32 in the range [-1.0, 1.0].
func (v *VAD) Process(samples []float32) {
	if len(samples) == 0 {
		return
	}

	// Compute RMS energy of the chunk.
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	isSpeech := rms >= v.thresholdLinear

	switch v.state {
	case VADIdle:
		if isSpeech {
			v.state = VADSpeaking
			v.silentSamples = 0
		}

	case VADSpeaking:
		if isSpeech {
			v.silentSamples = 0
		} else {
			v.state = VADTrailing
			v.silentSamples = len(samples)
		}

	case VADTrailing:
		if isSpeech {
			v.state = VADSpeaking
			v.silentSamples = 0
		} else {
			v.silentSamples += len(samples)
			if v.silentSamples >= v.hangoverSamples {
				v.state = VADIdle
				v.silentSamples = 0
			}
		}
	}
}

// Reset returns the VAD to its initial idle state.
func (v *VAD) Reset() {
	v.state = VADIdle
	v.silentSamples = 0
}
