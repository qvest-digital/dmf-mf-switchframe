package switcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/transition"
)

// mockTransitionCodecs returns factories that produce 4x4 mock decoders/encoders.
func mockTransitionCodecs() TransitionConfig {
	return TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}
}

// mockAudioTransHandler records calls to audio transition methods.
type mockAudioTransHandler struct {
	mu              sync.Mutex
	startCalls      []audioTransStartCall
	positionCalls   []float64
	completionCount int
}

type audioTransStartCall struct {
	oldSrc     string
	newSrc     string
	durationMs int
}

func (m *mockAudioTransHandler) OnTransitionStart(oldSource, newSource string, durationMs int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls = append(m.startCalls, audioTransStartCall{oldSource, newSource, durationMs})
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

// setupSwitcherWithTransition creates a switcher with two sources, program on cam1,
// preview on cam2, and transition config ready.
func setupSwitcherWithTransition(t *testing.T) (*Switcher, *mockProgramViewer) {
	t.Helper()

	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())

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

	err := sw.StartTransition(context.Background(), "cam2", "mix", 500)
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
	err := sw.StartTransition(context.Background(), "", "mix", 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no target source")
}

func TestSwitcherCannotDoubleTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 500)
	require.NoError(t, err)

	// Second transition should fail
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already active")
}

func TestSwitcherTransitionRoutesFramesToEngine(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())
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
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000)
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
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000)
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
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000)
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
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500)
	require.Error(t, err)
	// Could match "already active" or "FTB is active" — the FTB sets inTransition=true
	require.Error(t, err)
}

func TestSwitcherSetTransitionPosition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000)
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
	err := sw.StartTransition(context.Background(), "cam2", "mix", 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition not configured")
}

func TestSwitcherStartTransitionSourceNotFound(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "nonexistent", "mix", 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestSwitcherStartTransitionUnsupportedType(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "wipe", 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported transition type")
}

func TestSwitcherAbortTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000)
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

	// Toggle FTB off
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	state = sw.State()
	require.False(t, state.FTBActive, "FTB should be toggled off")
}

func TestSwitcherTransitionAudioNotified(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	err := sw.StartTransition(context.Background(), "cam2", "mix", 500)
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
	err := sw.StartTransition(context.Background(), "cam1", "mix", 500)
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

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000)
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
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "FTB is active")
}

func TestSwitcherDipTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "dip", 500)
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
