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
	// ramp to avoid clicks. This mirrors what OnCut does. A cut ramp already
	// running from the one to the other, the OnCut a cut sends ahead of
	// this, is left to run: started again, it would take the incoming
	// source back down if a period aired in between.
	restart := false
	if oldSource != "" && oldSource != newProgramSource {
		if _, ok := m.channels[newProgramSource]; ok && (m.cutFramesRemaining <= 0 ||
			m.transCrossfadeFrom != oldSource || m.transCrossfadeTo != newProgramSource) {
			m.transCrossfadeActive = true
			m.transCrossfades.Add(1)
			m.transCrossfadeFrom = oldSource
			m.transCrossfadeTo = newProgramSource
			m.transCrossfadeMode = Crossfade
			restart = true
		}
	}
	ramping := restart || m.cutFramesRemaining > 0

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
		} else if ch.active && ramping {
			// A crossfade ramp is in progress. Keep every currently-active AFV
			// channel that isn't the new program alive until the ramp completes
			// so it fades out with the transition instead of being hard-cut
			// (which clicks). The ramped outgoing source (transCrossfadeFrom)
			// fades smoothly; any extra stragglers at least avoid an instant
			// discontinuity. They are all deactivated together by advanceCutCrossfade when
			// the crossfade completes.
			m.deactivateOnCompleteLocked(key)
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

	// A ramp that does not already take every channel where this change
	// puts it starts again from the gains aired.
	if restart || ramping && !m.cutTargetsMatchLocked() {
		m.startCutRampLocked()
	}
}

// OnCut initiates a 4-period crossfade between old and new source, driven
// by AdvanceCrossfade, which advances transCrossfadePosition linearly over
// cutTotalFrames periods and auto-clears when complete. No timer goroutines
// needed. Every channel ramps from the gain it aired last, so a cut during
// another cut's ramp or during a transition carries on from there. The old
// source, when it follows video, fades out and goes off with the ramp, as
// does a channel only a transition the cut takes over had put on.
func (m *Mixer) OnCut(oldSource, newSource string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transCrossfadeActive = true
	m.transCrossfades.Add(1)
	m.transCrossfadeFrom = oldSource
	m.transCrossfadeTo = newSource
	m.transCrossfadeMode = Crossfade

	// Place the new source's ring again at the next read. That read drops
	// what the ring holds from before the read point, so audio buffered
	// while the source was off program is not played; what it holds for
	// the read point and after is kept, as a source writing ahead of its
	// media time has delivered it already.
	if ch, ok := m.channels[newSource]; ok {
		ch.placed = false
		ch.active = true
		m.initChannelDecoder(ch)
	}
	delete(m.afvDeactivateOnComplete, newSource)
	if ch, ok := m.channels[oldSource]; ok && ch.afv && ch.active && oldSource != newSource {
		m.deactivateOnCompleteLocked(oldSource)
	}
	// The cut put the new source on program, so a later OnTransitionAbort
	// must not roll back its activation; a source the transition put on
	// that is not the new one goes off.
	if key := m.transAbortActivated; key != "" && key != newSource {
		if ch, ok := m.channels[key]; ok && ch.active {
			m.deactivateOnCompleteLocked(key)
		}
	}
	m.transAbortActivated = ""
	m.startCutRampLocked()
}

// deactivateQueuedLocked switches off every channel queued to go off,
// except the one on program, and empties the queue. Caller must hold m.mu.
func (m *Mixer) deactivateQueuedLocked() {
	for key := range m.afvDeactivateOnComplete {
		if key == m.programAFVSource {
			continue
		}
		if ch, ok := m.channels[key]; ok {
			ch.active = false
		}
	}
	m.afvDeactivateOnComplete = nil
}

// deactivateOnCompleteLocked queues a channel to go off when the cut ramp
// completes. Caller must hold m.mu.
func (m *Mixer) deactivateOnCompleteLocked(sourceKey string) {
	if m.afvDeactivateOnComplete == nil {
		m.afvDeactivateOnComplete = make(map[string]struct{})
	}
	m.afvDeactivateOnComplete[sourceKey] = struct{}{}
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
	// A cut ramp still running ends here: the transition takes every
	// channel on from the gain it aired, and a channel the ramp was taking
	// off keeps fading under the transition and goes off when it ends.
	m.cutFramesRemaining = 0
	m.cutTotalFrames = 0
	m.cutSpans = nil
	m.mixStarts = nil

	// Activate the incoming source's channel, placed again at the next
	// read as on a cut.
	m.transAbortActivated = ""
	if ch, ok := m.channels[newSource]; ok {
		ch.placed = false
		_, queued := m.afvDeactivateOnComplete[newSource]
		if !ch.active || queued {
			// Remember that this transition is what activated the channel
			// or kept it on, so OnTransitionAbort can roll that back.
			m.transAbortActivated = newSource
		}
		delete(m.afvDeactivateOnComplete, newSource)
		ch.active = true
		// Pre-warm the decoder so the first frame from this source
		// doesn't produce warmup transients in the mix output.
		m.initChannelDecoder(ch)
	}
	m.mixStarts = make(map[string]float64, len(m.channels))
	for key, ch := range m.channels {
		if ch.active {
			m.mixStarts[key] = ch.xfGain
		}
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
// Called by the switcher when the video transition finishes. The
// transition's new source is on program from here: an outgoing source that
// follows video, faded out, goes off, and the program change the switcher
// sends after this finds nothing left to crossfade. A fade to black mutes
// the program.
func (m *Mixer) OnTransitionComplete() {
	m.mu.Lock()
	// A completed fade to black leaves the program silent, so it is muted
	// here, in the same step: muted only by the switcher's SetProgramMute
	// after this, a period produced between the two would air every
	// channel at full gain again.
	if m.transCrossfadeMode == FadeOut && m.transCrossfadeActive {
		m.setProgramMuteLocked(true)
	}
	if to := m.transCrossfadeTo; to != "" {
		if from, ok := m.channels[m.transCrossfadeFrom]; ok && from.afv && m.transCrossfadeFrom != to {
			from.active = false
			if m.programAFVSource == m.transCrossfadeFrom {
				m.programAFVSource = ""
			}
		}
		if ch, ok := m.channels[to]; ok && ch.afv {
			m.programAFVSource = to
		}
	}
	m.deactivateQueuedLocked()
	m.transCrossfadeActive = false
	m.transCrossfadeFrom = ""
	m.transCrossfadeTo = ""
	m.transCrossfadePosition = 0.0
	m.transCrossfadeMode = 0
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 0.0
	m.cutFramesRemaining = 0
	m.cutTotalFrames = 0
	m.cutSpans = nil
	m.mixStarts = nil
	m.stingerAudio = nil
	m.stingerOffset = 0
	m.stingerChannels = 0
	m.transAbortActivated = "" // the incoming source is on program now -- keep it active
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
	defer m.mu.Unlock()
	if uint64(m.transCrossfades.Load()) != token {
		// Stale abort: the transition state currently in the mixer was
		// established by a newer owner. Leave it untouched.
		return
	}
	// The incoming channel goes off if (and only if) OnTransitionStart was
	// what activated it or kept it on. The transition never completed, so
	// leaving it active would mix the preview source into program at full
	// fader gain forever: the switcher's abort path skips OnProgramChange,
	// which is otherwise the only thing that deactivates it. Never the
	// current program AFV source (e.g. a cut landed on that source
	// mid-transition).
	from, to := m.transCrossfadeFrom, m.transCrossfadeTo
	if key := m.transAbortActivated; key != "" && key != m.programAFVSource {
		if _, ok := m.channels[key]; ok {
			m.deactivateOnCompleteLocked(key)
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
	m.cutSpans = nil
	m.mixStarts = nil
	m.stingerAudio = nil
	m.stingerOffset = 0
	m.stingerChannels = 0

	// Pulled back before any gain moved, as a T-bar pulled back to 0 at
	// once leaves it, the channels are where the abort puts them: the
	// incoming one goes off and the rest stay on. Otherwise a cut ramp
	// takes each channel on from the gain it aired, so none steps.
	if m.cutTargetsAiredLocked() {
		m.deactivateQueuedLocked()
		return
	}
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = to
	m.transCrossfadeTo = from
	m.transCrossfadeMode = Crossfade
	m.startCutRampLocked()
}

// cutTargetsAiredLocked reports whether every active channel that has
// aired since it was activated aired, at the end of the last period, the
// gain a cut ramp would take it to. Caller must hold m.mu.
func (m *Mixer) cutTargetsAiredLocked() bool {
	for key, ch := range m.channels {
		if ch.active && ch.aired && math.Abs(ch.xfGain-m.cutTargetLocked(key)) > 1e-9 {
			return false
		}
	}
	return true
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
