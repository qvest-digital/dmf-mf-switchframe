package switcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/transition"
)

// Integration tests verifying that audio crossfade timing aligns with video
// transition progress. The existing integration_test.go verifies that the
// mixer enters transition crossfade on StartTransition; these tests verify
// the actual position tracking and the distinction between Cut and Dissolve
// audio behavior.

func TestIntegration_DissolveAudioMatchesVideo(t *testing.T) {
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { programRelay.BroadcastAudio(frame) },
	})
	defer func() { _ = mixer.Close() }()

	sw.SetMixer(mixer)
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	// Register two sources with AFV.
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	mixer.AddChannel("cam1")
	mixer.AddChannel("cam2")
	_ = mixer.SetAFV("cam1", true)
	_ = mixer.SetAFV("cam2", true)

	// Cut to cam1 to establish the program source.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Verify initial state: cam1 active, no transition crossfade.
	require.True(t, mixer.IsChannelActive("cam1"))
	require.False(t, mixer.IsInTransitionCrossfade())

	// Start a 5-second dissolve to cam2.
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))
	require.True(t, mixer.IsInTransitionCrossfade(),
		"audio should enter transition crossfade on dissolve start")
	require.InDelta(t, 0.0, mixer.TransitionPosition(), 0.01,
		"audio transition position should start at 0.0")

	// Advance the T-bar position to 0.3.
	_ = sw.SetTransitionPosition(context.Background(), 0.3)
	require.InDelta(t, 0.3, mixer.TransitionPosition(), 0.01,
		"audio transition position should track video at 0.3")

	// Verify the transition gains reflect 0.3 position (equal-power crossfade).
	oldGain, newGain := mixer.TransitionGains()
	require.Greater(t, oldGain, 0.0, "old source gain should be positive at 0.3")
	require.Greater(t, newGain, 0.0, "new source gain should be positive at 0.3")
	require.Greater(t, oldGain, newGain,
		"at 0.3, old source should be louder than new source")

	// Advance to 0.5 (midpoint).
	_ = sw.SetTransitionPosition(context.Background(), 0.5)
	require.InDelta(t, 0.5, mixer.TransitionPosition(), 0.01,
		"audio transition position should track video at 0.5")
	oldGain, newGain = mixer.TransitionGains()
	// At midpoint of equal-power crossfade, both gains should be approximately equal.
	require.InDelta(t, oldGain, newGain, 0.1,
		"at midpoint, old and new gains should be approximately equal")

	// Advance to 0.8.
	_ = sw.SetTransitionPosition(context.Background(), 0.8)
	require.InDelta(t, 0.8, mixer.TransitionPosition(), 0.01,
		"audio transition position should track video at 0.8")
	oldGain, newGain = mixer.TransitionGains()
	require.Less(t, oldGain, newGain,
		"at 0.8, new source should be louder than old source")

	// Clean up: abort the transition.
	sw.AbortTransition()
	require.False(t, mixer.IsInTransitionCrossfade(),
		"audio transition crossfade should be cleared after abort")
}

func TestIntegration_CutCrossfadeDuration(t *testing.T) {
	// Verify that a plain Cut uses the one-frame crossfade (OnCut path)
	// rather than the multi-frame transition crossfade used by dissolves.
	programRelay := newTestRelay()

	sw := New(programRelay)
	defer sw.Close()

	// Track audio frames sent through the mixer.
	var mu sync.Mutex
	var mixerFrames []struct {
		key   string
		frame *media.AudioFrame
	}

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			programRelay.BroadcastAudio(frame)
		},
	})
	defer func() { _ = mixer.Close() }()

	sw.SetMixer(mixer)
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mu.Lock()
		mixerFrames = append(mixerFrames, struct {
			key   string
			frame *media.AudioFrame
		}{sourceKey, frame})
		mu.Unlock()
		mixer.IngestFrame(sourceKey, frame)
	})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	mixer.AddChannel("cam1")
	mixer.AddChannel("cam2")
	_ = mixer.SetAFV("cam1", true)
	_ = mixer.SetAFV("cam2", true)

	// Cut to cam1.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Before the second cut, verify no transition crossfade.
	require.False(t, mixer.IsInTransitionCrossfade(),
		"should not be in transition crossfade before any dissolve")

	// Cut to cam2 — this should trigger OnCut (one-frame crossfade),
	// NOT OnTransitionStart (multi-frame crossfade).
	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	// The transition crossfade should NOT be active (Cut uses OnCut, not OnTransitionStart).
	require.False(t, mixer.IsInTransitionCrossfade(),
		"a plain Cut should NOT enter multi-frame transition crossfade")

	// The transition position should still be 0.0 (no transition started).
	require.Equal(t, 0.0, mixer.TransitionPosition(),
		"transition position should be 0.0 after a plain Cut")

	// Verify the state after cut: cam2 is now on program, cam1 is preview.
	state := sw.State()
	require.Equal(t, "cam2", state.ProgramSource)
	require.Equal(t, "cam1", state.PreviewSource)
}

func TestIntegration_DissolveCompletionClearsAudioTransition(t *testing.T) {
	// Verify that when a dissolve completes (position reaches 1.0),
	// the audio transition state is properly cleaned up.
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { programRelay.BroadcastAudio(frame) },
	})
	defer func() { _ = mixer.Close() }()

	sw.SetMixer(mixer)
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	mixer.AddChannel("cam1")
	mixer.AddChannel("cam2")
	_ = mixer.SetAFV("cam1", true)
	_ = mixer.SetAFV("cam2", true)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Start a very short dissolve (50ms).
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 50, ""))
	require.True(t, mixer.IsInTransitionCrossfade())

	// Feed frames to drive the transition to completion.
	for i := 0; i < 30; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS: int64(100 + i*33), IsKeyframe: i == 0, WireData: []byte{0x01},
		})
		cam2Relay.BroadcastVideo(&media.VideoFrame{
			PTS: int64(200 + i*33), IsKeyframe: i == 0, WireData: []byte{0x02},
		})
		time.Sleep(10 * time.Millisecond)
		if !sw.State().InTransition {
			break
		}
	}

	// After completion, audio transition crossfade should be cleared.
	require.Eventually(t, func() bool {
		return !mixer.IsInTransitionCrossfade()
	}, 2*time.Second, 50*time.Millisecond,
		"audio transition crossfade should be cleared after dissolve completes")

	// The transition position should be reset to 0.0.
	require.Equal(t, 0.0, mixer.TransitionPosition(),
		"transition position should be reset after completion")

	// cam2 should now be on program.
	state := sw.State()
	require.Equal(t, "cam2", state.ProgramSource)
}

func TestIntegration_DissolveAudioPositionMonotonic(t *testing.T) {
	// Verify that as the T-bar moves forward, the audio transition position
	// increases monotonically.
	programRelay := newTestRelay()

	sw := New(programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = mixer.Close() }()

	sw.SetMixer(mixer)
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	mixer.AddChannel("cam1")
	mixer.AddChannel("cam2")
	_ = mixer.SetAFV("cam1", true)
	_ = mixer.SetAFV("cam2", true)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))

	// Drive position from 0.0 to 1.0 in steps.
	positions := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9}
	prevPos := 0.0
	for _, pos := range positions {
		_ = sw.SetTransitionPosition(context.Background(), pos)
		currentPos := mixer.TransitionPosition()
		require.GreaterOrEqual(t, currentPos, prevPos,
			"audio position should increase monotonically: prev=%f current=%f", prevPos, currentPos)
		require.InDelta(t, pos, currentPos, 0.01,
			"audio position should closely match video position")
		prevPos = currentPos
	}

	sw.AbortTransition()
}
