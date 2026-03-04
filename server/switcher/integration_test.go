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
	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut(camera1): %v", err)
	}

	// Send video frames from both cameras.
	cam1Frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true, WireData: []byte{0x01}}
	cam1Relay.BroadcastVideo(cam1Frame)

	cam2Frame := &media.VideoFrame{PTS: 2000, IsKeyframe: true, WireData: []byte{0x02}}
	cam2Relay.BroadcastVideo(cam2Frame)

	// Give time for any asynchronous delivery (currently synchronous, but
	// this guards against future changes).
	time.Sleep(10 * time.Millisecond)

	// Only camera1 frame should arrive at program.
	capture.mu.Lock()
	if len(capture.videos) != 1 {
		t.Fatalf("got %d program frames, want 1", len(capture.videos))
	}
	if capture.videos[0].PTS != 1000 {
		t.Errorf("program frame PTS = %d, want 1000", capture.videos[0].PTS)
	}
	capture.mu.Unlock()

	// Now cut to camera2.
	if err := sw.Cut(context.Background(), "camera2"); err != nil {
		t.Fatalf("Cut(camera2): %v", err)
	}

	// Send another frame from camera2 (now on program).
	cam2Frame2 := &media.VideoFrame{PTS: 3000, IsKeyframe: true, WireData: []byte{0x03}}
	cam2Relay.BroadcastVideo(cam2Frame2)

	// Send frame from camera1 (no longer on program).
	cam1Frame2 := &media.VideoFrame{PTS: 4000, IsKeyframe: true, WireData: []byte{0x04}}
	cam1Relay.BroadcastVideo(cam1Frame2)

	time.Sleep(10 * time.Millisecond)

	capture.mu.Lock()
	if len(capture.videos) != 2 {
		t.Fatalf("got %d program frames after cut, want 2", len(capture.videos))
	}
	if capture.videos[1].PTS != 3000 {
		t.Errorf("second program frame PTS = %d, want 3000", capture.videos[1].PTS)
	}
	capture.mu.Unlock()

	// Verify state changes were published (at least 2: Cut(camera1) + Cut(camera2)).
	statesMu.Lock()
	if len(states) < 2 {
		t.Errorf("got %d state changes, want at least 2", len(states))
	}
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

	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut(camera1): %v", err)
	}

	// Send a keyframe to clear the IDR gate.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	// Send audio from camera1 -- should forward.
	audio1 := &media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	cam1Relay.BroadcastAudio(audio1)

	// Send audio from camera2 -- should drop.
	audio2 := &media.AudioFrame{PTS: 200, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
	cam2Relay.BroadcastAudio(audio2)

	time.Sleep(10 * time.Millisecond)

	capture.mu.Lock()
	if len(capture.audios) != 1 {
		t.Fatalf("got %d audio frames, want 1", len(capture.audios))
	}
	if capture.audios[0].PTS != 100 {
		t.Errorf("audio PTS = %d, want 100", capture.audios[0].PTS)
	}
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
	time.Sleep(10 * time.Millisecond)

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

	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut(camera1): %v", err)
	}

	// Unregister camera1.
	sw.UnregisterSource("camera1")

	// This frame should not forward (viewer was removed from relay).
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 5000, IsKeyframe: true})
	time.Sleep(10 * time.Millisecond)

	state := sw.State()
	if state.ProgramSource != "" {
		t.Errorf("ProgramSource = %q after unregister, want empty", state.ProgramSource)
	}

	capture.mu.Lock()
	if len(capture.videos) != 0 {
		t.Errorf("got %d frames after unregister, want 0", len(capture.videos))
	}
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
	time.Sleep(10 * time.Millisecond)

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
	defer mixer.Close()

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

	// Cut to cam1 — AFV activates it.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	mixer.OnProgramChange("cam1")

	// Clear IDR gate.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	// Mixer should be in passthrough (single active source at 0dB).
	require.True(t, mixer.IsPassthrough())

	// Send audio — should pass through to program relay.
	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	time.Sleep(10 * time.Millisecond)

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
	defer mixer.Close()

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

	// Cut to cam1.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	mixer.OnProgramChange("cam1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	require.True(t, mixer.IsChannelActive("cam1"))
	require.False(t, mixer.IsChannelActive("cam2"))

	// Send audio from cam1 (active) — should arrive.
	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	time.Sleep(10 * time.Millisecond)

	capture.mu.Lock()
	require.Equal(t, 1, len(capture.audios))
	capture.mu.Unlock()

	// Cut to cam2 — cam2 activates, cam1 deactivates.
	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	mixer.OnProgramChange("cam2")
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
	defer mixer.Close()

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
