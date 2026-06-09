package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// newCrossfadeStateMixer creates a clock-driven mixer (no background ticker)
// so tests fully control crossfade advancement via AdvanceCrossfade().
func newCrossfadeStateMixer(t *testing.T) *Mixer {
	t.Helper()
	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output:      func(*media.AudioFrame) {},
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

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	require.True(t, m.IsChannelActive("cam2"), "incoming channel activates for the transition")

	m.OnTransitionAbort()

	require.False(t, m.IsChannelActive("cam2"),
		"aborted transition must deactivate the incoming (preview) channel")
	require.True(t, m.IsChannelActive("cam1"),
		"original program channel must stay active after abort")

	// And the preview source's audio must not reach the program mix anymore.
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	m.mu.Lock()
	m.channels["cam2"].ringBuf.Push(pcm)
	m.mu.Unlock()

	mixed := m.ProduceSamples(1024)
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

	m.OnTransitionStart("cam1", "music", Crossfade, 1000)
	m.OnTransitionAbort()

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

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionComplete()
	m.OnTransitionAbort()

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
