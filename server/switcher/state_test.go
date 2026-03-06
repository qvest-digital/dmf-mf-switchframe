package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestSwitcherStateString(t *testing.T) {
	tests := []struct {
		state SwitcherState
		want  string
	}{
		{StateIdle, "idle"},
		{StateTransitioning, "transitioning"},
		{StateFTBTransitioning, "ftb_transitioning"},
		{StateFTB, "ftb"},
		{StateFTBReversing, "ftb_reversing"},
		{SwitcherState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			require.Equal(t, tt.want, tt.state.String())
		})
	}
}

func TestSwitcherStateIsInTransition(t *testing.T) {
	tests := []struct {
		state SwitcherState
		want  bool
	}{
		{StateIdle, false},
		{StateTransitioning, true},
		{StateFTBTransitioning, true},
		{StateFTB, false},
		{StateFTBReversing, true},
	}
	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			require.Equal(t, tt.want, tt.state.isInTransition(), "state=%s", tt.state)
		})
	}
}

func TestSwitcherStateIsFTBActive(t *testing.T) {
	tests := []struct {
		state SwitcherState
		want  bool
	}{
		{StateIdle, false},
		{StateTransitioning, false},
		{StateFTBTransitioning, true},
		{StateFTB, true},
		{StateFTBReversing, true},
	}
	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			require.Equal(t, tt.want, tt.state.isFTBActive(), "state=%s", tt.state)
		})
	}
}

func TestValidTransitions(t *testing.T) {
	// Verify the valid transition map is complete (every state has an entry)
	allStates := []SwitcherState{StateIdle, StateTransitioning, StateFTBTransitioning, StateFTB, StateFTBReversing}
	for _, s := range allStates {
		_, ok := validTransitions[s]
		require.True(t, ok, "validTransitions missing entry for %s", s)
	}
}

func TestSwitcherStartsInIdleState(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	require.Equal(t, StateIdle, sw.state, "new switcher should be in idle state")
	state := sw.State()
	require.False(t, state.InTransition)
	require.False(t, state.FTBActive)
}

func TestSwitcherStateTransitions_MixCycle(t *testing.T) {
	// Idle -> StateTransitioning -> StateIdle (after completion)
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start: Idle
	require.Equal(t, StateIdle, sw.state)

	// StartTransition -> StateTransitioning
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	sw.mu.RLock()
	require.Equal(t, StateTransitioning, sw.state)
	sw.mu.RUnlock()

	// Complete transition -> StateIdle
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	sw.mu.RLock()
	require.Equal(t, StateIdle, sw.state)
	sw.mu.RUnlock()

	state := sw.State()
	require.False(t, state.InTransition)
	require.False(t, state.FTBActive)
}

func TestSwitcherStateTransitions_MixAbort(t *testing.T) {
	// Idle -> StateTransitioning -> StateIdle (after abort)
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	sw.mu.RLock()
	require.Equal(t, StateTransitioning, sw.state)
	sw.mu.RUnlock()

	sw.AbortTransition()

	sw.mu.RLock()
	require.Equal(t, StateIdle, sw.state)
	sw.mu.RUnlock()
}

func TestSwitcherStateTransitions_FTBCycle(t *testing.T) {
	// Idle -> StateFTBTransitioning -> StateFTB -> StateFTBReversing -> StateIdle
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB: Idle -> StateFTBTransitioning
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	sw.mu.RLock()
	require.Equal(t, StateFTBTransitioning, sw.state)
	sw.mu.RUnlock()

	// API should report inTransition=true, ftbActive=true
	state := sw.State()
	require.True(t, state.InTransition)
	require.True(t, state.FTBActive)

	// Complete FTB: StateFTBTransitioning -> StateFTB
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	sw.mu.RLock()
	require.Equal(t, StateFTB, sw.state)
	sw.mu.RUnlock()

	// API should report inTransition=false, ftbActive=true
	state = sw.State()
	require.False(t, state.InTransition)
	require.True(t, state.FTBActive)

	// Toggle FTB off: StateFTB -> StateFTBReversing
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	sw.mu.RLock()
	require.Equal(t, StateFTBReversing, sw.state)
	sw.mu.RUnlock()

	// API should report inTransition=true, ftbActive=true
	state = sw.State()
	require.True(t, state.InTransition)
	require.True(t, state.FTBActive)

	// Complete reverse FTB: StateFTBReversing -> StateIdle
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	sw.mu.RLock()
	require.Equal(t, StateIdle, sw.state)
	sw.mu.RUnlock()

	state = sw.State()
	require.False(t, state.InTransition)
	require.False(t, state.FTBActive)
}

func TestSwitcherStateTransitions_FTBAbort(t *testing.T) {
	// Idle -> StateFTBTransitioning -> StateIdle (abort during FTB forward)
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	sw.mu.RLock()
	require.Equal(t, StateFTBTransitioning, sw.state)
	sw.mu.RUnlock()

	sw.AbortTransition()

	sw.mu.RLock()
	require.Equal(t, StateIdle, sw.state)
	sw.mu.RUnlock()

	state := sw.State()
	require.False(t, state.InTransition)
	require.False(t, state.FTBActive)
}

func TestSwitcherStateTransitions_FTBReverseAbort(t *testing.T) {
	// StateFTB -> StateFTBReversing -> StateFTB (abort during reverse)
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB and complete
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	sw.mu.RLock()
	require.Equal(t, StateFTB, sw.state)
	sw.mu.RUnlock()

	// Start reverse FTB
	err = sw.FadeToBlack(context.Background())
	require.NoError(t, err)

	sw.mu.RLock()
	require.Equal(t, StateFTBReversing, sw.state)
	sw.mu.RUnlock()

	// Abort reverse — should go back to StateFTB
	sw.AbortTransition()

	sw.mu.RLock()
	require.Equal(t, StateFTB, sw.state)
	sw.mu.RUnlock()

	state := sw.State()
	require.False(t, state.InTransition)
	require.True(t, state.FTBActive)
}

func TestSwitcherStateRejectsTransitionDuringFTB(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Start FTB and complete
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	// Mix should be rejected in StateFTB
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "FTB is active")

	// State should still be StateFTB
	sw.mu.RLock()
	require.Equal(t, StateFTB, sw.state)
	sw.mu.RUnlock()
}

func TestSwitcherStateRejectsFTBDuringTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// FTB should be rejected in StateTransitioning
	err = sw.FadeToBlack(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot FTB while mix/dip transition is active")
}

func TestSwitcherStateRejectsDoubleTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// Second transition should fail
	err = sw.StartTransition(context.Background(), "cam2", "mix", 500, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already active")
}

func TestSwitcherStateFTBBlocksPassthrough(t *testing.T) {
	// When in StateFTB, video frames should not be passed through
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test-viewer")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(mockTransitionCodecs())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})

	// Start FTB and complete
	err := sw.FadeToBlack(context.Background())
	require.NoError(t, err)
	err = sw.SetTransitionPosition(context.Background(), 1.0)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	// Count frames before
	viewer.mu.Lock()
	countBefore := len(viewer.videos)
	viewer.mu.Unlock()

	// Send a frame — should be blocked by FTB
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 200, IsKeyframe: true, WireData: []byte{0x01}})
	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	countAfter := len(viewer.videos)
	viewer.mu.Unlock()

	require.Equal(t, countBefore, countAfter, "frames should not pass through during FTB")
}

func TestSwitcherDebugSnapshotIncludesState(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	snap := sw.DebugSnapshot()
	require.Equal(t, "idle", snap["state"])
	require.Equal(t, false, snap["in_transition"])
	require.Equal(t, false, snap["ftb_active"])

	// Start transition
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	snap = sw.DebugSnapshot()
	require.Equal(t, "transitioning", snap["state"])
	require.Equal(t, true, snap["in_transition"])
	require.Equal(t, false, snap["ftb_active"])
}
