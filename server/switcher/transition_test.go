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

// mockTransitionCodecs returns factories that produce 4x4 mock decoders/encoders.
func mockTransitionCodecs() TransitionConfig {
	return TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}
}

// mockAudioTransHandler records calls to audio transition methods.
type mockAudioTransHandler struct {
	mu              sync.Mutex
	startCalls      []audioTransStartCall
	positionCalls   []float64
	completionCount int
	programMuted    bool
}

type audioTransStartCall struct {
	oldSrc     string
	newSrc     string
	mode       audio.TransitionMode
	durationMs int
}

func (m *mockAudioTransHandler) OnTransitionStart(oldSource, newSource string, mode audio.TransitionMode, durationMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls = append(m.startCalls, audioTransStartCall{oldSource, newSource, mode, durationMs})
}

func (m *mockAudioTransHandler) OnTransitionPosition(position float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positionCalls = append(m.positionCalls, position)
}

func (m *mockAudioTransHandler) OnTransitionComplete() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completionCount++
}

func (m *mockAudioTransHandler) SetProgramMute(muted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.programMuted = muted
}

func (m *mockAudioTransHandler) SetStingerAudio(audio []float32, sampleRate, channels int) {}


// setupSwitcherWithTransition creates a switcher with two sources, program on cam1,
// preview on cam2, and transition config ready.
func setupSwitcherWithTransition(t *testing.T) (*Switcher, *mockProgramViewer) {
	t.Helper()

	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
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

	// Set cam1 as program, cam2 as preview
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	return sw, viewer
}

func TestSwitcherStartTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.NoError(t, err)

	state := sw.State()
	require.True(t, state.InTransition)
	require.Equal(t, "mix", state.TransitionType)
	require.Equal(t, "cam2", state.PreviewSource)
}

func TestSwitcherStartTransitionNoPreview(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true})

	// No target specified
	err := sw.StartTransition(context.Background(), "", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no target source")
}

func TestSwitcherCannotDoubleTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.NoError(t, err)

	// Second transition should fail
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already active")
}

func TestSwitcherTransitionRoutesFramesToEngine(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Track output frames before transition
	viewer.mu.Lock()
	preTransitionCount := len(viewer.videos)
	viewer.mu.Unlock()

	// Start a long transition so it doesn't auto-complete
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// Now send frames from both sources — they should go to the engine
	// and produce blended output on the program relay.
	// The engine needs frames from "from" source first, then "to" source triggers blend.
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 101, IsKeyframe: true, WireData: []byte{0x02}})

	// Give a moment for frames to flow through
	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	postTransitionCount := len(viewer.videos)
	viewer.mu.Unlock()

	// Should have at least one new blended frame from the engine
	require.Greater(t, postTransitionCount, preTransitionCount,
		"transition engine should produce blended output frames")
}

func TestSwitcherTransitionCompletion(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start a transition with manual control (long duration)
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// Complete via SetPosition(1.0)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)

	// Give completion callback a moment to run
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.False(t, state.InTransition, "transition should be complete")
	require.Equal(t, "cam2", state.ProgramSource, "program should swap to cam2")
	require.Equal(t, "cam1", state.PreviewSource, "preview should swap to cam1")
}

func TestSwitcherFTBToggle(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	state := sw.State()
	require.True(t, state.InTransition)
	require.True(t, state.FTBActive)
	require.Equal(t, "ftb", state.TransitionType)
}

func TestSwitcherFTBRejectsWhileMixActive(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start a mix transition
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// FTB should be rejected
	err = sw.FadeToBlack(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot FTB while mix/dip transition is active")
}

func TestSwitcherMixRejectsWhileFTBActive(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	// Mix should be rejected while FTB is active (inTransition=true from FTB start)
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.Error(t, err)
	// Could match "already active" or "FTB is active" — the FTB sets inTransition=true
	require.Error(t, err)
}

func TestSwitcherSetTransitionPosition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// Set position via T-bar
	err = sw.SetTransitionPosition(context.Background(), 0.5)
	require.NoError(t, err)

	// Audio handler should have been notified
	audioTrans.mu.Lock()
	require.Len(t, audioTrans.positionCalls, 1)
	require.Equal(t, 0.5, audioTrans.positionCalls[0])
	audioTrans.mu.Unlock()
}

func TestSwitcherSetTransitionPositionNoTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// No active transition
	err := sw.SetTransitionPosition(context.Background(), 0.5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no active transition")
}

func TestSwitcherStartTransitionNotConfigured(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// No transition config set
	err := sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition not configured")
}

func TestSwitcherStartTransitionSourceNotFound(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "nonexistent", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestSwitcherStartTransitionSameSource(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// cam1 is already program — transitioning to cam1 should be rejected
	err := sw.StartTransition(context.Background(), "cam1", "mix", 500, "")
	require.ErrorIs(t, err, ErrAlreadyOnProgram)
}

func TestSwitcherStartTransitionUnsupportedType(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "fade", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported transition type")
}

func TestSwitcherStartWipeTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "wipe", 500, "h-left")
	require.NoError(t, err)

	state := sw.State()
	require.True(t, state.InTransition)
	require.Equal(t, "wipe", state.TransitionType)
}

func TestSwitcherStartWipeInvalidDirection(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "wipe", 500, "diagonal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid wipe direction")
}

func TestSwitcherStartWipeMissingDirection(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "wipe", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid wipe direction")
}

func TestSwitcherAbortTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	sw.AbortTransition()

	state := sw.State()
	require.False(t, state.InTransition)
	require.Equal(t, "cam1", state.ProgramSource, "program should stay cam1 after abort")

	audioTrans.mu.Lock()
	require.Equal(t, 1, audioTrans.completionCount)
	audioTrans.mu.Unlock()
}

func TestSwitcherFTBCompleteKeepsActive(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	// Complete it by setting position to 1.0
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)

	// Give callback time to run
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.False(t, state.InTransition, "transition itself should be done")
	require.True(t, state.FTBActive, "FTB should stay active (screen is black)")

	// Toggle FTB off -- now starts a reverse FTB transition
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	state = sw.State()
	require.True(t, state.InTransition, "reverse FTB transition should be active")
	require.True(t, state.FTBActive, "FTB should stay active during reverse transition")
	require.Equal(t, "ftb_reverse", state.TransitionType)

	// Complete the reverse transition
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	state = sw.State()
	require.False(t, state.InTransition, "reverse transition should be complete")
	require.False(t, state.FTBActive, "FTB should be toggled off after reverse completes")
}

func TestSwitcherTransitionAudioNotified(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	err := sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.NoError(t, err)

	audioTrans.mu.Lock()
	require.Len(t, audioTrans.startCalls, 1)
	require.Equal(t, "cam1", audioTrans.startCalls[0].oldSrc)
	require.Equal(t, "cam2", audioTrans.startCalls[0].newSrc)
	require.Equal(t, 500, audioTrans.startCalls[0].durationMs)
	audioTrans.mu.Unlock()
}

func TestSwitcherStartTransitionNoProgramSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	// No program source set — don't cut to anything
	err := sw.StartTransition(context.Background(), "cam1", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no program source set")
}

func TestSwitcherFTBNotConfigured(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	// No transition config
	err := sw.FadeToBlack(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition not configured")
}

func TestSwitcherTransitionStateCallback(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	var mu sync.Mutex
	var lastState internal.ControlRoomState
	var stateChanges int

	// Register callback before starting transition (after setup callbacks)
	sw.OnStateChange(func(state internal.ControlRoomState) {
		mu.Lock()
		lastState = state
		stateChanges++
		mu.Unlock()
	})

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	mu.Lock()
	require.GreaterOrEqual(t, stateChanges, 1)
	require.True(t, lastState.InTransition)
	require.Equal(t, "mix", lastState.TransitionType)
	mu.Unlock()
}

func TestSwitcherFTBNoProgramSource(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	// No program source set
	err := sw.FadeToBlack(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no program source set")
}

func TestSwitcherFTBToggleOffCreatesReverseTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	// Complete the FTB via position
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.True(t, state.FTBActive, "FTB should be active (screen is black)")
	require.False(t, state.InTransition, "transition should be complete")

	// Toggle FTB off -- should create a reverse FTB transition
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	state = sw.State()
	require.True(t, state.InTransition, "reverse FTB transition should be active")
	require.True(t, state.FTBActive, "FTB should still be active during reverse")
	require.Equal(t, "ftb_reverse", state.TransitionType)
}

func TestSwitcherFTBReverseCompletionClearsFTBActive(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB and complete it
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	// Toggle FTB off (starts reverse transition)
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	state := sw.State()
	require.True(t, state.InTransition)

	// Complete the reverse transition
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	state = sw.State()
	require.False(t, state.InTransition, "reverse transition should be complete")
	require.False(t, state.FTBActive, "FTB should be cleared after reverse completes")
	require.Equal(t, "cam1", state.ProgramSource, "program source should remain cam1")
}

func TestSwitcherFTBReverseAbortKeepsFTBActive(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB and complete it
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	// Toggle FTB off (starts reverse transition)
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	// Abort the reverse transition (user cancels)
	sw.AbortTransition()

	state := sw.State()
	require.False(t, state.InTransition, "transition should be aborted")
	// After aborting a reverse FTB, we should still be in FTB-active state (screen is black)
	require.True(t, state.FTBActive, "FTB should remain active when reverse is aborted")
}

func TestSwitcherFTBReverseAudioNotified(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	// Start FTB and complete it
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	audioTrans.mu.Lock()
	initialStartCalls := len(audioTrans.startCalls)
	audioTrans.mu.Unlock()

	// Toggle FTB off (starts reverse transition)
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	audioTrans.mu.Lock()
	require.Equal(t, initialStartCalls+1, len(audioTrans.startCalls), "audio should be notified of reverse FTB start")
	lastCall := audioTrans.startCalls[len(audioTrans.startCalls)-1]
	require.Equal(t, "cam1", lastCall.oldSrc)
	require.Equal(t, "", lastCall.newSrc)
	require.Equal(t, 1000, lastCall.durationMs)
	audioTrans.mu.Unlock()
}

func TestSwitcherMixRejectsAfterFTBComplete(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB and complete it
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	// Complete via position
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.True(t, state.FTBActive)
	require.False(t, state.InTransition)

	// Mix should be rejected while FTB is active (even though transition is done)
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "FTB is active")
}

func TestSwitcherDipTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "dip", 500, "")
	require.NoError(t, err)

	state := sw.State()
	require.True(t, state.InTransition)
	require.Equal(t, "dip", state.TransitionType)
}

func TestSwitcherAbortTransitionWhenIdle(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	initialSeq := sw.State().Seq

	// Abort when no transition is active — should be a no-op
	sw.AbortTransition()

	state := sw.State()
	require.Equal(t, initialSeq, state.Seq, "seq should not change on idle abort")
}

func TestStartTransitionDoesNotBlockFrameRouting(t *testing.T) {
	// Verify that starting a transition does not block frame routing.
	// In always-decode mode, there is no decoder warmup — the transition
	// starts immediately.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start the transition — should complete immediately (no warmup).
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// Engine should be published immediately.
	state := sw.State()
	require.True(t, state.InTransition)

	// Send a frame — should not block.
	frameDone := make(chan struct{})
	go func() {
		sw.handleVideoFrame("cam1", &media.VideoFrame{
			PTS: 200, IsKeyframe: true, WireData: []byte{0x01},
		})
		close(frameDone)
	}()

	select {
	case <-frameDone:
		// Good — frame was not blocked.
	case <-time.After(2 * time.Second):
		require.Fail(t, "handleVideoFrame blocked during transition")
	}
}

func TestFadeToBlackDoesNotBlockFrameRouting(t *testing.T) {
	// Verify that FTB does not block frame routing.
	// In always-decode mode, there is no decoder warmup.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Start FTB — should complete immediately (no warmup).
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	state := sw.State()
	require.True(t, state.FTBActive)
	require.True(t, state.InTransition)

	// Send a frame — should not block.
	frameDone := make(chan struct{})
	go func() {
		sw.handleVideoFrame("cam1", &media.VideoFrame{
			PTS: 200, IsKeyframe: true, WireData: []byte{0x01},
		})
		close(frameDone)
	}()

	select {
	case <-frameDone:
		// Good — not blocked.
	case <-time.After(2 * time.Second):
		require.Fail(t, "handleVideoFrame blocked during FTB")
	}
}

func TestFTBReverseDoesNotBlockFrameRouting(t *testing.T) {
	// Verify that FTB reverse does not block frame routing.
	// In always-decode mode, there is no decoder warmup.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Start FTB and complete it.
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.True(t, state.FTBActive)
	require.False(t, state.InTransition)

	// Toggle FTB off (starts reverse FTB transition).
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	// Send a frame — should not block.
	frameDone := make(chan struct{})
	go func() {
		sw.handleVideoFrame("cam1", &media.VideoFrame{
			PTS: 300, IsKeyframe: true, WireData: []byte{0x01},
		})
		close(frameDone)
	}()

	select {
	case <-frameDone:
		// Good — not blocked.
	case <-time.After(2 * time.Second):
		require.Fail(t, "handleVideoFrame blocked during FTB reverse")
	}
}

func TestTransitionGroupIDMonotonicity(t *testing.T) {
	// In always-decode mode, all sources provide raw YUV frames.
	// Verify GroupIDs remain monotonically non-decreasing across transitions.
	sw, viewer := setupSwitcherWithTransition(t)
	defer sw.Close()

	// 1. Send raw YUV frame from cam1 (program source)
	sendRawFrame(sw, "cam1", 300, true)
	time.Sleep(10 * time.Millisecond)

	// 2. Start transition to cam2
	err := sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.NoError(t, err)

	// 3. Feed raw YUV frames from both sources during transition
	sendRawFrame(sw, "cam1", 400, true)
	sendRawFrame(sw, "cam2", 400, true)

	// 4. Complete transition via SetTransitionPosition(1.0)
	time.Sleep(10 * time.Millisecond)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(30 * time.Millisecond)

	// 5. Send frame from cam2 (new program source)
	sendRawFrame(sw, "cam2", 500, true)
	time.Sleep(10 * time.Millisecond)

	// 6. Verify GroupIDs are monotonically non-decreasing
	viewer.mu.Lock()
	frames := make([]*media.VideoFrame, len(viewer.videos))
	copy(frames, viewer.videos)
	viewer.mu.Unlock()

	require.NotEmpty(t, frames)

	var lastGID uint32
	for i, f := range frames {
		require.GreaterOrEqual(t, f.GroupID, lastGID,
			"GroupID must be monotonically non-decreasing: frame %d has GroupID %d but previous was %d",
			i, f.GroupID, lastGID)
		lastGID = f.GroupID
	}
}
