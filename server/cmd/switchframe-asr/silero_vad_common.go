package main

import "math"

const (
	sileroChunkSize       = 512
	sileroContextSize     = 64
	sileroInputSize       = sileroContextSize + sileroChunkSize // 576
	sileroStateSize       = 2 * 1 * 128                        // [2,1,128]
	sileroSampleRate      = 16000
	sileroChunkDurationMs = 32
)

type sileroVADState struct {
	threshold    float64
	negThreshold float64
	speaking     bool
	hangover     int
	hangoverMs   int
}

func newSileroVADState(threshold float64, hangoverMs int) sileroVADState {
	if threshold <= 0 {
		threshold = 0.5
	}
	if hangoverMs <= 0 {
		hangoverMs = 750
	}
	return sileroVADState{
		threshold:    threshold,
		negThreshold: math.Max(threshold-0.15, 0.01),
		hangoverMs:   hangoverMs,
	}
}

func (s *sileroVADState) updateState(prob float64) {
	if prob > s.threshold {
		s.speaking = true
		s.hangover = s.hangoverMs
	} else if s.speaking && prob < s.negThreshold {
		s.hangover -= sileroChunkDurationMs
		if s.hangover <= 0 {
			s.speaking = false
			s.hangover = 0
		}
	} else if s.speaking {
		// In hysteresis zone (between negThreshold and threshold): stay speaking, reset hangover.
		s.hangover = s.hangoverMs
	}
}
