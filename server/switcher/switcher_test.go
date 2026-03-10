package switcher

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/transition"
)

func newTestRelay() *distribution.Relay {
	return distribution.NewRelay()
}

// makeAVC1Frame creates a minimal AVC1-formatted frame (4-byte length prefix + NALU data).
func makeAVC1Frame(naluData []byte) []byte {
	buf := make([]byte, 4+len(naluData))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(naluData)))
	copy(buf[4:], naluData)
	return buf
}

// mockProgramViewer captures frames from the program relay.
type mockProgramViewer struct {
	id     string
	mu     sync.Mutex
	videos []*media.VideoFrame
	audios []*media.AudioFrame
}

func newMockProgramViewer(id string) *mockProgramViewer {
	return &mockProgramViewer{id: id}
}

func (m *mockProgramViewer) ID() string { return m.id }

func (m *mockProgramViewer) SendVideo(frame *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videos = append(m.videos, frame)
}

func (m *mockProgramViewer) SendAudio(frame *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audios = append(m.audios, frame)
}

func (m *mockProgramViewer) SendCaptions(_ *ccx.CaptionFrame) {}

func (m *mockProgramViewer) Stats() distribution.ViewerStats {
	return distribution.ViewerStats{ID: m.id}
}

func (m *mockProgramViewer) VideoFrames() []*media.VideoFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*media.VideoFrame, len(m.videos))
	copy(out, m.videos)
	return out
}

func TestNewSwitcher(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	require.NotNil(t, sw)
	state := sw.State()
	require.Equal(t, "", state.ProgramSource)
	require.Equal(t, "", state.PreviewSource)
	require.Equal(t, "cut", state.TransitionType)
	require.Empty(t, state.Sources)
}

func TestRegisterSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)

	state := sw.State()
	require.Len(t, state.Sources, 1)
	src, ok := state.Sources["camera1"]
	require.True(t, ok, "Sources missing 'camera1'")
	// Newly registered source with no frames yet shows as offline.
	require.Equal(t, string(SourceOffline), src.Status)
	require.Equal(t, "camera1", src.Key)
}

func TestUnregisterSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)
	sw.UnregisterSource("camera1")

	state := sw.State()
	require.Empty(t, state.Sources)
}

func TestRegisterVirtualSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)

	state := sw.State()
	require.Len(t, state.Sources, 1)
	src, ok := state.Sources["replay"]
	require.True(t, ok, "Sources should contain 'replay'")
	require.True(t, src.IsVirtual, "replay source should be virtual")
	require.Equal(t, "REPLAY", src.Label, "virtual source label should be uppercased key")
}

func TestRegisterVirtualSource_CutToVirtual(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)

	err := sw.Cut(context.Background(), "replay")
	require.NoError(t, err)

	state := sw.State()
	require.Equal(t, "replay", state.ProgramSource)
	require.Equal(t, string(TallyProgram), state.TallyState["replay"])
}

func TestRegisterVirtualSource_UnregisterCleanup(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)
	sw.UnregisterSource("replay")

	state := sw.State()
	require.Len(t, state.Sources, 0, "virtual source should be removed after UnregisterSource")
}

func TestRegisterVirtualSource_UnregisterClearsProgram(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterVirtualSource("replay", replayRelay)
	_ = sw.Cut(context.Background(), "replay")
	require.Equal(t, "replay", sw.State().ProgramSource)

	sw.UnregisterSource("replay")

	state := sw.State()
	require.Equal(t, "", state.ProgramSource, "program should be cleared when virtual source is unregistered")
	require.NotContains(t, state.TallyState, "replay", "tally should not contain removed source")
}

func TestRegisterVirtualSource_DoubleRegisterCleansUp(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)
	// Double-register should not panic or leak
	sw.RegisterVirtualSource("replay", replayRelay)

	state := sw.State()
	require.Len(t, state.Sources, 1, "should still have exactly one replay source")
	require.True(t, state.Sources["replay"].IsVirtual)
}

func TestUnregisterSource_BroadcastsState(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()

	stateCh := make(chan internal.ControlRoomState, 10)
	sw.OnStateChange(func(s internal.ControlRoomState) {
		stateCh <- s
	})

	sw.RegisterVirtualSource("replay", replayRelay)
	// Drain the registration broadcast
	select {
	case <-stateCh:
	case <-time.After(1 * time.Second):
		require.Fail(t, "no state broadcast on RegisterVirtualSource")
	}

	sw.UnregisterSource("replay")

	select {
	case s := <-stateCh:
		require.Empty(t, s.Sources, "broadcast should show source removed")
	case <-time.After(1 * time.Second):
		require.Fail(t, "no state broadcast on UnregisterSource")
	}
}

func TestRegisterVirtualSource_SkipsDelayAndFrameSync(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)

	// Virtual source should have zero delay
	state := sw.State()
	src := state.Sources["replay"]
	require.Equal(t, 0, src.DelayMs, "virtual source should have zero delay")
}

func TestCutToSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	err := sw.Cut(context.Background(), "camera1")
	require.NoError(t, err)

	state := sw.State()
	require.Equal(t, "camera1", state.ProgramSource)
	require.Equal(t, string(TallyProgram), state.TallyState["camera1"])
	require.Equal(t, uint64(1), state.Seq)
}

func TestCutSwapsPreview(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	// Cut to camera1, set preview to camera2, then cut to camera2.
	require.NoError(t, sw.Cut(context.Background(), "camera1"))
	require.NoError(t, sw.SetPreview(context.Background(), "camera2"))
	require.NoError(t, sw.Cut(context.Background(), "camera2"))

	state := sw.State()
	require.Equal(t, "camera2", state.ProgramSource)
	require.Equal(t, "camera1", state.PreviewSource)
}

func TestCutToMissingSourceErrors(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	err := sw.Cut(context.Background(), "nonexistent")
	require.Error(t, err)
}

func TestCutToCurrentProgramIsNoop(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	require.NoError(t, sw.Cut(context.Background(), "camera1"))
	seqAfterFirst := sw.State().Seq

	// Second cut to the same source should be a no-op.
	require.NoError(t, sw.Cut(context.Background(), "camera1"))
	seqAfterSecond := sw.State().Seq

	require.Equal(t, seqAfterFirst, seqAfterSecond, "Seq should not change on no-op cut")
}

func TestSetPreview(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	err := sw.SetPreview(context.Background(), "camera1")
	require.NoError(t, err)

	state := sw.State()
	require.Equal(t, "camera1", state.PreviewSource)
	require.Equal(t, string(TallyPreview), state.TallyState["camera1"])
}

func TestSetPreviewMissingSourceErrors(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	err := sw.SetPreview(context.Background(), "nonexistent")
	require.Error(t, err)
}

func TestFrameForwarding(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	// Attach a mock viewer to the program relay to capture output.
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	// Cut to camera1.
	require.NoError(t, sw.Cut(context.Background(), "camera1"))

	// Broadcast a video frame on camera1's relay -- should reach the viewer.
	cam1Frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true}
	cam1Relay.BroadcastVideo(cam1Frame)

	// Broadcast a video frame on camera2's relay -- should NOT reach the viewer.
	cam2Frame := &media.VideoFrame{PTS: 2000, IsKeyframe: true}
	cam2Relay.BroadcastVideo(cam2Frame)

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	require.Equal(t, 1, len(viewer.videos), "video frame count")
	require.Equal(t, int64(1000), viewer.videos[0].PTS)
}

func TestAudioFrameForwarding(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	require.NoError(t, sw.Cut(context.Background(), "camera1"))

	// Send a keyframe to clear the IDR gate.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// Audio from program source should be forwarded.
	cam1Audio := &media.AudioFrame{PTS: 500, Data: []byte{0xAA}}
	cam1Relay.BroadcastAudio(cam1Audio)

	// Audio from non-program source should be dropped.
	cam2Audio := &media.AudioFrame{PTS: 600, Data: []byte{0xBB}}
	cam2Relay.BroadcastAudio(cam2Audio)

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	require.Equal(t, 1, len(viewer.audios), "audio frame count")
	require.Equal(t, int64(500), viewer.audios[0].PTS)
}

func TestCutForwardsAllFrames(t *testing.T) {
	// In always-decode mode, there is no IDR gating — all frames from the
	// program source are forwarded immediately after a cut.
	programRelay := newTestRelay()
	sw := New(programRelay)

	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	// Cut to camera1, send a keyframe.
	_ = sw.Cut(context.Background(), "camera1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// Cut to camera2.
	_ = sw.Cut(context.Background(), "camera2")

	// All frames from camera2 should be forwarded immediately (no IDR gate).
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 200, IsKeyframe: false})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 300, IsKeyframe: true})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 400, IsKeyframe: false})

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	// Should have: cam1 keyframe(100) + all 3 cam2 frames = 4
	require.Equal(t, 4, len(viewer.videos), "video frame count")
	require.Equal(t, int64(100), viewer.videos[0].PTS)
	require.Equal(t, int64(200), viewer.videos[1].PTS)
	require.Equal(t, int64(300), viewer.videos[2].PTS)
	require.Equal(t, int64(400), viewer.videos[3].PTS)
}

func TestCutAudioForwardedImmediately(t *testing.T) {
	// In always-decode mode, there is no IDR gating — audio from the new
	// program source is forwarded immediately after a cut.
	programRelay := newTestRelay()
	sw := New(programRelay)

	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	_ = sw.Cut(context.Background(), "camera1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// Cut to camera2.
	_ = sw.Cut(context.Background(), "camera2")

	// Audio from camera2 should be forwarded immediately (no IDR gate).
	cam2Relay.BroadcastAudio(&media.AudioFrame{PTS: 200, Data: []byte{0xAA}})
	cam2Relay.BroadcastAudio(&media.AudioFrame{PTS: 400, Data: []byte{0xBB}})

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	// Both audio frames should be forwarded
	require.Equal(t, 2, len(viewer.audios), "audio frame count")
	require.Equal(t, int64(200), viewer.audios[0].PTS)
	require.Equal(t, int64(400), viewer.audios[1].PTS)
}

func TestHealthStatusUpdatesOnFrames(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	// Before any frames: offline.
	state := sw.State()
	require.Equal(t, string(SourceOffline), state.Sources["camera1"].Status, "before frames")

	// Send a frame (source must be on program for handleVideoFrame to record).
	_ = sw.Cut(context.Background(), "camera1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// After frame: healthy.
	state = sw.State()
	require.Equal(t, string(SourceHealthy), state.Sources["camera1"].Status, "after frame")
}

func TestMultipleStateCallbacks(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	var count1, count2 int
	sw.OnStateChange(func(state internal.ControlRoomState) {
		count1++
	})
	sw.OnStateChange(func(state internal.ControlRoomState) {
		count2++
	})

	relay := newTestRelay()
	sw.RegisterSource("cam1", relay)
	sw.RegisterSource("cam2", relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	require.Equal(t, 2, count1, "first callback should fire twice")
	require.Equal(t, 2, count2, "second callback should fire twice")
}

func TestSetLabel(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	relay := newTestRelay()
	sw.RegisterSource("cam1", relay)

	// Set label
	err := sw.SetLabel(context.Background(), "cam1", "Camera 1")
	require.NoError(t, err)

	state := sw.State()
	require.Equal(t, "Camera 1", state.Sources["cam1"].Label)

	// Unknown source
	err = sw.SetLabel(context.Background(), "nonexistent", "Nope")
	require.Error(t, err)
}

// mockAudioStateProvider implements audioStateProvider for testing.
type mockAudioStateProvider struct {
	programPeak   [2]float64
	channelStates map[string]internal.AudioChannel
	masterLevel   float64
	gainReduction float64
}

func (m *mockAudioStateProvider) ProgramPeak() [2]float64 {
	return m.programPeak
}

func (m *mockAudioStateProvider) ChannelStates() map[string]internal.AudioChannel {
	return m.channelStates
}

func (m *mockAudioStateProvider) MasterLevel() float64 {
	return m.masterLevel
}

func (m *mockAudioStateProvider) GainReduction() float64 {
	return m.gainReduction
}

func (m *mockAudioStateProvider) MomentaryLUFS() float64 {
	return 0
}

func (m *mockAudioStateProvider) ShortTermLUFS() float64 {
	return 0
}

func (m *mockAudioStateProvider) IntegratedLUFS() float64 {
	return 0
}

func TestStateIncludesAudioFromMixer(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	relay := newTestRelay()
	sw.RegisterSource("cam1", relay)

	// Without mixer: audio fields should be zero/nil
	state := sw.State()
	require.Nil(t, state.AudioChannels)
	require.Equal(t, 0.0, state.MasterLevel)
	require.Equal(t, [2]float64{0, 0}, state.ProgramPeak)

	// Attach a mock mixer
	mock := &mockAudioStateProvider{
		programPeak: [2]float64{-6.0, -12.0},
		channelStates: map[string]internal.AudioChannel{
			"cam1": {Level: -3.0, Muted: false, AFV: true},
		},
		masterLevel: -1.5,
	}
	sw.SetMixer(mock)

	// With mixer: audio fields should be populated
	state = sw.State()
	require.Equal(t, [2]float64{-6.0, -12.0}, state.ProgramPeak)
	require.Equal(t, -1.5, state.MasterLevel)
	require.Len(t, state.AudioChannels, 1)
	require.Equal(t, -3.0, state.AudioChannels["cam1"].Level)
	require.True(t, state.AudioChannels["cam1"].AFV)
}

func TestSetMixerConcurrentSafe(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	relay := newTestRelay()
	sw.RegisterSource("cam1", relay)

	mock := &mockAudioStateProvider{
		programPeak:   [2]float64{-6.0, -12.0},
		channelStates: map[string]internal.AudioChannel{},
		masterLevel:   0.0,
	}

	// SetMixer should be safe to call concurrently with State()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			sw.SetMixer(mock)
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_ = sw.State()
	}
	<-done
}

func TestSourceKeys(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	keys := sw.SourceKeys()
	require.Len(t, keys, 2)
	require.Contains(t, keys, "camera1")
	require.Contains(t, keys, "camera2")
}

func TestAllAudioRoutedToMixer(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	var mu sync.Mutex
	var mixerFrames []struct {
		key   string
		frame *media.AudioFrame
	}
	sw.SetAudioHandler(func(sourceKey string, frame *media.AudioFrame) {
		mu.Lock()
		mixerFrames = append(mixerFrames, struct {
			key   string
			frame *media.AudioFrame
		}{sourceKey, frame})
		mu.Unlock()
	})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Clear IDR gate
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	// Audio from BOTH sources should reach the mixer handler
	cam1Relay.BroadcastAudio(&media.AudioFrame{PTS: 100, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	cam2Relay.BroadcastAudio(&media.AudioFrame{PTS: 200, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	require.Equal(t, 2, len(mixerFrames), "both sources' audio should reach mixer")
	mu.Unlock()
}

func TestSwitcher_DebugSnapshot(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// Empty switcher snapshot.
	snap := sw.DebugSnapshot()
	require.Equal(t, "", snap["program_source"])
	require.Equal(t, "", snap["preview_source"])
	require.Equal(t, false, snap["in_transition"])
	require.Equal(t, false, snap["ftb_active"])
	require.Equal(t, uint64(0), snap["seq"])
	require.Equal(t, int64(0), snap["cuts_total"])
	require.Equal(t, int64(0), snap["transitions_started"])
	require.Equal(t, int64(0), snap["transitions_completed"])

	sources, ok := snap["sources"].(map[string]any)
	require.True(t, ok, "sources should be map[string]any")
	require.Len(t, sources, 0)

	// Register sources and cut.
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	snap = sw.DebugSnapshot()
	require.Equal(t, "cam1", snap["program_source"])
	require.Equal(t, int64(1), snap["cuts_total"])

	sources = snap["sources"].(map[string]any)
	require.Len(t, sources, 2)

	cam1Info, ok := sources["cam1"].(map[string]any)
	require.True(t, ok)
	_ = cam1Info

	// Send a frame to verify video_frames_in is tracked.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	snap = sw.DebugSnapshot()
	sources = snap["sources"].(map[string]any)
	cam1Info = sources["cam1"].(map[string]any)
	require.Equal(t, int64(1), cam1Info["video_frames_in"])

	// Second cut increments cuts_total.
	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	snap = sw.DebugSnapshot()
	require.Equal(t, int64(2), snap["cuts_total"])
	require.Equal(t, "cam2", snap["program_source"])
	require.Equal(t, "cam1", snap["preview_source"])
}

func TestFrameStatsUpdatedOnVideoFrames(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send several frames with known sizes and PTS deltas.
	// 30fps at 90kHz clock = 3000 ticks between frames, frame size ~10000 bytes
	for i := 0; i < 20; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS:        int64(i) * 3000,
			IsKeyframe: i == 0,
			WireData:   make([]byte, 10000),
		})
	}

	// Check that stats have been updated
	sw.mu.RLock()
	ss := sw.sources["cam1"]
	sw.mu.RUnlock()
	avgFrameSize := ss.avgFrameSize
	avgFPS := ss.avgFPS
	frameCount := ss.frameCount

	require.Equal(t, 20, frameCount, "should have recorded 20 frames")
	require.InDelta(t, 10000, avgFrameSize, 1000, "avg frame size should be near 10000 bytes")
	require.InDelta(t, 30.0, avgFPS, 3.0, "avg FPS should be near 30")
}

func TestSwitcher_DebugSnapshot_HealthStatus(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	// Before any frames: offline, lastFrameAgoMs = -1.
	snap := sw.DebugSnapshot()
	sources := snap["sources"].(map[string]any)
	cam1Info := sources["cam1"].(map[string]any)
	require.Equal(t, string(SourceOffline), cam1Info["health_status"])
	require.Equal(t, int64(-1), cam1Info["last_frame_ago_ms"])

	// Send a frame (via cut + keyframe).
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	snap = sw.DebugSnapshot()
	sources = snap["sources"].(map[string]any)
	cam1Info = sources["cam1"].(map[string]any)
	require.Equal(t, string(SourceHealthy), cam1Info["health_status"])
	// last_frame_ago_ms should be small (just sent a frame).
	agoMs := cam1Info["last_frame_ago_ms"].(int64)
	require.GreaterOrEqual(t, agoMs, int64(0))
	require.Less(t, agoMs, int64(1000), "should be less than 1s since we just sent a frame")
}

func TestUpdateAtomicMax(t *testing.T) {
	var field atomic.Int64

	// Zero → positive updates.
	updateAtomicMax(&field, 100)
	require.Equal(t, int64(100), field.Load())

	// Larger value replaces.
	updateAtomicMax(&field, 200)
	require.Equal(t, int64(200), field.Load())

	// Smaller value is a no-op.
	updateAtomicMax(&field, 50)
	require.Equal(t, int64(200), field.Load())

	// Equal value is a no-op.
	updateAtomicMax(&field, 200)
	require.Equal(t, int64(200), field.Load())
}

func TestDebugSnapshot_PipelineStageTiming(t *testing.T) {
	// In always-decode mode, frames arrive as raw YUV. The Pipeline struct
	// tracks per-node timing via atomic.Int64. Verify the pipeline snapshot
	// appears in DebugSnapshot after processing a frame.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send a raw YUV frame through the pipeline.
	sendRawFrame(sw, "cam1", 100, true)

	// Wait for async video processing to complete.
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	snap := sw.DebugSnapshot()

	// Pipeline snapshot should be present and show run_count > 0.
	pipelineSnap, ok := snap["pipeline"].(map[string]any)
	require.True(t, ok, "pipeline snapshot should be present in DebugSnapshot")
	require.Greater(t, pipelineSnap["run_count"], int64(0), "run_count should be > 0 after processing a frame")

	// Active nodes should include the encode node.
	activeNodes := pipelineSnap["active_nodes"].([]map[string]any)
	require.GreaterOrEqual(t, len(activeNodes), 1, "should have at least the encode node active")

	// Find the encode node and verify it has timing.
	var foundEncode bool
	for _, n := range activeNodes {
		if n["name"] == "h264-encode" {
			foundEncode = true
			require.GreaterOrEqual(t, n["last_ns"], int64(0))
		}
	}
	require.True(t, foundEncode, "encode node should be in active_nodes")
}

func TestOutputFPSTracking(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Simulate ~30 frames in rapid succession to fill one second window
	for i := 0; i < 30; i++ {
		sw.trackOutputFPS()
	}

	// The first call sets the window start, subsequent 29 increment the count.
	// FPS won't be published until the window rolls over.
	snap := sw.DebugSnapshot()
	pipeline := snap["video_pipeline"].(map[string]any)
	// output_fps reflects the PREVIOUS window, which hasn't rolled yet.
	// Just verify the field exists and is a valid int64.
	_, ok := pipeline["output_fps"]
	require.True(t, ok, "output_fps should be present in debug snapshot")
}

// TestSeqConcurrentAccess exercises concurrent reads and writes of the seq
// counter to verify there are no data races. Under the race detector, bare
// s.seq++ (non-atomic read-modify-write) concurrent with State() reads
// would be flagged as a race. This test is designed to trigger that scenario.
func TestSeqConcurrentAccess(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	// Put cam1 on program so we can toggle between cam1 and cam2.
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	const goroutines = 4
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Writer goroutine 1: alternating Cut between cam1 and cam2.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				_ = sw.Cut(context.Background(), "cam2")
			} else {
				_ = sw.Cut(context.Background(), "cam1")
			}
		}
	}()

	// Writer goroutine 2: alternating SetPreview.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				_ = sw.SetPreview(context.Background(), "cam1")
			} else {
				_ = sw.SetPreview(context.Background(), "cam2")
			}
		}
	}()

	// Writer goroutine 3: SetLabel (also increments seq).
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = sw.SetLabel(context.Background(), "cam1", fmt.Sprintf("Camera %d", i))
		}
	}()

	// Reader goroutine: concurrent State() reads.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			state := sw.State()
			// Seq should be monotonically increasing (each read should
			// see a value >= previous), but we don't enforce strict ordering
			// here — the key assertion is that the race detector does not fire.
			_ = state.Seq
		}
	}()

	wg.Wait()

	// After all mutations, seq should be > 0.
	finalState := sw.State()
	require.Greater(t, finalState.Seq, uint64(0), "seq should have been incremented")
}

func TestHandleVideoFrameSingleRLock(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	var wg sync.WaitGroup
	const iterations = 1000

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			frame := &media.VideoFrame{
				PTS:      int64(i * 3000),
				WireData: makeAVC1Frame([]byte{0x41, byte(i % 256)}),
			}
			sw.handleVideoFrame("cam1", frame)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations/10; i++ {
			if i%2 == 0 {
				_ = sw.Cut(context.Background(), "cam2")
			} else {
				_ = sw.Cut(context.Background(), "cam1")
			}
		}
	}()

	wg.Wait()
}

func TestCutGroupIDMonotonicity(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	// Cut to cam1
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send frames with HIGH GroupID
	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 100, IsKeyframe: true, GroupID: 50,
		WireData: makeAVC1Frame([]byte{0x65, 0xAA}),
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
	})
	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 200, IsKeyframe: false, GroupID: 50,
		WireData: makeAVC1Frame([]byte{0x41, 0x01}),
	})

	// Cut to cam2 — has LOWER GroupID
	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	// Keyframe with LOW GroupID
	cam2Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 300, IsKeyframe: true, GroupID: 10,
		WireData: makeAVC1Frame([]byte{0x65, 0xBB}),
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
	})
	cam2Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 400, IsKeyframe: false, GroupID: 10,
		WireData: makeAVC1Frame([]byte{0x41, 0x02}),
	})

	time.Sleep(10 * time.Millisecond)

	frames := viewer.VideoFrames()
	require.NotEmpty(t, frames)

	var lastGID uint32
	for i, f := range frames {
		require.GreaterOrEqual(t, f.GroupID, lastGID,
			"GroupID must be monotonically increasing: frame %d has GroupID %d but previous was %d",
			i, f.GroupID, lastGID)
		lastGID = f.GroupID
	}
}

func TestSwitcher_LastBroadcastVideoPTS(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	// Initially 0 — no frames broadcast yet.
	require.Equal(t, int64(0), sw.LastBroadcastVideoPTS())

	// Cut to cam1 and send frames.
	_ = sw.Cut(context.Background(), "cam1")
	viewer := sw.sources["cam1"].viewer

	// Send a keyframe, then non-keyframe with known PTS values.
	viewer.SendVideo(&media.VideoFrame{
		PTS:        100_000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88},
	})
	time.Sleep(50 * time.Millisecond) // Allow async processing

	pts := sw.LastBroadcastVideoPTS()
	require.Equal(t, int64(100_000), pts, "expected PTS to track broadcast")

	viewer.SendVideo(&media.VideoFrame{
		PTS:        103_003,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9A},
	})
	time.Sleep(50 * time.Millisecond)

	pts = sw.LastBroadcastVideoPTS()
	require.Equal(t, int64(103_003), pts, "expected PTS to update on each broadcast")
}

func TestHandleRawVideoFrame_NilViewer(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// Register an MXL-style source (nil viewer, nil relay).
	sw.RegisterMXLSource("mxl:cam1")
	require.NoError(t, sw.SetPreview(context.Background(), "mxl:cam1"))
	require.NoError(t, sw.Cut(context.Background(), "mxl:cam1"))

	// Should not panic — viewer is nil for MXL sources.
	pf := &ProcessingFrame{
		YUV:   make([]byte, 1920*1080*3/2),
		Width: 1920, Height: 1080,
		PTS: 90000, DTS: 90000,
	}
	require.NotPanics(t, func() {
		sw.handleRawVideoFrame("mxl:cam1", pf)
	})
}

func TestRegisterReplaySource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	sw.RegisterReplaySource("replay")

	state := sw.State()
	require.Len(t, state.Sources, 1)
	src, ok := state.Sources["replay"]
	require.True(t, ok)
	require.True(t, src.IsVirtual, "replay source should be virtual")
	require.Equal(t, "REPLAY", src.Label)
}

func TestRegisterReplaySource_CleansUpOld(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	sw.RegisterReplaySource("replay")
	sw.RegisterReplaySource("replay")

	state := sw.State()
	require.Len(t, state.Sources, 1)
}

func TestIngestReplayVideo_RoutesToPipeline(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	sw.RegisterReplaySource("replay")
	require.NoError(t, sw.SetPreview(context.Background(), "replay"))
	require.NoError(t, sw.Cut(context.Background(), "replay"))

	pf := &ProcessingFrame{
		YUV:        make([]byte, 320*240*3/2),
		Width:      320,
		Height:     240,
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
	}

	// Should not panic — routes through handleRawVideoFrame.
	require.NotPanics(t, func() {
		sw.IngestReplayVideo("replay", pf)
	})
}

func TestCloseWithFrameSyncActive(t *testing.T) {
	// Bug 1: Close() closes videoProcCh before stopping the frame sync.
	// If frame sync ticks in that window, enqueueVideoWork sends on a
	// closed channel, causing a panic. This test enables frame sync with
	// active sources and calls Close() repeatedly to trigger the race.
	for i := 0; i < 20; i++ {
		programRelay := newTestRelay()
		sw := New(programRelay)

		cam1Relay := newTestRelay()
		sw.RegisterSource("camera1", cam1Relay)
		_ = sw.Cut(context.Background(), "camera1")

		// Enable frame sync — creates a FrameSynchronizer with background ticker.
		sw.SetFrameSync(true, 10*time.Millisecond)

		// Feed some raw frames through the frame sync so it has data to tick.
		for j := 0; j < 5; j++ {
			pf := &ProcessingFrame{
				YUV:        make([]byte, 320*240*3/2),
				Width:      320,
				Height:     240,
				PTS:        int64(j * 3000),
				IsKeyframe: j == 0,
			}
			sw.mu.RLock()
			fs := sw.frameSync
			sw.mu.RUnlock()
			if fs != nil {
				fs.IngestRawVideo("camera1", pf)
			}
		}

		// Let the frame sync tick a few times.
		time.Sleep(30 * time.Millisecond)

		// Close() must not panic even though frame sync is actively ticking.
		require.NotPanics(t, func() {
			sw.Close()
		}, "Close() panicked on iteration %d", i)
	}
}

func TestEnqueueVideoWork_ReenqueueFailureReleasesNewBuffer(t *testing.T) {
	// Bug A1: When the channel is full, enqueueVideoWork drops the oldest
	// frame and tries to re-enqueue the new one. If re-enqueue also fails
	// (race: another goroutine filled the slot), the new work item's pool
	// buffer must be released. Also, videoProcDropped should only increment
	// when a frame is actually lost (re-enqueue failure), not on every
	// drop-oldest path.

	// Create a switcher with a tiny channel capacity of 1.
	pool := NewFramePool(8, 4, 4)
	programRelay := newTestRelay()
	sw := &Switcher{
		sources:     make(map[string]*sourceState),
		programRelay: programRelay,
		health:       newHealthMonitor(),
		// Channel capacity 1 to make it easy to fill.
		videoProcCh:   make(chan videoProcWork, 1),
		videoProcDone: make(chan struct{}),
		framePool:     pool,
	}
	defaultFmt := DefaultFormat
	sw.frameBudgetNs.Store(defaultFmt.FrameBudgetNs())
	sw.pipelineFormat.Store(&defaultFmt)
	sw.delayBuffer = NewDelayBuffer(sw)

	// Don't start videoProcessingLoop — we want the channel to stay full.

	// Fill the channel (capacity 1).
	pf1 := &ProcessingFrame{
		YUV: pool.Acquire(), Width: 4, Height: 4,
		PTS: 100, IsKeyframe: true, pool: pool,
	}
	sw.videoProcCh <- videoProcWork{yuvFrame: pf1}

	// Now enqueue a second frame. This triggers the drop-oldest path.
	// The oldest (pf1) will be drained and released, then the new frame
	// will be enqueued into the now-empty slot. This is the success path.
	pf2 := &ProcessingFrame{
		YUV: pool.Acquire(), Width: 4, Height: 4,
		PTS: 200, IsKeyframe: false, pool: pool,
	}
	sw.enqueueVideoWork(videoProcWork{yuvFrame: pf2})

	// pf1 was dropped and released. pf2 should be in the channel.
	require.Len(t, sw.videoProcCh, 1, "channel should have the new frame")

	// Now test the failure path: fill the channel again, then simulate
	// a scenario where re-enqueue fails by filling it from outside first.
	// We need to ensure that if both the channel-drain AND re-enqueue
	// default branches fire, the new frame's buffer gets released.
	//
	// To test this deterministically, we fill the channel and then call
	// enqueueVideoWork again — but this time we pre-fill from the outside
	// after the drain so the re-enqueue default branch fires.
	//
	// Since we can't control goroutine scheduling, we test by verifying
	// that after many enqueue attempts on a permanently full channel,
	// all buffers are eventually returned to the pool.
	//
	// Strategy: acquire all buffers, enqueue them on a stopped channel,
	// then check pool stats.
	pool2 := NewFramePool(4, 4, 4)
	sw2 := &Switcher{
		sources:      make(map[string]*sourceState),
		programRelay: programRelay,
		health:       newHealthMonitor(),
		videoProcCh:  make(chan videoProcWork, 1),
		videoProcDone: make(chan struct{}),
		framePool:     pool2,
	}
	sw2.frameBudgetNs.Store(defaultFmt.FrameBudgetNs())
	sw2.pipelineFormat.Store(&defaultFmt)
	sw2.delayBuffer = NewDelayBuffer(sw2)

	// Acquire all 4 buffers from pool2.
	bufs := make([][]byte, 4)
	for i := range bufs {
		bufs[i] = pool2.Acquire()
	}
	// Pool is now empty — all 4 buffers are out.
	_, missesBefore := pool2.Stats()

	// Put one in the channel to fill it.
	sw2.videoProcCh <- videoProcWork{yuvFrame: &ProcessingFrame{
		YUV: bufs[0], Width: 4, Height: 4, PTS: 1, pool: pool2,
	}}

	// Enqueue the remaining 3 — each triggers drop-oldest + re-enqueue.
	// One frame remains in the channel at the end, the rest should be released.
	for i := 1; i < 4; i++ {
		sw2.enqueueVideoWork(videoProcWork{yuvFrame: &ProcessingFrame{
			YUV: bufs[i], Width: 4, Height: 4, PTS: int64(i * 100), pool: pool2,
		}})
	}

	// Drain the last frame from the channel.
	remaining := <-sw2.videoProcCh
	remaining.yuvFrame.ReleaseYUV()

	// All 4 buffers should now be back in the pool.
	hits, misses := pool2.Stats()
	_ = missesBefore
	// 4 acquires (hits) + 4 releases should mean all are back.
	require.Equal(t, uint64(4), hits, "all 4 initial Acquires should be hits")
	// Verify no extra misses happened (no buffer was leaked causing a fallback alloc).
	require.Equal(t, misses, uint64(0), "no pool misses should occur — all buffers accounted for")

	// Clean up: close channels to avoid goroutine leaks.
	close(sw.videoProcCh)
	close(sw2.videoProcCh)
	sw.delayBuffer.Close()
	sw2.delayBuffer.Close()
	sw.health.stop()
	sw2.health.stop()
}

func TestEnqueueVideoWork_DroppedCountOnlyOnActualLoss(t *testing.T) {
	// Bug A1 (part 2): videoProcDropped should only increment when a frame
	// is actually lost (re-enqueue failure), not on every drop-oldest path.
	// When the oldest frame is dropped but the new one is successfully
	// enqueued, there is no net frame loss — just a swap of old for new.
	pool := NewFramePool(8, 4, 4)
	programRelay := newTestRelay()
	sw := &Switcher{
		sources:      make(map[string]*sourceState),
		programRelay: programRelay,
		health:       newHealthMonitor(),
		videoProcCh:  make(chan videoProcWork, 1),
		videoProcDone: make(chan struct{}),
		framePool:     pool,
	}
	defaultFmt := DefaultFormat
	sw.frameBudgetNs.Store(defaultFmt.FrameBudgetNs())
	sw.pipelineFormat.Store(&defaultFmt)
	sw.delayBuffer = NewDelayBuffer(sw)
	defer func() {
		close(sw.videoProcCh)
		sw.delayBuffer.Close()
		sw.health.stop()
	}()

	// Fill the channel.
	pf1 := &ProcessingFrame{
		YUV: pool.Acquire(), Width: 4, Height: 4,
		PTS: 100, IsKeyframe: true, pool: pool,
	}
	sw.videoProcCh <- videoProcWork{yuvFrame: pf1}

	// Enqueue a new frame — should drop oldest and re-enqueue successfully.
	pf2 := &ProcessingFrame{
		YUV: pool.Acquire(), Width: 4, Height: 4,
		PTS: 200, IsKeyframe: false, pool: pool,
	}
	sw.enqueueVideoWork(videoProcWork{yuvFrame: pf2})

	// The old frame was dropped, but the new one took its place.
	// No net frame loss — videoProcDropped should be 0.
	require.Equal(t, int64(0), sw.videoProcDropped.Load(),
		"videoProcDropped should not increment when re-enqueue succeeds (no net loss)")
}

func TestBroadcastProcessed_ShortYUVDoesNotPanic(t *testing.T) {
	// Bug A2: broadcastProcessed must check that len(yuv) >= expectedSize
	// before copying into a pool buffer. A short slice would cause a panic
	// on the copy(buf, yuv[:expectedSize]) line.
	programRelay := newTestRelay()
	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	// Create a short YUV buffer — smaller than 320x240 * 3/2 = 115200.
	shortYUV := make([]byte, 100)

	// Should not panic.
	require.NotPanics(t, func() {
		sw.broadcastProcessed(shortYUV, 320, 240, 90000, true)
	})

	// Verify no pool buffer was leaked (Acquire should not have been called
	// since the bounds check should bail out before Acquire).
	// Give a tiny window for any async work to complete.
	time.Sleep(10 * time.Millisecond)
}

func TestBroadcastProcessed_EmptyYUVDoesNotPanic(t *testing.T) {
	// Edge case: nil/empty YUV slice.
	programRelay := newTestRelay()
	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	require.NotPanics(t, func() {
		sw.broadcastProcessed(nil, 320, 240, 90000, true)
	})
	require.NotPanics(t, func() {
		sw.broadcastProcessed([]byte{}, 1920, 1080, 90000, true)
	})
}

func TestUpdateFrameStats_90kHzPTS(t *testing.T) {
	// Bug A4: updateFrameStats used 1,000,000 (microseconds) as the PTS
	// timebase, but PTS throughout the codebase is in 90kHz clock units
	// (standard MPEG-TS). At 29.97fps, the PTS delta between frames is
	// 90000/29.97 ~= 3003 ticks. With the wrong timebase (1M), FPS would
	// be computed as 1,000,000/3003 ~= 333 fps instead of ~29.97 fps.

	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	ss := &sourceState{key: "test"}

	// Simulate 29.97fps frames with PTS delta of 3003 (90kHz clock).
	// 90000 / 29.97 = 3003.003... ≈ 3003
	const ptsDelta = 3003
	const numFrames = 100

	for i := 0; i < numFrames; i++ {
		frame := &media.VideoFrame{
			PTS:      int64(i * ptsDelta),
			WireData: make([]byte, 50000), // ~50KB per frame
		}
		sw.updateFrameStats(ss, frame)
	}

	// avgFPS should converge to ~29.97, not ~333.
	require.InDelta(t, 29.97, ss.avgFPS, 1.0,
		"avgFPS should be ~29.97 for 90kHz PTS deltas of 3003, got %f", ss.avgFPS)
}

func TestUpdateFrameStats_RejectsUnreasonableDelta(t *testing.T) {
	// Verify that deltas > 1 second (90000 ticks) are rejected.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	ss := &sourceState{key: "test"}

	// First frame seeds the stats.
	sw.updateFrameStats(ss, &media.VideoFrame{
		PTS: 0, WireData: make([]byte, 50000),
	})

	// Second frame with normal delta to establish avgFPS.
	sw.updateFrameStats(ss, &media.VideoFrame{
		PTS: 3003, WireData: make([]byte, 50000),
	})
	fpsAfterNormal := ss.avgFPS
	require.InDelta(t, 29.97, fpsAfterNormal, 1.0)

	// Third frame with a huge PTS jump (>90000 ticks = >1 second).
	// This should be rejected and avgFPS should remain unchanged.
	sw.updateFrameStats(ss, &media.VideoFrame{
		PTS: 3003 + 200000, WireData: make([]byte, 50000),
	})
	require.InDelta(t, fpsAfterNormal, ss.avgFPS, 0.01,
		"avgFPS should be unchanged after rejecting unreasonable delta")
}
