package switcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/transition"
)

// Integration tests verify the full pipeline: source relays → switcher → program relay.
// They exercise RegisterSource, Cut, UnregisterSource, and state-change callbacks
// together, ensuring frames flow end-to-end exactly as expected.

func TestIntegrationAudioFollowsVideo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	require.NoError(t, sw.Cut(context.Background(), "camera1"))

	// Send a keyframe to clear the IDR gate.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	// Send audio from camera1 -- should forward.
	audio1 := &media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	cam1Relay.BroadcastAudio(audio1)

	// Send audio from camera2 -- should drop.
	audio2 := &media.AudioFrame{PTS: 200, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
	cam2Relay.BroadcastAudio(audio2)

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.audios), "audio frame count")
	require.Equal(t, int64(100), capture.audios[0].PTS)
	capture.mu.Unlock()
}

func TestIntegrationUnregisterStopsForwarding(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)

	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	require.NoError(t, sw.Cut(context.Background(), "camera1"))

	// Unregister camera1.
	sw.UnregisterSource("camera1")

	// This frame should not forward (viewer was removed from relay).
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 5000, IsKeyframe: true})

	state := sw.State()
	require.Equal(t, "", state.ProgramSource, "ProgramSource after unregister")

	capture.mu.Lock()
	require.Equal(t, 0, len(capture.videos), "frame count after unregister")
	capture.mu.Unlock()
}

func TestIntegrationAudioWithMixerHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		programRelay.BroadcastAudio(frame)
	})

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.audios))
	capture.mu.Unlock()
}

func TestIntegrationMixerPassthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Create a mixer that outputs to program relay.
	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	driveProgramAudio(sw, mixer, mockAACEncode)
	defer func() { _ = mixer.Close() }()

	// Wire mixer to switcher.
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetMixer(mixer)

	// Register source and add mixer channel.
	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	mixer.AddChannel("cam1")
	require.NoError(t, mixer.SetAFV("cam1", true))

	// Cut to cam1 — AFV activates it (Switcher auto-calls OnProgramChange).
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Clear IDR gate.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	// Clock-driven mixer always mixes (no passthrough mode).
	// Audio output is produced by the ticker, not directly from IngestFrame.
	// Verify the mixer is active by checking that the channel is active.
	require.True(t, mixer.IsChannelActive("cam1"), "cam1 should be active after AFV cut")
}

func TestIntegrationMixerAFVOnCut(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (audio.Decoder, error) {
			return audio.NewFDKDecoder(sampleRate, channels)
		},
	})
	driveProgramAudio(sw, mixer, fdkEncode(t))
	defer func() { _ = mixer.Close() }()

	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mixer.IngestFrame(sourceKey, frame)
	})
	sw.SetMixer(mixer)

	// Register two sources.
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	mixer.AddChannel("cam1")
	mixer.AddChannel("cam2")
	require.NoError(t, mixer.SetAFV("cam1", true))
	require.NoError(t, mixer.SetAFV("cam2", true))

	// Cut to cam1 (Switcher auto-calls OnProgramChange + OnCut).
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	require.True(t, mixer.IsChannelActive("cam1"))
	require.False(t, mixer.IsChannelActive("cam2"))

	// Send audio from cam1 (active); the sync loop mixes and encodes on
	// every tick.
	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	// The lock is not held across a failing assertion: Close waits for the
	// sync loop, which delivers to capture under the same lock.
	require.Eventually(t, func() bool {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return len(capture.audios) >= 1
	}, time.Second, 5*time.Millisecond, "sync loop should have produced at least 1 output frame")

	// Cut to cam2 — cam2 activates, cam1 deactivation is deferred until
	// the cut crossfade ramp completes (4 ticks, ~85ms).
	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 150, IsKeyframe: true})

	require.True(t, mixer.IsChannelActive("cam2"))

	// The cut crossfade ramp completes after 4 ticks.
	require.Eventually(t, func() bool { return !mixer.IsChannelActive("cam1") },
		time.Second, 5*time.Millisecond, "cam1 still active after the cut crossfade")
}

func TestIntegrationStateBroadcastIncludesAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	driveProgramAudio(sw, mixer, mockAACEncode)
	sw.SetOutputAudioCallback(func(*media.AudioFrame) {})
	defer func() { _ = mixer.Close() }()

	sw.SetMixer(mixer)

	// Register source and add mixer channel.
	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	mixer.AddChannel("cam1")
	require.NoError(t, mixer.SetLevel("cam1", -6.0))

	// Track state changes.
	var lastState internal.ControlRoomState
	var statesMu sync.Mutex
	sw.OnStateChange(func(state internal.ControlRoomState) {
		statesMu.Lock()
		lastState = state
		statesMu.Unlock()
	})

	// Trigger a state change.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	statesMu.Lock()
	require.NotNil(t, lastState.AudioChannels)
	require.Contains(t, lastState.AudioChannels, "cam1")
	require.InDelta(t, -6.0, lastState.AudioChannels["cam1"].Level, 0.001)
	statesMu.Unlock()
}

func TestIntegrationTransitionCrossfadeWired(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	driveProgramAudio(sw, mixer, mockAACEncode)
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

	// Start transition — audio should enter transition crossfade
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))
	require.True(t, mixer.IsInTransitionCrossfade())

	// Set position — audio should track
	_ = sw.SetTransitionPosition(context.Background(), 0.5)
	require.InDelta(t, 0.5, mixer.TransitionPosition(), 0.01)

	// Abort — audio should exit crossfade
	sw.AbortTransition()
	require.False(t, mixer.IsInTransitionCrossfade())
}

func TestIntegrationDissolveProducesBlendedOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(mockTransitionCodecs())
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Start dissolve
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))

	// Feed frames from both sources; every tick blends and encodes.
	sendRawFrame(sw, "cam1", 0, true)
	sendRawFrame(sw, "cam2", 0, true)
	require.Eventually(t, func() bool {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return len(capture.videos) >= 3
	}, time.Second, 5*time.Millisecond)

	capture.mu.Lock()
	blendedCount := len(capture.videos)
	capture.mu.Unlock()

	// Should have received blended frames on program relay
	require.GreaterOrEqual(t, blendedCount, 3, "should have blended output frames")

	sw.AbortTransition()
}

func TestIntegrationDissolveCompletesAndResumesReEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Short transition, completed by the ticks.
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 50, ""))
	require.Eventually(t, func() bool { return !sw.State().InTransition }, time.Second, 5*time.Millisecond)

	// Verify completion
	state := sw.State()
	require.False(t, state.InTransition)
	require.Equal(t, "cam2", state.ProgramSource)

	// Clear count and verify re-encode resumes
	capture.mu.Lock()
	capture.videos = nil
	capture.mu.Unlock()

	// After transition, cam2 frames are still re-encoded through the pipeline
	sendRawFrame(sw, "cam2", 0, true)
	require.Eventually(t, func() bool {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return len(capture.videos) >= 1
	}, time.Second, 5*time.Millisecond, "after transition, frames should be re-encoded")
}

func TestIntegrationTBarPartialAbort(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))

	// Move T-bar partway
	_ = sw.SetTransitionPosition(context.Background(), 0.5)
	require.True(t, sw.State().InTransition)

	// Move back to 0 → abort
	_ = sw.SetTransitionPosition(context.Background(), 0.0)

	time.Sleep(20 * time.Millisecond)

	// Should be back to idle with cam1 still on program
	state := sw.State()
	require.False(t, state.InTransition)
	require.Equal(t, "cam1", state.ProgramSource, "abort should not swap sources")
}

func TestIntegrationFTBProducesBlackFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.FadeToBlack(context.Background()))

	// Feed a frame from the program source; every tick fades it.
	sendRawFrame(sw, "cam1", 0, true)
	require.Eventually(t, func() bool {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return len(capture.videos) >= 1
	}, time.Second, 5*time.Millisecond)

	capture.mu.Lock()
	outputCount := len(capture.videos)
	capture.mu.Unlock()

	require.GreaterOrEqual(t, outputCount, 1, "FTB should produce output frames")

	sw.AbortTransition()
}

func TestIntegrationFTBMutesAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	driveProgramAudio(sw, mixer, mockAACEncode)
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
	sw.RegisterSource("cam1", cam1Relay)
	mixer.AddChannel("cam1")
	_ = mixer.SetAFV("cam1", true)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Before FTB: audio should NOT be program-muted
	require.False(t, mixer.IsProgramMuted(), "audio should not be muted before FTB")

	// Start FTB — audio enters transition crossfade (fade out)
	require.NoError(t, sw.FadeToBlack(context.Background()))
	require.True(t, mixer.IsInTransitionCrossfade(), "audio should be in transition during FTB")

	// Feed a keyframe so the encoder initializes, then drive position to 1.0 via T-bar
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 133, IsKeyframe: false, WireData: []byte{0x01}})
	time.Sleep(10 * time.Millisecond)
	_ = sw.SetTransitionPosition(context.Background(), 1.0) // triggers completion
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 166, IsKeyframe: false, WireData: []byte{0x01}})
	time.Sleep(30 * time.Millisecond)

	// After FTB completes: audio should be program-muted (screen is black)
	require.True(t, mixer.IsProgramMuted(), "audio should be muted after FTB completes")
	require.False(t, mixer.IsInTransitionCrossfade(), "transition should be complete")
}

func TestIntegrationFTBReverseFadesIn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	programRelay := newTestRelay()

	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	driveProgramAudio(sw, mixer, mockAACEncode)
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
	sw.RegisterSource("cam1", cam1Relay)
	mixer.AddChannel("cam1")
	_ = mixer.SetAFV("cam1", true)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Complete FTB first using T-bar
	require.NoError(t, sw.FadeToBlack(context.Background()))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 133, IsKeyframe: false, WireData: []byte{0x01}})
	time.Sleep(10 * time.Millisecond)
	_ = sw.SetTransitionPosition(context.Background(), 1.0)
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 166, IsKeyframe: false, WireData: []byte{0x01}})
	time.Sleep(30 * time.Millisecond)
	require.True(t, mixer.IsProgramMuted(), "audio should be muted after FTB")

	// Start FTB reverse — audio should unmute and enter fade-in transition
	require.NoError(t, sw.FadeToBlack(context.Background()))
	require.False(t, mixer.IsProgramMuted(), "audio should be unmuted during FTB reverse")
	require.True(t, mixer.IsInTransitionCrossfade(), "audio should be in transition during FTB reverse")
}
