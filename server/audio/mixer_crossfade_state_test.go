package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// newCrossfadeStateMixer creates a mixer whose tests control crossfade
// advancement via AdvanceCrossfade().
func newCrossfadeStateMixer(t *testing.T) *Mixer {
	t.Helper()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestOnTransitionAbort_DeactivatesIncomingChannel verifies that aborting a
// transition (e.g. T-bar pulled back to 0) rolls back the incoming channel
// activation done by OnTransitionStart. Without the rollback, the preview
// source's channel stays active=true forever and its audio leaks into program
// at full fader gain (no crossfade gain applies once transCrossfadeActive is
// false).
func TestOnTransitionAbort_DeactivatesIncomingChannel(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	token := m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	require.True(t, m.IsChannelActive("cam2"), "incoming channel activates for the transition")

	m.OnTransitionAbort(token)

	require.False(t, m.IsChannelActive("cam2"),
		"aborted transition must deactivate the incoming (preview) channel")
	require.True(t, m.IsChannelActive("cam1"),
		"original program channel must stay active after abort")

	// And the preview source's audio must not reach the program mix anymore.
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	tl := newTimeline()
	tl.push(m, "cam2", pcm, 0)

	mixed := tl.produce(m, 1024)
	for i, s := range mixed {
		require.Equal(t, float32(0), s,
			"sample %d: preview audio leaked into program after aborted transition", i)
	}
}

// TestOnTransitionAbort_KeepsAlreadyActiveIncomingChannel verifies the abort
// rollback only undoes the activation OnTransitionStart itself performed. A
// channel that was already active before the transition (e.g. a manually
// activated music bed) must stay active after an abort.
func TestOnTransitionAbort_KeepsAlreadyActiveIncomingChannel(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("music")
	m.SetActive("cam1", true)
	m.SetActive("music", true) // active before the transition

	token := m.OnTransitionStart("cam1", "music", Crossfade, 1000)
	m.OnTransitionAbort(token)

	require.True(t, m.IsChannelActive("music"),
		"channel that was active before the transition must stay active after abort")
}

// TestOnTransitionComplete_KeepsIncomingChannelActive verifies completion does
// not roll back the incoming activation — the new source is now on program.
func TestOnTransitionComplete_KeepsIncomingChannelActive(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionComplete()

	require.True(t, m.IsChannelActive("cam2"),
		"incoming channel stays active after the transition completes")
}

// TestOnTransitionAbort_AfterComplete_DoesNotDeactivate verifies a stray abort
// arriving after the transition already completed does not deactivate the now
// on-program channel.
func TestOnTransitionAbort_AfterComplete_DoesNotDeactivate(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	token := m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionComplete()
	m.OnTransitionAbort(token)

	require.True(t, m.IsChannelActive("cam2"),
		"abort after complete must not deactivate the on-program channel")
}

// TestMixerAFVBounceBackKeepsProgramActive reproduces the rapid bounce-back
// bug: program changes A→B→A within the 4-tick crossfade window. The first
// change queues A for deferred deactivation; the second change re-activates A
// but (before the fix) left it in the deferred set, so completing the
// crossfade muted the CURRENT program's AFV audio.
func TestMixerAFVBounceBackKeepsProgramActive(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("camA")
	m.AddChannel("camB")
	require.NoError(t, m.SetAFV("camA", true))
	require.NoError(t, m.SetAFV("camB", true))

	// Initial program: camA.
	m.OnProgramChange("camA")
	require.True(t, m.IsChannelActive("camA"))

	// Rapid bounce-back within the crossfade window (no ticks in between).
	m.OnProgramChange("camB")
	m.OnProgramChange("camA")

	// Drive the crossfade to completion.
	for i := 0; i < 6; i++ {
		m.AdvanceCrossfade()
	}

	require.True(t, m.IsChannelActive("camA"),
		"the current program's AFV channel must stay active after the crossfade completes")
	require.False(t, m.IsChannelActive("camB"),
		"the bounced-away AFV channel deactivates after the crossfade completes")
}

// TestMixerAFVCutReactivationNotMutedOnComplete is defense-in-depth for the
// same class of bug: even if a channel queued for deferred deactivation is
// re-activated via OnCut (without an OnProgramChange pruning the set), the
// completion handler must never deactivate the channel currently tracked as
// the program AFV source.
func TestMixerAFVCutReactivationNotMutedOnComplete(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("camA")
	m.AddChannel("camB")
	require.NoError(t, m.SetAFV("camA", true))
	require.NoError(t, m.SetAFV("camB", true))

	m.OnProgramChange("camA")
	m.OnProgramChange("camB") // queues camA for deferred deactivation

	// Simulate camA being placed back on program. Force the deferred set to
	// still contain camA (as OnCut alone would leave it).
	m.OnCut("camB", "camA")
	m.mu.Lock()
	if m.afvDeactivateOnComplete == nil {
		m.afvDeactivateOnComplete = make(map[string]struct{})
	}
	m.afvDeactivateOnComplete["camA"] = struct{}{}
	m.programAFVSource = "camA"
	m.mu.Unlock()

	for i := 0; i < 6; i++ {
		m.AdvanceCrossfade()
	}

	require.True(t, m.IsChannelActive("camA"),
		"completion must never deactivate the current program AFV source")
}

// TestOnTransitionAbort_StaleAfterCut_PreservesCutState reproduces the
// pre-empted-transition race: StartTransition begins (OnTransitionStart),
// a concurrent Cut wins the switcher state and installs its own crossfade
// (OnCut + OnProgramChange, queuing the old program AFV channel for deferred
// deactivation), and only then does the abandoned transition's cleanup call
// OnTransitionAbort with its now-stale token. The stale abort must be a
// no-op: wiping cutFramesRemaining/afvDeactivateOnComplete would snap the
// cut's crossfade mid-ramp and leave the old program AFV channel mixing
// forever (double audio).
func TestOnTransitionAbort_StaleAfterCut_PreservesCutState(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("camA")
	m.AddChannel("camB")
	m.AddChannel("camC")
	require.NoError(t, m.SetAFV("camA", true))
	require.NoError(t, m.SetAFV("camB", true))

	// camA is the on-air program with an active AFV channel.
	m.OnProgramChange("camA")
	require.True(t, m.IsChannelActive("camA"))

	// A transition toward camC starts its (unlocked) setup window...
	staleToken := m.OnTransitionStart("camA", "camC", Crossfade, 1000)

	// ...but a concurrent Cut to camB wins and establishes its own crossfade.
	m.OnCut("camA", "camB")
	m.OnProgramChange("camB")

	m.mu.Lock()
	require.Contains(t, m.afvDeactivateOnComplete, "camA",
		"sanity: the cut queues the old program AFV channel for deferred deactivation")
	m.mu.Unlock()

	// The abandoned transition's late cleanup must not wipe the cut's state.
	m.OnTransitionAbort(staleToken)

	m.mu.Lock()
	require.True(t, m.transCrossfadeActive,
		"stale abort must not snap the cut's crossfade mid-ramp")
	require.Equal(t, "camB", m.transCrossfadeTo,
		"the cut's crossfade target must be preserved")
	require.Equal(t, 4, m.cutFramesRemaining,
		"the cut's tick-driven ramp must keep running")
	require.Contains(t, m.afvDeactivateOnComplete, "camA",
		"the cut's deferred AFV deactivation must be preserved")
	m.mu.Unlock()

	// Drive the cut crossfade to completion: camA must deactivate so the old
	// program audio does not keep mixing into program (double audio).
	for i := 0; i < 6; i++ {
		m.AdvanceCrossfade()
	}
	require.False(t, m.IsChannelActive("camA"),
		"old program AFV channel must deactivate when the cut crossfade completes")
	require.True(t, m.IsChannelActive("camB"),
		"new program AFV channel stays active")
}

// TestOnTransitionAbort_StaleAfterNewerStart_PreservesNewTransition verifies
// that when a newer transition (T2) established its own mixer state during
// the abandoned transition's (T1) setup window, T1's late abort is a no-op:
// it must not deactivate T2's just-activated incoming channel or wipe T2's
// crossfade. The abort with T2's own (current) token must still clear state.
func TestOnTransitionAbort_StaleAfterNewerStart_PreservesNewTransition(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.AddChannel("cam3")
	m.SetActive("cam1", true)

	token1 := m.OnTransitionStart("cam1", "cam2", Crossfade, 1000) // T1, abandoned
	token2 := m.OnTransitionStart("cam1", "cam3", Crossfade, 1000) // T2, live
	require.NotEqual(t, token1, token2, "each transition start gets its own token")
	require.True(t, m.IsChannelActive("cam3"), "T2 activates its incoming channel")

	// T1's late cleanup must not touch T2's state.
	m.OnTransitionAbort(token1)

	require.True(t, m.IsChannelActive("cam3"),
		"stale abort must not deactivate T2's just-activated incoming channel")
	m.mu.Lock()
	require.True(t, m.transCrossfadeActive, "T2's crossfade must stay active")
	require.Equal(t, "cam3", m.transCrossfadeTo, "T2's crossfade target must be preserved")
	m.mu.Unlock()

	// A current abort (T2's own token) still clears the transition state.
	m.OnTransitionAbort(token2)

	require.False(t, m.IsChannelActive("cam3"),
		"current abort rolls back the activation T2 performed")
	m.mu.Lock()
	require.False(t, m.transCrossfadeActive, "current abort clears the crossfade")
	require.Equal(t, "", m.transCrossfadeTo)
	require.Equal(t, 0, m.cutFramesRemaining)
	m.mu.Unlock()
}

// TestOnTransitionAbort_CurrentTokenClearsState verifies the normal abort
// path (e.g. T-bar pulled back) is unchanged by the token gating: an abort
// carrying the token of the transition that owns the mixer state fully
// clears it.
func TestOnTransitionAbort_CurrentTokenClearsState(t *testing.T) {
	t.Parallel()
	m := newCrossfadeStateMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	token := m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)

	m.OnTransitionAbort(token)

	m.mu.Lock()
	require.False(t, m.transCrossfadeActive, "current abort clears the crossfade")
	require.Equal(t, "", m.transCrossfadeFrom)
	require.Equal(t, "", m.transCrossfadeTo)
	require.Equal(t, 0.0, m.transCrossfadePosition, "position snaps back to 0")
	require.Equal(t, 0, m.cutFramesRemaining)
	require.Nil(t, m.afvDeactivateOnComplete)
	m.mu.Unlock()
	require.False(t, m.IsChannelActive("cam2"),
		"current abort rolls back the incoming channel activation")
}
