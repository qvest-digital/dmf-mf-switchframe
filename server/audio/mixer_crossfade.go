package audio

import (
	"math"
	"sort"
)

// OnProgramChange updates AFV channel states based on the new program source.
// Channels with AFV enabled activate when they match the program source and
// deactivate when they don't. Non-AFV channels are unaffected.
//
// To avoid audible clicks, a 4-period crossfade ramp is set up between
// the outgoing and incoming AFV channels, identical to what OnCut does.
func (m *Mixer) OnProgramChange(newProgramSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Determine the outgoing AFV source deterministically. Prefer the tracked
	// program AFV source; only fall back to a scan (in sorted key order, never
	// random map order) if the tracked source is gone, no longer AFV, or no
	// longer active. This keeps oldSource — and therefore the resulting mix —
	// deterministic even when several AFV channels are momentarily active.
	oldSource := ""
	if m.programAFVSource != "" {
		if ch, ok := m.channels[m.programAFVSource]; ok && ch.afv && ch.active {
			oldSource = m.programAFVSource
		}
	}
	if oldSource == "" {
		keys := make([]string, 0, len(m.channels))
		for key := range m.channels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			ch := m.channels[key]
			if ch.afv && ch.active {
				oldSource = key
				break
			}
		}
	}

	// If there's both an outgoing and incoming AFV channel, set up a crossfade
	// ramp to avoid clicks. This mirrors what OnCut does.
	if oldSource != "" && oldSource != newProgramSource {
		if _, ok := m.channels[newProgramSource]; ok {
			m.transCrossfadeActive = true
			m.transCrossfades.Add(1)
			m.transCrossfadeFrom = oldSource
			m.transCrossfadeTo = newProgramSource
			m.transCrossfadeMode = Crossfade
			m.transCrossfadePosition = 0.0
			m.transCrossfadeAudioPos = 0.0
			m.mixCycleTransPos = 0.0
			m.cutFramesRemaining = 4
			m.cutTotalFrames = 4
		}
	}

	// Track whether the new program actually has an AFV channel; if not, there
	// is no AFV source on program anymore.
	newProgramHasAFV := false

	for key, ch := range m.channels {
		if !ch.afv {
			continue
		}
		if key == newProgramSource {
			// Activate the incoming AFV channel.
			ch.active = true
			newProgramHasAFV = true
			// A rapid bounce-back (A→B→A within the crossfade window) may
			// have queued this channel for deferred deactivation on the
			// previous program change. It is the program source again, so it
			// must not be deactivated when the ramp completes.
			delete(m.afvDeactivateOnComplete, key)
			// The program change put this source on program; a later (stale)
			// OnTransitionAbort must not roll back its activation.
			if m.transAbortActivated == key {
				m.transAbortActivated = ""
			}
			m.initChannelDecoder(ch)
		} else if ch.active && m.cutFramesRemaining > 0 {
			// A crossfade ramp is in progress. Keep every currently-active AFV
			// channel that isn't the new program alive until the ramp completes
			// so it fades out with the transition instead of being hard-cut
			// (which clicks). The ramped outgoing source (transCrossfadeFrom)
			// fades smoothly; any extra stragglers at least avoid an instant
			// discontinuity. They are all deactivated together by advanceCutCrossfade when
			// the crossfade completes.
			if m.afvDeactivateOnComplete == nil {
				m.afvDeactivateOnComplete = make(map[string]struct{})
			}
			m.afvDeactivateOnComplete[key] = struct{}{}
		} else {
			// No crossfade to ride out (e.g. switching to a non-AFV program):
			// deactivate immediately.
			ch.active = false
		}
	}

	if newProgramHasAFV {
		m.programAFVSource = newProgramSource
	} else {
		m.programAFVSource = ""
	}
}

// OnCut initiates a 4-period crossfade between old and new source, driven
// by AdvanceCrossfade, which advances transCrossfadePosition linearly over
// cutTotalFrames periods and auto-clears when complete. No timer goroutines
// needed.
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

	// Set up cut crossfade: AdvanceCrossfade advances position over 4 periods.
	m.cutFramesRemaining = 4
	m.cutTotalFrames = 4

	// Flush the new source's ring buffer to discard stale audio that
	// accumulated between source connect and this cut. Without this,
	// the mixer reads samples that are hundreds of ms old, causing
	// audible A/V desync. After flush, push and pop rates match
	// (both 48kHz), so depth stays near zero.
	if ch, ok := m.channels[newSource]; ok {
		if ch.ringBuf != nil {
			ch.ringBuf.Reset()
			ch.placed = false
		}
		ch.active = true
		// The cut put this source on program; a later OnTransitionAbort must
		// not roll back its activation.
		if m.transAbortActivated == newSource {
			m.transAbortActivated = ""
		}
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
//
// It returns an ownership token identifying the transition state it just
// established. The token must be passed to OnTransitionAbort, which is a
// no-op unless that token still owns the mixer's transition state. The
// transCrossfades counter doubles as the state generation: every event that
// establishes new transition/cut crossfade state (OnTransitionStart, OnCut,
// a ramping OnProgramChange) bumps it, invalidating older tokens. This makes
// stale aborts — e.g. a pre-empted StartTransition cleaning up after a
// concurrent Cut already installed its own crossfade — harmless.
func (m *Mixer) OnTransitionStart(oldSource, newSource string, mode TransitionMode, durationMs int) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transCrossfadeActive = true
	token := uint64(m.transCrossfades.Add(1))
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = mode
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0

	// Flush + activate the incoming source's channel so we start from
	// fresh audio rather than stale data buffered before the transition.
	m.transAbortActivated = ""
	if ch, ok := m.channels[newSource]; ok {
		if ch.ringBuf != nil {
			ch.ringBuf.Reset()
			ch.placed = false
		}
		if !ch.active {
			// Remember that this transition is what activated the channel
			// so OnTransitionAbort can roll the activation back.
			m.transAbortActivated = newSource
		}
		ch.active = true
		// Pre-warm the decoder so the first frame from this source
		// doesn't produce warmup transients in the mix output.
		m.initChannelDecoder(ch)
	}

	return token
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
	m.afvDeactivateOnComplete = nil
	m.transAbortActivated = "" // the incoming source is on program now — keep it active
	m.mu.Unlock()
}

// OnTransitionAbort handles a cancelled transition (e.g. T-bar pulled back
// to 0). Snaps the crossfade position to 0.0 (fully original source) and
// clears all transition state, rolling back the incoming channel activation
// performed by OnTransitionStart so preview audio does not keep leaking into
// program.
//
// token is the ownership token returned by the OnTransitionStart being
// aborted. The abort is a no-op when the token is stale — i.e. a newer
// OnTransitionStart, OnCut, or ramping OnProgramChange has since established
// its own transition state — so a pre-empted transition's late cleanup can
// never wipe crossfade state (or the deferred AFV deactivations) owned by a
// concurrent cut or newer transition.
func (m *Mixer) OnTransitionAbort(token uint64) {
	m.mu.Lock()
	if uint64(m.transCrossfades.Load()) != token {
		// Stale abort: the transition state currently in the mixer was
		// established by a newer owner. Leave it untouched.
		m.mu.Unlock()
		return
	}
	// Deactivate the incoming channel if (and only if) OnTransitionStart was
	// what activated it. The transition never completed, so leaving it active
	// would mix the preview source into program at full fader gain forever —
	// the switcher's abort path skips OnProgramChange, which is otherwise the
	// only thing that deactivates it. Never touch the current program AFV
	// source (e.g. a cut landed on that source mid-transition).
	if m.transAbortActivated != "" && m.transAbortActivated != m.programAFVSource {
		if ch, ok := m.channels[m.transAbortActivated]; ok {
			ch.active = false
		}
	}
	m.transAbortActivated = ""
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
	m.afvDeactivateOnComplete = nil
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
