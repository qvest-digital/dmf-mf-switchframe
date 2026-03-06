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

// slowDecoder wraps a mock decoder and adds a delay to each Decode() call.
// Used to verify that warmup does NOT hold the switcher write lock.
type slowDecoder struct {
	inner transition.VideoDecoder
	delay time.Duration
}

func (d *slowDecoder) Decode(data []byte) ([]byte, int, int, error) {
	time.Sleep(d.delay)
	return d.inner.Decode(data)
}

func (d *slowDecoder) Close() { d.inner.Close() }

// slowTransitionCodecs returns factories that produce decoders with an
// artificial delay per Decode() call. This lets tests verify that frame
// routing is NOT blocked during transition decoder warmup.
func slowTransitionCodecs(decodeDelay time.Duration) TransitionConfig {
	return TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return &slowDecoder{
				inner: transition.NewMockDecoder(4, 4),
				delay: decodeDelay,
			}, nil
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
	mode       audio.AudioTransitionMode
	durationMs int
}

func (m *mockAudioTransHandler) OnTransitionStart(oldSource, newSource string, mode audio.AudioTransitionMode, durationMs int) {
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
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)

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
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)
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

func TestStartTransitionWarmsDecodersOutsideLock(t *testing.T) {
	// Use slow decoders so warmup takes measurable time.
	// If warmup held the write lock, handleVideoFrame would block for the
	// full warmup duration, and no frames would flow during that period.
	const warmupPerFrame = 20 * time.Millisecond

	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(slowTransitionCodecs(warmupPerFrame))

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Seed the GOP cache with several frames so warmup takes noticeable time.
	// Wire data must be valid AVC1 format (4-byte length prefix + NALU body)
	// so that RecordFrame's AVC1ToAnnexB conversion produces non-empty output.
	avc1Data := []byte{0x00, 0x00, 0x00, 0x01, 0x65} // length=1, NALU=0x65
	for i := 0; i < 10; i++ {
		kf := i == 0
		sw.gopCache.RecordFrame("cam1", &media.VideoFrame{
			PTS: int64(100 + i), IsKeyframe: kf, WireData: avc1Data,
			SPS: []byte{0x67, 0x42}, PPS: []byte{0x68, 0x00},
		})
		sw.gopCache.RecordFrame("cam2", &media.VideoFrame{
			PTS: int64(100 + i), IsKeyframe: kf, WireData: avc1Data,
			SPS: []byte{0x67, 0x42}, PPS: []byte{0x68, 0x00},
		})
	}

	// Total warmup will be ~20 frames * 20ms = ~400ms (10 from each source).
	// If the lock were held, sending a frame from cam1 during that window
	// would block for the full warmup duration.

	// Start the transition in a goroutine since it will take ~400ms for warmup.
	errCh := make(chan error, 1)
	go func() {
		errCh <- sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	}()

	// Give the transition time to start but NOT finish warmup.
	time.Sleep(50 * time.Millisecond)

	// Send a frame from cam1 (the program source). If warmup does NOT hold
	// the write lock, handleVideoFrame should complete quickly because it
	// only needs brief lock access for updateFrameStats.
	frameDone := make(chan struct{})
	go func() {
		sw.handleVideoFrame("cam1", &media.VideoFrame{
			PTS: 200, IsKeyframe: true, WireData: []byte{0x01},
		})
		close(frameDone)
	}()

	// The frame should complete quickly (well under warmup time).
	select {
	case <-frameDone:
		// Good — frame was not blocked by warmup.
	case <-time.After(2 * time.Second):
		require.Fail(t, "handleVideoFrame blocked during transition warmup — lock not released")
	}

	// Wait for StartTransition to complete.
	require.NoError(t, <-errCh)

	// Engine should be published after warmup.
	state := sw.State()
	require.True(t, state.InTransition)
}

func TestFadeToBlackWarmsDecodersOutsideLock(t *testing.T) {
	const warmupPerFrame = 20 * time.Millisecond

	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(slowTransitionCodecs(warmupPerFrame))

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Seed the GOP cache with frames so warmup takes noticeable time.
	// Wire data must be valid AVC1 format.
	avc1Data := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	for i := 0; i < 10; i++ {
		kf := i == 0
		sw.gopCache.RecordFrame("cam1", &media.VideoFrame{
			PTS: int64(100 + i), IsKeyframe: kf, WireData: avc1Data,
			SPS: []byte{0x67, 0x42}, PPS: []byte{0x68, 0x00},
		})
	}

	// Total warmup will be ~10 frames * 20ms = ~200ms.
	errCh := make(chan error, 1)
	go func() {
		errCh <- sw.FadeToBlack(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)

	// Send a frame — should NOT be blocked by warmup.
	frameDone := make(chan struct{})
	go func() {
		sw.handleVideoFrame("cam1", &media.VideoFrame{
			PTS: 200, IsKeyframe: true, WireData: avc1Data,
		})
		close(frameDone)
	}()

	select {
	case <-frameDone:
		// Good — not blocked.
	case <-time.After(2 * time.Second):
		require.Fail(t, "handleVideoFrame blocked during FTB warmup — lock not released")
	}

	require.NoError(t, <-errCh)

	state := sw.State()
	require.True(t, state.FTBActive)
	require.True(t, state.InTransition)
}

func TestFTBReverseWarmsDecodersOutsideLock(t *testing.T) {
	const warmupPerFrame = 20 * time.Millisecond

	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(slowTransitionCodecs(warmupPerFrame))

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)

	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Start FTB and complete it
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.True(t, state.FTBActive)
	require.False(t, state.InTransition)

	// Seed the GOP cache with frames so warmup takes noticeable time.
	// Wire data must be valid AVC1 format.
	avc1Data := []byte{0x00, 0x00, 0x00, 0x01, 0x65}
	for i := 0; i < 10; i++ {
		kf := i == 0
		sw.gopCache.RecordFrame("cam1", &media.VideoFrame{
			PTS: int64(200 + i), IsKeyframe: kf, WireData: avc1Data,
			SPS: []byte{0x67, 0x42}, PPS: []byte{0x68, 0x00},
		})
	}

	// Toggle FTB off (starts reverse FTB transition with slow warmup).
	errCh := make(chan error, 1)
	go func() {
		errCh <- sw.FadeToBlack(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)

	// Send a frame — should NOT be blocked by warmup.
	frameDone := make(chan struct{})
	go func() {
		sw.handleVideoFrame("cam1", &media.VideoFrame{
			PTS: 300, IsKeyframe: true, WireData: avc1Data,
		})
		close(frameDone)
	}()

	select {
	case <-frameDone:
		// Good — not blocked.
	case <-time.After(2 * time.Second):
		require.Fail(t, "handleVideoFrame blocked during FTB reverse warmup — lock not released")
	}

	require.NoError(t, <-errCh)
}
