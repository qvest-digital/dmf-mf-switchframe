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

func TestIntegrationCutSwitchesFrames(t *testing.T) {
	// Setup: program relay with a capture viewer.
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	// Create switcher.
	sw := New(programRelay)

	// Track state changes.
	var states []internal.ControlRoomState
	var statesMu sync.Mutex
	sw.OnStateChange(func(state internal.ControlRoomState) {
		statesMu.Lock()
		states = append(states, state)
		statesMu.Unlock()
	})

	// Create two source relays.
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	// Cut to camera1.
	require.NoError(t, sw.Cut(context.Background(), "camera1"))

	// Send video frames from both cameras.
	cam1Frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true, WireData: []byte{0x01}}
	cam1Relay.BroadcastVideo(cam1Frame)

	cam2Frame := &media.VideoFrame{PTS: 2000, IsKeyframe: true, WireData: []byte{0x02}}
	cam2Relay.BroadcastVideo(cam2Frame)

	// Only camera1 frame should arrive at program (path is synchronous).
	capture.mu.Lock()
	require.Equal(t, 1, len(capture.videos), "program frame count")
	require.Equal(t, int64(1000), capture.videos[0].PTS)
	capture.mu.Unlock()

	// Now cut to camera2.
	require.NoError(t, sw.Cut(context.Background(), "camera2"))

	// Send another frame from camera2 (now on program).
	cam2Frame2 := &media.VideoFrame{PTS: 3000, IsKeyframe: true, WireData: []byte{0x03}}
	cam2Relay.BroadcastVideo(cam2Frame2)

	// Send frame from camera1 (no longer on program).
	cam1Frame2 := &media.VideoFrame{PTS: 4000, IsKeyframe: true, WireData: []byte{0x04}}
	cam1Relay.BroadcastVideo(cam1Frame2)

	capture.mu.Lock()
	require.Equal(t, 2, len(capture.videos), "program frame count after cut")
	require.Equal(t, int64(3000), capture.videos[1].PTS)
	capture.mu.Unlock()

	// Verify state changes were published (at least 2: Cut(camera1) + Cut(camera2)).
	statesMu.Lock()
	require.GreaterOrEqual(t, len(states), 2, "state change count")
	statesMu.Unlock()
}

func TestIntegrationAudioFollowsVideo(t *testing.T) {
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)

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

func TestProgramRelayFromPrismServer(t *testing.T) {
	// Simulate the pattern used in restructured main.go:
	// Get a relay (as RegisterStream would return), create switcher with it.
	programRelay := newTestRelay()
	capture := newMockProgramViewer("moq-viewer")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send keyframe — should flow through switcher to program relay viewer.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 1000, IsKeyframe: true, WireData: []byte{0x01}})

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.videos), "frame should reach MoQ viewer via program relay")
	capture.mu.Unlock()
}

func TestIntegrationUnregisterStopsForwarding(t *testing.T) {
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)

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
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
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
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	// Create a mixer that outputs to program relay.
	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			programRelay.BroadcastAudio(frame)
		},
	})
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

	// Mixer should be in passthrough (single active source at 0dB).
	require.True(t, mixer.IsPassthrough())

	// Send audio — should pass through to program relay.
	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.audios), "audio should reach program via passthrough")
	require.Equal(t, int64(100), capture.audios[0].PTS)
	capture.mu.Unlock()
}

func TestIntegrationMixerAFVOnCut(t *testing.T) {
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			programRelay.BroadcastAudio(frame)
		},
	})
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

	// Send audio from cam1 (active) — should arrive.
	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.audios))
	capture.mu.Unlock()

	// Cut to cam2 — cam2 activates, cam1 deactivates (auto-wired).
	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 150, IsKeyframe: true})

	require.True(t, mixer.IsChannelActive("cam2"))
	require.False(t, mixer.IsChannelActive("cam1"))
}

func TestIntegrationStateBroadcastIncludesAudio(t *testing.T) {
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
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	mixer := audio.NewMixer(audio.MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { programRelay.BroadcastAudio(frame) },
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
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(mockTransitionCodecs())
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Clear IDR gate
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Start dissolve
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 5000, ""))

	// Feed frames from both sources
	for i := 0; i < 5; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(100 + i*33), IsKeyframe: i == 0, WireData: []byte{0x01}})
		cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(200 + i*33), IsKeyframe: i == 0, WireData: []byte{0x02}})
	}

	time.Sleep(20 * time.Millisecond)

	capture.mu.Lock()
	blendedCount := len(capture.videos)
	capture.mu.Unlock()

	// Should have received blended frames on program relay
	require.GreaterOrEqual(t, blendedCount, 3, "should have blended output frames")

	sw.AbortTransition()
}

func TestIntegrationDissolveCompletesAndResumesReEncode(t *testing.T) {
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Short transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 50, ""))

	// Feed frames until completion
	for i := 0; i < 30; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(100 + i*33), IsKeyframe: i == 0, WireData: []byte{0x01}})
		cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(200 + i*33), IsKeyframe: i == 0, WireData: []byte{0x02}})
		time.Sleep(10 * time.Millisecond)
		if !sw.State().InTransition {
			break
		}
	}

	// Verify completion
	state := sw.State()
	require.False(t, state.InTransition)
	require.Equal(t, "cam2", state.ProgramSource)

	// Clear count and verify re-encode resumes (not passthrough)
	capture.mu.Lock()
	capture.videos = nil
	capture.mu.Unlock()

	// After transition, cam2 frames are still re-encoded through the pipeline
	originalFrame := &media.VideoFrame{PTS: 9999, IsKeyframe: true, WireData: []byte{0xFF, 0xFE}}
	cam2Relay.BroadcastVideo(originalFrame)

	time.Sleep(10 * time.Millisecond)

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.videos))
	// Always re-encode: frame was received with correct PTS
	require.Equal(t, originalFrame.PTS, capture.videos[0].PTS,
		"after transition, frames should be re-encoded with preserved PTS")
	capture.mu.Unlock()
}

func TestIntegrationTBarPartialAbort(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)

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
	programRelay := newTestRelay()
	capture := newMockProgramViewer("capture")
	programRelay.AddViewer(capture)

	sw := New(programRelay)
	defer sw.Close()

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	require.NoError(t, sw.FadeToBlack(context.Background()))

	// Feed frames from program source
	for i := 0; i < 5; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: int64(100 + i*33), IsKeyframe: i == 0, WireData: []byte{0x01}})
	}

	time.Sleep(20 * time.Millisecond)

	capture.mu.Lock()
	outputCount := len(capture.videos)
	capture.mu.Unlock()

	require.GreaterOrEqual(t, outputCount, 1, "FTB should produce output frames")

	sw.AbortTransition()
}

func TestIntegrationFTBMutesAudio(t *testing.T) {
	programRelay := newTestRelay()

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
	programRelay := newTestRelay()

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
