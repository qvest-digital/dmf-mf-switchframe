package audio

import (
	"math"
)

// OnProgramChange updates AFV channel states based on the new program source.
// Channels with AFV enabled activate when they match the program source and
// deactivate when they don't. Non-AFV channels are unaffected.
func (m *Mixer) OnProgramChange(newProgramSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, ch := range m.channels {
		if !ch.afv {
			continue
		}
		wasActive := ch.active
		ch.active = (key == newProgramSource)

		// Flush the ring buffer when a source first goes on program.
		// Audio accumulates in the FIFO ring buffer from the moment the
		// source connects, but video doesn't start until the first keyframe
		// is decoded (~500-1000ms later). Without flushing, the mixer outputs
		// stale audio from before video started, causing video to appear
		// 500-1000ms ahead of audio. Flushing aligns the audio start with
		// the video start.
		if ch.active && !wasActive && ch.ringBuf != nil {
			ch.ringBuf.Reset()
		}
	}
}

// OnCut initiates a 2-tick (~42ms) crossfade between old and new source,
// driven entirely by the output ticker. The ticker advances
// transCrossfadePosition linearly over cutTotalFrames ticks and auto-clears
// when complete. No timer goroutines needed.
func (m *Mixer) OnCut(oldSource, newSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transCrossfadeActive = true
	m.transCrossfades.Add(1)
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadeMode = Crossfade
	m.transCrossfadePosition = 0.0
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0

	// Set up cut crossfade: the ticker will advance position over 2 ticks.
	m.cutFramesRemaining = 2
	m.cutTotalFrames = 2

	// Activate the new source channel so its audio frames are accepted.
	if ch, ok := m.channels[newSource]; ok {
		ch.active = true
		m.initChannelDecoder(ch)
	}
}

// OnTransitionStart begins a multi-frame crossfade between old and new source,
// synchronized with a video transition. The mode selects the gain curve:
//   - Crossfade: equal-power A->B (mix/dissolve)
//   - DipToSilence: A->silence->B (dip through black)
//   - FadeOut: A->silence (fade to black)
//   - FadeIn: silence->A (fade from black)
//
// The new source channel is activated so its audio frames are accepted.
func (m *Mixer) OnTransitionStart(oldSource, newSource string, mode TransitionMode, durationMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transCrossfadeActive = true
	m.transCrossfades.Add(1)
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = mode
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0

	// Ensure the incoming source's channel is active so frames are accepted.
	if ch, ok := m.channels[newSource]; ok {
		ch.active = true
		// Pre-warm the decoder so the first frame from this source
		// doesn't produce warmup transients in the mix output.
		m.initChannelDecoder(ch)
	}

	// Pre-warm the encoder so the first real output frame doesn't have
	// MDCT warmup artifacts (audible pop at passthrough->mixing boundary).
	_ = m.ensureEncoder()
}

// OnTransitionPosition updates the crossfade position (0.0 = fully old, 1.0 = fully new).
// Called by the switcher as the video transition progresses. Tracks the previous position
// for per-sample gain interpolation within audio frames.
func (m *Mixer) OnTransitionPosition(position float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transCrossfadePosition = position
}

// OnTransitionComplete clears the transition crossfade state.
// Called by the switcher when the video transition finishes.
func (m *Mixer) OnTransitionComplete() {
	m.mu.Lock()
	m.transCrossfadeActive = false
	m.transCrossfadeFrom = ""
	m.transCrossfadeTo = ""
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = 0
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0
	m.cutFramesRemaining = 0
	m.cutTotalFrames = 0
	m.stingerAudio = nil
	m.stingerOffset = 0
	m.stingerChannels = 0
	m.mu.Unlock()
}

// OnTransitionAbort handles a cancelled transition (e.g. T-bar pulled back
// to 0). Snaps the crossfade position to 0.0 (fully original source) and
// clears all transition state.
func (m *Mixer) OnTransitionAbort() {
	m.mu.Lock()
	m.transCrossfadeActive = false
	m.transCrossfadeFrom = ""
	m.transCrossfadeTo = ""
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = 0
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0
	m.cutFramesRemaining = 0
	m.cutTotalFrames = 0
	m.stingerAudio = nil
	m.stingerOffset = 0
	m.stingerChannels = 0
	m.mu.Unlock()
}

// IsInTransitionCrossfade returns whether a multi-frame transition crossfade is active.
func (m *Mixer) IsInTransitionCrossfade() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transCrossfadeActive
}

// TransitionPosition returns the current transition crossfade position (0.0-1.0).
func (m *Mixer) TransitionPosition() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transCrossfadePosition
}

// TransitionGains returns the crossfade gains for the old and new sources based
// on the current transition position and mode. When no transition is active,
// returns (1.0, 0.0).
func (m *Mixer) TransitionGains() (oldGain, newGain float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.transCrossfadeActive {
		return 1.0, 0.0
	}
	return transitionFromGain(m.transCrossfadeMode, m.transCrossfadePosition),
		transitionToGain(m.transCrossfadeMode, m.transCrossfadePosition)
}

// transitionFromGain computes the gain for the outgoing ("from") source at the
// given position and mode.
func transitionFromGain(mode TransitionMode, pos float64) float64 {
	switch mode {
	case Crossfade:
		return math.Cos(pos * math.Pi / 2)
	case DipToSilence:
		if pos < 0.5 {
			// Phase 1: fade out A (equal-power over the first half)
			return math.Cos(pos * 2 * math.Pi / 2)
		}
		return 0
	case FadeOut:
		return math.Cos(pos * math.Pi / 2)
	case FadeIn:
		// FTB reverse: fade the "from" source IN from silence
		return math.Sin(pos * math.Pi / 2)
	}
	return 1.0
}

// transitionToGain computes the gain for the incoming ("to") source at the
// given position and mode.
func transitionToGain(mode TransitionMode, pos float64) float64 {
	switch mode {
	case Crossfade:
		return math.Sin(pos * math.Pi / 2)
	case DipToSilence:
		if pos >= 0.5 {
			// Phase 2: fade in B (equal-power over the second half)
			return math.Sin((pos*2 - 1) * math.Pi / 2)
		}
		return 0
	case FadeOut, FadeIn:
		// FTB has no "to" source
		return 0
	}
	return 0
}
