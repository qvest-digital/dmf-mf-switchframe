package switcher

import (
	"context"
	"sync"
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

func TestNewSwitcher(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	if sw == nil {
		t.Fatal("New() returned nil")
	}
	state := sw.State()
	if state.ProgramSource != "" {
		t.Errorf("ProgramSource = %q, want empty", state.ProgramSource)
	}
	if state.PreviewSource != "" {
		t.Errorf("PreviewSource = %q, want empty", state.PreviewSource)
	}
	if state.TransitionType != "cut" {
		t.Errorf("TransitionType = %q, want %q", state.TransitionType, "cut")
	}
	if len(state.Sources) != 0 {
		t.Errorf("Sources has %d entries, want 0", len(state.Sources))
	}
}

func TestRegisterSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)

	state := sw.State()
	if len(state.Sources) != 1 {
		t.Fatalf("Sources has %d entries, want 1", len(state.Sources))
	}
	src, ok := state.Sources["camera1"]
	if !ok {
		t.Fatal("Sources missing 'camera1'")
	}
	// Newly registered source with no frames yet shows as offline.
	if src.Status != internal.SourceOffline {
		t.Errorf("Source status = %q, want %q", src.Status, internal.SourceOffline)
	}
	if src.Key != "camera1" {
		t.Errorf("Source key = %q, want %q", src.Key, "camera1")
	}
}

func TestUnregisterSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)
	sw.UnregisterSource("camera1")

	state := sw.State()
	if len(state.Sources) != 0 {
		t.Errorf("Sources has %d entries, want 0", len(state.Sources))
	}
}

func TestCutToSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	err := sw.Cut(context.Background(), "camera1")
	if err != nil {
		t.Fatalf("Cut() error: %v", err)
	}

	state := sw.State()
	if state.ProgramSource != "camera1" {
		t.Errorf("ProgramSource = %q, want %q", state.ProgramSource, "camera1")
	}
	if state.TallyState["camera1"] != internal.TallyProgram {
		t.Errorf("tally[camera1] = %q, want %q", state.TallyState["camera1"], internal.TallyProgram)
	}
	if state.Seq != 1 {
		t.Errorf("Seq = %d, want 1", state.Seq)
	}
}

func TestCutSwapsPreview(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	// Cut to camera1, set preview to camera2, then cut to camera2.
	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut(camera1) error: %v", err)
	}
	if err := sw.SetPreview(context.Background(), "camera2"); err != nil {
		t.Fatalf("SetPreview(camera2) error: %v", err)
	}
	if err := sw.Cut(context.Background(), "camera2"); err != nil {
		t.Fatalf("Cut(camera2) error: %v", err)
	}

	state := sw.State()
	if state.ProgramSource != "camera2" {
		t.Errorf("ProgramSource = %q, want %q", state.ProgramSource, "camera2")
	}
	if state.PreviewSource != "camera1" {
		t.Errorf("PreviewSource = %q, want %q", state.PreviewSource, "camera1")
	}
}

func TestCutToMissingSourceErrors(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	err := sw.Cut(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("Cut(nonexistent) should return error")
	}
}

func TestCutToCurrentProgramIsNoop(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut() error: %v", err)
	}
	seqAfterFirst := sw.State().Seq

	// Second cut to the same source should be a no-op.
	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut() error: %v", err)
	}
	seqAfterSecond := sw.State().Seq

	if seqAfterSecond != seqAfterFirst {
		t.Errorf("Seq changed from %d to %d; want no change", seqAfterFirst, seqAfterSecond)
	}
}

func TestSetPreview(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	err := sw.SetPreview(context.Background(), "camera1")
	if err != nil {
		t.Fatalf("SetPreview() error: %v", err)
	}

	state := sw.State()
	if state.PreviewSource != "camera1" {
		t.Errorf("PreviewSource = %q, want %q", state.PreviewSource, "camera1")
	}
	if state.TallyState["camera1"] != internal.TallyPreview {
		t.Errorf("tally[camera1] = %q, want %q", state.TallyState["camera1"], internal.TallyPreview)
	}
}

func TestSetPreviewMissingSourceErrors(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	err := sw.SetPreview(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("SetPreview(nonexistent) should return error")
	}
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
	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut() error: %v", err)
	}

	// Broadcast a video frame on camera1's relay -- should reach the viewer.
	cam1Frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true}
	cam1Relay.BroadcastVideo(cam1Frame)

	// Broadcast a video frame on camera2's relay -- should NOT reach the viewer.
	cam2Frame := &media.VideoFrame{PTS: 2000, IsKeyframe: true}
	cam2Relay.BroadcastVideo(cam2Frame)

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	if len(viewer.videos) != 1 {
		t.Fatalf("got %d video frames, want 1", len(viewer.videos))
	}
	if viewer.videos[0].PTS != 1000 {
		t.Errorf("forwarded frame PTS = %d, want 1000", viewer.videos[0].PTS)
	}
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

	if err := sw.Cut(context.Background(), "camera1"); err != nil {
		t.Fatalf("Cut() error: %v", err)
	}

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

	if len(viewer.audios) != 1 {
		t.Fatalf("got %d audio frames, want 1", len(viewer.audios))
	}
	if viewer.audios[0].PTS != 500 {
		t.Errorf("forwarded audio PTS = %d, want 500", viewer.audios[0].PTS)
	}
}

func TestCutGatesUntilKeyframe(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	// Cut to camera1, send a keyframe to establish it.
	sw.Cut(context.Background(), "camera1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// Cut to camera2.
	sw.Cut(context.Background(), "camera2")

	// Send P-frame from camera2 — should be DROPPED (no keyframe yet).
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 200, IsKeyframe: false})

	// Send keyframe from camera2 — should be forwarded.
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 300, IsKeyframe: true})

	// Send P-frame from camera2 — should be forwarded (keyframe was seen).
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 400, IsKeyframe: false})

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	// Should have: cam1 keyframe(100) + cam2 keyframe(300) + cam2 P-frame(400) = 3
	if len(viewer.videos) != 3 {
		t.Fatalf("got %d video frames, want 3", len(viewer.videos))
	}
	if viewer.videos[0].PTS != 100 {
		t.Errorf("frame[0] PTS = %d, want 100", viewer.videos[0].PTS)
	}
	if viewer.videos[1].PTS != 300 {
		t.Errorf("frame[1] PTS = %d, want 300", viewer.videos[1].PTS)
	}
	if viewer.videos[2].PTS != 400 {
		t.Errorf("frame[2] PTS = %d, want 400", viewer.videos[2].PTS)
	}
}

func TestCutAudioGatedUntilVideoKeyframe(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	sw.Cut(context.Background(), "camera1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// Cut to camera2.
	sw.Cut(context.Background(), "camera2")

	// Audio from camera2 before video keyframe — should be DROPPED.
	cam2Relay.BroadcastAudio(&media.AudioFrame{PTS: 200, Data: []byte{0xAA}})

	// Video keyframe from camera2 — clears the gate.
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 300, IsKeyframe: true})

	// Audio from camera2 after keyframe — should be forwarded.
	cam2Relay.BroadcastAudio(&media.AudioFrame{PTS: 400, Data: []byte{0xBB}})

	viewer.mu.Lock()
	defer viewer.mu.Unlock()

	if len(viewer.audios) != 1 {
		t.Fatalf("got %d audio frames, want 1", len(viewer.audios))
	}
	if viewer.audios[0].PTS != 400 {
		t.Errorf("audio PTS = %d, want 400", viewer.audios[0].PTS)
	}
}

func TestHealthStatusUpdatesOnFrames(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)

	cam1Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)

	// Before any frames: offline.
	state := sw.State()
	if state.Sources["camera1"].Status != internal.SourceOffline {
		t.Errorf("before frames: status = %q, want %q", state.Sources["camera1"].Status, internal.SourceOffline)
	}

	// Send a frame (source must be on program for handleVideoFrame to record).
	sw.Cut(context.Background(), "camera1")
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// After frame: healthy.
	state = sw.State()
	if state.Sources["camera1"].Status != internal.SourceHealthy {
		t.Errorf("after frame: status = %q, want %q", state.Sources["camera1"].Status, internal.SourceHealthy)
	}
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
	if len(keys) != 2 {
		t.Fatalf("SourceKeys() returned %d keys, want 2", len(keys))
	}

	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	if !keySet["camera1"] || !keySet["camera2"] {
		t.Errorf("SourceKeys() = %v, want [camera1, camera2]", keys)
	}
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
	require.Equal(t, int64(0), snap["idr_gate_events"])
	require.Equal(t, int64(0), snap["last_idr_gate_duration_ms"])
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
	require.Equal(t, int64(1), snap["idr_gate_events"])

	sources = snap["sources"].(map[string]any)
	require.Len(t, sources, 2)

	cam1Info, ok := sources["cam1"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, cam1Info["pending_idr"], "should be pending IDR after cut")

	// Send keyframe to clear IDR gate.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	snap = sw.DebugSnapshot()
	sources = snap["sources"].(map[string]any)
	cam1Info = sources["cam1"].(map[string]any)
	require.Equal(t, false, cam1Info["pending_idr"], "should be cleared after keyframe")
	require.Equal(t, int64(1), cam1Info["video_frames_in"])
	require.GreaterOrEqual(t, snap["last_idr_gate_duration_ms"].(int64), int64(0))

	// Second cut increments idr_gate_events.
	require.NoError(t, sw.Cut(context.Background(), "cam2"))
	snap = sw.DebugSnapshot()
	require.Equal(t, int64(2), snap["idr_gate_events"])
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
	// 30fps = ~33333µs between frames, frame size ~10000 bytes
	for i := 0; i < 20; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS:        int64(i) * 33333,
			IsKeyframe: i == 0,
			WireData:   make([]byte, 10000),
		})
	}

	// Check that stats have been updated
	sw.mu.RLock()
	ss := sw.sources["cam1"]
	avgFrameSize := ss.avgFrameSize
	avgFPS := ss.avgFPS
	frameCount := ss.frameCount
	sw.mu.RUnlock()

	require.Equal(t, 20, frameCount, "should have recorded 20 frames")
	require.InDelta(t, 10000, avgFrameSize, 1000, "avg frame size should be near 10000 bytes")
	require.InDelta(t, 30.0, avgFPS, 3.0, "avg FPS should be near 30")
}

func TestEstimateEncoderParamsDefaults(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	// No frames received yet — should return defaults
	sw.mu.RLock()
	bitrate, fps := sw.estimateEncoderParams("cam1")
	sw.mu.RUnlock()

	require.Equal(t, transition.DefaultBitrate, bitrate, "should return default bitrate with no frames")
	require.Equal(t, transition.DefaultFPS, fps, "should return default FPS with no frames")
}

func TestEstimateEncoderParamsFromFrames(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Simulate a 1080p60 stream at ~8 Mbps
	// 8 Mbps = 8_000_000 bits/s. At 60fps: 8_000_000 / 60 / 8 = ~16667 bytes/frame
	frameSize := 16667
	for i := 0; i < 30; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS:        int64(i) * 16667, // ~60fps in µs
			IsKeyframe: i == 0,
			WireData:   make([]byte, frameSize),
		})
	}

	sw.mu.RLock()
	bitrate, fps := sw.estimateEncoderParams("cam1")
	sw.mu.RUnlock()

	// FPS should be near 60
	require.InDelta(t, 60.0, fps, 5.0, "FPS should be near 60")
	// Bitrate should be near 8 Mbps (frameSize * fps * 8)
	require.InDelta(t, 8_000_000, float64(bitrate), 2_000_000, "bitrate should be near 8 Mbps")
}

func TestEstimateEncoderParamsClamping(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Very tiny frames → should clamp bitrate to 1 Mbps minimum
	for i := 0; i < 20; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS:        int64(i) * 33333,
			IsKeyframe: i == 0,
			WireData:   make([]byte, 100), // tiny frames
		})
	}

	sw.mu.RLock()
	bitrate, fps := sw.estimateEncoderParams("cam1")
	sw.mu.RUnlock()

	require.GreaterOrEqual(t, bitrate, 1_000_000, "bitrate should be clamped to 1 Mbps minimum")
	require.LessOrEqual(t, bitrate, 20_000_000, "bitrate should be clamped to 20 Mbps maximum")
	require.GreaterOrEqual(t, fps, 15.0, "FPS should be clamped to 15 minimum")
	require.LessOrEqual(t, fps, 60.0, "FPS should be clamped to 60 maximum")
}

func TestEstimateEncoderParamsUnknownSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// Unknown source should return defaults
	sw.mu.RLock()
	bitrate, fps := sw.estimateEncoderParams("nonexistent")
	sw.mu.RUnlock()

	require.Equal(t, transition.DefaultBitrate, bitrate)
	require.Equal(t, transition.DefaultFPS, fps)
}

func TestTransitionUsesEstimatedEncoderParams(t *testing.T) {
	// Verify that StartTransition passes estimated params to the engine.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	var capturedBitrate int
	var capturedFPS float32

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			capturedBitrate = bitrate
			capturedFPS = fps
			return transition.NewMockEncoder(), nil
		},
	})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Feed frames to build up stats — simulate 30fps, ~5000 bytes/frame
	for i := 0; i < 20; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS:        int64(i) * 33333,
			IsKeyframe: i == 0,
			WireData:   make([]byte, 5000),
		})
	}

	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start transition — engine should be created with estimated params
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000))

	// Feed a frame to trigger encoder init (lazy)
	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS:        int64(20) * 33333,
		IsKeyframe: true,
		WireData:   make([]byte, 5000),
	})
	time.Sleep(10 * time.Millisecond)

	// The encoder should NOT have been called with default 4Mbps/30fps
	// because the source had frames. Exact values depend on EMA convergence,
	// but bitrate should be near 5000 * 30 * 8 = 1.2 Mbps (clamped to 1 Mbps min).
	require.GreaterOrEqual(t, capturedBitrate, 1_000_000, "bitrate should be at least 1 Mbps")
	require.LessOrEqual(t, capturedBitrate, 20_000_000, "bitrate should be at most 20 Mbps")
	require.InDelta(t, 30.0, capturedFPS, 5.0, "FPS should be near 30")
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
	require.Equal(t, string(internal.SourceOffline), cam1Info["health_status"])
	require.Equal(t, int64(-1), cam1Info["last_frame_ago_ms"])

	// Send a frame (via cut + keyframe).
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	snap = sw.DebugSnapshot()
	sources = snap["sources"].(map[string]any)
	cam1Info = sources["cam1"].(map[string]any)
	require.Equal(t, string(internal.SourceHealthy), cam1Info["health_status"])
	// last_frame_ago_ms should be small (just sent a frame).
	agoMs := cam1Info["last_frame_ago_ms"].(int64)
	require.GreaterOrEqual(t, agoMs, int64(0))
	require.Less(t, agoMs, int64(1000), "should be less than 1s since we just sent a frame")
}
