package switcher

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/transition"
)

func TestMain(m *testing.M) {
	// The production init() sets GOGC=400 for real-time frame processing.
	// In tests, 52+ Switcher instances each allocate frame pools. With
	// GOGC=400, GC runs too infrequently to reclaim pools between tests.
	debug.SetGCPercent(100)
	os.Exit(m.Run())
}

func newTestRelay() *distribution.Relay {
	return distribution.NewRelay()
}

// newTestSwitcher is a convenience alias for NewTestSwitcher within the package.
func newTestSwitcher(t *testing.T, programRelay *distribution.Relay) *Switcher {
	t.Helper()
	return NewTestSwitcher(t, programRelay)
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

// builtinSourceCount is how many virtual sources Switcher.New auto-registers
// (source:black + source:colorbars). Test assertions on the length of
// state.Sources add this to the user-registered count.
const builtinSourceCount = 2

func TestNewSwitcher(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	require.NotNil(t, sw)
	state := sw.State()
	require.Equal(t, "", state.ProgramSource)
	require.Equal(t, "", state.PreviewSource)
	require.Equal(t, "cut", state.TransitionType)
	require.Len(t, state.Sources, builtinSourceCount,
		"only the two built-in virtual sources (black, colorbars) before any RegisterSource call")
}

func TestRegisterSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)

	state := sw.State()
	require.Len(t, state.Sources, 1+builtinSourceCount)
	src, ok := state.Sources["camera1"]
	require.True(t, ok, "Sources missing 'camera1'")
	// Newly registered source with no frames yet shows as offline.
	require.Equal(t, string(SourceOffline), src.Status)
	require.Equal(t, "camera1", src.Key)
}

func TestUnregisterSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	cam1Relay := newTestRelay()

	sw.RegisterSource("camera1", cam1Relay)
	sw.UnregisterSource("camera1")

	state := sw.State()
	require.Len(t, state.Sources, builtinSourceCount,
		"only the built-ins remain after user source unregistered")
}

func TestRegisterVirtualSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)

	state := sw.State()
	require.Len(t, state.Sources, 1+builtinSourceCount)
	src, ok := state.Sources["replay"]
	require.True(t, ok, "Sources should contain 'replay'")
	require.True(t, src.IsVirtual, "replay source should be virtual")
	require.Equal(t, "REPLAY", src.Label, "virtual source label should be uppercased key")
}

func TestRegisterVirtualSource_CutToVirtual(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)
	sw.UnregisterSource("replay")

	state := sw.State()
	require.Len(t, state.Sources, builtinSourceCount,
		"only built-ins remain after virtual source unregistered")
}

func TestRegisterVirtualSource_UnregisterClearsProgram(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)
	// Double-register should not panic or leak
	sw.RegisterVirtualSource("replay", replayRelay)

	state := sw.State()
	require.Len(t, state.Sources, 1+builtinSourceCount, "replay + built-ins only")
	require.True(t, state.Sources["replay"].IsVirtual)
}

func TestUnregisterSource_BroadcastsState(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
		require.Len(t, s.Sources, builtinSourceCount,
			"broadcast should show only the built-ins after user source removed")
	case <-time.After(1 * time.Second):
		require.Fail(t, "no state broadcast on UnregisterSource")
	}
}

func TestUnregisterSource_CleansUpGPUSourceManager(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	cam1Relay := newTestRelay()

	// Track RemoveSource calls.
	var removedKeys []string
	mockMgr := &mockGPUSourceManager{
		onRemove: func(key string) { removedKeys = append(removedKeys, key) },
	}
	sw.SetGPUSourceManager(mockMgr)

	sw.RegisterSource("camera1", cam1Relay)
	sw.UnregisterSource("camera1")

	require.Equal(t, []string{"camera1"}, removedKeys,
		"UnregisterSource should call GPUSourceManager.RemoveSource")
}

// A source delay reaches the GPU source manager, set before or after the
// manager is: the GPU store keeps a delayed source's frames for the delay,
// or a source decoded to the GPU shows its video ahead of its delayed audio.
func TestSourceDelayReachesTheGPUSourceManager(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	sw.RegisterSource("camera1", newTestRelay())
	sw.RegisterSource("camera2", newTestRelay())
	require.NoError(t, sw.SetSourceDelay("camera1", 120))

	delays := map[string]int{}
	sw.SetGPUSourceManager(&mockGPUSourceManager{
		onDelay: func(key string, ms int) { delays[key] = ms },
	})
	require.Equal(t, map[string]int{"camera1": 120}, delays,
		"a delay set before the GPU source manager is not applied to it")

	require.NoError(t, sw.SetSourceDelay("camera2", 300))
	require.NoError(t, sw.SetSourceDelay("camera1", 0))
	require.Equal(t, map[string]int{"camera1": 0, "camera2": 300}, delays,
		"SetSourceDelay does not reach the GPU source manager")
}

// delayMixer is a sync mixer that records the source delays it is told.
type delayMixer struct {
	*mockSyncMixer
	delays map[string]int
}

func (m *delayMixer) SetSourceDelay(key string, ms int) { m.delays[key] = ms }

// A source's delay is the switcher's, by source key: a source removed and
// registered again, as an SRT source was on every reconnect, keeps it in
// the frame sync, the mixer and the GPU source manager alike. A delay kept
// by some of them only delays its audio and not its video.
func TestSourceDelaySurvivesReRegistration(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	mixer := &delayMixer{mockSyncMixer: newMockSyncMixer(2), delays: map[string]int{}}
	sw.SetSyncMixer(mixer)
	gpuDelays := map[string]int{}
	sw.SetGPUSourceManager(&mockGPUSourceManager{onDelay: func(k string, ms int) { gpuDelays[k] = ms }})
	sw.RegisterSRTSource("srt:cam")
	require.NoError(t, sw.SetSourceDelay("srt:cam", 200))

	sw.UnregisterSource("srt:cam")
	clear(mixer.delays)
	clear(gpuDelays)
	sw.RegisterSRTSource("srt:cam")

	require.Equal(t, 200, sw.GetSourceDelay("srt:cam"), "the switcher forgot the source's delay")
	snap := sw.frameSync.DebugSnapshot()["sources"].(map[string]any)["srt:cam"].(map[string]any)
	require.Equal(t, int64(200*90), snap["delay_90k"], "the frame sync reads the source undelayed")
	require.Equal(t, map[string]int{"srt:cam": 200}, mixer.delays, "the mixer is not given the source's delay")
	require.Equal(t, map[string]int{"srt:cam": 200}, gpuDelays, "the GPU source manager is not given the source's delay")
}

// A delay set while a GPU source manager is swapped in, as at a GPU
// rebuild, reaches the manager that ends up installed: one set between
// the new manager taking the delays and its installation reached neither
// manager, and the store kept too few frames until the next change. The
// delay here is set while the new manager is being given the delays.
func TestSourceDelaySetDuringGPUSwapReachesTheNewManager(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	sw.RegisterSRTSource("srt:cam")
	require.NoError(t, sw.SetSourceDelay("srt:cam", 120))

	var mu sync.Mutex
	got := 0
	set := make(chan struct{})
	var once sync.Once
	mgr := &mockGPUSourceManager{onDelay: func(_ string, ms int) {
		mu.Lock()
		got = ms
		mu.Unlock()
		once.Do(func() {
			go func() {
				_ = sw.SetSourceDelay("srt:cam", 300)
				close(set)
			}()
			// Give the concurrent SetSourceDelay the chance to run inside
			// the swap; a swap that holds it off until it is done is right.
			select {
			case <-set:
			case <-time.After(50 * time.Millisecond):
			}
		})
	}}
	sw.SetGPUSourceManager(mgr)
	<-set

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 300, sw.GetSourceDelay("srt:cam"))
	require.Equal(t, 300, got, "a delay set during the GPU swap did not reach the installed manager")
}

// mockGPUSourceManager implements GPUSourceManagerIface for testing.
type mockGPUSourceManager struct {
	onRemove func(key string)
	onDelay  func(key string, delayMs int)
}

func (m *mockGPUSourceManager) SetSourceDelay(sourceKey string, delayMs int) {
	if m.onDelay != nil {
		m.onDelay(sourceKey, delayMs)
	}
}

func (m *mockGPUSourceManager) IngestYUV(sourceKey string, yuv []byte, w, h int, pts int64) {}
func (m *mockGPUSourceManager) RemoveSource(sourceKey string) {
	if m.onRemove != nil {
		m.onRemove(sourceKey)
	}
}

func TestRegisterVirtualSource_SkipsDelayAndFrameSync(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	replayRelay := newTestRelay()

	sw.RegisterVirtualSource("replay", replayRelay)

	// Virtual source should have zero delay
	state := sw.State()
	src := state.Sources["replay"]
	require.Equal(t, 0, src.DelayMs, "virtual source should have zero delay")
}

func TestCutToSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)

	err := sw.Cut(context.Background(), "nonexistent")
	require.Error(t, err)
}

func TestCutToCurrentProgramIsNoop(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)

	err := sw.SetPreview(context.Background(), "nonexistent")
	require.Error(t, err)
}

func TestAudioFrameForwarding(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

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

func TestCutAudioForwardedImmediately(t *testing.T) {
	// In always-decode mode, there is no IDR gating — audio from the new
	// program source is forwarded immediately after a cut.
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

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
	sw := newTestSwitcher(t, programRelay)

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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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

func (m *mockAudioStateProvider) LimiterSettings() (float64, float64, float64, float64) {
	return -1.0, 0.0, m.gainReduction, -144.0
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("camera1", cam1Relay)
	sw.RegisterSource("camera2", cam2Relay)

	keys := sw.SourceKeys()
	// 2 user sources + 2 built-ins.
	require.Len(t, keys, 2+builtinSourceCount)
	require.Contains(t, keys, "camera1")
	require.Contains(t, keys, "camera2")
	require.Contains(t, keys, BuiltinSourceKeyBlack)
	require.Contains(t, keys, BuiltinSourceKeyColorbars)
}

func TestAllAudioRoutedToMixer(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	// 2 built-ins are always present on a fresh switcher.
	require.Len(t, sources, builtinSourceCount)

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
	require.Len(t, sources, 2+builtinSourceCount)

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
	sw := newTestSwitcher(t, programRelay)
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
	sw.configMu.Lock()
	ss := (*sw.sourcesAtomic.Load())["cam1"]
	sw.configMu.Unlock()
	avgFrameSize := ss.getAvgFrameSize()
	avgFPS := ss.getAvgFPS()
	frameCount := ss.getFrameCount()

	require.Equal(t, 20, frameCount, "should have recorded 20 frames")
	require.InDelta(t, 10000, avgFrameSize, 1000, "avg frame size should be near 10000 bytes")
	require.InDelta(t, 30.0, avgFPS, 3.0, "avg FPS should be near 30")
}

func TestSwitcher_DebugSnapshot_HealthStatus(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	atomicutil.UpdateMax(&field, 100)
	require.Equal(t, int64(100), field.Load())

	// Larger value replaces.
	atomicutil.UpdateMax(&field, 200)
	require.Equal(t, int64(200), field.Load())

	// Smaller value is a no-op.
	atomicutil.UpdateMax(&field, 50)
	require.Equal(t, int64(200), field.Load())

	// Equal value is a no-op.
	atomicutil.UpdateMax(&field, 200)
	require.Equal(t, int64(200), field.Load())
}

func TestDebugSnapshot_PipelineStageTiming(t *testing.T) {
	// In always-decode mode, frames arrive as raw YUV. The Pipeline struct
	// tracks per-node timing via atomic.Int64. Verify the pipeline snapshot
	// appears in DebugSnapshot after processing a frame.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)
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

	// Run counts a run after its last node, and the encode node
	// broadcasts before that, so a frame at the viewer does not yet mean
	// run_count has moved: wait for the count itself.
	require.Eventually(t, func() bool {
		ps, ok := sw.DebugSnapshot()["pipeline"].(map[string]any)
		return ok && ps["run_count"].(int64) > 0
	}, 2*time.Second, 5*time.Millisecond, "run_count should be > 0 after processing a frame")

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

func TestSwitcher_SetCodecInfo(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Before SetCodecInfo, codec section should still be present with zero values.
	snap := sw.DebugSnapshot()
	codecInfo, ok := snap["codec"].(map[string]any)
	require.True(t, ok, "codec section should be present in DebugSnapshot")
	require.Equal(t, "", codecInfo["encoder"])
	require.Equal(t, "", codecInfo["decoder"])
	require.Equal(t, false, codecInfo["hw_accel"])

	// Set codec info.
	sw.SetCodecInfo("libx264", "h264", false)

	snap = sw.DebugSnapshot()
	codecInfo = snap["codec"].(map[string]any)
	require.Equal(t, "libx264", codecInfo["encoder"])
	require.Equal(t, "h264", codecInfo["decoder"])
	require.Equal(t, false, codecInfo["hw_accel"])

	// Update with hardware acceleration.
	sw.SetCodecInfo("h264_nvenc", "h264_cuvid", true)

	snap = sw.DebugSnapshot()
	codecInfo = snap["codec"].(map[string]any)
	require.Equal(t, "h264_nvenc", codecInfo["encoder"])
	require.Equal(t, "h264_cuvid", codecInfo["decoder"])
	require.Equal(t, true, codecInfo["hw_accel"])
}

func TestDebugSnapshot_PerSourceDecoderStats(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Set up a source decoder factory so RegisterSource creates decoders.
	sw.SetSourceDecoderFactory(func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	})

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send some frames to populate decoder stats.
	for i := 0; i < 10; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS:        int64(i) * 3000,
			IsKeyframe: i == 0,
			WireData:   make([]byte, 5000),
		})
	}

	// Allow decode goroutine to process frames.
	time.Sleep(50 * time.Millisecond)

	snap := sw.DebugSnapshot()
	sources := snap["sources"].(map[string]any)
	cam1Info := sources["cam1"].(map[string]any)

	// Decoder should be active.
	require.Equal(t, true, cam1Info["decoder_active"])

	// Stats should be populated (avgFrameSize > 0 after 10 frames).
	avgBytes, ok := cam1Info["decoder_avg_frame_bytes"].(int)
	require.True(t, ok, "decoder_avg_frame_bytes should be an int")
	require.Greater(t, avgBytes, 0, "decoder_avg_frame_bytes should be > 0 after frames sent")

	// FPS should be populated (need at least 2 frames for FPS calc).
	avgFPS, ok := cam1Info["decoder_avg_fps"].(float64)
	require.True(t, ok, "decoder_avg_fps should be a float64")
	require.Greater(t, avgFPS, 0.0, "decoder_avg_fps should be > 0 after frames sent")
}

func TestOutputFPSTracking(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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

	sw := newTestSwitcher(t, programRelay)
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	// Cut to cam1
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send frames with HIGH GroupID
	send := func(key string, gid uint32, keyframe bool) {
		sw.IngestRawVideo(key, &ProcessingFrame{
			Data: make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), Width: 4, Height: 4,
			IsKeyframe: keyframe, GroupID: gid, Codec: "h264",
		})
	}
	send("cam1", 50, true)
	send("cam1", 50, false)
	waitFrames := func(n int) {
		require.Eventually(t, func() bool { return len(viewer.VideoFrames()) >= n },
			time.Second, 5*time.Millisecond)
	}
	waitFrames(clock.ReaderDelayGrains + 2)

	// Cut to cam2 — has LOWER GroupID
	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	// Keyframe with LOW GroupID
	send("cam2", 10, true)
	send("cam2", 10, false)
	waitFrames(len(viewer.VideoFrames()) + clock.ReaderDelayGrains + 2)

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
	sw := tickSwitcher(t)

	// Initially 0 — no frames broadcast yet.
	require.Equal(t, int64(0), sw.LastBroadcastVideoPTS())

	// Cut to cam1 and show frames.
	onProgram(t, sw, "mxl:cam1")
	g := grainNow(sw)
	sw.IngestRawVideo("mxl:cam1", mxlFrame(sw, g, 50))
	sw.processVideoTick(tickAt(sw, g+clock.ReaderDelayGrains))
	require.Equal(t, grainPTS(sw, g+clock.ReaderDelayGrains), sw.LastBroadcastVideoPTS(),
		"expected PTS to track broadcast")

	sw.processVideoTick(tickAt(sw, g+clock.ReaderDelayGrains+1))
	require.Equal(t, grainPTS(sw, g+clock.ReaderDelayGrains+1), sw.LastBroadcastVideoPTS(),
		"expected PTS to update on each broadcast")
}

func TestIngestRawVideo_NilViewer(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Register an MXL-style source (nil viewer, nil relay).
	sw.RegisterMXLSource("mxl:cam1")
	require.NoError(t, sw.SetPreview(context.Background(), "mxl:cam1"))
	require.NoError(t, sw.Cut(context.Background(), "mxl:cam1"))

	// Should not panic — viewer is nil for MXL sources.
	pf := &ProcessingFrame{
		Data:  make([]byte, ColorYUV420_8bit.FrameSize(1920, 1080)),
		Width: 1920, Height: 1080,
		PTS: 90000, DTS: 90000,
	}
	require.NotPanics(t, func() {
		sw.IngestRawVideo("mxl:cam1", pf)
	})
}

func TestSwitcher_E2ELatency(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	// Set up always-decode mode with mock decoder.
	sw.SetSourceDecoderFactory(func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	})

	// Set up pipeline with mock encoder so frames flow through the sync loop.
	sw.SetPipelineCodecs(func(width, height, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	require.NoError(t, sw.BuildPipeline())

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send a keyframe: relay -> sourceViewer.SendVideo (stamps arrivalNano)
	// -> sourceDecoder -> IngestRawVideo -> frame sync -> sync loop tick
	// (measures E2E).
	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		Codec:      "h264",
		GroupID:    1,
	})

	// Wait for async pipeline to process.
	deadline := time.After(3 * time.Second)
	for sw.lastE2ENs.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for E2E latency to be recorded")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	lastE2E := sw.lastE2ENs.Load()
	maxE2E := sw.maxE2ENs.Load()
	require.Greater(t, lastE2E, int64(0), "lastE2ENs should be > 0")
	require.Greater(t, maxE2E, int64(0), "maxE2ENs should be > 0")
	require.GreaterOrEqual(t, maxE2E, lastE2E, "maxE2ENs should be >= lastE2ENs")
}

func TestRegisterReplaySource(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	sw.RegisterReplaySource("replay")

	state := sw.State()
	require.Len(t, state.Sources, 1+builtinSourceCount)
	src, ok := state.Sources["replay"]
	require.True(t, ok)
	require.True(t, src.IsVirtual, "replay source should be virtual")
	require.Equal(t, "REPLAY", src.Label)
}

func TestRegisterReplaySource_CleansUpOld(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	sw.RegisterReplaySource("replay")
	sw.RegisterReplaySource("replay")

	state := sw.State()
	require.Len(t, state.Sources, 1+builtinSourceCount)
}

func TestIngestReplayVideo_RoutesToPipeline(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	sw.RegisterReplaySource("replay")
	require.NoError(t, sw.SetPreview(context.Background(), "replay"))
	require.NoError(t, sw.Cut(context.Background(), "replay"))

	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(320, 240)),
		Width:      320,
		Height:     240,
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
	}

	// Should not panic: routes through IngestRawVideo.
	require.NotPanics(t, func() {
		sw.IngestReplayVideo("replay", pf)
	})
}

func TestCloseWithFrameSyncActive(t *testing.T) {
	// Close() while the sync loop ticks over a source with buffered frames
	// must not panic. Repeated to hit the window between the clock stopping
	// and the frame sync releasing its frames.
	//
	// Reduced from 20 to 5 iterations: New() allocates a FramePool with
	// 512 × 1080p buffers (~1.5 GB). With GOGC=400, 20 iterations can OOM
	// before GC reclaims closed pools. 5 iterations still exercises the race
	// while staying under memory limits.
	for i := 0; i < 5; i++ {
		programRelay := newTestRelay()
		sw := newTestSwitcher(t, programRelay)

		cam1Relay := newTestRelay()
		sw.RegisterSource("camera1", cam1Relay)
		_ = sw.Cut(context.Background(), "camera1")

		// Feed some raw frames through the frame sync so it has data to tick.
		for j := 0; j < 5; j++ {
			pf := &ProcessingFrame{
				Data:       make([]byte, ColorYUV420_8bit.FrameSize(320, 240)),
				Width:      320,
				Height:     240,
				PTS:        int64(j * 3000),
				IsKeyframe: j == 0,
			}
			sw.IngestRawVideo("camera1", pf)
		}

		// Let the sync loop tick a few times.
		time.Sleep(30 * time.Millisecond)

		// Close() must not panic even though the sync loop is ticking.
		require.NotPanics(t, func() {
			sw.Close()
		}, "Close() panicked on iteration %d", i)

		// Force GC to reclaim the ~1.5 GB frame pool before next iteration.
		runtime.GC()
	}
}

func TestUpdateFrameStats_90kHzPTS(t *testing.T) {
	// Bug A4: updateFrameStats used 1,000,000 (microseconds) as the PTS
	// timebase, but PTS throughout the codebase is in 90kHz clock units
	// (standard MPEG-TS). At 29.97fps, the PTS delta between frames is
	// 90000/29.97 ~= 3003 ticks. With the wrong timebase (1M), FPS would
	// be computed as 1,000,000/3003 ~= 333 fps instead of ~29.97 fps.

	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	require.InDelta(t, 29.97, ss.getAvgFPS(), 1.0,
		"avgFPS should be ~29.97 for 90kHz PTS deltas of 3003, got %f", ss.getAvgFPS())
}

func TestUpdateFrameStats_RejectsUnreasonableDelta(t *testing.T) {
	// Verify that deltas > 1 second (90000 ticks) are rejected.
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
	fpsAfterNormal := ss.getAvgFPS()
	require.InDelta(t, 29.97, fpsAfterNormal, 1.0)

	// Third frame with a huge PTS jump (>90000 ticks = >1 second).
	// This should be rejected and avgFPS should remain unchanged.
	sw.updateFrameStats(ss, &media.VideoFrame{
		PTS: 3003 + 200000, WireData: make([]byte, 50000),
	})
	require.InDelta(t, fpsAfterNormal, ss.getAvgFPS(), 0.01,
		"avgFPS should be unchanged after rejecting unreasonable delta")
}

func TestUpdateFrameStats_ConcurrentReadWrite(t *testing.T) {
	// Verify that concurrent reads of sourceState frame stats don't race
	// with updateFrameStats writes. The writer (source viewer goroutine)
	// calls updateFrameStats, while readers (state broadcast, debug snapshot)
	// access the same fields via sourceState.
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	ss := &sourceState{key: "test"}

	const iterations = 1000
	var wg sync.WaitGroup

	// Writer goroutine: simulate source viewer calling updateFrameStats.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			frame := &media.VideoFrame{
				PTS:      int64(i * 3003),
				WireData: make([]byte, 50000),
			}
			sw.updateFrameStats(ss, frame)
		}
	}()

	// Reader goroutine: simulate buildStateLocked / DebugSnapshot reading stats.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = ss.getAvgFrameSize()
			_ = ss.getAvgFPS()
			_ = ss.getLastPTS()
			_ = ss.getFrameCount()
		}
	}()

	wg.Wait()

	// After all frames, stats should be populated.
	require.Greater(t, ss.getFrameCount(), 0, "frameCount should be > 0")
	require.Greater(t, ss.getAvgFrameSize(), 0.0, "avgFrameSize should be > 0")
}

func TestCutDuringActiveTransition_AbortsThenCuts(t *testing.T) {
	// Bug: Cut() doesn't check for active transition. handleTransitionComplete
	// later overwrites the cut's programSource with the transition's toSource.
	//
	// Scenario: transition A→B is active. User calls Cut(cam3). Expected:
	// transition is aborted, programSource becomes cam3 and stays cam3 even
	// after the transition engine fires its OnComplete callback.
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	cam3Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.RegisterSource("cam3", cam3Relay)

	// Cut to cam1 first (set up initial program source).
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.Equal(t, "cam1", sw.State().ProgramSource)

	// Set up a minimal pipelineCodecs so StartTransition doesn't nil-deref.
	sw.pipeCodecs = &pipelineCodecs{}

	// Configure transition support with no-op decoder factory.
	sw.transConfig = &TransitionConfig{
		DecoderFactory: nil, // raw YUV mode
	}

	// Start a long transition from cam1 → cam2 (60 seconds so it won't complete).
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// Verify transition is active.
	state := sw.State()
	require.True(t, state.InTransition, "transition should be active")

	// Now cut to cam3 mid-transition.
	err = sw.Cut(context.Background(), "cam3")
	require.NoError(t, err)

	// programSource must be cam3.
	state = sw.State()
	require.Equal(t, "cam3", state.ProgramSource, "Cut should set program to cam3")
	require.False(t, state.InTransition, "transition should be aborted after Cut")

	// Simulate what would happen if the transition engine's OnComplete fires
	// (which it would have, for the now-stopped engine). The engine calls
	// handleTransitionComplete(aborted=false). Since we already cleared the
	// engine, this should be a no-op. Let's verify programSource is still cam3.
	sw.handleTransitionComplete(false)

	state = sw.State()
	require.Equal(t, "cam3", state.ProgramSource,
		"handleTransitionComplete must not overwrite programSource after Cut aborted the transition")
}

func TestSwitcher_SetEncoder(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	require.NoError(t, sw.BuildPipeline())

	avail := []codec.EncoderInfo{
		{Name: "libx264", DisplayName: "x264 (Software)", IsDefault: true},
		{Name: "h264_nvenc", DisplayName: "NVENC (CUDA)", IsDefault: false},
	}
	sw.SetAvailableEncoders(avail)
	sw.SetCodecInfo("libx264", "h264", false)

	require.Equal(t, "libx264", sw.EncoderName())

	// Switch to a valid encoder.
	err := sw.SetEncoder("h264_nvenc")
	require.NoError(t, err)
	require.Equal(t, "h264_nvenc", sw.EncoderName())

	// Switch to invalid encoder — should fail with sentinel error.
	err = sw.SetEncoder("nonexistent")
	require.ErrorIs(t, err, ErrEncoderNotAvailable)
	require.Equal(t, "h264_nvenc", sw.EncoderName(), "should not have changed")

	// AvailableEncoders should return the list.
	got := sw.AvailableEncoders()
	require.Len(t, got, 2)
	require.Equal(t, "libx264", got[0].Name)
}

func TestSwitcher_SetEncoder_RejectsDuringTransition(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	require.NoError(t, sw.BuildPipeline())

	avail := []codec.EncoderInfo{
		{Name: "libx264", DisplayName: "x264 (Software)", IsDefault: true},
		{Name: "h264_nvenc", DisplayName: "NVENC (CUDA)", IsDefault: false},
	}
	sw.SetAvailableEncoders(avail)
	sw.SetCodecInfo("libx264", "h264", false)

	// Register sources and cut to set up program.
	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Start a long transition.
	sw.transConfig = &TransitionConfig{DecoderFactory: nil}
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// SetEncoder during transition should be rejected.
	err = sw.SetEncoder("h264_nvenc")
	require.ErrorIs(t, err, ErrFormatDuringTransition)
}

func TestState_KeyFillTally(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	cam3Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.RegisterSource("cam3", cam3Relay)

	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	sw.SetKeyBridge(bridge)

	kp.SetKey("cam1", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		FillSource: "cam3",
	})

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	state := sw.State()
	require.Equal(t, string(TallyProgram), state.TallyState["cam1"],
		"cam1 is on program; program must take precedence over key_alpha")
	require.Equal(t, string(TallyPreview), state.TallyState["cam2"])
	require.Equal(t, string(TallyKeyFill), state.TallyState["cam3"],
		"cam3 is fill for enabled key, must be tallied as key_fill (RED)")
}

func TestState_KeyFillSelfFillSourceTallied(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	sw.SetKeyBridge(bridge)

	// cam1 has no explicit FillSource — itself acts as fill and alpha.
	kp.SetKey("cam1", graphics.KeyConfig{Type: graphics.KeyTypeChroma, Enabled: true})

	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	state := sw.State()
	require.Equal(t, string(TallyKeyFill), state.TallyState["cam1"],
		"cam1 is self-fill for its enabled key, must be tallied as key_fill (RED)")
}

func TestState_KeyFillOnPreviewIsRedNotGreen(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	cam3Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.RegisterSource("cam3", cam3Relay)

	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	sw.SetKeyBridge(bridge)

	kp.SetKey("cam2", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		FillSource: "cam3",
	})

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam3"))

	state := sw.State()
	require.Equal(t, string(TallyKeyFill), state.TallyState["cam3"],
		"cam3 is both on preview and a key fill; key_fill must win (RED not GREEN) because it's visibly on-air via the enabled key")
}

func TestState_KeyAlphaTallied(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	cam3Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	sw.RegisterSource("cam3", cam3Relay)

	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	sw.SetKeyBridge(bridge)

	kp.SetKey("cam1", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		FillSource: "cam3",
	})

	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	state := sw.State()
	require.Equal(t, string(TallyKeyAlpha), state.TallyState["cam1"],
		"cam1 is the alpha input of an enabled key, must be tallied as key_alpha (amber)")
	require.Equal(t, string(TallyKeyFill), state.TallyState["cam3"])
}

// TestSwitcher_SetInitialCommandSeq_SeedsTracker verifies that the startup
// seed path advances lastCommandSeq to the persisted value before any API
// traffic runs, and that the seed does NOT mark seqDirty (the value came
// from disk — nothing to write back).
func TestSwitcher_SetInitialCommandSeq_SeedsTracker(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())

	if got := sw.State().LastCommandSeq; got != 0 {
		t.Fatalf("fresh switcher LastCommandSeq = %d, want 0", got)
	}

	sw.SetInitialCommandSeq(50)
	if got := sw.State().LastCommandSeq; got != 50 {
		t.Fatalf("after SetInitialCommandSeq(50): LastCommandSeq = %d, want 50", got)
	}
	if sw.seqDirty.Load() {
		t.Error("seqDirty must be false after seed — the value came from disk")
	}

	// SetInitialCommandSeq must never regress a higher current value.
	sw.TrackCommandSeq(75)
	sw.SetInitialCommandSeq(40) // lower than current 75
	if got := sw.State().LastCommandSeq; got != 75 {
		t.Errorf("SetInitialCommandSeq regressed 75→%d on a lower seed — must be monotonic", got)
	}
}

// TestSwitcher_TrackCommandSeq_PersistsViaDebounce pins the persist contract:
// TrackCommandSeq advances trigger the debounced disk write, and only the
// latest seq within a ≥100 ms window is flushed (coalescing a CUT storm to a
// single write).
func TestSwitcher_TrackCommandSeq_PersistsViaDebounce(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())

	var mu sync.Mutex
	var writes []uint64
	sw.SetSeqPersistFn(func(seq uint64) error {
		mu.Lock()
		writes = append(writes, seq)
		mu.Unlock()
		return nil
	})

	// Burst 20 advances within the 100ms tick window — all should coalesce.
	for i := uint64(1); i <= 20; i++ {
		sw.TrackCommandSeq(i)
	}

	// Wait for at least one persist tick (debounce is 100ms).
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	got := append([]uint64(nil), writes...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no persist calls after 250ms — debounce loop never fired")
	}
	// The final write MUST be 20 (the highest advance). Intermediate writes
	// are acceptable but the high-water mark must land.
	if got[len(got)-1] != 20 {
		t.Errorf("final persist seq = %d, want 20 (latest advance)", got[len(got)-1])
	}
	// Coalescing check: the 20 burst advances should not produce 20 writes.
	if len(got) > 5 {
		t.Errorf("persist fired %d times for 20 burst advances — debounce not coalescing", len(got))
	}

	// No further advances: persist must stop firing after the seq is flushed.
	mu.Lock()
	mark := len(writes)
	mu.Unlock()
	time.Sleep(250 * time.Millisecond)
	mu.Lock()
	after := len(writes)
	mu.Unlock()
	if after != mark {
		t.Errorf("persist fired %d extra times with no advances — dirty flag not cleared", after-mark)
	}
}

// TestClockSyncState_BroadcastInControlRoomState — discipline source and flag
// broadcast must carry a real ClockSync reading through to subscribers so
// the UI can surface PTP/NTP discipline failures; historically the field
// did not exist and the raw clock.GetSyncState response was hardcoded.
// Install a test fixture via the clock.SetGetSyncStateFn seam that reports
// unsynchronized and assert it reaches ControlRoomState unchanged.
func TestClockSyncState_BroadcastInControlRoomState(t *testing.T) {
	prev := clock.SetGetSyncStateFn(func() clock.SyncState {
		return clock.SyncState{
			Synchronized:  false,
			WindowDriftUs: 12345,
			Source:        "test-unsynced",
		}
	})
	defer clock.SetGetSyncStateFn(prev)

	sw := newTestSwitcher(t, newTestRelay())

	got := sw.State().ClockSync
	if got.Synchronized {
		t.Errorf("ClockSync.Synchronized = true; mock returned false")
	}
	if got.WindowDriftUs != 12345 {
		t.Errorf("ClockSync.WindowDriftUs = %d, want 12345", got.WindowDriftUs)
	}
	if got.Source != "test-unsynced" {
		t.Errorf("ClockSync.Source = %q, want test-unsynced", got.Source)
	}
}

// TestSwitcher_SeqPersistFlushOnClose asserts that a final flush runs when
// Close shuts down the persist loop, so an advance that landed inside the
// current debounce window is not lost.
func TestSwitcher_SeqPersistFlushOnClose(t *testing.T) {
	sw := NewTestSwitcher(t, newTestRelay())

	var mu sync.Mutex
	var writes []uint64
	sw.SetSeqPersistFn(func(seq uint64) error {
		mu.Lock()
		writes = append(writes, seq)
		mu.Unlock()
		return nil
	})

	// Advance and IMMEDIATELY close — within the 100 ms debounce window.
	sw.TrackCommandSeq(42)
	sw.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(writes) == 0 {
		t.Fatal("Close did not trigger final flush — advance lost")
	}
	if writes[len(writes)-1] != 42 {
		t.Errorf("final flush seq = %d, want 42", writes[len(writes)-1])
	}
}

// A browser keeps the state snapshot with the latest Timestamp, so two
// snapshots must never share one: a burst of broadcasts within one
// millisecond, such as the end of a clip upload, would otherwise tie and
// leave the browser on whichever arrived last.
func TestState_TimestampsStrictlyIncrease(t *testing.T) {
	sw := newTestSwitcher(t, newTestRelay())
	defer sw.Close()

	var wg sync.WaitGroup
	stamps := make([][]int64, 4)
	for g := range stamps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				stamps[g] = append(stamps[g], sw.State().Timestamp)
			}
		}()
	}
	wg.Wait()

	seen := make(map[int64]bool)
	for _, own := range stamps {
		for i, ts := range own {
			require.False(t, seen[ts], "timestamp %d returned twice", ts)
			seen[ts] = true
			if i > 0 {
				require.Greater(t, ts, own[i-1], "timestamps of one goroutine went backwards")
			}
		}
	}
	now := time.Now().UnixMilli()
	for ts := range seen {
		require.LessOrEqual(t, ts, now+int64(len(seen)), "timestamp ran ahead of the clock")
	}
}
